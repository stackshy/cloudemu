package managedidentity_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/managedidentity"
)

func newMock() *managedidentity.Mock {
	return managedidentity.New(config.NewOptions())
}

func TestCreateMintsStableIDs(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "id", managedidentity.Input{Location: "eastus"})
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	if created.ClientID == "" || created.PrincipalID == "" || created.TenantID == "" {
		t.Fatalf("empty ids: %+v", created)
	}

	// Update preserves the minted ids and mutates location.
	updated, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "id", managedidentity.Input{Location: "westus"})
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if updated.ClientID != created.ClientID ||
		updated.PrincipalID != created.PrincipalID ||
		updated.TenantID != created.TenantID {
		t.Fatalf("update changed ids: %+v vs %+v", updated, created)
	}

	if updated.Location != "westus" {
		t.Errorf("location = %q, want westus", updated.Location)
	}

	got, err := m.Get(ctx, "sub", "rg", "id")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.PrincipalID != created.PrincipalID {
		t.Errorf("get principalId = %q, want %q", got.PrincipalID, created.PrincipalID)
	}
}

func TestTenantSharedAcrossIdentities(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	a, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "a", managedidentity.Input{})
	b, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "b", managedidentity.Input{})

	if a.TenantID != b.TenantID {
		t.Errorf("tenant differs: %q vs %q", a.TenantID, b.TenantID)
	}

	if a.ClientID == b.ClientID || a.PrincipalID == b.PrincipalID {
		t.Errorf("client/principal ids collided across identities")
	}
}

func TestGetNotFound(t *testing.T) {
	_, err := newMock().Get(context.Background(), "sub", "rg", "nope")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	m.CreateOrUpdate(ctx, "sub", "rg", "id", managedidentity.Input{}) //nolint:errcheck // seeded

	existed, err := m.Delete(ctx, "sub", "rg", "id")
	if err != nil || !existed {
		t.Fatalf("delete: err=%v existed=%v", err, existed)
	}

	existed, err = m.Delete(ctx, "sub", "rg", "id")
	if err != nil || existed {
		t.Fatalf("second delete: err=%v existed=%v", err, existed)
	}
}

func TestListScopes(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	m.CreateOrUpdate(ctx, "sub", "rg-a", "a1", managedidentity.Input{}) //nolint:errcheck // seeded
	m.CreateOrUpdate(ctx, "sub", "rg-a", "a2", managedidentity.Input{}) //nolint:errcheck // seeded
	m.CreateOrUpdate(ctx, "sub", "rg-b", "b1", managedidentity.Input{}) //nolint:errcheck // seeded

	byRG, _ := m.ListByResourceGroup(ctx, "sub", "rg-a")
	if len(byRG) != 2 || byRG[0].Name != "a1" || byRG[1].Name != "a2" {
		t.Fatalf("ListByResourceGroup = %+v, want a1,a2 sorted", byRG)
	}

	bySub, _ := m.ListBySubscription(ctx, "sub")
	if len(bySub) != 3 {
		t.Fatalf("ListBySubscription len = %d, want 3", len(bySub))
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	m.CreateOrUpdate(ctx, "sub", "rg-a", "a1", managedidentity.Input{}) //nolint:errcheck // seeded
	m.CreateOrUpdate(ctx, "sub", "rg-b", "b1", managedidentity.Input{}) //nolint:errcheck // seeded

	if err := m.PurgeResourceGroup(ctx, "sub", "rg-a"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if got, _ := m.ListByResourceGroup(ctx, "sub", "rg-a"); len(got) != 0 {
		t.Errorf("rg-a not purged: %+v", got)
	}

	if got, _ := m.ListByResourceGroup(ctx, "sub", "rg-b"); len(got) != 1 {
		t.Errorf("rg-b wrongly purged: %+v", got)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	created, _, _ := src.CreateOrUpdate(ctx, "sub", "rg", "id", managedidentity.Input{
		Location: "eastus",
		Tags:     map[string]string{"env": "prod"},
	})

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.Get(ctx, "sub", "rg", "id")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.PrincipalID != created.PrincipalID || got.ClientID != created.ClientID || got.TenantID != created.TenantID {
		t.Fatalf("restore changed ids: %+v vs %+v", got, created)
	}

	if got.Location != "eastus" || got.Tags["env"] != "prod" {
		t.Errorf("restore lost fields: %+v", got)
	}
}
