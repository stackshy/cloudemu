package signalr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/signalr"
)

func newMock() *signalr.Mock {
	return signalr.New(config.NewOptions())
}

func standardInput() *signalr.Input {
	return &signalr.Input{
		Location: "East US",
		Tags:     map[string]string{"env": "dev"},
		Sku:      &signalr.Sku{Name: "Standard_S1", Capacity: 1},
		Identity: &signalr.Identity{Type: "SystemAssigned"},
		Cors:     &signalr.Cors{AllowedOrigins: []string{"https://example.com"}},
	}
}

func TestCreateComputesStableFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	created, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig1", standardInput())
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	if created.HostName != "sig1.service.signalr.net" {
		t.Errorf("hostName = %q, want sig1.service.signalr.net", created.HostName)
	}

	if created.PublicPort != 443 || created.ServerPort != 443 {
		t.Errorf("ports = %d/%d, want 443/443", created.PublicPort, created.ServerPort)
	}

	if created.ExternalIP == "" || !strings.HasPrefix(created.ExternalIP, "20.") {
		t.Errorf("externalIP = %q, want 20.x.x.x", created.ExternalIP)
	}

	if created.PrimaryKey == "" || created.SecondaryKey == "" || created.PrimaryKey == created.SecondaryKey {
		t.Errorf("keys = %q/%q, want distinct non-empty", created.PrimaryKey, created.SecondaryKey)
	}

	if created.Sku == nil || created.Sku.Tier != "Standard" || created.Sku.Size != "S1" {
		t.Errorf("sku tier/size derivation wrong: %+v", created.Sku)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Errorf("system identity ids not minted: %+v", created.Identity)
	}

	if created.ServerlessTimeout != 30 {
		t.Errorf("serverlessTimeout = %d, want default 30", created.ServerlessTimeout)
	}
}

func TestGetIsStableAcrossReads(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig1", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	a, err := m.Get(ctx, "sub", "rg", "sig1")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}

	b, err := m.Get(ctx, "sub", "rg", "sig1")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}

	if a.HostName != b.HostName || a.ExternalIP != b.ExternalIP ||
		a.PrimaryKey != b.PrimaryKey || a.SecondaryKey != b.SecondaryKey ||
		a.PrimaryConnectionString() != b.PrimaryConnectionString() {
		t.Errorf("computed fields drifted between reads: %+v vs %+v", a, b)
	}
}

func TestUpdatePreservesComputedFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	first, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig1", standardInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	in := standardInput()
	in.Sku = &signalr.Sku{Name: "Standard_S1", Capacity: 5}
	in.Tags = map[string]string{"env": "prod"}

	updated, isNew, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig1", in)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if updated.HostName != first.HostName || updated.ExternalIP != first.ExternalIP ||
		updated.PrimaryKey != first.PrimaryKey || updated.SecondaryKey != first.SecondaryKey {
		t.Errorf("computed fields changed on update")
	}

	if updated.Sku.Capacity != 5 {
		t.Errorf("capacity = %d, want 5", updated.Sku.Capacity)
	}

	if updated.Tags["env"] != "prod" {
		t.Errorf("tags not updated: %v", updated.Tags)
	}
}

func TestLocationImmutableIsPreservedByCaller(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig1", standardInput()); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := m.Get(ctx, "sub", "rg", "sig1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Location != "East US" {
		t.Errorf("location = %q, want East US", got.Location)
	}
}

func TestSkuTierDerivation(t *testing.T) {
	ctx := context.Background()

	cases := map[string]string{"Free_F1": "Free", "Standard_S1": "Standard", "Premium_P1": "Premium"}
	for name, wantTier := range cases {
		m := newMock()

		in := &signalr.Input{Location: "East US", Sku: &signalr.Sku{Name: name, Capacity: 1}}

		got, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig-"+name, in)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}

		if got.Sku.Tier != wantTier {
			t.Errorf("sku %s tier = %q, want %q", name, got.Sku.Tier, wantTier)
		}
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig1", &signalr.Input{}); !cerrors.IsInvalidArgument(err) {
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

	created, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "sig1", standardInput())
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

	got, err := restored.Get(ctx, "sub", "rg", "sig1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.HostName != created.HostName || got.PrimaryKey != created.PrimaryKey ||
		got.ExternalIP != created.ExternalIP {
		t.Errorf("computed fields not preserved across snapshot/restore")
	}
}
