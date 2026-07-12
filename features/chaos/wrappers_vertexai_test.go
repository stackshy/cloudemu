package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/chaos"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

func newChaosVertexAI(t *testing.T) (driver.VertexAI, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapVertexAI(cloudemu.NewGCP().VertexAI, e), e
}

func TestWrapVertexAICreateCustomJobChaos(t *testing.T) {
	v, e := newChaosVertexAI(t)
	ctx := context.Background()

	if _, err := v.CreateCustomJob(ctx, driver.CustomJobConfig{Location: "us-central1", DisplayName: "j1"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("vertexai", time.Hour))

	if _, err := v.CreateCustomJob(ctx, driver.CustomJobConfig{Location: "us-central1", DisplayName: "j2"}); err == nil {
		t.Error("expected chaos error on CreateCustomJob")
	}
}

func TestWrapVertexAIGenerateContentChaos(t *testing.T) {
	v, e := newChaosVertexAI(t)
	ctx := context.Background()

	req := driver.GenerateContentRequest{
		Contents: []driver.Content{{Role: "user", Parts: []driver.Part{{Text: "hi there"}}}},
	}

	if _, err := v.GenerateContent(ctx, "gemini-2.5-pro", req); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("vertexai", time.Hour))

	if _, err := v.GenerateContent(ctx, "gemini-2.5-pro", req); err == nil {
		t.Error("expected chaos error on GenerateContent")
	}
}

func TestWrapVertexAIUnwrappedOperationPassesThrough(t *testing.T) {
	v, e := newChaosVertexAI(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("vertexai", time.Hour))

	// ListModels is not a chaos-wrapped operation; it must still succeed.
	if _, err := v.ListModels(ctx, "us-central1"); err != nil {
		t.Errorf("unwrapped ListModels should pass through, got: %v", err)
	}
}
