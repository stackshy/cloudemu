package bedrock

import (
	"context"
	"testing"

	bedrockdriver "github.com/stackshy/cloudemu/bedrock/driver"
)

func newGuardrail(t *testing.T, m *Mock, name string) *bedrockdriver.Guardrail {
	t.Helper()

	g, err := m.CreateGuardrail(context.Background(), bedrockdriver.GuardrailConfig{
		Name:                    name,
		BlockedInputMessaging:   "blocked in",
		BlockedOutputsMessaging: "blocked out",
	})
	requireNoError(t, err)

	return g
}

func TestGuardrailLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	g := newGuardrail(t, m, "gr-1")
	assertNotEmpty(t, g.ID)
	assertNotEmpty(t, g.ARN)
	assertEqual(t, bedrockdriver.GuardrailReady, g.Status)

	got, err := m.GetGuardrail(ctx, g.ID, "")
	requireNoError(t, err)
	assertEqual(t, "gr-1", got.Name)

	// lookup by ARN works too
	_, err = m.GetGuardrail(ctx, g.ARN, "")
	requireNoError(t, err)

	list, err := m.ListGuardrails(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	upd, err := m.UpdateGuardrail(ctx, g.ID, bedrockdriver.GuardrailConfig{
		Name:                    "gr-1",
		BlockedInputMessaging:   "new in",
		BlockedOutputsMessaging: "new out",
	})
	requireNoError(t, err)
	assertEqual(t, "new in", upd.BlockedInputMessaging)

	requireNoError(t, m.DeleteGuardrail(ctx, g.ID))

	_, err = m.GetGuardrail(ctx, g.ID, "")
	assertError(t, err, true)
}

func TestGuardrailValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateGuardrail(ctx, bedrockdriver.GuardrailConfig{BlockedInputMessaging: "x", BlockedOutputsMessaging: "y"})
	assertError(t, err, true)

	_, err = m.CreateGuardrail(ctx, bedrockdriver.GuardrailConfig{Name: "g", BlockedOutputsMessaging: "y"})
	assertError(t, err, true)

	newGuardrail(t, m, "dup")
	_, err = m.CreateGuardrail(ctx, bedrockdriver.GuardrailConfig{
		Name: "dup", BlockedInputMessaging: "x", BlockedOutputsMessaging: "y",
	})
	assertError(t, err, true)
}

func TestProvisionedThroughputLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	pt, err := m.CreateProvisionedModelThroughput(ctx, bedrockdriver.ProvisionedThroughputConfig{
		ProvisionedModelName: "pt-1",
		ModelID:              titanModel,
		ModelUnits:           2,
	})
	requireNoError(t, err)
	assertEqual(t, bedrockdriver.ProvisionedInService, pt.Status)
	assertNotEmpty(t, pt.ARN)
	assertEqual(t, 2, pt.ModelUnits)

	got, err := m.GetProvisionedModelThroughput(ctx, "pt-1")
	requireNoError(t, err)
	assertNotEmpty(t, got.FoundationModelARN)

	// by ARN
	_, err = m.GetProvisionedModelThroughput(ctx, pt.ARN)
	requireNoError(t, err)

	list, err := m.ListProvisionedModelThroughputs(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	requireNoError(t, m.DeleteProvisionedModelThroughput(ctx, "pt-1"))

	_, err = m.GetProvisionedModelThroughput(ctx, "pt-1")
	assertError(t, err, true)
}

func TestProvisionedThroughputValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateProvisionedModelThroughput(ctx, bedrockdriver.ProvisionedThroughputConfig{
		ProvisionedModelName: "pt", ModelID: titanModel, ModelUnits: 0,
	})
	assertError(t, err, true)

	_, err = m.CreateProvisionedModelThroughput(ctx, bedrockdriver.ProvisionedThroughputConfig{
		ProvisionedModelName: "pt", ModelID: "nope.unknown-v1", ModelUnits: 1,
	})
	assertError(t, err, true)
}

func TestModelInvocationLogging(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Unset → nil, no error.
	cfg, err := m.GetModelInvocationLoggingConfiguration(ctx)
	requireNoError(t, err)
	if cfg != nil {
		t.Fatalf("expected nil logging config, got %+v", cfg)
	}

	requireNoError(t, m.PutModelInvocationLoggingConfiguration(ctx, bedrockdriver.LoggingConfig{
		TextDataDeliveryEnabled: true,
		S3:                      &bedrockdriver.S3LoggingConfig{BucketName: "logs", KeyPrefix: "p/"},
	}))

	cfg, err = m.GetModelInvocationLoggingConfiguration(ctx)
	requireNoError(t, err)
	if cfg == nil || cfg.S3 == nil {
		t.Fatal("expected logging config with S3")
	}
	assertEqual(t, "logs", cfg.S3.BucketName)

	requireNoError(t, m.DeleteModelInvocationLoggingConfiguration(ctx))

	cfg, err = m.GetModelInvocationLoggingConfiguration(ctx)
	requireNoError(t, err)
	if cfg != nil {
		t.Fatalf("expected nil after delete, got %+v", cfg)
	}
}
