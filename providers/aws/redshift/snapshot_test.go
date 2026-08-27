package redshift

import (
	"context"
	"testing"

	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestSnapshotRoundTripRedshift proves a snapshot/restore round-trip preserves
// every store and the mu-guarded ARN-keyed tag map under their original
// identities.
func TestSnapshotRoundTripRedshift(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateClusterSubnetGroup(ctx, "sng", "sng desc", []string{"subnet-1"}); err != nil {
		t.Fatalf("create subnet group: %v", err)
	}

	if _, err := src.CreateClusterParameterGroup(ctx, "pg", "redshift-1.0", "pg desc"); err != nil {
		t.Fatalf("create param group: %v", err)
	}

	cl, err := src.CreateCluster(ctx, rdbdriver.ClusterConfig{
		ID: "cl1", Engine: "redshift", NodeType: "dc2.large", NumberOfNodes: 2,
		MasterUsername: "admin", SubnetGroupName: "sng",
	})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := src.CreateClusterSnapshot(ctx, rdbdriver.ClusterSnapshotConfig{
		ID: "snap1", ClusterID: "cl1",
	}); err != nil {
		t.Fatalf("create cluster snapshot: %v", err)
	}

	if err := src.CreateTags(ctx, cl.ARN, map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("create tags: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	clusters, err := dst.DescribeClusters(ctx, []string{"cl1"})
	if err != nil || len(clusters) != 1 || clusters[0].NodeType != "dc2.large" || clusters[0].NumberOfNodes != 2 {
		t.Fatalf("restored clusters = %+v, err %v", clusters, err)
	}

	snaps, err := dst.DescribeClusterSnapshots(ctx, []string{"snap1"}, "")
	if err != nil || len(snaps) != 1 || snaps[0].ClusterID != "cl1" {
		t.Fatalf("restored cluster snapshots = %+v, err %v", snaps, err)
	}

	pgs, err := dst.DescribeClusterParameterGroups(ctx, []string{"pg"})
	if err != nil || len(pgs) != 1 || pgs[0].Family != "redshift-1.0" {
		t.Fatalf("restored param groups = %+v, err %v", pgs, err)
	}

	sngs, err := dst.DescribeClusterSubnetGroups(ctx, []string{"sng"})
	if err != nil || len(sngs) != 1 || sngs[0].Description != "sng desc" {
		t.Fatalf("restored subnet groups = %+v, err %v", sngs, err)
	}

	// Tags (mu-guarded, ARN-keyed) survived the round-trip.
	tags, err := dst.DescribeTags(ctx, cl.ARN)
	if err != nil || tags["env"] != "prod" {
		t.Fatalf("restored tags = %+v, err %v", tags, err)
	}
}
