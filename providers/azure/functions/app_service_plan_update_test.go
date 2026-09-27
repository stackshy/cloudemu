package functions

import (
	"context"
	"errors"
	"maps"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func planMustNotErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func planWantEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// planWantInvalid asserts err is an InvalidArgument (400) refusal.
func planWantInvalid(t *testing.T, what string, err error) {
	t.Helper()

	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("%s: err = %v, want InvalidArgument", what, err)
	}
}

// planWantOSChangeRefused asserts err is the 400 BadRequest Azure answers an
// OS change with.
func planWantOSChangeRefused(t *testing.T, what string, err error) {
	t.Helper()

	var pe *PlanError
	if !errors.As(err, &pe) || pe.Code != codeBadRequest || !cerrors.IsInvalidArgument(err) {
		t.Errorf("%s: err = %v, want a BadRequest PlanError", what, err)
	}

	if err != nil && err.Error() == "" {
		t.Errorf("%s: empty error text", what)
	}
}

func createPlanForUpdate(t *testing.T, m *Mock, p AppServicePlan) {
	t.Helper()

	p.Subscription, p.ResourceGroup = "sub", "rg"
	if p.Name == "" {
		p.Name = "p"
	}

	_, err := m.CreateAppServicePlan(context.Background(), p)
	planMustNotErr(t, err)
}

func patchPlan(m *Mock, patch AppServicePlanPatch) (*AppServicePlan, error) {
	return m.PatchAppServicePlan(context.Background(), "sub", "rg", "p", patch)
}

func TestPatchAppServicePlan(t *testing.T) {
	m := newTestMock()
	createPlanForUpdate(t, m, AppServicePlan{SKUName: "S1", Tags: map[string]string{"env": "dev"}})

	workers, sku, perSite := 10, "P1v3", true

	got, err := patchPlan(m, AppServicePlanPatch{
		SKUName: &sku, PerSiteScaling: &perSite, MaximumElasticWorkerCount: &workers,
	})
	planMustNotErr(t, err)

	planWantEqual(t, "sku name", got.SKUName, "P1v3")
	planWantEqual(t, "sku tier (re-derived)", got.SKUTier, tierPremiumV3)
	planWantEqual(t, "perSiteScaling", got.PerSiteScaling, true)
	planWantEqual(t, "maximumElasticWorkerCount", got.MaximumElasticWorkerCount, 10)
	planWantEqual(t, "capacity (omitted, kept)", got.Capacity, 1)

	if !maps.Equal(got.Tags, map[string]string{"env": "dev"}) {
		t.Errorf("tags = %v, want the omitted tags kept", got.Tags)
	}

	got, err = patchPlan(m, AppServicePlanPatch{Tags: map[string]string{}})
	planMustNotErr(t, err)

	if len(got.Tags) != 0 {
		t.Errorf("tags = %v, want an empty tags map to clear them", got.Tags)
	}

	planWantEqual(t, "perSiteScaling (omitted, kept)", got.PerSiteScaling, true)

	stored, err := m.GetAppServicePlan(context.Background(), "sub", "rg", "p")
	planMustNotErr(t, err)
	planWantEqual(t, "stored sku", stored.SKUName, got.SKUName)

	_, err = m.PatchAppServicePlan(context.Background(), "sub", "other-rg", "p", AppServicePlanPatch{PerSiteScaling: &perSite})
	if !cerrors.IsNotFound(err) {
		t.Errorf("PATCH in another resource group: err = %v, want NotFound", err)
	}
}

// TestPatchAppServicePlanRefusesOSChange: real Azure refuses to change the OS
// of an existing plan (Azure/bicep#5724), by reserved or by kind.
func TestPatchAppServicePlanRefusesOSChange(t *testing.T) {
	m := newTestMock()
	createPlanForUpdate(t, m, AppServicePlan{SKUName: "P1v3", Kind: "linux", Reserved: true})

	windows, appKind, linuxFunc := false, "app", "functionapp,linux"

	_, err := patchPlan(m, AppServicePlanPatch{Reserved: &windows})
	planWantOSChangeRefused(t, "reserved:false on a Linux plan", err)

	_, err = patchPlan(m, AppServicePlanPatch{Kind: &appKind})
	planWantOSChangeRefused(t, "kind:app on a Linux plan", err)

	got, err := patchPlan(m, AppServicePlanPatch{Kind: &linuxFunc})
	planMustNotErr(t, err)
	planWantEqual(t, "kind (same OS)", got.Kind, "functionapp,linux")
	planWantEqual(t, "reserved", got.Reserved, true)
}

// TestPutAppServicePlanRefusesOSChange: a re-PUT that omits reserved on a
// Linux plan used to turn it into a Windows plan silently.
func TestPutAppServicePlanRefusesOSChange(t *testing.T) {
	m := newTestMock()
	createPlanForUpdate(t, m, AppServicePlan{SKUName: "P1v3", Kind: "linux", Reserved: true})

	_, err := m.CreateAppServicePlan(context.Background(), AppServicePlan{
		Name: "p", Subscription: "sub", ResourceGroup: "rg", SKUName: "P1v3", Kind: "linux",
	})
	planWantOSChangeRefused(t, "re-PUT without reserved", err)

	stored, err := m.GetAppServicePlan(context.Background(), "sub", "rg", "p")
	planMustNotErr(t, err)
	planWantEqual(t, "reserved after refused re-PUT", stored.Reserved, true)

	got, err := m.CreateAppServicePlan(context.Background(), AppServicePlan{
		Name: "p", Subscription: "sub", ResourceGroup: "rg", SKUName: "P2v3", Kind: "linux", Reserved: true, Capacity: 3,
	})
	planMustNotErr(t, err)
	planWantEqual(t, "re-PUT sku", got.SKUName, "P2v3")
	planWantEqual(t, "re-PUT capacity", got.Capacity, 3)
}

// TestPatchAppServicePlanSKUAndCapacityRules covers the SKU and capacity
// checks: capacity at least 1 and at most the tier's instance limit, a known
// SKU, no move between hosting families, and a tier that matches the name.
func TestPatchAppServicePlanSKUAndCapacityRules(t *testing.T) {
	m := newTestMock()
	createPlanForUpdate(t, m, AppServicePlan{SKUName: "B1"})
	createPlanForUpdate(t, m, AppServicePlan{Name: "consumption", SKUName: "Y1"})

	ptr := func(s string) *string { return &s }
	n := func(i int) *int { return &i }

	for _, tc := range []struct {
		what  string
		patch AppServicePlanPatch
	}{
		{"capacity 0", AppServicePlanPatch{Capacity: n(0)}},
		{"capacity -5", AppServicePlanPatch{Capacity: n(-5)}},
		{"capacity above the Basic limit of 3", AppServicePlanPatch{Capacity: n(4)}},
		{"unknown sku", AppServicePlanPatch{SKUName: ptr("ZZ9")}},
		{"dedicated to Elastic Premium", AppServicePlanPatch{SKUName: ptr("EP1")}},
		{"dedicated to Consumption", AppServicePlanPatch{SKUName: ptr("Y1")}},
		{"mismatched explicit tier", AppServicePlanPatch{SKUName: ptr("S1"), SKUTier: ptr("Premium")}},
		{"tier alone", AppServicePlanPatch{SKUTier: ptr("Standard")}},
		{"sku move above the new tier limit", AppServicePlanPatch{SKUName: ptr("S1"), Capacity: n(11)}},
	} {
		_, err := patchPlan(m, tc.patch)
		planWantInvalid(t, tc.what, err)
	}

	stored, err := m.GetAppServicePlan(context.Background(), "sub", "rg", "p")
	planMustNotErr(t, err)
	planWantEqual(t, "sku after refusals", stored.SKUName, "B1")
	planWantEqual(t, "capacity after refusals", stored.Capacity, 1)

	got, err := patchPlan(m, AppServicePlanPatch{SKUName: ptr("S1"), SKUTier: ptr("Standard"), Capacity: n(10)})
	planMustNotErr(t, err)
	planWantEqual(t, "tier", got.SKUTier, tierStandard)
	planWantEqual(t, "capacity", got.Capacity, 10)

	_, err = m.PatchAppServicePlan(context.Background(), "sub", "rg", "consumption",
		AppServicePlanPatch{SKUName: ptr("S1")})
	planWantInvalid(t, "Consumption to dedicated", err)

	_, err = m.CreateAppServicePlan(context.Background(), AppServicePlan{
		Name: "neg", Subscription: "sub", ResourceGroup: "rg", SKUName: "S1", Capacity: -1,
	})
	planWantInvalid(t, "create with negative capacity", err)
}
