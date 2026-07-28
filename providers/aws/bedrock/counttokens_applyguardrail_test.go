package bedrock

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

func TestCountTokensConverse(t *testing.T) {
	m := newTestMock()

	n, err := m.CountTokens(context.Background(), bedrockdriver.CountTokensInput{
		ModelID:  titanModel,
		System:   []string{"Be concise."},
		Messages: []bedrockdriver.Message{{Role: "user", Text: []string{"What is Bedrock?"}}},
	})
	requireNoError(t, err)

	if n <= 0 {
		t.Fatalf("expected a positive token count, got %d", n)
	}
}

func TestCountTokensInvokeBody(t *testing.T) {
	m := newTestMock()

	n, err := m.CountTokens(context.Background(), bedrockdriver.CountTokensInput{
		ModelID:    titanModel,
		InvokeBody: []byte(`{"inputText":"hello there world"}`),
	})
	requireNoError(t, err)
	assertEqual(t, 3, n)
}

func TestCountTokensInvalidModel(t *testing.T) {
	m := newTestMock()

	_, err := m.CountTokens(context.Background(), bedrockdriver.CountTokensInput{
		ModelID:  "nope.unknown-v1",
		Messages: []bedrockdriver.Message{{Role: "user", Text: []string{"hi"}}},
	})
	assertError(t, err, true)

	_, err = m.CountTokens(context.Background(), bedrockdriver.CountTokensInput{})
	assertError(t, err, true)
}

func TestApplyGuardrailEchoes(t *testing.T) {
	m := newTestMock()
	g := newGuardrail(t, m, "gr-apply")

	out, err := m.ApplyGuardrail(context.Background(), bedrockdriver.ApplyGuardrailInput{
		GuardrailIdentifier: g.ID,
		GuardrailVersion:    "DRAFT",
		Source:              bedrockdriver.GuardrailSourceInput,
		Content:             []string{"hello", "world"},
	})
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.GuardrailActionNone, out.Action)
	assertEqual(t, 2, len(out.Outputs))
	assertEqual(t, "hello", out.Outputs[0])
	assertEqual(t, "world", out.Outputs[1])
}

func TestApplyGuardrailUnknown(t *testing.T) {
	m := newTestMock()

	_, err := m.ApplyGuardrail(context.Background(), bedrockdriver.ApplyGuardrailInput{
		GuardrailIdentifier: "gr-missing",
		Source:              bedrockdriver.GuardrailSourceInput,
		Content:             []string{"hi"},
	})
	assertError(t, err, true)

	if !errors.IsNotFound(err) {
		t.Fatalf("expected NotFound error, got %v", err)
	}
}

func TestApplyGuardrailBadSource(t *testing.T) {
	m := newTestMock()
	g := newGuardrail(t, m, "gr-src")

	_, err := m.ApplyGuardrail(context.Background(), bedrockdriver.ApplyGuardrailInput{
		GuardrailIdentifier: g.ID,
		Source:              "SIDEWAYS",
		Content:             []string{"hi"},
	})
	assertError(t, err, true)

	if !errors.IsInvalidArgument(err) {
		t.Fatalf("expected InvalidArgument error, got %v", err)
	}

	_, err = m.ApplyGuardrail(context.Background(), bedrockdriver.ApplyGuardrailInput{
		GuardrailIdentifier: "",
		Source:              bedrockdriver.GuardrailSourceInput,
	})
	assertError(t, err, true)
}
