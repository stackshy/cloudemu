package datafactory

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/datafactory/driver"
)

func newMock() *Mock {
	return New(config.NewOptions(config.WithAccountID("sub-123")))
}

func mustCreate(t *testing.T, m *Mock, cfg driver.FactoryConfig) *driver.Factory {
	t.Helper()

	f, created, err := m.CreateOrUpdateFactory(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateOrUpdateFactory: %v", err)
	}

	if !created {
		t.Fatalf("expected created=true on first create")
	}

	return f
}

func TestCreateFactoryComputedFields(t *testing.T) {
	m := newMock()
	f := mustCreate(t, m, driver.FactoryConfig{
		Name:          "adf1",
		ResourceGroup: "rg1",
		Location:      "eastus",
	})

	if f.ProvisioningState != driver.StateSucceeded {
		t.Errorf("provisioningState = %q, want Succeeded", f.ProvisioningState)
	}

	if f.Version != driver.Version {
		t.Errorf("version = %q, want %q", f.Version, driver.Version)
	}

	if f.CreateTime == "" {
		t.Error("createTime not set")
	}

	if f.PublicNetworkAccess != driver.PublicNetworkAccessEnabled {
		t.Errorf("publicNetworkAccess = %q, want Enabled (default)", f.PublicNetworkAccess)
	}

	wantID := "/subscriptions/sub-123/resourceGroups/rg1/providers/Microsoft.DataFactory/factories/adf1"
	if f.ID != wantID {
		t.Errorf("id = %q, want %q", f.ID, wantID)
	}
}

func TestSystemAssignedIdentityStableAcrossGets(t *testing.T) {
	m := newMock()
	mustCreate(t, m, driver.FactoryConfig{
		Name:          "adf1",
		ResourceGroup: "rg1",
		Location:      "eastus",
		Identity:      &driver.ManagedIdentity{Type: "SystemAssigned"},
	})

	first, err := m.GetFactory(context.Background(), "rg1", "adf1")
	if err != nil {
		t.Fatalf("GetFactory: %v", err)
	}

	if first.Identity == nil || first.Identity.PrincipalID == "" || first.Identity.TenantID == "" {
		t.Fatalf("system-assigned identity missing principal/tenant: %+v", first.Identity)
	}

	// The prime drift suspects: principalId, tenantId and createTime must be
	// byte-identical across repeated reads.
	for i := 0; i < 5; i++ {
		got, err := m.GetFactory(context.Background(), "rg1", "adf1")
		if err != nil {
			t.Fatalf("GetFactory #%d: %v", i, err)
		}

		if got.Identity.PrincipalID != first.Identity.PrincipalID {
			t.Errorf("principalId drifted: %q != %q", got.Identity.PrincipalID, first.Identity.PrincipalID)
		}

		if got.Identity.TenantID != first.Identity.TenantID {
			t.Errorf("tenantId drifted: %q != %q", got.Identity.TenantID, first.Identity.TenantID)
		}

		if got.CreateTime != first.CreateTime {
			t.Errorf("createTime drifted: %q != %q", got.CreateTime, first.CreateTime)
		}
	}
}

func TestPrincipalIDDistinctPerResourceGroup(t *testing.T) {
	m := newMock()
	id := &driver.ManagedIdentity{Type: "SystemAssigned"}

	a := mustCreate(t, m, driver.FactoryConfig{Name: "adf", ResourceGroup: "rgA", Location: "eastus", Identity: id})
	b := mustCreate(t, m, driver.FactoryConfig{Name: "adf", ResourceGroup: "rgB", Location: "eastus", Identity: id})

	if a.Identity.PrincipalID == b.Identity.PrincipalID {
		t.Error("same-named factories in different resource groups share a principalId")
	}
}

func TestNoneIdentityResolvesNil(t *testing.T) {
	m := newMock()
	f := mustCreate(t, m, driver.FactoryConfig{
		Name:          "adf1",
		ResourceGroup: "rg1",
		Location:      "eastus",
		Identity:      &driver.ManagedIdentity{Type: "None"},
	})

	if f.Identity != nil {
		t.Errorf("None identity should resolve to nil, got %+v", f.Identity)
	}
}

func TestUpdatePreservesCreateTimeAndID(t *testing.T) {
	m := newMock()
	first := mustCreate(t, m, driver.FactoryConfig{Name: "adf1", ResourceGroup: "rg1", Location: "eastus"})

	updated, created, err := m.CreateOrUpdateFactory(context.Background(), driver.FactoryConfig{
		Name:                "adf1",
		ResourceGroup:       "rg1",
		Location:            "eastus",
		Tags:                map[string]string{"env": "prod"},
		PublicNetworkAccess: driver.PublicNetworkAccessDisabled,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if created {
		t.Error("expected created=false on update")
	}

	if updated.CreateTime != first.CreateTime {
		t.Errorf("createTime changed on update: %q != %q", updated.CreateTime, first.CreateTime)
	}

	if updated.ID != first.ID {
		t.Errorf("id changed on update: %q != %q", updated.ID, first.ID)
	}

	if updated.PublicNetworkAccess != driver.PublicNetworkAccessDisabled {
		t.Errorf("publicNetworkAccess = %q, want Disabled", updated.PublicNetworkAccess)
	}
}

func TestPatchReplacesTags(t *testing.T) {
	m := newMock()
	mustCreate(t, m, driver.FactoryConfig{
		Name:          "adf1",
		ResourceGroup: "rg1",
		Location:      "eastus",
		Tags:          map[string]string{"a": "1", "b": "2"},
	})

	got, err := m.UpdateFactory(context.Background(), "rg1", "adf1", map[string]string{"c": "3"}, nil)
	if err != nil {
		t.Fatalf("UpdateFactory: %v", err)
	}

	if len(got.Tags) != 1 || got.Tags["c"] != "3" {
		t.Errorf("PATCH should REPLACE tags, got %+v", got.Tags)
	}
}

func TestPatchAddsIdentity(t *testing.T) {
	m := newMock()
	mustCreate(t, m, driver.FactoryConfig{Name: "adf1", ResourceGroup: "rg1", Location: "eastus"})

	got, err := m.UpdateFactory(
		context.Background(), "rg1", "adf1", nil, &driver.ManagedIdentity{Type: "SystemAssigned"},
	)
	if err != nil {
		t.Fatalf("UpdateFactory: %v", err)
	}

	if got.Identity == nil || got.Identity.PrincipalID == "" {
		t.Errorf("PATCH identity not applied: %+v", got.Identity)
	}
}

func TestGlobalParametersRoundTrip(t *testing.T) {
	m := newMock()
	mustCreate(t, m, driver.FactoryConfig{
		Name:          "adf1",
		ResourceGroup: "rg1",
		Location:      "eastus",
		GlobalParameters: map[string]driver.GlobalParameterSpec{
			"region": {Type: "String", Value: "east"},
		},
	})

	got, err := m.GetFactory(context.Background(), "rg1", "adf1")
	if err != nil {
		t.Fatalf("GetFactory: %v", err)
	}

	gp, ok := got.GlobalParameters["region"]
	if !ok || gp.Type != "String" || gp.Value != "east" {
		t.Errorf("globalParameters round-trip failed: %+v", got.GlobalParameters)
	}
}

func TestListDeterministicAndScoped(t *testing.T) {
	m := newMock()
	mustCreate(t, m, driver.FactoryConfig{Name: "z", ResourceGroup: "rg1", Location: "eastus"})
	mustCreate(t, m, driver.FactoryConfig{Name: "a", ResourceGroup: "rg1", Location: "eastus"})
	mustCreate(t, m, driver.FactoryConfig{Name: "m", ResourceGroup: "rg2", Location: "eastus"})

	all, err := m.ListFactories(context.Background())
	if err != nil {
		t.Fatalf("ListFactories: %v", err)
	}

	if len(all) != 3 {
		t.Fatalf("ListFactories len = %d, want 3", len(all))
	}

	// Ordered by ID (deterministic — no list drift).
	for i := 1; i < len(all); i++ {
		if all[i-1].ID > all[i].ID {
			t.Errorf("ListFactories not sorted by ID: %q > %q", all[i-1].ID, all[i].ID)
		}
	}

	byRG, err := m.ListFactoriesByResourceGroup(context.Background(), "rg1")
	if err != nil {
		t.Fatalf("ListFactoriesByResourceGroup: %v", err)
	}

	if len(byRG) != 2 {
		t.Errorf("rg1 list len = %d, want 2", len(byRG))
	}
}

func TestDeleteFactory(t *testing.T) {
	m := newMock()
	mustCreate(t, m, driver.FactoryConfig{Name: "adf1", ResourceGroup: "rg1", Location: "eastus"})

	if err := m.DeleteFactory(context.Background(), "rg1", "adf1"); err != nil {
		t.Fatalf("DeleteFactory: %v", err)
	}

	if _, err := m.GetFactory(context.Background(), "rg1", "adf1"); err == nil {
		t.Error("expected NotFound after delete")
	}

	if err := m.DeleteFactory(context.Background(), "rg1", "adf1"); err == nil {
		t.Error("expected NotFound deleting missing factory")
	}
}

func TestCreateValidation(t *testing.T) {
	m := newMock()

	if _, _, err := m.CreateOrUpdateFactory(context.Background(), driver.FactoryConfig{ResourceGroup: "rg1"}); err == nil {
		t.Error("expected error for missing name")
	}

	if _, _, err := m.CreateOrUpdateFactory(context.Background(), driver.FactoryConfig{Name: "adf1"}); err == nil {
		t.Error("expected error for missing resource group")
	}
}

func TestReturnedCopyIsIsolated(t *testing.T) {
	m := newMock()
	tags := map[string]string{"a": "1"}
	f := mustCreate(t, m, driver.FactoryConfig{Name: "adf1", ResourceGroup: "rg1", Location: "eastus", Tags: tags})

	f.Tags["a"] = "mutated"
	tags["a"] = "caller-mutated"

	got, err := m.GetFactory(context.Background(), "rg1", "adf1")
	if err != nil {
		t.Fatalf("GetFactory: %v", err)
	}

	if got.Tags["a"] != "1" {
		t.Errorf("store aliased a caller/returned map: got %q", got.Tags["a"])
	}
}
