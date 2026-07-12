package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/chaos"
	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
)

func newChaosAzureAI(t *testing.T) (driver.AzureAI, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapAzureAI(cloudemu.NewAzure().AzureAI, e), e
}

func TestWrapAzureAICreateAccountChaos(t *testing.T) {
	a, e := newChaosAzureAI(t)
	ctx := context.Background()

	cfg := driver.AccountConfig{Name: "ai", ResourceGroup: "rg", Location: "eastus", Kind: "AIServices"}
	if _, err := a.CreateAccount(ctx, cfg); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("azureai", time.Hour))

	cfg.Name = "ai2"
	if _, err := a.CreateAccount(ctx, cfg); err == nil {
		t.Error("expected chaos error on CreateAccount")
	}
}

func TestWrapAzureAIChatCompletionsChaos(t *testing.T) {
	a, e := newChaosAzureAI(t)
	ctx := context.Background()

	req := driver.ChatCompletionRequest{Messages: []driver.ChatMessage{{Role: "user", Content: "hi"}}}
	if _, err := a.ChatCompletions(ctx, "ai", "gpt4o", req); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("azureai", time.Hour))

	if _, err := a.ChatCompletions(ctx, "ai", "gpt4o", req); err == nil {
		t.Error("expected chaos error on ChatCompletions")
	}
}

func TestWrapAzureAIUnwrappedOperationPassesThrough(t *testing.T) {
	a, e := newChaosAzureAI(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("azureai", time.Hour))

	// ListAccounts is not a chaos-wrapped operation; it must still succeed.
	if _, err := a.ListAccounts(ctx); err != nil {
		t.Errorf("unwrapped ListAccounts should pass through, got: %v", err)
	}
}
