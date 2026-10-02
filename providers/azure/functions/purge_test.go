package functions

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

func TestPurgeResourceGroupSitesThenPlans(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for _, rg := range []string{"Cas1", "cas10"} {
		if _, err := m.CreateAppServicePlan(ctx, AppServicePlan{Name: "plan", Subscription: "s1", ResourceGroup: rg}); err != nil {
			t.Fatal(err)
		}

		farm := idgen.AzureID("s1", rg, "Microsoft.Web", "serverfarms", "plan")
		if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "site-" + rg, Subscription: "s1", ResourceGroup: rg,
			ServerFarmID: farm}); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeResourceGroup(ctx, "s1", "cas1"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetSiteMeta(ctx, "s1", "Cas1", "site-Cas1"); !cerrors.IsNotFound(err) {
		t.Errorf("site in Cas1: err = %v, want NotFound", err)
	}

	if _, err := m.GetAppServicePlan(ctx, "s1", "Cas1", "plan"); !cerrors.IsNotFound(err) {
		t.Errorf("plan in Cas1: err = %v, want NotFound", err)
	}

	if _, err := m.GetSiteMeta(ctx, "s1", "cas10", "site-cas10"); err != nil {
		t.Errorf("site in cas10 was purged with cas1: %v", err)
	}

	if _, err := m.GetAppServicePlan(ctx, "s1", "cas10", "plan"); err != nil {
		t.Errorf("plan in cas10 was purged with cas1: %v", err)
	}
}

// TestPurgeResourceGroupKeepsPlanUsedElsewhere pins that a plan still serving
// a site in another group survives the purge and the failure is reported, as
// the real delete of that plan fails.
func TestPurgeResourceGroupKeepsPlanUsedElsewhere(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateAppServicePlan(ctx, AppServicePlan{Name: "plan", Subscription: "s1", ResourceGroup: "rgA"}); err != nil {
		t.Fatal(err)
	}

	farm := idgen.AzureID("s1", "rgA", "Microsoft.Web", "serverfarms", "plan")
	if _, err := m.UpsertSiteMeta(ctx, SiteMeta{Name: "site", Subscription: "s1", ResourceGroup: "rgB",
		ServerFarmID: farm}); err != nil {
		t.Fatal(err)
	}

	if err := m.PurgeResourceGroup(ctx, "s1", "rgA"); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("PurgeResourceGroup err = %v, want FailedPrecondition", err)
	}

	if _, err := m.GetAppServicePlan(ctx, "s1", "rgA", "plan"); err != nil {
		t.Errorf("plan still in use was deleted: %v", err)
	}
}
