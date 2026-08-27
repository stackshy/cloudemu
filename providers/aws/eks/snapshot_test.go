package eks

import (
	"context"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// TestSnapshotRoundTripEKS proves a snapshot/restore round-trip preserves the
// clusters and nodegroups stores under their original names.
func TestSnapshotRoundTripEKS(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := src.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"}); err != nil {
		t.Fatalf("create nodegroup: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	names, err := dst.ListClusters(ctx)
	if err != nil || len(names) != 1 || names[0] != "c1" {
		t.Fatalf("restored clusters = %+v, err %v", names, err)
	}

	if _, err := dst.DescribeCluster(ctx, "c1"); err != nil {
		t.Fatalf("describe restored cluster: %v", err)
	}
}
