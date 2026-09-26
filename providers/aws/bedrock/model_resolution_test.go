package bedrock

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

const (
	profileOnlyModel = "anthropic.claude-3-7-sonnet-20250219-v1:0"
	usProfileID      = "us." + profileOnlyModel
	onDemandMsg      = "Invocation of model ID " + profileOnlyModel +
		" with on-demand throughput isn’t supported. Retry your request with the ID or ARN of an inference profile" +
		" that contains this model."
)

func TestListFoundationModelsFilters(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	tests := []struct {
		name   string
		filter bedrockdriver.FoundationModelFilter
		check  func(fm *bedrockdriver.FoundationModel) bool
	}{
		{"provider ignores case", bedrockdriver.FoundationModelFilter{ByProvider: "anthropic"},
			func(fm *bedrockdriver.FoundationModel) bool { return fm.ProviderName == "Anthropic" }},
		{"output modality", bedrockdriver.FoundationModelFilter{ByOutputModality: "EMBEDDING"},
			func(fm *bedrockdriver.FoundationModel) bool { return contains(fm.OutputModalities, "EMBEDDING") }},
		{"inference type", bedrockdriver.FoundationModelFilter{ByInferenceType: "PROVISIONED"},
			func(fm *bedrockdriver.FoundationModel) bool {
				return contains(fm.InferenceTypesSupported, "PROVISIONED")
			}},
		{"customization", bedrockdriver.FoundationModelFilter{ByCustomizationType: "DISTILLATION"},
			func(fm *bedrockdriver.FoundationModel) bool {
				return contains(fm.CustomizationsSupported, "DISTILLATION")
			}},
		{"combined", bedrockdriver.FoundationModelFilter{ByProvider: "Amazon", ByOutputModality: "EMBEDDING"},
			func(fm *bedrockdriver.FoundationModel) bool {
				return fm.ProviderName == "Amazon" && contains(fm.OutputModalities, "EMBEDDING")
			}},
	}

	all, err := m.ListFoundationModels(ctx, bedrockdriver.FoundationModelFilter{})
	requireNoError(t, err)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.ListFoundationModels(ctx, tc.filter)
			requireNoError(t, err)

			want := 0

			for i := range all {
				if tc.check(&all[i]) {
					want++
				}
			}

			if want == 0 || len(got) != want {
				t.Fatalf("got %d models, want %d (non-zero)", len(got), want)
			}

			for i := range got {
				if !tc.check(&got[i]) {
					t.Fatalf("model %s does not match the filter", got[i].ModelID)
				}
			}
		})
	}

	for _, bad := range []bedrockdriver.FoundationModelFilter{
		{ByOutputModality: "text"}, {ByInferenceType: "INFERENCE_PROFILE"}, {ByCustomizationType: "LORA"},
	} {
		_, err := m.ListFoundationModels(ctx, bad)
		if !cerrors.IsInvalidArgument(err) {
			t.Fatalf("filter %+v: want InvalidArgument, got %v", bad, err)
		}
	}
}

// invokeKeys invokes id and returns the top-level keys of the response body.
func invokeKeys(t *testing.T, m *Mock, id string) map[string]json.RawMessage {
	t.Helper()

	res, err := m.InvokeModel(context.Background(), bedrockdriver.InvokeModelInput{ModelID: id, Body: []byte(`{"prompt":"hi"}`)})
	requireNoError(t, err)

	var body map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(res.Body, &body))

	return body
}

func TestInvokeModelResolvesTargets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateModelCustomizationJob(ctx, bedrockdriver.CustomizationJobConfig{
		JobName: "job", CustomModelName: "my-haiku", RoleARN: "arn:aws:iam::123456789012:role/r",
		BaseModelIdentifier: "anthropic.claude-3-haiku-20240307-v1:0",
	})
	requireNoError(t, err)

	custom, err := m.GetCustomModel(ctx, "my-haiku")
	requireNoError(t, err)

	sys, err := m.GetInferenceProfile(ctx, usProfileID)
	requireNoError(t, err)

	app, err := m.CreateInferenceProfile(ctx, bedrockdriver.InferenceProfileConfig{Name: "app", ModelSourceCopyFrom: sys.ARN})
	requireNoError(t, err)
	assertEqual(t, 3, len(app.Models))

	pt, err := m.CreateProvisionedModelThroughput(ctx, bedrockdriver.ProvisionedThroughputConfig{
		ProvisionedModelName: "pt", ModelID: titanModel, ModelUnits: 1,
	})
	requireNoError(t, err)

	tests := []struct{ name, id, key string }{
		{"custom model ARN uses base family", custom.ModelARN, "content"},
		{"custom model name", "my-haiku", "content"},
		{"system profile ID", usProfileID, "content"},
		{"system profile ARN", sys.ARN, "content"},
		{"application profile ARN", app.ARN, "content"},
		{"provisioned throughput ARN", pt.ARN, "results"},
		{"nova", "amazon.nova-lite-v1:0", "output"},
		{"mistral", "mistral.mistral-7b-instruct-v0:2", "outputs"},
		{"command r", "cohere.command-r-v1:0", "generation_id"},
		{"titan embed v2", "amazon.titan-embed-text-v2:0", "embedding"},
		{"cohere embed", "cohere.embed-english-v3", "embeddings"},
		{"image model", "amazon.titan-image-generator-v2:0", "images"},
		{"deepseek profile", "us.deepseek.r1-v1:0", "choices"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := invokeKeys(t, m, tc.id)[tc.key]; !ok {
				t.Fatalf("response for %s has no %q key", tc.id, tc.key)
			}
		})
	}
}

func TestInvokeModelOnDemandErrors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.InvokeModel(ctx, bedrockdriver.InvokeModelInput{ModelID: profileOnlyModel, Body: []byte(`{}`)})
	if !cerrors.IsInvalidArgument(err) || cerrors.Message(err) != onDemandMsg {
		t.Fatalf("got %v, want InvalidArgument %q", err, onDemandMsg)
	}

	_, err = m.Converse(ctx, bedrockdriver.ConverseInput{
		ModelID: profileOnlyModel, Messages: []bedrockdriver.Message{{Role: "user", Text: []string{"hi"}}},
	})
	if cerrors.Message(err) != onDemandMsg {
		t.Fatalf("Converse: got %v, want %q", err, onDemandMsg)
	}

	_, err = m.Converse(ctx, bedrockdriver.ConverseInput{
		ModelID: usProfileID, Messages: []bedrockdriver.Message{{Role: "user", Text: []string{"hi"}}},
	})
	requireNoError(t, err)

	// A provisioned-only variant gets the first sentence only.
	_, err = m.InvokeModel(ctx, bedrockdriver.InvokeModelInput{ModelID: "amazon.titan-text-express-v1:0:8k", Body: []byte(`{}`)})
	if msg := cerrors.Message(err); !strings.HasSuffix(msg, "isn’t supported.") {
		t.Fatalf("provisioned-only: got %q", msg)
	}

	_, err = m.InvokeModel(ctx, bedrockdriver.InvokeModelInput{ModelID: "nope.model-v1", Body: []byte(`{}`)})
	if cerrors.Message(err) != "The provided model identifier is invalid." {
		t.Fatalf("unknown model: got %v", err)
	}

	// CountTokens takes the model ID without the on-demand check.
	_, err = m.CountTokens(ctx, bedrockdriver.CountTokensInput{ModelID: profileOnlyModel, InvokeBody: []byte(`{"prompt":"a b"}`)})
	requireNoError(t, err)
}

func TestInferenceProfileTypes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	sys, err := m.ListInferenceProfiles(ctx, "")
	requireNoError(t, err)

	if len(sys) == 0 {
		t.Fatal("expected seeded system profiles")
	}

	for i := range sys {
		assertEqual(t, bedrockdriver.InferenceProfileTypeSystemDefined, sys[i].Type)

		geo := strings.SplitN(sys[i].ID, ".", 2)[0]
		if (geo != "us" && geo != "global") || !strings.Contains(sys[i].ARN, ":123456789012:inference-profile/"+geo+".") {
			t.Fatalf("bad system profile id/arn: %s %s", sys[i].ID, sys[i].ARN)
		}
	}

	app, err := m.ListInferenceProfiles(ctx, bedrockdriver.InferenceProfileTypeApplication)
	requireNoError(t, err)
	assertEqual(t, 0, len(app))

	_, err = m.ListInferenceProfiles(ctx, "BOGUS")
	assertError(t, err, true)

	if err = m.DeleteInferenceProfile(ctx, usProfileID); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("delete system profile: want InvalidArgument, got %v", err)
	}

	_, err = m.CreateInferenceProfile(ctx, bedrockdriver.InferenceProfileConfig{Name: "p", ModelSourceCopyFrom: "arn:model"})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad copyFrom: want InvalidArgument, got %v", err)
	}
}

func TestGlobalProfilesAndNewModels(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, id := range []string{
		"global.anthropic.claude-sonnet-4-20250514-v1:0",
		"global.anthropic.claude-sonnet-4-5-20250929-v1:0",
		"global.anthropic.claude-haiku-4-5-20251001-v1:0",
		"us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		"us.anthropic.claude-opus-4-1-20250805-v1:0",
	} {
		p, err := m.GetInferenceProfile(ctx, id)
		requireNoError(t, err)
		assertEqual(t, bedrockdriver.InferenceProfileTypeSystemDefined, p.Type)

		if _, ok := invokeKeys(t, m, id)["content"]; !ok {
			t.Fatalf("invoke via %s did not return an Anthropic body", id)
		}
	}

	// Opus 4.1 has no global profile, and Claude 3 Haiku has no global one.
	for _, id := range []string{"global.anthropic.claude-opus-4-1-20250805-v1:0", "global.anthropic.claude-3-haiku-20240307-v1:0"} {
		if _, err := m.GetInferenceProfile(ctx, id); !cerrors.IsNotFound(err) {
			t.Fatalf("%s: want NotFound, got %v", id, err)
		}
	}

	// The 4.5 models have no on-demand throughput.
	_, err := m.InvokeModel(ctx, bedrockdriver.InvokeModelInput{ModelID: "anthropic.claude-haiku-4-5-20251001-v1:0", Body: []byte(`{}`)})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bare Haiku 4.5: want InvalidArgument, got %v", err)
	}
}

func TestCatalogLifecycleAndInferenceTypes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for id, want := range map[string]string{
		"anthropic.claude-3-sonnet-20240229-v1:0":   bedrockdriver.LifecycleLegacy,
		"anthropic.claude-3-opus-20240229-v1:0":     bedrockdriver.LifecycleLegacy,
		"anthropic.claude-3-5-sonnet-20240620-v1:0": bedrockdriver.LifecycleLegacy,
		"anthropic.claude-3-5-sonnet-20241022-v2:0": bedrockdriver.LifecycleLegacy,
		"anthropic.claude-sonnet-4-5-20250929-v1:0": bedrockdriver.LifecycleActive,
		"amazon.nova-pro-v1:0":                      bedrockdriver.LifecycleActive,
	} {
		fm, err := m.GetFoundationModel(ctx, id)
		requireNoError(t, err)
		assertEqual(t, want, fm.LifecycleStatus)
	}

	// INFERENCE_PROFILE appears only on models with no on-demand throughput,
	// even when the model also has a system profile.
	all, err := m.ListFoundationModels(ctx, bedrockdriver.FoundationModelFilter{})
	requireNoError(t, err)

	for i := range all {
		it := all[i].InferenceTypesSupported
		if contains(it, bedrockdriver.InferenceTypeInferenceProfile) && contains(it, bedrockdriver.InferenceTypeOnDemand) {
			t.Fatalf("%s lists both ON_DEMAND and INFERENCE_PROFILE", all[i].ModelID)
		}
	}

	nova, err := m.GetFoundationModel(ctx, "amazon.nova-pro-v1:0")
	requireNoError(t, err)
	assertEqual(t, 1, len(nova.InferenceTypesSupported))

	_, err = m.GetInferenceProfile(ctx, "us.amazon.nova-pro-v1:0")
	requireNoError(t, err)
}

func TestApplicationProfileIDFormat(t *testing.T) {
	m := newTestMock()

	p, err := m.CreateInferenceProfile(context.Background(), bedrockdriver.InferenceProfileConfig{
		Name: "fmt", ModelSourceCopyFrom: "arn:aws:bedrock:us-east-1::foundation-model/" + titanModel,
	})
	requireNoError(t, err)

	if !regexp.MustCompile(`^[a-z0-9]{12}$`).MatchString(p.ID) {
		t.Fatalf("profile id %q is not 12 lowercase alphanumerics", p.ID)
	}

	assertEqual(t, "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/"+p.ID, p.ARN)
}

func TestSystemProfilesFollowRegion(t *testing.T) {
	m := New(config.NewOptions(config.WithRegion("eu-west-1"), config.WithAccountID("123456789012")))

	p, err := m.GetInferenceProfile(context.Background(), "eu."+profileOnlyModel)
	requireNoError(t, err)

	if !strings.HasPrefix(p.Models[0], "arn:aws:bedrock:eu-west-1::foundation-model/") {
		t.Fatalf("first model should be the local region, got %s", p.Models[0])
	}

	_, err = m.InvokeModel(context.Background(), bedrockdriver.InvokeModelInput{ModelID: p.ARN, Body: []byte(`{}`)})
	requireNoError(t, err)
}
