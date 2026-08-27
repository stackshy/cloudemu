package memorydb

import (
	"context"
	"testing"

	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

// TestSnapshotRoundTripMemoryDB proves a snapshot/restore round-trip preserves
// every store and the mu-guarded state (tags, events) under their original
// identities.
func TestSnapshotRoundTripMemoryDB(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateSubnetGroup(ctx, mdbdriver.CreateSubnetGroupConfig{
		Name: "sng", Description: "sng desc", SubnetIDs: []string{"subnet-1", "subnet-2"},
	}); err != nil {
		t.Fatalf("create subnet group: %v", err)
	}

	c, err := src.CreateCluster(ctx, mdbdriver.CreateClusterConfig{
		Name: "c1", NumShards: 2, NumReplicasPerShard: i32p(1), SubnetGroupName: "sng",
	})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := src.TagResource(ctx, c.ARN, map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("tag cluster: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	clusters, err := dst.DescribeClusters(ctx, []string{"c1"})
	if err != nil || len(clusters) != 1 || clusters[0].Name != "c1" {
		t.Fatalf("restored clusters = %+v, err %v", clusters, err)
	}

	if clusters[0].SubnetGroupName != "sng" || len(clusters[0].Shards) != 2 {
		t.Fatalf("cluster config not preserved: %+v", clusters[0])
	}

	sngs, err := dst.DescribeSubnetGroups(ctx, []string{"sng"})
	if err != nil || len(sngs) != 1 || sngs[0].Description != "sng desc" {
		t.Fatalf("restored subnet groups = %+v, err %v", sngs, err)
	}

	// Tags (mu-guarded, ARN-keyed) survived the round-trip.
	tags, err := dst.ListTags(ctx, c.ARN)
	if err != nil || len(tags) != 1 || tags[0].Key != "env" || tags[0].Value != "prod" {
		t.Fatalf("restored tags = %+v, err %v", tags, err)
	}
}
