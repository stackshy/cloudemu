package alloydb

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestSnapshotRoundTripAlloyDB proves the mock serializes its entire state —
// portable stores plus the AlloyDB-native side-stores (clusterExtra/
// instanceExtra/initialPasswords) — and restores it into a fresh mock
// identity-preservingly: re-snapshotting yields byte-identical JSON and the
// seeded cluster and instance come back under their original ids.
func TestSnapshotRoundTripAlloyDB(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateAlloyDBCluster(ctx, rdsdriver.AlloyDBClusterConfig{
		ID: "c1", DatabaseVersion: "POSTGRES_15", Network: "default",
		InitialUser: "postgres", InitialPassword: "secret",
		ContinuousBackup: true, AutomatedBackupEnabled: true, MaintenanceDay: "SUNDAY",
	}); err != nil {
		t.Fatalf("CreateAlloyDBCluster: %v", err)
	}

	if _, err := src.CreateAlloyDBInstance(ctx, rdsdriver.AlloyDBInstanceConfig{
		ClusterID: "c1", ID: "i1", InstanceType: "PRIMARY",
	}); err != nil {
		t.Fatalf("CreateAlloyDBInstance: %v", err)
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	data2, err := dst.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("re-Snapshot: %v", err)
	}

	if string(data) != string(data2) {
		t.Fatalf("snapshot not stable across restore:\n%s\nvs\n%s", data, data2)
	}

	clusters, err := dst.DescribeClusters(ctx, []string{"c1"})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(clusters) != 1 || clusters[0].ID != "c1" {
		t.Fatalf("restored clusters = %+v, want id c1", clusters)
	}
}

// TestSnapshotEmptyAlloyDB confirms a fresh mock snapshots and restores cleanly.
func TestSnapshotEmptyAlloyDB(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}
