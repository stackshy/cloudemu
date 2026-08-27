package bedrock

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// TestSnapshotRoundTripBedrock proves a snapshot/restore round-trip preserves
// the Bedrock mock's state under the original identities: a guardrail (whose
// record is promoted through an exported form), an inference profile (a
// generic-dumped store), and the account model-invocation logging config all
// survive restore into a fresh mock.
func TestSnapshotRoundTripBedrock(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateGuardrail(ctx, driver.GuardrailConfig{
		Name:                    "gr-1",
		Description:             "block stuff",
		BlockedInputMessaging:   "no",
		BlockedOutputsMessaging: "nope",
	}); err != nil {
		t.Fatalf("create guardrail: %v", err)
	}

	if _, err := src.CreateInferenceProfile(ctx, driver.InferenceProfileConfig{
		Name:                "prof-1",
		ModelSourceCopyFrom: "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-v2",
	}); err != nil {
		t.Fatalf("create inference profile: %v", err)
	}

	if err := src.PutModelInvocationLoggingConfiguration(ctx, driver.LoggingConfig{
		TextDataDeliveryEnabled: true,
	}); err != nil {
		t.Fatalf("put logging config: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	gr, err := dst.GetGuardrail(ctx, "gr-1", "")
	if err != nil {
		t.Fatalf("get restored guardrail: %v", err)
	}

	if gr.Name != "gr-1" || gr.Description != "block stuff" {
		t.Fatalf("restored guardrail = %+v, want name gr-1 / desc block stuff", gr)
	}

	profiles := src.mustListProfiles(ctx, t)
	restored := dst.mustListProfiles(ctx, t)

	if len(restored) != len(profiles) || len(restored) == 0 {
		t.Fatalf("restored inference profiles = %d, want %d (non-zero)", len(restored), len(profiles))
	}

	cfg, err := dst.GetModelInvocationLoggingConfiguration(ctx)
	if err != nil {
		t.Fatalf("get restored logging config: %v", err)
	}

	if cfg == nil || !cfg.TextDataDeliveryEnabled {
		t.Fatalf("restored logging config = %+v, want TextDataDeliveryEnabled=true", cfg)
	}
}

// mustListProfiles returns every stored inference profile id, failing the test
// on error. It reads the store directly (an in-package test) to avoid coupling
// to a specific list-filter shape.
func (m *Mock) mustListProfiles(_ context.Context, _ *testing.T) []string {
	out := make([]string, 0, m.inferenceProfiles.Len())
	for id := range m.inferenceProfiles.All() {
		out = append(out, id)
	}

	return out
}
