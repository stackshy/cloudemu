package digitaltwins_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/digitaltwins"
)

func newMock() *digitaltwins.Mock {
	return digitaltwins.New(config.NewOptions())
}

func sysAssigned() *digitaltwins.Identity {
	return &digitaltwins.Identity{Type: "SystemAssigned"}
}

func TestCreateComputesStableFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := digitaltwins.Input{
		Location: "East US",
		Tags:     map[string]string{"env": "dev"},
		Identity: sysAssigned(),
	}

	created, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1", in)
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	if created.HostName != "dt1.api.eastus.digitaltwins.azure.net" {
		t.Errorf("hostName = %q, want dt1.api.eastus.digitaltwins.azure.net", created.HostName)
	}

	if created.PublicNetworkAccess != "Enabled" {
		t.Errorf("publicNetworkAccess = %q, want Enabled (default)", created.PublicNetworkAccess)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Fatalf("system-assigned identity ids not minted: %+v", created.Identity)
	}

	if created.CreatedTime == "" {
		t.Errorf("createdTime not stamped")
	}

	// Update (change tags) must preserve every computed field.
	upd := digitaltwins.Input{
		Location: "West US", // immutable — must be ignored
		Tags:     map[string]string{"env": "prod"},
		Identity: sysAssigned(),
	}

	updated, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1", upd)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if updated.HostName != created.HostName {
		t.Errorf("hostName drifted: %q -> %q", created.HostName, updated.HostName)
	}

	if updated.Identity.PrincipalID != created.Identity.PrincipalID ||
		updated.Identity.TenantID != created.Identity.TenantID {
		t.Errorf("identity ids drifted on update")
	}

	if updated.Location != "East US" {
		t.Errorf("location mutated on update: %q, want East US (immutable)", updated.Location)
	}

	if updated.CreatedTime != created.CreatedTime {
		t.Errorf("createdTime drifted on update: %q -> %q", created.CreatedTime, updated.CreatedTime)
	}

	if updated.Tags["env"] != "prod" {
		t.Errorf("update did not apply tags: %+v", updated.Tags)
	}
}

func TestGetReturnsStableComputedFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1",
		digitaltwins.Input{Location: "eastus", Identity: sysAssigned()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := 0; i < 3; i++ {
		got, err := m.Get(ctx, "sub", "rg", "dt1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}

		if got.HostName != created.HostName {
			t.Errorf("get #%d hostName = %q, want %q", i, got.HostName, created.HostName)
		}

		if got.Identity.PrincipalID != created.Identity.PrincipalID {
			t.Errorf("get #%d principalId drifted", i)
		}

		if got.CreatedTime != created.CreatedTime {
			t.Errorf("get #%d createdTime drifted", i)
		}
	}
}

func TestDeterministicClockTimestamps(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := digitaltwins.New(config.NewOptions(config.WithClock(config.NewFakeClock(at))))

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1", digitaltwins.Input{Location: "eastus"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.CreatedTime != at.Format(time.RFC3339Nano) {
		t.Errorf("createdTime = %q, want %q", got.CreatedTime, at.Format(time.RFC3339Nano))
	}
}

func TestReturnedCopiesAreIsolated(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "dt1",
		digitaltwins.Input{Location: "eastus", Tags: map[string]string{"a": "1"}})

	created.Tags["a"] = "mutated"

	got, _ := m.Get(ctx, "sub", "rg", "dt1")
	if got.Tags["a"] != "1" {
		t.Errorf("store aliased returned tags: got %q", got.Tags["a"])
	}
}

func TestNoIdentityStaysNil(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1",
		digitaltwins.Input{Location: "eastus", Identity: &digitaltwins.Identity{Type: "None"}})
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
	in := digitaltwins.Input{
		Location: "eastus",
		Identity: &digitaltwins.Identity{
			Type:         "UserAssigned",
			UserAssigned: map[string]digitaltwins.UserAssignedValue{uaID: {}},
		},
	}

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1", in)
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

func TestPublicNetworkAccessRoundTrips(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1",
		digitaltwins.Input{Location: "eastus", PublicNetworkAccess: "Disabled"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.PublicNetworkAccess != "Disabled" {
		t.Errorf("publicNetworkAccess = %q, want Disabled", got.PublicNetworkAccess)
	}
}

func TestListAndDelete(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg", "a", digitaltwins.Input{Location: "eastus"})
	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg", "b", digitaltwins.Input{Location: "eastus"})
	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg2", "c", digitaltwins.Input{Location: "eastus"})

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

	_, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "dt1", digitaltwins.Input{})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing location err = %v, want InvalidArgument", err)
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg", "a", digitaltwins.Input{Location: "eastus"})
	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg2", "b", digitaltwins.Input{Location: "eastus"})

	if err := m.PurgeResourceGroup(ctx, "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	left, _ := m.ListBySubscription(ctx, "sub")
	if len(left) != 1 || left[0].Name != "b" {
		t.Fatalf("after purge = %+v, want only [b]", left)
	}
}

func TestDiscoverInstances(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg", "a", digitaltwins.Input{Location: "eastus"})
	_, _, _ = m.CreateOrUpdate(ctx, "sub", "rg2", "b", digitaltwins.Input{Location: "westus"})

	all, err := m.DiscoverInstances(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("DiscoverInstances len = %d, want 2", len(all))
	}

	if !strings.HasPrefix(all[0].ARMID(), "/subscriptions/sub/") {
		t.Errorf("unexpected ARM id: %q", all[0].ARMID())
	}
}
