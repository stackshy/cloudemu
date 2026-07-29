package azuresql

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/azuremonitor"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("eastus"),
	)

	return New(opts)
}

func TestServerLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cluster, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID:             "srv1",
		MasterUsername: "admin",
		EngineVersion:  "12.0",
	})
	requireNoError(t, err)

	assertEqual(t, "srv1", cluster.ID)
	assertEqual(t, "available", cluster.State)
	assertNotEmpty(t, cluster.Endpoint)

	// Listing.
	list, err := m.DescribeClusters(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 1, len(list))

	// Modify.
	updated, err := m.ModifyCluster(ctx, "srv1", rdsdriver.ModifyInstanceInput{
		EngineVersion: "12.1",
	})
	requireNoError(t, err)
	assertEqual(t, "12.1", updated.EngineVersion)

	requireNoError(t, m.DeleteCluster(ctx, "srv1"))

	if _, err := m.DescribeClusters(ctx, []string{"srv1"}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestDatabaseRequiresServer(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:     "db1",
		Engine: "SQLServer",
	}); err == nil {
		t.Fatal("expected error: database without ClusterID")
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:        "db1",
		ClusterID: "ghost",
		Engine:    "SQLServer",
	}); err == nil {
		t.Fatal("expected error: database with non-existent server")
	}
}

func TestDatabaseLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv1"})
	requireNoError(t, err)

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "appdb",
		ClusterID:        "srv1",
		Engine:           "SQLServer",
		AllocatedStorage: 100,
	})
	requireNoError(t, err)

	assertEqual(t, "appdb", inst.ID)
	assertEqual(t, "srv1", inst.ClusterID)
	assertEqual(t, 100, inst.AllocatedStorage)
	assertEqual(t, 1433, inst.Port)

	// Bare-name lookup works when there's exactly one match.
	insts, err := m.DescribeInstances(ctx, []string{"appdb"})
	requireNoError(t, err)
	assertEqual(t, 1, len(insts))

	// Composite-key lookup also works.
	insts, err = m.DescribeInstances(ctx, []string{"srv1/appdb"})
	requireNoError(t, err)
	assertEqual(t, 1, len(insts))

	// State transitions via portable API.
	requireNoError(t, m.StopInstance(ctx, "srv1/appdb"))
	requireNoError(t, m.StartInstance(ctx, "srv1/appdb"))
	requireNoError(t, m.RebootInstance(ctx, "srv1/appdb"))

	requireNoError(t, m.DeleteInstance(ctx, "srv1/appdb"))
}

func TestAmbiguousDatabaseName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv-a"})
	requireNoError(t, err)
	_, err = m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv-b"})
	requireNoError(t, err)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "main", ClusterID: "srv-a"})
	requireNoError(t, err)
	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "main", ClusterID: "srv-b"})
	requireNoError(t, err)

	// Bare "main" is ambiguous now.
	if _, err := m.DescribeInstances(ctx, []string{"main"}); err == nil {
		t.Fatal("expected ambiguity error for bare 'main'")
	}

	// Composite resolves cleanly.
	insts, err := m.DescribeInstances(ctx, []string{"srv-a/main"})
	requireNoError(t, err)
	assertEqual(t, 1, len(insts))
}

func TestCascadeDeleteServer(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv1"})
	requireNoError(t, err)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", ClusterID: "srv1"})
	requireNoError(t, err)

	requireNoError(t, m.DeleteCluster(ctx, "srv1"))

	insts, err := m.DescribeInstances(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 0, len(insts))
}

func TestSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv1"})
	requireNoError(t, err)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "src", ClusterID: "srv1", AllocatedStorage: 50,
	})
	requireNoError(t, err)

	snap, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{
		ID:         "backup-1",
		InstanceID: "srv1/src",
	})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)
	assertEqual(t, 50, snap.AllocatedStorage)

	// Restore into the same server with a different name.
	restored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "srv1/restored",
		SnapshotID:    "backup-1",
	})
	requireNoError(t, err)
	assertEqual(t, "restored", restored.ID)
	assertEqual(t, "srv1", restored.ClusterID)
	assertEqual(t, 50, restored.AllocatedStorage)

	// Bare new-instance ID inherits the source server.
	bareRestored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "bare-restored",
		SnapshotID:    "backup-1",
	})
	requireNoError(t, err)
	assertEqual(t, "srv1", bareRestored.ClusterID)

	requireNoError(t, m.DeleteSnapshot(ctx, "backup-1"))
}

func TestClusterSnapshotsUnsupported(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{
		ID: "x", ClusterID: "y",
	}); err == nil {
		t.Fatal("expected unsupported")
	}

	csnaps, err := m.DescribeClusterSnapshots(ctx, nil, "")
	requireNoError(t, err)
	assertEqual(t, 0, len(csnaps))
}

func TestStartStopClusterIsNoop(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Both pass on a non-existent server — Azure SQL doesn't have explicit
	// server-level start/stop, so we keep these calls inert.
	requireNoError(t, m.StartCluster(ctx, "nonexistent"))
	requireNoError(t, m.StopCluster(ctx, "nonexistent"))
}

// Hand-rolled helpers per CLAUDE.md.

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func TestSubResourcesRequireServer(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{Server: "ghost", Name: "r"}); err == nil {
		t.Error("CreateFirewallRule on missing server: expected error")
	}

	if _, err := m.CreateVNetRule(ctx, rdsdriver.VNetRuleConfig{Server: "ghost", Name: "v"}); err == nil {
		t.Error("CreateVNetRule on missing server: expected error")
	}

	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "ghost", Name: "p"}); err == nil {
		t.Error("CreateElasticPool on missing server: expected error")
	}

	if _, err := m.CreateFailoverGroup(ctx, rdsdriver.FailoverGroupConfig{Server: "ghost", Name: "f"}); err == nil {
		t.Error("CreateFailoverGroup on missing server: expected error")
	}

	if _, err := m.SetAADAdmin(ctx, rdsdriver.AADAdminConfig{Server: "ghost"}); err == nil {
		t.Error("SetAADAdmin on missing server: expected error")
	}
}

func TestFailoverGroupRoleFlipAndCascade(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	fg, err := m.CreateFailoverGroup(ctx, rdsdriver.FailoverGroupConfig{
		Server: "srv", Name: "fg", PartnerServers: []string{"partner"}, Databases: []string{"db1"},
	})
	if err != nil {
		t.Fatalf("CreateFailoverGroup: %v", err)
	}

	if fg.ReplicationRole != "Primary" {
		t.Errorf("initial role: got %q, want Primary", fg.ReplicationRole)
	}

	flipped, err := m.FailoverFailoverGroup(ctx, "srv", "fg")
	if err != nil {
		t.Fatalf("FailoverFailoverGroup: %v", err)
	}

	if flipped.ReplicationRole != "Secondary" {
		t.Errorf("after failover: got %q, want Secondary", flipped.ReplicationRole)
	}

	// Mutating the returned slice must not affect stored state.
	flipped.PartnerServers[0] = "tampered"

	reread, _ := m.GetFailoverGroup(ctx, "srv", "fg")
	if reread.PartnerServers[0] != "partner" {
		t.Error("returned slice aliased stored state")
	}

	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{Server: "srv", Name: "r"}); err != nil {
		t.Fatalf("CreateFirewallRule: %v", err)
	}

	if err := m.DeleteCluster(ctx, "srv"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := m.ListFirewallRules(ctx, "srv"); err == nil {
		t.Error("ListFirewallRules after server delete: expected server NotFound")
	}
}

func TestElasticPoolEmitsMetrics(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))

	m := New(opts)
	mon := azuremonitor.New(opts)
	m.SetMonitoring(mon)

	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "srv", Name: "pool"}); err != nil {
		t.Fatalf("CreateElasticPool: %v", err)
	}

	names, err := mon.ListMetrics(ctx, "Microsoft.Sql/servers/elasticpools")
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}

	var sawCPU bool
	for _, n := range names {
		if n == "cpu_percent" {
			sawCPU = true
		}
	}

	if !sawCPU {
		t.Fatalf("expected cpu_percent on the elastic-pool namespace, got %v", names)
	}
}
