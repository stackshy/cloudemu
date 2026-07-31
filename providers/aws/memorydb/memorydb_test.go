package memorydb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))

	return New(opts)
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClusterShardTopologyLimits(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{
		Name: "too-many-shards", NumShards: maxShardsPerCluster + 1,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("NumShards over limit: got %v, want InvalidArgument", err)
	}

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{
		Name: "too-many-replicas", NumShards: 1, NumReplicasPerShard: maxReplicasPerShard + 1,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("ReplicasPerShard over limit: got %v, want InvalidArgument", err)
	}

	// At the limits it succeeds.
	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{
		Name: "at-limit", NumShards: 1, NumReplicasPerShard: maxReplicasPerShard,
	}); err != nil {
		t.Fatalf("at-limit create: unexpected error %v", err)
	}
}

func TestClusterLifecycleAndTopology(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{
		Name: "c1", NumShards: 2, NumReplicasPerShard: 1, TLSEnabled: true,
	})
	requireNoError(t, err)

	if c.Status != mdbdriver.StatusAvailable || c.ACLName != "open-access" || c.ParameterGroupName != "default.memorydb-redis7" {
		t.Fatalf("cluster defaults wrong: %+v", c)
	}

	// Topology: 2 shards, each 2 nodes (primary+replica) with endpoints; cluster endpoint set.
	if len(c.Shards) != 2 || len(c.Shards[0].Nodes) != 2 {
		t.Fatalf("topology wrong: %d shards, %d nodes", len(c.Shards), len(c.Shards[0].Nodes))
	}

	if c.Shards[0].Nodes[0].Endpoint.Address == "" || c.Shards[0].Nodes[0].Endpoint.Port != defaultPort {
		t.Errorf("node endpoint not populated: %+v", c.Shards[0].Nodes[0].Endpoint)
	}

	if c.ClusterEndpoint.Address == "" {
		t.Error("cluster endpoint not populated")
	}

	// The default ACL now records the cluster.
	acls, err := m.DescribeACLs(ctx, []string{"open-access"})
	requireNoError(t, err)

	if len(acls[0].Clusters) != 1 || acls[0].Clusters[0] != "c1" {
		t.Errorf("ACL not linked to cluster: %+v", acls[0].Clusters)
	}

	// Duplicate create rejected.
	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1"}); err == nil {
		t.Error("duplicate cluster: expected AlreadyExists")
	}

	// Failover works (has a replica); node-type updates listed.
	if _, err := m.FailoverShard(ctx, "c1", "0001"); err != nil {
		t.Errorf("FailoverShard: %v", err)
	}

	up, down, err := m.ListAllowedNodeTypeUpdates(ctx, "c1")
	requireNoError(t, err)

	if len(up) == 0 || len(down) == 0 {
		t.Errorf("node-type updates empty: up=%v down=%v", up, down)
	}

	// Delete cluster + verify ACL detached.
	if _, err := m.DeleteCluster(ctx, "c1", ""); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	acls, _ = m.DescribeACLs(ctx, []string{"open-access"})
	if len(acls[0].Clusters) != 0 {
		t.Errorf("ACL still linked after cluster delete: %v", acls[0].Clusters)
	}
}

func TestClusterReferenceValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c", ACLName: "ghost"}); err == nil {
		t.Error("missing ACL: expected NotFound")
	}

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c", ParameterGroupName: "ghost"}); err == nil {
		t.Error("missing parameter group: expected NotFound")
	}

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c", SubnetGroupName: "ghost"}); err == nil {
		t.Error("missing subnet group: expected NotFound")
	}
}

func TestFailoverRequiresReplica(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1", NumShards: 1, NumReplicasPerShard: 0}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.FailoverShard(ctx, "c1", "0001"); err == nil {
		t.Error("failover on a replica-less shard: expected FailedPrecondition")
	}

	if _, err := m.FailoverShard(ctx, "c1", "9999"); err == nil {
		t.Error("failover on a missing shard: expected NotFound")
	}
}

func TestACLUserLinkage(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, mdbdriver.CreateUserConfig{Name: "u1", AccessString: "on ~* +@all"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	acl, err := m.CreateACL(ctx, "acl1", []string{"u1"}, nil)
	requireNoError(t, err)

	if len(acl.UserNames) != 1 {
		t.Fatalf("ACL users: %+v", acl.UserNames)
	}

	// User now belongs to the ACL → delete blocked.
	if _, err := m.DeleteUser(ctx, "u1"); err == nil {
		t.Error("delete user in ACL: expected FailedPrecondition")
	}

	// Attach the ACL to a cluster → ACL delete blocked.
	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1", ACLName: "acl1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.DeleteACL(ctx, "acl1"); err == nil {
		t.Error("delete ACL attached to cluster: expected FailedPrecondition")
	}

	// ACL over a missing user rejected.
	if _, err := m.CreateACL(ctx, "acl2", []string{"ghost"}, nil); err == nil {
		t.Error("ACL over missing user: expected NotFound")
	}

	// Update ACL removes the user; then user delete works.
	if _, err := m.UpdateACL(ctx, "acl1", nil, []string{"u1"}); err != nil {
		t.Fatalf("UpdateACL: %v", err)
	}

	if _, err := m.DeleteUser(ctx, "u1"); err != nil {
		t.Errorf("delete user after ACL removal: %v", err)
	}
}

func TestParameterGroups(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateParameterGroup(ctx, "pg1", "memorydb_redis7", "custom", nil); err != nil {
		t.Fatalf("CreateParameterGroup: %v", err)
	}

	if _, err := m.UpdateParameterGroup(ctx, "pg1", []mdbdriver.ParameterNameValue{{Name: "maxmemory-policy", Value: "allkeys-lru"}}); err != nil {
		t.Fatalf("UpdateParameterGroup: %v", err)
	}

	params, err := m.DescribeParameters(ctx, "pg1")
	requireNoError(t, err)

	var sawOverride bool
	for _, p := range params {
		if p.Name == "maxmemory-policy" && p.Value == "allkeys-lru" {
			sawOverride = true
		}
	}

	if !sawOverride {
		t.Error("parameter override not reflected in DescribeParameters")
	}

	// Unknown parameter rejected.
	if _, err := m.UpdateParameterGroup(ctx, "pg1", []mdbdriver.ParameterNameValue{{Name: "bogus", Value: "1"}}); err == nil {
		t.Error("unknown parameter: expected InvalidArgument")
	}

	// Reset restores the default.
	if _, err := m.ResetParameterGroup(ctx, "pg1", true, nil); err != nil {
		t.Fatalf("ResetParameterGroup: %v", err)
	}

	params, _ = m.DescribeParameters(ctx, "pg1")
	for _, p := range params {
		if p.Name == "maxmemory-policy" && p.Value != "noeviction" {
			t.Errorf("reset did not restore default: %q", p.Value)
		}
	}

	// Default parameter group cannot be deleted.
	if _, err := m.DeleteParameterGroup(ctx, "default.memorydb-redis7"); err == nil {
		t.Error("delete default parameter group: expected error")
	}

	// In-use parameter group cannot be deleted.
	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1", ParameterGroupName: "pg1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.DeleteParameterGroup(ctx, "pg1"); err == nil {
		t.Error("delete in-use parameter group: expected FailedPrecondition")
	}
}

func TestSubnetGroups(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateSubnetGroup(ctx, mdbdriver.CreateSubnetGroupConfig{Name: "sg1", SubnetIDs: []string{"subnet-a", "subnet-b"}}); err != nil {
		t.Fatalf("CreateSubnetGroup: %v", err)
	}

	got, err := m.DescribeSubnetGroups(ctx, []string{"sg1"})
	requireNoError(t, err)

	if len(got[0].Subnets) != 2 || got[0].VpcID == "" {
		t.Errorf("subnet group: %+v", got[0])
	}

	// Empty subnet list rejected.
	if _, err := m.CreateSubnetGroup(ctx, mdbdriver.CreateSubnetGroupConfig{Name: "x"}); err == nil {
		t.Error("empty subnet ids: expected InvalidArgument")
	}

	// In-use subnet group cannot be deleted.
	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1", SubnetGroupName: "sg1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.DeleteSubnetGroup(ctx, "sg1"); err == nil {
		t.Error("delete in-use subnet group: expected FailedPrecondition")
	}
}

func TestSnapshotsAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "src", NumShards: 3, NodeType: "db.r7g.large"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	snap, err := m.CreateSnapshot(ctx, mdbdriver.CreateSnapshotConfig{Name: "snap1", ClusterName: "src"})
	requireNoError(t, err)

	if snap.ClusterConfiguration.NumShards != 3 || snap.ClusterConfiguration.NodeType != "db.r7g.large" {
		t.Errorf("snapshot config not captured: %+v", snap.ClusterConfiguration)
	}

	// Snapshot of a missing cluster rejected.
	if _, err := m.CreateSnapshot(ctx, mdbdriver.CreateSnapshotConfig{Name: "x", ClusterName: "ghost"}); err == nil {
		t.Error("snapshot of missing cluster: expected NotFound")
	}

	// Copy.
	if _, err := m.CopySnapshot(ctx, mdbdriver.CopySnapshotConfig{SourceName: "snap1", TargetName: "snap2"}); err != nil {
		t.Fatalf("CopySnapshot: %v", err)
	}

	snaps, err := m.DescribeSnapshots(ctx, nil, "src")
	requireNoError(t, err)

	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots for src, got %d", len(snaps))
	}

	// Restore into a new cluster inherits the shape.
	restored, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "restored", SnapshotName: "snap1"})
	requireNoError(t, err)

	if restored.NumberOfShards != 3 || restored.NodeType != "db.r7g.large" {
		t.Errorf("restore did not inherit shape: %+v", restored)
	}

	if _, err := m.DeleteSnapshot(ctx, "snap1"); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
}

func TestMultiRegionAndReservedNodes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mrc, err := m.CreateMultiRegionCluster(ctx, mdbdriver.CreateMultiRegionClusterConfig{NameSuffix: "app", NumShards: 2})
	requireNoError(t, err)

	if mrc.Name != "virtual-app" {
		t.Errorf("multi-region name: %q", mrc.Name)
	}

	if _, err := m.UpdateMultiRegionCluster(ctx, "virtual-app", "db.r7g.xlarge", "", nil); err != nil {
		t.Fatalf("UpdateMultiRegionCluster: %v", err)
	}

	if _, err := m.DeleteMultiRegionCluster(ctx, "virtual-app"); err != nil {
		t.Fatalf("DeleteMultiRegionCluster: %v", err)
	}

	// Reserved nodes: offerings + purchase.
	offerings, err := m.DescribeReservedNodesOfferings(ctx)
	requireNoError(t, err)

	if len(offerings) == 0 {
		t.Fatal("expected reserved-node offerings")
	}

	if _, err := m.PurchaseReservedNodesOffering(ctx, offerings[0].OfferingID, "res1", 2); err != nil {
		t.Fatalf("PurchaseReservedNodesOffering: %v", err)
	}

	nodes, err := m.DescribeReservedNodes(ctx)
	requireNoError(t, err)

	if len(nodes) != 1 || nodes[0].NodeCount != 2 {
		t.Errorf("reserved nodes: %+v", nodes)
	}

	if _, err := m.PurchaseReservedNodesOffering(ctx, "ghost", "res2", 1); err == nil {
		t.Error("purchase unknown offering: expected NotFound")
	}
}

func TestClusterResultDoesNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1", Tags: map[string]string{"env": "prod"}}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)

	got[0].Tags["env"] = "tampered"
	got[0].Shards[0].Nodes[0].Name = "tampered"

	reread, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)

	if reread[0].Tags["env"] != "prod" {
		t.Errorf("Tags aliased: %q", reread[0].Tags["env"])
	}

	if reread[0].Shards[0].Nodes[0].Name == "tampered" {
		t.Error("Shards/Nodes aliased")
	}
}

func TestClusterMetricsEmitted(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))

	m := New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()
	if _, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	names, err := cw.ListMetrics(ctx, metricsNamespace)
	requireNoError(t, err)

	if len(names) == 0 {
		t.Fatal("expected AWS/MemoryDB metrics to be emitted")
	}
}

func TestTagsAndCatalogs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, mdbdriver.CreateClusterConfig{Name: "c1"})
	requireNoError(t, err)

	if _, err := m.TagResource(ctx, c.ARN, map[string]string{"team": "data"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := m.ListTags(ctx, c.ARN)
	requireNoError(t, err)

	if len(tags) != 1 || tags[0].Key != "team" {
		t.Errorf("tags: %+v", tags)
	}

	if _, err := m.UntagResource(ctx, c.ARN, []string{"team"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	versions, err := m.DescribeEngineVersions(ctx, "redis", "")
	requireNoError(t, err)

	if len(versions) == 0 {
		t.Error("expected engine versions")
	}

	events, err := m.DescribeEvents(ctx)
	requireNoError(t, err)

	if len(events) == 0 {
		t.Error("expected lifecycle events after cluster create")
	}
}
