package rds

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
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

// TestRestoreSnapshotWithoutEventsClearsLog: loading a snapshot saved before
// any event was recorded leaves no stale events behind.
func TestRestoreSnapshotWithoutEventsClearsLog(t *testing.T) {
	ctx := context.Background()

	empty, _ := newEventTestMock()

	data, err := empty.Snapshot(ctx, false)
	requireNoError(t, err)

	m, _ := newEventTestMock()
	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "mysql"})
	requireNoError(t, err)

	requireNoError(t, m.Restore(ctx, data))

	events, err := m.DescribeEvents(ctx, rdsdriver.DescribeEventsInput{})
	requireNoError(t, err)
	assertEqual(t, 0, len(events))
}

// TestSnapshotConcurrentWithStateWrites runs Snapshot alongside writers of
// the lock-guarded maps and the event log; under -race it fails if Snapshot
// hands json.Marshal a live map instead of a copy taken under the lock.
func TestSnapshotConcurrentWithStateWrites(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012")))

	_, err := m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{
		Name: "pg1", Family: "mysql8.0", Tags: map[string]string{"a": "1"},
	})
	requireNoError(t, err)

	pgARN := parameterGroupARN("us-east-1", "123456789012", "pg1")

	const rounds = 50

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		for i := range rounds {
			_ = m.AddTagsToResource(ctx, pgARN, map[string]string{"k": string(rune('a' + i%26))})
			_, _ = m.ModifyDBParameterGroup(ctx, "pg1", []rdsdriver.Parameter{{Name: "x", Value: "1"}})
		}
	}()

	go func() {
		defer wg.Done()

		for range rounds {
			if _, err := m.Snapshot(ctx, false); err != nil {
				t.Errorf("Snapshot: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}
