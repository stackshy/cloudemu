package managedlustre_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/managedlustre"
)

func newMock() *managedlustre.Mock {
	return managedlustre.New(config.NewOptions())
}

func f64(v float64) *float64 { return &v }

func str(v string) *string { return &v }

func standardInput() *managedlustre.Input {
	return &managedlustre.Input{
		Tags:               map[string]string{"env": "dev"},
		Sku:                &managedlustre.Sku{Name: "AMLFS-Durable-Premium-125"},
		Zones:              []string{"1"},
		StorageCapacityTiB: f64(16),
		FilesystemSubnet:   str("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/fs"),
		MaintenanceWindow:  &managedlustre.MaintenanceWindow{DayOfWeek: "Friday", TimeOfDayUTC: "22:00"},
	}
}

func createStd(t *testing.T, m *managedlustre.Mock) managedlustre.AmlFilesystem {
	t.Helper()

	s, isNew, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs1", "eastus", standardInput())
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

	// 16 TiB * 125 MB/s per TiB = 2000 MB/s.
	if created.ThroughputProvisionedMBps != 2000 {
		t.Errorf("throughput = %d, want 2000", created.ThroughputProvisionedMBps)
	}

	if created.MgsAddress == "" {
		t.Error("mgsAddress is empty, want a deterministic IP")
	}

	if created.ClusterUUID == "" {
		t.Error("clusterUuid is empty, want a deterministic uuid")
	}

	if created.Sku == nil || created.Sku.Name != "AMLFS-Durable-Premium-125" {
		t.Errorf("sku = %+v, want AMLFS-Durable-Premium-125", created.Sku)
	}

	if len(created.Zones) != 1 || created.Zones[0] != "1" {
		t.Errorf("zones = %v, want [1]", created.Zones)
	}

	if created.MaintenanceWindow == nil || created.MaintenanceWindow.DayOfWeek != "Friday" {
		t.Errorf("maintenanceWindow = %+v", created.MaintenanceWindow)
	}
}

func TestComputedFieldsStableAcrossReads(t *testing.T) {
	m := newMock()
	created := createStd(t, m)

	got, err := m.Get(context.Background(), "sub", "rg", "fs1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.MgsAddress != created.MgsAddress {
		t.Errorf("mgsAddress drifted: %q vs %q", got.MgsAddress, created.MgsAddress)
	}

	if got.ClusterUUID != created.ClusterUUID {
		t.Errorf("clusterUuid drifted: %q vs %q", got.ClusterUUID, created.ClusterUUID)
	}
}

func TestUpdatePreservesComputedAndLocation(t *testing.T) {
	m := newMock()
	created := createStd(t, m)

	// PATCH only tags — everything else preserved, computed unchanged.
	patched, isNew, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs1", "westus",
		&managedlustre.Input{Tags: map[string]string{"env": "prod"}})
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if patched.Location != "eastus" {
		t.Errorf("location mutated to %q, want eastus (immutable)", patched.Location)
	}

	if patched.MgsAddress != created.MgsAddress {
		t.Errorf("mgsAddress changed on update: %q vs %q", patched.MgsAddress, created.MgsAddress)
	}

	if patched.Sku == nil || patched.Sku.Name != "AMLFS-Durable-Premium-125" {
		t.Errorf("sku not preserved on tag-only patch: %+v", patched.Sku)
	}

	if patched.StorageCapacityTiB != 16 {
		t.Errorf("storageCapacity not preserved: %v", patched.StorageCapacityTiB)
	}

	if patched.Tags["env"] != "prod" {
		t.Errorf("tags not replaced: %v", patched.Tags)
	}
}

func TestUpdateRecomputesThroughput(t *testing.T) {
	m := newMock()
	createStd(t, m)

	updated, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs1", "eastus",
		&managedlustre.Input{StorageCapacityTiB: f64(32)})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// 32 TiB * 125 = 4000.
	if updated.ThroughputProvisionedMBps != 4000 {
		t.Errorf("throughput = %d, want 4000", updated.ThroughputProvisionedMBps)
	}
}

func TestIdentityMintedOnceAndStable(t *testing.T) {
	m := newMock()
	uaID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/id1"

	in := standardInput()
	in.Identity = &managedlustre.Identity{
		Type:         "UserAssigned",
		UserAssigned: map[string]managedlustre.UserAssignedValue{uaID: {}},
	}

	created, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs1", "eastus", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.Identity == nil || created.Identity.TenantID == "" {
		t.Fatalf("identity not resolved: %+v", created.Identity)
	}

	minted := created.Identity.UserAssigned[uaID]
	if minted.PrincipalID == "" || minted.ClientID == "" {
		t.Fatalf("user-assigned ids not minted: %+v", minted)
	}

	// A subsequent update with the same identity keeps the minted ids stable.
	in2 := &managedlustre.Input{Identity: in.Identity}

	updated, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs1", "eastus", in2)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got := updated.Identity.UserAssigned[uaID]
	if got.PrincipalID != minted.PrincipalID || got.ClientID != minted.ClientID {
		t.Errorf("identity ids drifted: %+v vs %+v", got, minted)
	}
}

func TestCreateValidation(t *testing.T) {
	m := newMock()

	cases := map[string]*managedlustre.Input{
		"missing sku":      {StorageCapacityTiB: f64(16), FilesystemSubnet: str("subnet")},
		"missing capacity": {Sku: &managedlustre.Sku{Name: "AMLFS-Durable-Premium-125"}, FilesystemSubnet: str("subnet")},
		"missing subnet":   {Sku: &managedlustre.Sku{Name: "AMLFS-Durable-Premium-125"}, StorageCapacityTiB: f64(16)},
	}

	for name, in := range cases {
		_, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs1", "eastus", in)
		if !cerrors.IsInvalidArgument(err) {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
}

func TestArchiveAndCancel(t *testing.T) {
	m := newMock()

	in := standardInput()
	in.Hsm = &managedlustre.HsmSettings{Container: "c", LoggingContainer: "l"}

	if _, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs1", "eastus", in); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.Archive(context.Background(), "sub", "rg", "fs1", "/data"); err != nil {
		t.Fatalf("archive: %v", err)
	}

	got, _ := m.Get(context.Background(), "sub", "rg", "fs1")
	if len(got.ArchiveStatus) != 1 || got.ArchiveStatus[0].Status.State != "Completed" {
		t.Fatalf("archive status = %+v, want one Completed entry", got.ArchiveStatus)
	}

	if got.ArchiveStatus[0].FilesystemPath != "/data" {
		t.Errorf("archive path = %q, want /data", got.ArchiveStatus[0].FilesystemPath)
	}

	if err := m.CancelArchive(context.Background(), "sub", "rg", "fs1"); err != nil {
		t.Fatalf("cancelArchive: %v", err)
	}

	got, _ = m.Get(context.Background(), "sub", "rg", "fs1")
	if got.ArchiveStatus[0].Status.State != "Canceled" {
		t.Errorf("archive state = %q, want Canceled", got.ArchiveStatus[0].Status.State)
	}
}

func TestArchiveRequiresHsm(t *testing.T) {
	m := newMock()
	createStd(t, m) // no HSM configured

	err := m.Archive(context.Background(), "sub", "rg", "fs1", "/")
	if !cerrors.IsFailedPrecondition(err) {
		t.Errorf("archive without HSM: err = %v, want FailedPrecondition", err)
	}
}

func TestGetNotFound(t *testing.T) {
	m := newMock()

	_, err := m.Get(context.Background(), "sub", "rg", "missing")
	if !cerrors.IsNotFound(err) {
		t.Errorf("get missing: err = %v, want NotFound", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	m := newMock()
	createStd(t, m)

	existed, _ := m.Delete(context.Background(), "sub", "rg", "fs1")
	if !existed {
		t.Error("first delete: existed = false, want true")
	}

	existed, _ = m.Delete(context.Background(), "sub", "rg", "fs1")
	if existed {
		t.Error("second delete: existed = true, want false")
	}
}

func TestListAndPurge(t *testing.T) {
	m := newMock()
	createStd(t, m)

	if _, _, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "fs2", "eastus", standardInput()); err != nil {
		t.Fatalf("create fs2: %v", err)
	}

	byRG, _ := m.ListByResourceGroup(context.Background(), "sub", "rg")
	if len(byRG) != 2 {
		t.Errorf("list by rg = %d, want 2", len(byRG))
	}

	bySub, _ := m.ListBySubscription(context.Background(), "sub")
	if len(bySub) != 2 {
		t.Errorf("list by sub = %d, want 2", len(bySub))
	}

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	byRG, _ = m.ListByResourceGroup(context.Background(), "sub", "rg")
	if len(byRG) != 0 {
		t.Errorf("after purge: list = %d, want 0", len(byRG))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	created := createStd(t, m)

	data, err := m.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(context.Background(), data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := restored.Get(context.Background(), "sub", "rg", "fs1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.MgsAddress != created.MgsAddress || got.ThroughputProvisionedMBps != created.ThroughputProvisionedMBps {
		t.Errorf("restored resource drifted: %+v vs %+v", got, created)
	}
}
