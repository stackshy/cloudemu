package loadtesting_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/loadtesting"
)

func newMock() *loadtesting.Mock {
	return loadtesting.New(config.NewOptions())
}

func sysAssigned() *loadtesting.Identity {
	return &loadtesting.Identity{Type: "SystemAssigned"}
}

func TestCreateComputesStableFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := loadtesting.Input{
		Location:    "East US",
		Description: "perf tests",
		Tags:        map[string]string{"env": "dev"},
		Identity:    sysAssigned(),
	}

	created, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "lt1", in)
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	if !strings.HasSuffix(created.DataPlaneURI, ".cnt-prod.loadtesting.azure.com") {
		t.Errorf("dataPlaneURI = %q, want ...cnt-prod.loadtesting.azure.com", created.DataPlaneURI)
	}

	if !strings.Contains(created.DataPlaneURI, ".eastus.") {
		t.Errorf("dataPlaneURI = %q, want region segment eastus", created.DataPlaneURI)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Fatalf("system-assigned identity ids not minted: %+v", created.Identity)
	}

	// Update (change description + tags) must preserve every computed field.
	upd := loadtesting.Input{
		Location:    "West US", // immutable — must be ignored
		Description: "changed",
		Tags:        map[string]string{"env": "prod"},
		Identity:    sysAssigned(),
	}

	updated, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "lt1", upd)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if updated.DataPlaneURI != created.DataPlaneURI {
		t.Errorf("dataPlaneURI drifted: %q -> %q", created.DataPlaneURI, updated.DataPlaneURI)
	}

	if updated.Identity.PrincipalID != created.Identity.PrincipalID ||
		updated.Identity.TenantID != created.Identity.TenantID {
		t.Errorf("identity ids drifted on update")
	}

	if updated.Location != "East US" {
		t.Errorf("location mutated on update: %q, want East US (immutable)", updated.Location)
	}

	if updated.Description != "changed" || updated.Tags["env"] != "prod" {
		t.Errorf("update did not apply description/tags: %+v", updated)
	}
}

func TestGetReturnsStableComputedFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "lt1",
		loadtesting.Input{Location: "eastus", Identity: sysAssigned()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := 0; i < 3; i++ {
		got, err := m.Get(ctx, "sub", "rg", "lt1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}

		if got.DataPlaneURI != created.DataPlaneURI {
			t.Errorf("get #%d dataPlaneURI = %q, want %q", i, got.DataPlaneURI, created.DataPlaneURI)
		}

		if got.Identity.PrincipalID != created.Identity.PrincipalID {
			t.Errorf("get #%d principalId drifted", i)
		}
	}
}

func TestReturnedCopiesAreIsolated(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "lt1",
		loadtesting.Input{Location: "eastus", Tags: map[string]string{"a": "1"}})

	created.Tags["a"] = "mutated"

	got, _ := m.Get(ctx, "sub", "rg", "lt1")
	if got.Tags["a"] != "1" {
		t.Errorf("store aliased returned tags: got %q", got.Tags["a"])
	}
}

func TestNoIdentityStaysNil(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "lt1",
		loadtesting.Input{Location: "eastus", Identity: &loadtesting.Identity{Type: "None"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.Identity != nil {
		t.Errorf("None identity should resolve to nil, got %+v", got.Identity)
	}
}

func TestUserAssignedIdentityMintsIDs(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	uaID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/uai1"
	in := loadtesting.Input{
		Location: "eastus",
		Identity: &loadtesting.Identity{
			Type:         "UserAssigned",
			UserAssigned: map[string]loadtesting.UserAssignedValue{uaID: {}},
		},
	}

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "lt1", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	v, ok := got.Identity.UserAssigned[uaID]
	if !ok || v.PrincipalID == "" || v.ClientID == "" {
		t.Fatalf("user-assigned ids not minted: %+v", got.Identity)
	}

	// System-assigned ids are not minted for a UserAssigned-only identity.
	if got.Identity.PrincipalID != "" {
		t.Errorf("UserAssigned-only identity should not carry a system principalId")
	}
}

func TestEncryptionRoundTrips(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := loadtesting.Input{
		Location: "eastus",
		Encryption: &loadtesting.Encryption{
			KeyURL:   "https://vault.vault.azure.net/keys/k/1",
			Identity: &loadtesting.EncryptionIdentity{Type: "SystemAssigned"},
		},
	}

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "lt1", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.Encryption == nil || got.Encryption.KeyURL != in.Encryption.KeyURL {
		t.Fatalf("encryption not stored: %+v", got.Encryption)
	}
}

func TestListAndDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg", "a", loadtesting.Input{Location: "eastus"})
	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg", "b", loadtesting.Input{Location: "eastus"})
	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg2", "c", loadtesting.Input{Location: "eastus"})

	byRG, _ := m.ListByResourceGroup(ctx, "sub", "rg")
	if len(byRG) != 2 || byRG[0].Name != "a" || byRG[1].Name != "b" {
		t.Fatalf("ListByResourceGroup = %+v, want [a b]", byRG)
	}

	bySub, _ := m.ListBySubscription(ctx, "sub")
	if len(bySub) != 3 {
		t.Fatalf("ListBySubscription len = %d, want 3", len(bySub))
	}

	existed, _ := m.Delete(ctx, "sub", "rg", "a")
	if !existed {
		t.Errorf("Delete existing = false")
	}

	again, _ := m.Delete(ctx, "sub", "rg", "a")
	if again {
		t.Errorf("Delete missing = true, want idempotent false")
	}
}

func TestGetNotFound(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	_, err := m.Get(ctx, "sub", "rg", "missing")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("Get missing err = %v, want NotFound", err)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	_, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "lt1", loadtesting.Input{})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing location err = %v, want InvalidArgument", err)
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg", "a", loadtesting.Input{Location: "eastus"})
	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg2", "b", loadtesting.Input{Location: "eastus"})

	if err := m.PurgeResourceGroup(ctx, "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	left, _ := m.ListBySubscription(ctx, "sub")
	if len(left) != 1 || left[0].Name != "b" {
		t.Fatalf("after purge = %+v, want only [b]", left)
	}
}
