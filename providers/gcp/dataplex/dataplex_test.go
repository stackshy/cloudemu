package dataplex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
)

func newMock() *Mock {
	return New(config.NewOptions(config.WithProjectID("p")))
}

func rawFields(kv map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}

	for k, v := range kv {
		b, _ := json.Marshal(v)
		out[k] = b
	}

	return out
}

func mustLake(t *testing.T, m *Mock, id string) {
	t.Helper()

	_, _, err := m.CreateLake(context.Background(), &dpdriver.Config{
		Project: "p", Location: "us-central1", ID: id, Fields: rawFields(map[string]any{"description": "d"}),
	})
	if err != nil {
		t.Fatalf("CreateLake(%s): %v", id, err)
	}
}

func mustZone(t *testing.T, m *Mock, lake, id string) {
	t.Helper()

	_, _, err := m.CreateZone(context.Background(), &dpdriver.Config{
		Project: "p", Location: "us-central1", Lake: lake, ID: id,
		Fields: rawFields(map[string]any{"type": "RAW"}),
	})
	if err != nil {
		t.Fatalf("CreateZone(%s/%s): %v", lake, id, err)
	}
}

func mustAsset(t *testing.T, m *Mock, lake, zone, id string) {
	t.Helper()

	_, _, err := m.CreateAsset(context.Background(), &dpdriver.Config{
		Project: "p", Location: "us-central1", Lake: lake, Zone: zone, ID: id,
		Fields: rawFields(map[string]any{"description": "a"}),
	})
	if err != nil {
		t.Fatalf("CreateAsset(%s/%s/%s): %v", lake, zone, id, err)
	}
}

func TestLakeZoneAssetLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	mustLake(t, m, "lake1")
	mustZone(t, m, "lake1", "zone1")
	mustAsset(t, m, "lake1", "zone1", "asset1")

	if _, err := m.GetLake(ctx, "p", "us-central1", "lake1"); err != nil {
		t.Fatalf("GetLake: %v", err)
	}

	if _, err := m.GetZone(ctx, "p", "us-central1", "lake1", "zone1"); err != nil {
		t.Fatalf("GetZone: %v", err)
	}

	asset, err := m.GetAsset(ctx, "p", "us-central1", "lake1", "zone1", "asset1")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}

	if asset.CreateTime.IsZero() {
		t.Fatalf("asset createTime not set")
	}
}

func TestZoneRequiresParentLake(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateZone(context.Background(), &dpdriver.Config{
		Project: "p", Location: "us-central1", Lake: "missing", ID: "z", Fields: rawFields(map[string]any{"type": "RAW"}),
	})
	if cerrors.GetCode(err) != cerrors.NotFound {
		t.Fatalf("CreateZone under missing lake: got %v, want NotFound", err)
	}
}

func TestAssetRequiresParentZone(t *testing.T) {
	m := newMock()
	mustLake(t, m, "lake1")

	_, _, err := m.CreateAsset(context.Background(), &dpdriver.Config{
		Project: "p", Location: "us-central1", Lake: "lake1", Zone: "missing", ID: "a",
	})
	if cerrors.GetCode(err) != cerrors.NotFound {
		t.Fatalf("CreateAsset under missing zone: got %v, want NotFound", err)
	}
}

func TestDuplicateLake(t *testing.T) {
	m := newMock()
	mustLake(t, m, "lake1")

	_, _, err := m.CreateLake(context.Background(), &dpdriver.Config{
		Project: "p", Location: "us-central1", ID: "lake1",
	})
	if cerrors.GetCode(err) != cerrors.AlreadyExists {
		t.Fatalf("duplicate lake: got %v, want AlreadyExists", err)
	}
}

func TestDeleteLakeCascades(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	mustLake(t, m, "lake1")
	mustZone(t, m, "lake1", "zone1")
	mustAsset(t, m, "lake1", "zone1", "asset1")

	if _, err := m.DeleteLake(ctx, "p", "us-central1", "lake1"); err != nil {
		t.Fatalf("DeleteLake: %v", err)
	}

	if _, err := m.GetZone(ctx, "p", "us-central1", "lake1", "zone1"); cerrors.GetCode(err) != cerrors.NotFound {
		t.Fatalf("zone survived lake cascade: %v", err)
	}

	if _, err := m.GetAsset(ctx, "p", "us-central1", "lake1", "zone1", "asset1"); cerrors.GetCode(err) != cerrors.NotFound {
		t.Fatalf("asset survived lake cascade: %v", err)
	}
}

func TestDeleteZoneCascades(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	mustLake(t, m, "lake1")
	mustZone(t, m, "lake1", "zone1")
	mustAsset(t, m, "lake1", "zone1", "asset1")

	if _, err := m.DeleteZone(ctx, "p", "us-central1", "lake1", "zone1"); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}

	if _, err := m.GetAsset(ctx, "p", "us-central1", "lake1", "zone1", "asset1"); cerrors.GetCode(err) != cerrors.NotFound {
		t.Fatalf("asset survived zone cascade: %v", err)
	}

	if _, err := m.GetLake(ctx, "p", "us-central1", "lake1"); err != nil {
		t.Fatalf("lake removed by zone cascade: %v", err)
	}
}

// TestPrefixCollisionCascade is the adversarial servicedirectory ns1/ns10 guard:
// deleting lake `l1` must not delete `l10`'s zones or assets, which share the
// `.../lakes/l1` string prefix but not the trailing-slash-bounded prefix.
func TestPrefixCollisionCascade(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	mustLake(t, m, "l1")
	mustZone(t, m, "l1", "z1")
	mustAsset(t, m, "l1", "z1", "a1")

	mustLake(t, m, "l10")
	mustZone(t, m, "l10", "z10")
	mustAsset(t, m, "l10", "z10", "a10")

	if _, err := m.DeleteLake(ctx, "p", "us-central1", "l1"); err != nil {
		t.Fatalf("DeleteLake(l1): %v", err)
	}

	if _, err := m.GetZone(ctx, "p", "us-central1", "l10", "z10"); err != nil {
		t.Fatalf("l10's zone was wrongly cascaded by l1 delete: %v", err)
	}

	if _, err := m.GetAsset(ctx, "p", "us-central1", "l10", "z10", "a10"); err != nil {
		t.Fatalf("l10's asset was wrongly cascaded by l1 delete: %v", err)
	}

	if _, err := m.GetZone(ctx, "p", "us-central1", "l1", "z1"); cerrors.GetCode(err) != cerrors.NotFound {
		t.Fatalf("l1's zone should have been cascaded: %v", err)
	}
}

func TestPatchLakeMaskedUpdate(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	mustLake(t, m, "lake1")

	_, _, err := m.PatchLake(ctx, &dpdriver.Config{
		Project: "p", Location: "us-central1", ID: "lake1",
		Fields: rawFields(map[string]any{"description": "updated"}),
	}, []string{"description"})
	if err != nil {
		t.Fatalf("PatchLake: %v", err)
	}

	got, err := m.GetLake(ctx, "p", "us-central1", "lake1")
	if err != nil {
		t.Fatalf("GetLake: %v", err)
	}

	var desc string
	_ = json.Unmarshal(got.Fields["description"], &desc)

	if desc != "updated" {
		t.Fatalf("description not patched: %q", desc)
	}
}

func TestListScoping(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	mustLake(t, m, "lake1")
	mustLake(t, m, "lake2")
	mustZone(t, m, "lake1", "zoneA")
	mustZone(t, m, "lake1", "zoneB")
	mustZone(t, m, "lake2", "zoneC")

	zones, err := m.ListZones(ctx, "p", "us-central1", "lake1")
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}

	if len(zones) != 2 {
		t.Fatalf("ListZones(lake1) = %d, want 2", len(zones))
	}
}

func TestSnapshotRestore(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	mustLake(t, m, "lake1")
	mustZone(t, m, "lake1", "zone1")
	mustAsset(t, m, "lake1", "zone1", "asset1")

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	m2 := newMock()
	if err := m2.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if _, err := m2.GetAsset(ctx, "p", "us-central1", "lake1", "zone1", "asset1"); err != nil {
		t.Fatalf("restored asset missing: %v", err)
	}
}
