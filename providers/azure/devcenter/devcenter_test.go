package devcenter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/devcenter"
)

func newMock() *devcenter.Mock {
	return devcenter.New(config.NewOptions())
}

func ptr(s string) *string { return &s }

func standardInput() *devcenter.Input {
	return &devcenter.Input{
		Tags:                        map[string]string{"env": "dev"},
		Identity:                    &devcenter.Identity{Type: "SystemAssigned"},
		DisplayName:                 ptr("Contoso Dev Center"),
		CatalogItemSyncEnableStatus: ptr("Enabled"),
	}
}

func createStd(t *testing.T, m *devcenter.Mock) devcenter.DevCenter {
	t.Helper()

	s, isNew, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "dc1", "Central US", standardInput())
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

	// devCenterUri: https://<guid>-<name>.<region>.devcenter.azure.com
	if !strings.HasPrefix(created.DevCenterURI, "https://") ||
		!strings.HasSuffix(created.DevCenterURI, "-dc1.centralus.devcenter.azure.com") {
		t.Errorf("devCenterUri = %q, want https://<guid>-dc1.centralus.devcenter.azure.com", created.DevCenterURI)
	}

	if created.DisplayName != "Contoso Dev Center" {
		t.Errorf("displayName = %q", created.DisplayName)
	}

	if created.CatalogItemSyncEnableStatus != "Enabled" {
		t.Errorf("catalogItemSyncEnableStatus = %q, want Enabled", created.CatalogItemSyncEnableStatus)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Errorf("system identity ids not minted: %+v", created.Identity)
	}
}

func TestDefaultCatalogSyncDisabled(t *testing.T) {
	s, _, err := newMock().CreateOrUpdate(context.Background(), "sub", "rg", "dc0", "eastus", &devcenter.Input{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if s.CatalogItemSyncEnableStatus != "Disabled" {
		t.Errorf("default catalogItemSyncEnableStatus = %q, want Disabled", s.CatalogItemSyncEnableStatus)
	}
}

func TestGetStableAcrossReadsAndUpdates(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	created := createStd(t, m)

	got1, err := m.Get(ctx, "sub", "rg", "dc1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// A tag-only PATCH must not move any computed field.
	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dc1", "Central US", &devcenter.Input{
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	for _, tc := range []struct {
		name    string
		a, b, c string
	}{
		{"devCenterUri", created.DevCenterURI, got1.DevCenterURI, updated.DevCenterURI},
		{"principalId", created.Identity.PrincipalID, got1.Identity.PrincipalID, updated.Identity.PrincipalID},
		{"tenantId", created.Identity.TenantID, got1.Identity.TenantID, updated.Identity.TenantID},
	} {
		if tc.a != tc.b || tc.b != tc.c {
			t.Errorf("%s drifted: create=%q get=%q update=%q", tc.name, tc.a, tc.b, tc.c)
		}
	}

	// PATCH replaced tags and preserved identity + displayName (omitted).
	if updated.Tags["env"] != "prod" || len(updated.Tags) != 1 {
		t.Errorf("tags not replaced: %v", updated.Tags)
	}

	if updated.Identity == nil {
		t.Errorf("identity wiped by tag-only patch")
	}

	if updated.DisplayName != "Contoso Dev Center" {
		t.Errorf("displayName drifted on tag-only patch: %q", updated.DisplayName)
	}

	if updated.CatalogItemSyncEnableStatus != "Enabled" {
		t.Errorf("catalog sync drifted on tag-only patch: %q", updated.CatalogItemSyncEnableStatus)
	}
}

func TestPatchMergesFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	// PATCH only the catalog toggle → displayName preserved.
	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dc1", "Central US", &devcenter.Input{
		CatalogItemSyncEnableStatus: ptr("Disabled"),
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if updated.CatalogItemSyncEnableStatus != "Disabled" {
		t.Errorf("catalogItemSyncEnableStatus = %q, want Disabled", updated.CatalogItemSyncEnableStatus)
	}

	if updated.DisplayName != "Contoso Dev Center" {
		t.Errorf("displayName not preserved: %q", updated.DisplayName)
	}
}

func TestExplicitNoneClearsIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dc1", "Central US", &devcenter.Input{
		Identity: &devcenter.Identity{Type: "None"},
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
	s, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dc2", "eastus", &devcenter.Input{
		Identity: &devcenter.Identity{
			Type:         "UserAssigned",
			UserAssigned: map[string]devcenter.UserAssignedValue{uaID: {}},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	v, ok := s.Identity.UserAssigned[uaID]
	if !ok || v.PrincipalID == "" || v.ClientID == "" {
		t.Errorf("user-assigned ids not minted: %+v", s.Identity.UserAssigned)
	}

	// A user-assigned-only identity mints no system principal/tenant ids.
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

	existed, err := m.Delete(ctx, "sub", "rg", "dc1")
	if err != nil || !existed {
		t.Fatalf("first delete: err=%v existed=%v", err, existed)
	}

	existed, err = m.Delete(ctx, "sub", "rg", "dc1")
	if err != nil || existed {
		t.Fatalf("second delete: err=%v existed=%v, want existed=false", err, existed)
	}
}

func TestListAndPurge(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	for _, n := range []string{"a", "b"} {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", n, "eastus", &devcenter.Input{}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg2", "c", "eastus", &devcenter.Input{}); err != nil {
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

	got, err := restored.Get(ctx, "sub", "rg", "dc1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.DevCenterURI != created.DevCenterURI || got.Identity.PrincipalID != created.Identity.PrincipalID {
		t.Errorf("restore lost stable fields: uri %q vs %q", got.DevCenterURI, created.DevCenterURI)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "", "rg", "n", "eastus", &devcenter.Input{}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("empty sub err = %v, want InvalidArgument", err)
	}
}
