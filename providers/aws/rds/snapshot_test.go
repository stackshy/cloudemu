package rds

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestSnapshotRoundTripRDS proves a snapshot/restore round-trip preserves every
// store and the mu-guarded maps (cluster creds, group tags, root passwords)
// under their original identities.
func TestSnapshotRoundTripRDS(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateDBSubnetGroup(ctx, rdsdriver.SubnetGroupConfig{
		Name: "sng", Description: "sng", SubnetIDs: []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("create subnet group: %v", err)
	}

	if _, err := src.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{
		Name: "pg", Family: "postgres15", Description: "pg",
	}); err != nil {
		t.Fatalf("create param group: %v", err)
	}

	if _, err := src.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db1", Engine: "postgres", AllocatedStorage: 20, MasterUsername: "admin", MasterUserPassword: "pw",
	}); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if _, err := src.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID: "cl1", Engine: "aurora-mysql", MasterUsername: "root", MasterUserPassword: "clpw",
	}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := src.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap1", InstanceID: "db1"}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	insts, err := dst.DescribeInstances(ctx, nil)
	if err != nil || len(insts) != 1 || insts[0].ID != "db1" {
		t.Fatalf("restored instances = %+v, err %v", insts, err)
	}

	clusters, err := dst.DescribeClusters(ctx, nil)
	if err != nil || len(clusters) != 1 || clusters[0].ID != "cl1" {
		t.Fatalf("restored clusters = %+v, err %v", clusters, err)
	}

	snaps, err := dst.DescribeSnapshots(ctx, nil, "")
	if err != nil || len(snaps) != 1 || snaps[0].ID != "snap1" {
		t.Fatalf("restored snapshots = %+v, err %v", snaps, err)
	}

	sngs, err := dst.DescribeDBSubnetGroups(ctx, nil)
	if err != nil || len(sngs) != 1 {
		t.Fatalf("restored subnet groups = %+v, err %v", sngs, err)
	}

	// clusterCreds is promoted through an exported snapshot form; confirm the
	// Aurora cluster's master credentials survived.
	dst.mu.RLock()
	cred, ok := dst.clusterCreds["cl1"]
	dst.mu.RUnlock()

	if !ok || cred.user != "root" {
		t.Fatalf("restored clusterCreds[cl1] = %+v, ok %v", cred, ok)
	}
}
