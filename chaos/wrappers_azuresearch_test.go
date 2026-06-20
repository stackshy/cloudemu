package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/azuresearch/driver"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	provsearch "github.com/stackshy/cloudemu/providers/azure/azuresearch"
)

func newChaosAzureSearch(t *testing.T) (driver.AzureSearch, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	o := config.NewOptions(config.WithAccountID("sub-1"))

	return chaos.WrapAzureSearch(provsearch.New(o), e), e
}

func TestWrapAzureSearchCreateServiceChaos(t *testing.T) {
	a, e := newChaosAzureSearch(t)
	ctx := context.Background()

	cfg := driver.ServiceConfig{Name: "s", ResourceGroup: "rg", Location: "eastus"}
	if _, err := a.CreateService(ctx, cfg); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("azuresearch", time.Hour))

	cfg.Name = "s2"
	if _, err := a.CreateService(ctx, cfg); err == nil {
		t.Error("expected chaos error on CreateService")
	}
}

func TestWrapAzureSearchSearchChaos(t *testing.T) {
	a, e := newChaosAzureSearch(t)
	ctx := context.Background()

	_, _ = a.CreateService(ctx, driver.ServiceConfig{Name: "s", ResourceGroup: "rg", Location: "eastus"})
	_, _ = a.CreateOrUpdateIndex(ctx, "s", driver.Index{Name: "i"})

	if _, err := a.SearchDocuments(ctx, "s", "i", driver.SearchRequest{Search: "*"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("azuresearch", time.Hour))

	if _, err := a.SearchDocuments(ctx, "s", "i", driver.SearchRequest{Search: "*"}); err == nil {
		t.Error("expected chaos error on SearchDocuments")
	}
}

func TestWrapAzureSearchUnwrappedPassesThrough(t *testing.T) {
	a, e := newChaosAzureSearch(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("azuresearch", time.Hour))

	// ListServices is not a chaos-wrapped operation; it must still succeed.
	if _, err := a.ListServices(ctx); err != nil {
		t.Errorf("unwrapped ListServices should pass through, got: %v", err)
	}
}
