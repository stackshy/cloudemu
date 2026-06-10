package bedrock

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	bedrockdriver "github.com/stackshy/cloudemu/bedrock/driver"
	"github.com/stackshy/cloudemu/config"
)

const titanModel = "amazon.titan-text-express-v1"

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	return New(opts)
}

func TestListFoundationModels(t *testing.T) {
	m := newTestMock()

	models, err := m.ListFoundationModels(context.Background())
	requireNoError(t, err)

	if len(models) == 0 {
		t.Fatal("expected a seeded foundation-model catalog")
	}

	for _, fm := range models {
		assertNotEmpty(t, fm.ModelARN)
		assertNotEmpty(t, fm.ModelID)
		assertEqual(t, bedrockdriver.LifecycleActive, fm.LifecycleStatus)
	}
}

func TestGetFoundationModel(t *testing.T) {
	m := newTestMock()

	fm, err := m.GetFoundationModel(context.Background(), titanModel)
	requireNoError(t, err)
	assertEqual(t, titanModel, fm.ModelID)
	assertEqual(t, "Amazon", fm.ProviderName)

	_, err = m.GetFoundationModel(context.Background(), "nope.unknown-v1")
	assertError(t, err, true)
}

func TestCreateModelCustomizationJob(t *testing.T) {
	tests := []struct {
		name      string
		cfg       bedrockdriver.CustomizationJobConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg: bedrockdriver.CustomizationJobConfig{
				JobName:             "job-1",
				CustomModelName:     "cm-1",
				RoleARN:             "arn:aws:iam::123456789012:role/bedrock",
				BaseModelIdentifier: titanModel,
				TrainingDataURI:     "s3://b/t.jsonl",
				OutputDataURI:       "s3://b/o/",
			},
		},
		{name: "missing job name", cfg: bedrockdriver.CustomizationJobConfig{CustomModelName: "x", RoleARN: "r", BaseModelIdentifier: titanModel}, expectErr: true},
		{name: "missing base model", cfg: bedrockdriver.CustomizationJobConfig{JobName: "j", CustomModelName: "x", RoleARN: "r"}, expectErr: true},
		{name: "unknown base model", cfg: bedrockdriver.CustomizationJobConfig{JobName: "j", CustomModelName: "x", RoleARN: "r", BaseModelIdentifier: "nope.x-v1"}, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			job, err := m.CreateModelCustomizationJob(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, bedrockdriver.JobCompleted, job.Status)
			assertNotEmpty(t, job.JobARN)
			assertNotEmpty(t, job.OutputModelARN)
			assertEqual(t, tc.cfg.CustomModelName, job.OutputModelName)
		})
	}
}

func TestCustomizationProducesActiveModel(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModelCustomizationJob(ctx, bedrockdriver.CustomizationJobConfig{
		JobName:             "job-1",
		CustomModelName:     "cm-1",
		RoleARN:             "arn:aws:iam::123456789012:role/bedrock",
		BaseModelIdentifier: titanModel,
	})
	requireNoError(t, err)

	models, err := m.ListCustomModels(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(models))

	cm, err := m.GetCustomModel(ctx, "cm-1")
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.ModelActive, cm.ModelStatus)
	assertEqual(t, "Titan Text G1 - Express", cm.BaseModelName)

	requireNoError(t, m.DeleteCustomModel(ctx, "cm-1"))

	_, err = m.GetCustomModel(ctx, "cm-1")
	assertError(t, err, true)
}

func TestDuplicateJobName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	cfg := bedrockdriver.CustomizationJobConfig{
		JobName: "dup", CustomModelName: "cm-1", RoleARN: "r", BaseModelIdentifier: titanModel,
	}

	_, err := m.CreateModelCustomizationJob(ctx, cfg)
	requireNoError(t, err)

	cfg.CustomModelName = "cm-2"
	_, err = m.CreateModelCustomizationJob(ctx, cfg)
	assertError(t, err, true)
}

func TestInvokeModelFamilies(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		body    string
		key     string // a JSON key expected in the family's response envelope
	}{
		{"anthropic", "anthropic.claude-3-haiku-20240307-v1:0", `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`, "content"},
		{"titan", titanModel, `{"inputText":"hi"}`, "results"},
		{"llama", "meta.llama3-8b-instruct-v1:0", `{"prompt":"hi"}`, "generation"},
		{"cohere", "cohere.command-text-v14", `{"prompt":"hi"}`, "generations"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			res, err := m.InvokeModel(context.Background(), bedrockdriver.InvokeModelInput{
				ModelID:     tc.modelID,
				ContentType: "application/json",
				Body:        []byte(tc.body),
			})
			requireNoError(t, err)
			assertEqual(t, "application/json", res.ContentType)

			var generic map[string]json.RawMessage
			requireNoError(t, json.Unmarshal(res.Body, &generic))

			if _, ok := generic[tc.key]; !ok {
				t.Fatalf("expected key %q in response %s", tc.key, res.Body)
			}
		})
	}
}

func TestInvokeModelUnknown(t *testing.T) {
	m := newTestMock()

	_, err := m.InvokeModel(context.Background(), bedrockdriver.InvokeModelInput{
		ModelID: "nope.unknown-v1",
		Body:    []byte(`{"inputText":"hi"}`),
	})
	assertError(t, err, true)
}

func TestInvokeModelUsesCustomModel(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	job, err := m.CreateModelCustomizationJob(ctx, bedrockdriver.CustomizationJobConfig{
		JobName: "j", CustomModelName: "cm-1", RoleARN: "r", BaseModelIdentifier: titanModel,
	})
	requireNoError(t, err)

	_, err = m.InvokeModel(ctx, bedrockdriver.InvokeModelInput{ModelID: job.OutputModelARN, Body: []byte(`{"inputText":"hi"}`)})
	requireNoError(t, err)
}

func TestConverse(t *testing.T) {
	m := newTestMock()

	out, err := m.Converse(context.Background(), bedrockdriver.ConverseInput{
		ModelID:  titanModel,
		System:   []string{"Be concise."},
		Messages: []bedrockdriver.Message{{Role: "user", Text: []string{"What is Bedrock?"}}},
	})
	requireNoError(t, err)
	assertEqual(t, "assistant", out.Message.Role)
	assertEqual(t, "end_turn", out.StopReason)

	if out.TotalTokens != out.InputTokens+out.OutputTokens {
		t.Fatalf("token totals inconsistent: %d != %d + %d", out.TotalTokens, out.InputTokens, out.OutputTokens)
	}

	if out.InputTokens == 0 || out.OutputTokens == 0 {
		t.Fatalf("expected non-zero tokens, got in=%d out=%d", out.InputTokens, out.OutputTokens)
	}
}

func TestConverseValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.Converse(ctx, bedrockdriver.ConverseInput{ModelID: titanModel})
	assertError(t, err, true)

	_, err = m.Converse(ctx, bedrockdriver.ConverseInput{
		ModelID:  "nope.unknown-v1",
		Messages: []bedrockdriver.Message{{Role: "user", Text: []string{"hi"}}},
	})
	assertError(t, err, true)
}

// requireNoError fails the test immediately if err is non-nil.
func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertError asserts that err matches the expectErr expectation.
func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error, got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertEqual asserts that expected and actual are equal.
func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

// assertNotEmpty asserts that s is non-empty.
func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}
