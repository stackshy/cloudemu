package appconfiguration_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/appconfiguration"
)

func newMock() *appconfiguration.Mock {
	return appconfiguration.New(config.NewOptions())
}

func boolPtr(b bool) *bool { return &b }

func standardInput() *appconfiguration.Input {
	return &appconfiguration.Input{
		Location:            "East US",
		Tags:                map[string]string{"env": "dev"},
		Sku:                 &appconfiguration.Sku{Name: "standard"},
		Identity:            &appconfiguration.Identity{Type: "SystemAssigned"},
		DisableLocalAuth:    boolPtr(false),
		PublicNetworkAccess: "Enabled",
	}
}

func TestCreateComputesStableFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", standardInput())
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	if created.Endpoint != "https://store1.azconfig.io" {
		t.Errorf("endpoint = %q, want https://store1.azconfig.io", created.Endpoint)
	}

	if len(created.Keys) != 4 {
		t.Fatalf("keys = %d, want 4", len(created.Keys))
	}

	wantNames := []string{"Primary", "Secondary", "Primary Read Only", "Secondary Read Only"}
	wantRO := []bool{false, false, true, true}

	for i, k := range created.Keys {
		if k.Name != wantNames[i] {
			t.Errorf("key[%d] name = %q, want %q", i, k.Name, wantNames[i])
		}

		if k.ReadOnly != wantRO[i] {
			t.Errorf("key[%d] readOnly = %v, want %v", i, k.ReadOnly, wantRO[i])
		}

		if k.ID == "" || k.Value == "" {
			t.Errorf("key[%d] id/value empty", i)
		}

		cs := created.ConnectionString(&created.Keys[i])
		want := "Endpoint=https://store1.azconfig.io;Id=" + k.ID + ";Secret=" + k.Value
		if cs != want {
			t.Errorf("key[%d] connectionString = %q, want %q", i, cs, want)
		}
	}

	if created.Keys[0].ID == created.Keys[1].ID {
		t.Errorf("primary and secondary ids must differ")
	}

	if created.Sku == nil || created.Sku.Name != "standard" {
		t.Errorf("sku echoed wrong: %+v", created.Sku)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Errorf("system identity ids not minted: %+v", created.Identity)
	}
}

func TestGetIsStableAcrossReads(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	a, err := m.Get(ctx, "sub", "rg", "store1")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}

	b, err := m.Get(ctx, "sub", "rg", "store1")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}

	if a.Endpoint != b.Endpoint || a.Keys[0].ID != b.Keys[0].ID ||
		a.Keys[0].Value != b.Keys[0].Value || a.CreationDate != b.CreationDate ||
		a.Identity.PrincipalID != b.Identity.PrincipalID {
		t.Errorf("computed fields drifted between reads")
	}
}

func TestUpdatePreservesComputedFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	first, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", standardInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	in := standardInput()
	in.Sku = &appconfiguration.Sku{Name: "premium"}
	in.Tags = map[string]string{"env": "prod"}

	updated, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", in)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if updated.Endpoint != first.Endpoint || updated.Keys[0].ID != first.Keys[0].ID ||
		updated.Keys[0].Value != first.Keys[0].Value || updated.CreationDate != first.CreationDate ||
		updated.Identity.PrincipalID != first.Identity.PrincipalID {
		t.Errorf("computed fields changed on update")
	}

	if updated.Sku.Name != "premium" {
		t.Errorf("sku = %q, want premium", updated.Sku.Name)
	}

	if updated.Tags["env"] != "prod" {
		t.Errorf("tags not updated: %v", updated.Tags)
	}
}

func TestLocationImmutableIsPreserved(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := m.Get(ctx, "sub", "rg", "store1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Location != "East US" {
		t.Errorf("location = %q, want East US", got.Location)
	}
}

func TestIdentityNoneResolvesNil(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	in := standardInput()
	in.Identity = &appconfiguration.Identity{Type: "None"}

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.Identity != nil {
		t.Errorf("identity None should resolve to nil, got %+v", got.Identity)
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", &appconfiguration.Input{}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("missing location: err = %v, want InvalidArgument", err)
	}
}

func TestGetNotFound(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, err := m.Get(ctx, "sub", "rg", "nope"); !cerrors.IsNotFound(err) {
		t.Errorf("get missing: err = %v, want NotFound", err)
	}
}

func TestDeleteAndList(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	for _, n := range []string{"aaaaa", "bbbbb"} {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", n, standardInput()); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	items, err := m.ListByResourceGroup(ctx, "sub", "rg")
	if err != nil || len(items) != 2 {
		t.Fatalf("list rg: err=%v n=%d", err, len(items))
	}

	existed, err := m.Delete(ctx, "sub", "rg", "aaaaa")
	if err != nil || !existed {
		t.Fatalf("delete: err=%v existed=%v", err, existed)
	}

	again, err := m.Delete(ctx, "sub", "rg", "aaaaa")
	if err != nil || again {
		t.Fatalf("delete again: err=%v existed=%v", err, again)
	}

	subItems, err := m.ListBySubscription(ctx, "sub")
	if err != nil || len(subItems) != 1 {
		t.Fatalf("list sub: err=%v n=%d", err, len(subItems))
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg1", "aaaaa", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg2", "bbbbb", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.PurgeResourceGroup(ctx, "sub", "rg1"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	left, err := m.ListBySubscription(ctx, "sub")
	if err != nil || len(left) != 1 || left[0].ResourceGroup != "rg2" {
		t.Fatalf("after purge: err=%v items=%+v", err, left)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", standardInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := restored.Get(ctx, "sub", "rg", "store1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.Endpoint != created.Endpoint || got.Keys[0].ID != created.Keys[0].ID ||
		got.Keys[0].Value != created.Keys[0].Value || got.CreationDate != created.CreationDate {
		t.Errorf("computed fields not preserved across snapshot/restore")
	}
}

func TestDiscoverConfigurationStores(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "store1", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	items, err := m.DiscoverConfigurationStores(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("discover: err=%v n=%d", err, len(items))
	}
}
