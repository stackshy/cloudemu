package gke

import (
	"context"
	"testing"
)

// TestSnapshotRoundTripGKE proves the mock serializes its clusters, node pools,
// and operations and restores them into a fresh mock identity-preservingly:
// re-snapshotting yields byte-identical JSON and the seeded cluster and node
// pool come back under their original ids (node pools keyed "cluster/nodePool").
func TestSnapshotRoundTripGKE(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, _, err := src.CreateCluster(ctx, &CreateClusterInput{
		Name: "c1", Location: "us-central1", InitialNodeCount: int64Ptr(1),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, _, err := src.CreateNodePool(ctx, "us-central1", "c1", &NodePoolSpec{
		Name: "pool-1", InitialNodeCount: int64Ptr(2), MachineType: "e2-standard-4",
	}); err != nil {
		t.Fatalf("CreateNodePool: %v", err)
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

	c, err := dst.GetCluster(ctx, "us-central1", "c1")
	if err != nil {
		t.Fatalf("GetCluster after restore: %v", err)
	}

	if c.Name != "c1" {
		t.Fatalf("restored cluster name = %q, want c1", c.Name)
	}

	pool, err := dst.GetNodePool(ctx, "us-central1", "c1", "pool-1")
	if err != nil {
		t.Fatalf("GetNodePool after restore: %v", err)
	}

	if pool.NodeCount != 2 {
		t.Fatalf("restored node pool count = %d, want 2", pool.NodeCount)
	}
}

// TestSnapshotEmptyGKE confirms a fresh mock snapshots and restores cleanly.
func TestSnapshotEmptyGKE(t *testing.T) {
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
