package communication_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/communication"
)

func newMock() *communication.Mock {
	return communication.New(config.NewOptions())
}

func standardInput() *communication.Input {
	return &communication.Input{
		Tags:          map[string]string{"env": "dev"},
		Identity:      &communication.Identity{Type: "SystemAssigned"},
		DataLocation:  "United States",
		LinkedDomains: []string{"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Communication/emailServices/es/domains/d"},
	}
}

func TestCreateComputesStableFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", standardInput())
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	if created.HostName != "acs1.communication.azure.com" {
		t.Errorf("hostName = %q, want acs1.communication.azure.com", created.HostName)
	}

	if created.Location != "global" {
		t.Errorf("location = %q, want global", created.Location)
	}

	if created.DataLocation != "United States" {
		t.Errorf("dataLocation = %q, want United States", created.DataLocation)
	}

	if created.ImmutableResourceID == "" {
		t.Errorf("immutableResourceId not minted")
	}

	if created.PrimaryKey == "" || created.SecondaryKey == "" || created.PrimaryKey == created.SecondaryKey {
		t.Errorf("keys = %q/%q, want distinct non-empty", created.PrimaryKey, created.SecondaryKey)
	}

	if !strings.HasPrefix(created.PrimaryConnectionString(), "endpoint=https://acs1.communication.azure.com/;accesskey=") {
		t.Errorf("primary connection string = %q", created.PrimaryConnectionString())
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Errorf("system identity ids not minted: %+v", created.Identity)
	}
}

func TestGetIsStableAcrossReads(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	a, err := m.Get(ctx, "sub", "rg", "acs1")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}

	b, err := m.Get(ctx, "sub", "rg", "acs1")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}

	if a.HostName != b.HostName || a.ImmutableResourceID != b.ImmutableResourceID ||
		a.PrimaryKey != b.PrimaryKey || a.SecondaryKey != b.SecondaryKey ||
		a.PrimaryConnectionString() != b.PrimaryConnectionString() {
		t.Errorf("computed fields drifted between reads: %+v vs %+v", a, b)
	}
}

func TestUpdatePreservesComputedAndDataLocation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	first, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", standardInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// An update that (incorrectly) attempts to change the immutable dataLocation
	// and mutates tags. dataLocation must stay pinned to the create-time value.
	in := standardInput()
	in.DataLocation = "Europe"
	in.Tags = map[string]string{"env": "prod"}

	updated, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", in)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if updated.HostName != first.HostName || updated.ImmutableResourceID != first.ImmutableResourceID ||
		updated.PrimaryKey != first.PrimaryKey || updated.SecondaryKey != first.SecondaryKey {
		t.Errorf("computed fields changed on update")
	}

	if updated.DataLocation != "United States" {
		t.Errorf("dataLocation = %q, want United States (immutable)", updated.DataLocation)
	}

	if updated.Tags["env"] != "prod" {
		t.Errorf("tags not updated: %v", updated.Tags)
	}
}

func TestLocationAlwaysGlobal(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", standardInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.Location != "global" {
		t.Errorf("location = %q, want global", got.Location)
	}
}

func TestUserAssignedIdentityMintsIDs(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	uaID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ua"
	in := &communication.Input{
		DataLocation: "United States",
		Identity: &communication.Identity{
			Type:         "UserAssigned",
			UserAssigned: map[string]communication.UserAssignedValue{uaID: {}},
		},
	}

	got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	v, ok := got.Identity.UserAssigned[uaID]
	if !ok || v.PrincipalID == "" || v.ClientID == "" {
		t.Errorf("user-assigned ids not minted: %+v", got.Identity)
	}

	if got.Identity.PrincipalID != "" {
		t.Errorf("user-assigned-only identity should have no system principalId: %q", got.Identity.PrincipalID)
	}
}

func TestValidateRejectsMissingDataLocation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", &communication.Input{}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("missing dataLocation: err = %v, want InvalidArgument", err)
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

	for _, n := range []string{"a", "b"} {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", n, standardInput()); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	items, err := m.ListByResourceGroup(ctx, "sub", "rg")
	if err != nil || len(items) != 2 {
		t.Fatalf("list rg: err=%v n=%d", err, len(items))
	}

	existed, err := m.Delete(ctx, "sub", "rg", "a")
	if err != nil || !existed {
		t.Fatalf("delete a: err=%v existed=%v", err, existed)
	}

	again, err := m.Delete(ctx, "sub", "rg", "a")
	if err != nil || again {
		t.Fatalf("delete a again: err=%v existed=%v", err, again)
	}

	subItems, err := m.ListBySubscription(ctx, "sub")
	if err != nil || len(subItems) != 1 {
		t.Fatalf("list sub: err=%v n=%d", err, len(subItems))
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg1", "a", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg2", "b", standardInput()); err != nil {
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

	created, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", standardInput())
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

	got, err := restored.Get(ctx, "sub", "rg", "acs1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.HostName != created.HostName || got.PrimaryKey != created.PrimaryKey ||
		got.ImmutableResourceID != created.ImmutableResourceID || got.DataLocation != created.DataLocation {
		t.Errorf("computed fields not preserved across snapshot/restore")
	}
}

func TestDiscoverCommunication(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "acs1", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	items, err := m.DiscoverCommunication(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("discover: err=%v n=%d", err, len(items))
	}
}
