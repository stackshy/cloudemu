package purview_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/purview"
)

func newMock() *purview.Mock {
	return purview.New(config.NewOptions())
}

func ptr(s string) *string { return &s }

func standardInput() *purview.Input {
	return &purview.Input{
		Tags:                 map[string]string{"env": "dev"},
		Identity:             &purview.Identity{Type: "SystemAssigned"},
		PublicNetworkAccess:  ptr("Enabled"),
		ManagedEventHubState: ptr("Enabled"),
	}
}

func createStd(t *testing.T, m *purview.Mock) purview.Account {
	t.Helper()

	s, isNew, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "pv1", "East US", standardInput())
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	return s
}

func TestCreateComputesStableFields(t *testing.T) {
	m := newMock()
	created := createStd(t, m)

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	if created.Endpoints == nil ||
		created.Endpoints.Catalog != "https://pv1.purview.azure.com/catalog" ||
		created.Endpoints.Guardian != "https://pv1.purview.azure.com/guardian" ||
		created.Endpoints.Scan != "https://pv1.purview.azure.com/scan" {
		t.Errorf("endpoints = %+v", created.Endpoints)
	}

	if created.ManagedResourceGroupName != "managed-rg-pv1" {
		t.Errorf("managedResourceGroupName = %q, want managed-rg-pv1", created.ManagedResourceGroupName)
	}

	if created.ManagedResources == nil ||
		created.ManagedResources.ResourceGroup != "/subscriptions/sub/resourceGroups/managed-rg-pv1" ||
		!strings.HasPrefix(created.ManagedResources.StorageAccount,
			"/subscriptions/sub/resourceGroups/managed-rg-pv1/providers/Microsoft.Storage/storageAccounts/scan") ||
		!strings.HasPrefix(created.ManagedResources.EventHubNamespace,
			"/subscriptions/sub/resourceGroups/managed-rg-pv1/providers/Microsoft.EventHub/namespaces/Atlas-") {
		t.Errorf("managedResources = %+v", created.ManagedResources)
	}

	if created.Sku == nil || created.Sku.Name != "Standard" || created.Sku.Capacity != 1 {
		t.Errorf("sku = %+v, want {Standard 1}", created.Sku)
	}

	if created.PublicNetworkAccess != "Enabled" || created.ManagedEventHubState != "Enabled" {
		t.Errorf("toggles = %q/%q", created.PublicNetworkAccess, created.ManagedEventHubState)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Errorf("system identity ids not minted: %+v", created.Identity)
	}
}

func TestDefaults(t *testing.T) {
	s, _, err := newMock().CreateOrUpdate(context.Background(), "sub", "rg", "pv0", "eastus", &purview.Input{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if s.PublicNetworkAccess != "Enabled" {
		t.Errorf("default publicNetworkAccess = %q, want Enabled", s.PublicNetworkAccess)
	}

	if s.ManagedEventHubState != "Disabled" {
		t.Errorf("default managedEventHubState = %q, want Disabled", s.ManagedEventHubState)
	}

	if s.Sku == nil || s.Sku.Name != "Standard" || s.Sku.Capacity != 1 {
		t.Errorf("default sku = %+v", s.Sku)
	}
}

func TestExplicitManagedResourceGroupName(t *testing.T) {
	s, _, err := newMock().CreateOrUpdate(context.Background(), "sub", "rg", "pv9", "eastus", &purview.Input{
		ManagedResourceGroupName: ptr("custom-mrg"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if s.ManagedResourceGroupName != "custom-mrg" {
		t.Errorf("managedResourceGroupName = %q, want custom-mrg", s.ManagedResourceGroupName)
	}

	if s.ManagedResources.ResourceGroup != "/subscriptions/sub/resourceGroups/custom-mrg" {
		t.Errorf("managedResources.resourceGroup = %q", s.ManagedResources.ResourceGroup)
	}
}

func TestListKeysStable(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	k1, err := m.ListKeys(ctx, "sub", "rg", "pv1")
	if err != nil {
		t.Fatalf("listkeys: %v", err)
	}

	k2, _ := m.ListKeys(ctx, "sub", "rg", "pv1")
	if k1 != k2 {
		t.Errorf("keys drifted: %+v vs %+v", k1, k2)
	}

	if !strings.HasPrefix(k1.AtlasKafkaPrimaryEndpoint, "Endpoint=sb://atlas-") ||
		k1.AtlasKafkaPrimaryEndpoint == k1.AtlasKafkaSecondaryEndpoint {
		t.Errorf("kafka endpoints = %+v", k1)
	}

	if _, err := m.ListKeys(ctx, "sub", "rg", "missing"); !cerrors.IsNotFound(err) {
		t.Errorf("listkeys missing err = %v, want NotFound", err)
	}
}

func TestGetStableAcrossReadsAndUpdates(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	created := createStd(t, m)

	got1, err := m.Get(ctx, "sub", "rg", "pv1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// A tag-only PATCH must not move any computed field.
	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "pv1", "East US", &purview.Input{
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	for _, tc := range []struct {
		name    string
		a, b, c string
	}{
		{"catalog", created.Endpoints.Catalog, got1.Endpoints.Catalog, updated.Endpoints.Catalog},
		{"storageAccount", created.ManagedResources.StorageAccount, got1.ManagedResources.StorageAccount, updated.ManagedResources.StorageAccount},
		{"eventHubNamespace", created.ManagedResources.EventHubNamespace, got1.ManagedResources.EventHubNamespace, updated.ManagedResources.EventHubNamespace},
		{"managedRG", created.ManagedResourceGroupName, got1.ManagedResourceGroupName, updated.ManagedResourceGroupName},
		{"principalId", created.Identity.PrincipalID, got1.Identity.PrincipalID, updated.Identity.PrincipalID},
		{"tenantId", created.Identity.TenantID, got1.Identity.TenantID, updated.Identity.TenantID},
	} {
		if tc.a != tc.b || tc.b != tc.c {
			t.Errorf("%s drifted: create=%q get=%q update=%q", tc.name, tc.a, tc.b, tc.c)
		}
	}

	// PATCH replaced tags and preserved identity + toggles (omitted).
	if updated.Tags["env"] != "prod" || len(updated.Tags) != 1 {
		t.Errorf("tags not replaced: %v", updated.Tags)
	}

	if updated.Identity == nil {
		t.Errorf("identity wiped by tag-only patch")
	}

	if updated.ManagedEventHubState != "Enabled" {
		t.Errorf("managedEventHubState drifted on tag-only patch: %q", updated.ManagedEventHubState)
	}
}

func TestPatchMergesFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	// PATCH only the public-network toggle → managedEventHubState preserved.
	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "pv1", "East US", &purview.Input{
		PublicNetworkAccess: ptr("Disabled"),
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if updated.PublicNetworkAccess != "Disabled" {
		t.Errorf("publicNetworkAccess = %q, want Disabled", updated.PublicNetworkAccess)
	}

	if updated.ManagedEventHubState != "Enabled" {
		t.Errorf("managedEventHubState not preserved: %q", updated.ManagedEventHubState)
	}
}

func TestExplicitNoneClearsIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "pv1", "East US", &purview.Input{
		Identity: &purview.Identity{Type: "None"},
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if updated.Identity != nil {
		t.Errorf("identity = %+v, want nil after None", updated.Identity)
	}
}

func TestUserAssignedIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	uaID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/uai1"
	s, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "pv2", "eastus", &purview.Input{
		Identity: &purview.Identity{
			Type:         "UserAssigned",
			UserAssigned: map[string]purview.UserAssignedValue{uaID: {}},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	v, ok := s.Identity.UserAssigned[uaID]
	if !ok || v.PrincipalID == "" || v.ClientID == "" {
		t.Errorf("user-assigned ids not minted: %+v", s.Identity.UserAssigned)
	}

	if s.Identity.PrincipalID != "" || s.Identity.TenantID != "" {
		t.Errorf("unexpected system ids on user-assigned identity: %+v", s.Identity)
	}
}

func TestGetNotFound(t *testing.T) {
	_, err := newMock().Get(context.Background(), "sub", "rg", "missing")
	if !cerrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	existed, err := m.Delete(ctx, "sub", "rg", "pv1")
	if err != nil || !existed {
		t.Fatalf("first delete: err=%v existed=%v", err, existed)
	}

	existed, err = m.Delete(ctx, "sub", "rg", "pv1")
	if err != nil || existed {
		t.Fatalf("second delete: err=%v existed=%v, want existed=false", err, existed)
	}
}

func TestListAndPurge(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	for _, n := range []string{"a", "b"} {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", n, "eastus", &purview.Input{}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg2", "c", "eastus", &purview.Input{}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	byRG, _ := m.ListByResourceGroup(ctx, "sub", "rg")
	if len(byRG) != 2 {
		t.Errorf("ListByResourceGroup = %d, want 2", len(byRG))
	}

	bySub, _ := m.ListBySubscription(ctx, "sub")
	if len(bySub) != 3 {
		t.Errorf("ListBySubscription = %d, want 3", len(bySub))
	}

	if err := m.PurgeResourceGroup(ctx, "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	bySub, _ = m.ListBySubscription(ctx, "sub")
	if len(bySub) != 1 {
		t.Errorf("after purge ListBySubscription = %d, want 1", len(bySub))
	}
}

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	created := createStd(t, m)

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := restored.Get(ctx, "sub", "rg", "pv1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.Endpoints.Catalog != created.Endpoints.Catalog ||
		got.ManagedResources.StorageAccount != created.ManagedResources.StorageAccount ||
		got.Identity.PrincipalID != created.Identity.PrincipalID {
		t.Errorf("restore lost stable fields: %+v", got)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "", "rg", "n", "eastus", &purview.Input{}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("empty sub err = %v, want InvalidArgument", err)
	}
}
