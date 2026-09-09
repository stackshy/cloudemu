package elasticsan_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/elasticsan"
)

func newMock() *elasticsan.Mock {
	return elasticsan.New(config.NewOptions())
}

func i64(v int64) *int64 { return &v }

func standardInput() *elasticsan.Input {
	return &elasticsan.Input{
		Tags:        map[string]string{"env": "dev"},
		Sku:         &elasticsan.Sku{Name: "Premium_LRS"},
		Zones:       []string{"1"},
		BaseSizeTiB: i64(1),
	}
}

func createStd(t *testing.T, m *elasticsan.Mock) elasticsan.ElasticSan {
	t.Helper()

	s, isNew, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "san1", "East US", standardInput())
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	return s
}

func TestCreateComputesTotals(t *testing.T) {
	m := newMock()
	created := createStd(t, m)

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	// base=1 TiB -> 5,000 IOPS, 200 MB/s, total size 1 TiB.
	if created.TotalIops != 5000 {
		t.Errorf("totalIops = %d, want 5000", created.TotalIops)
	}

	if created.TotalMBps != 200 {
		t.Errorf("totalMBps = %d, want 200", created.TotalMBps)
	}

	if created.TotalSizeTiB != 1 {
		t.Errorf("totalSizeTiB = %d, want 1", created.TotalSizeTiB)
	}

	if created.TotalVolumeSizeGiB != 0 || created.VolumeGroupCount != 0 {
		t.Errorf("volume totals = %d/%d, want 0/0", created.TotalVolumeSizeGiB, created.VolumeGroupCount)
	}

	if created.Sku == nil || created.Sku.Name != "Premium_LRS" || created.Sku.Tier != "Premium" {
		t.Errorf("sku = %+v, want {Premium_LRS Premium}", created.Sku)
	}

	if len(created.Zones) != 1 || created.Zones[0] != "1" {
		t.Errorf("zones = %v, want [1]", created.Zones)
	}

	if created.PublicNetworkAccess != "Enabled" {
		t.Errorf("publicNetworkAccess = %q, want Enabled", created.PublicNetworkAccess)
	}
}

func TestExtendedSizeAddsCapacityNotPerformance(t *testing.T) {
	m := newMock()

	in := standardInput()
	in.BaseSizeTiB = i64(6)
	in.ExtendedSizeTiB = i64(10)

	s, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "san1", "East US", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Performance scales only off base (6*5000, 6*200); size = base+extended.
	if s.TotalIops != 30000 {
		t.Errorf("totalIops = %d, want 30000", s.TotalIops)
	}

	if s.TotalMBps != 1200 {
		t.Errorf("totalMBps = %d, want 1200", s.TotalMBps)
	}

	if s.TotalSizeTiB != 16 {
		t.Errorf("totalSizeTiB = %d, want 16", s.TotalSizeTiB)
	}
}

func TestGetIsByteStableAcrossReads(t *testing.T) {
	m := newMock()
	createStd(t, m)

	first, err := m.Get(context.Background(), "sub", "rg", "san1")
	if err != nil {
		t.Fatalf("get1: %v", err)
	}

	second, err := m.Get(context.Background(), "sub", "rg", "san1")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}

	if first.TotalIops != second.TotalIops || first.TotalMBps != second.TotalMBps ||
		first.TotalSizeTiB != second.TotalSizeTiB || first.ProvisioningState != second.ProvisioningState {
		t.Errorf("computed fields drifted across reads: %+v vs %+v", first, second)
	}
}

func TestUpdateBaseSizeRecomputesTotals(t *testing.T) {
	m := newMock()
	createStd(t, m)

	// PATCH-style: only base size + tags supplied; sku/zones preserved.
	patch := &elasticsan.Input{
		Tags:        map[string]string{"env": "prod"},
		BaseSizeTiB: i64(2),
	}

	s, isNew, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "san1", "East US", patch)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if s.TotalIops != 10000 || s.TotalMBps != 400 || s.TotalSizeTiB != 2 {
		t.Errorf("recomputed totals = %d/%d/%d, want 10000/400/2", s.TotalIops, s.TotalMBps, s.TotalSizeTiB)
	}

	// Merge preserved the immutable sku and zones; tags were replaced.
	if s.Sku == nil || s.Sku.Name != "Premium_LRS" {
		t.Errorf("sku after patch = %+v, want Premium_LRS preserved", s.Sku)
	}

	if len(s.Zones) != 1 || s.Zones[0] != "1" {
		t.Errorf("zones after patch = %v, want [1] preserved", s.Zones)
	}

	if s.Tags["env"] != "prod" {
		t.Errorf("tags after patch = %v, want env=prod", s.Tags)
	}
}

func TestLocationImmutableOnUpdate(t *testing.T) {
	m := newMock()
	createStd(t, m)

	patch := &elasticsan.Input{BaseSizeTiB: i64(3)}

	s, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "san1", "West Europe", patch)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if s.Location != "East US" {
		t.Errorf("location = %q, want East US (immutable)", s.Location)
	}
}

func TestCreateRequiresSkuAndBaseSize(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "san1", "East US",
		&elasticsan.Input{BaseSizeTiB: i64(1)})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("missing sku: err=%v, want InvalidArgument", err)
	}

	_, _, err = m.CreateOrUpdate(context.Background(), "sub", "rg", "san1", "East US",
		&elasticsan.Input{Sku: &elasticsan.Sku{Name: "Premium_LRS"}})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("missing base size: err=%v, want InvalidArgument", err)
	}
}

func TestGetNotFound(t *testing.T) {
	m := newMock()

	_, err := m.Get(context.Background(), "sub", "rg", "missing")
	if !cerrors.IsNotFound(err) {
		t.Errorf("err=%v, want NotFound", err)
	}
}

func TestDeleteReportsExistence(t *testing.T) {
	m := newMock()
	createStd(t, m)

	existed, err := m.Delete(context.Background(), "sub", "rg", "san1")
	if err != nil || !existed {
		t.Fatalf("delete existing: err=%v existed=%v", err, existed)
	}

	existed, err = m.Delete(context.Background(), "sub", "rg", "san1")
	if err != nil || existed {
		t.Fatalf("delete missing: err=%v existed=%v", err, existed)
	}
}

func TestListAndPurge(t *testing.T) {
	m := newMock()
	createStd(t, m)

	in2 := standardInput()
	if _, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "san2", "East US", in2); err != nil {
		t.Fatalf("create san2: %v", err)
	}

	byRG, err := m.ListByResourceGroup(context.Background(), "sub", "rg")
	if err != nil || len(byRG) != 2 {
		t.Fatalf("list by rg: err=%v n=%d", err, len(byRG))
	}

	bySub, err := m.ListBySubscription(context.Background(), "sub")
	if err != nil || len(bySub) != 2 {
		t.Fatalf("list by sub: err=%v n=%d", err, len(bySub))
	}

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	byRG, _ = m.ListByResourceGroup(context.Background(), "sub", "rg")
	if len(byRG) != 0 {
		t.Errorf("after purge n=%d, want 0", len(byRG))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	createStd(t, m)

	data, err := m.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(context.Background(), data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	s, err := restored.Get(context.Background(), "sub", "rg", "san1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if s.TotalIops != 5000 || s.TotalMBps != 200 || s.Sku == nil || s.Sku.Name != "Premium_LRS" {
		t.Errorf("restored resource lost fields: %+v", s)
	}
}
