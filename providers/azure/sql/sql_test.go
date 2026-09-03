package sql

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/monitor"
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

func TestFailoverGroupWithoutPartnerRejected(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// A group with no partner server can't fail over — otherwise it would
	// ping-pong its role and leave a Secondary with no Primary.
	if _, err := m.CreateFailoverGroup(ctx, rdsdriver.FailoverGroupConfig{Server: "srv", Name: "fg"}); err != nil {
		t.Fatalf("CreateFailoverGroup: %v", err)
	}

	if _, err := m.FailoverFailoverGroup(ctx, "srv", "fg"); err == nil {
		t.Error("FailoverFailoverGroup with no partner: expected FailedPrecondition")
	}
}

func TestCreateFirewallRuleRejectsReversedRange(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{
		Server: "srv", Name: "r", StartIPAddress: "10.0.0.9", EndIPAddress: "10.0.0.1",
	}); err == nil {
		t.Error("CreateFirewallRule with start > end: expected InvalidArgument")
	}

	// Equal start/end (single-address rule) is allowed.
	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{
		Server: "srv", Name: "single", StartIPAddress: "10.0.0.5", EndIPAddress: "10.0.0.5",
	}); err != nil {
		t.Errorf("CreateFirewallRule with start == end: %v", err)
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

	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{Server: "srv", Name: "r", StartIPAddress: "10.0.0.1", EndIPAddress: "10.0.0.9"}); err != nil {
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
	mon := monitor.New(opts)
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

func TestManagedInstanceLifecycleAndCascade(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{Name: "mi", SubnetID: "/subnets/mi"}); err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	if _, err := m.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{Name: "mi"}); err == nil {
		t.Error("duplicate managed instance: expected AlreadyExists")
	}

	if _, err := m.CreateManagedDatabase(ctx, rdsdriver.ManagedDatabaseConfig{Instance: "ghost", Name: "db"}); err == nil {
		t.Error("managed database on missing instance: expected NotFound")
	}

	if _, err := m.CreateManagedDatabase(ctx, rdsdriver.ManagedDatabaseConfig{Instance: "mi", Name: "db"}); err != nil {
		t.Fatalf("CreateManagedDatabase: %v", err)
	}

	if err := m.StopManagedInstance(ctx, "mi"); err != nil {
		t.Fatalf("StopManagedInstance: %v", err)
	}

	got, _ := m.GetManagedInstance(ctx, "mi")
	if got.State != "Stopped" {
		t.Errorf("state after stop: got %q, want Stopped", got.State)
	}

	// Deleting the instance cascades to its managed databases.
	if err := m.DeleteManagedInstance(ctx, "mi"); err != nil {
		t.Fatalf("DeleteManagedInstance: %v", err)
	}

	if _, err := m.ListManagedDatabases(ctx, "mi"); err == nil {
		t.Error("ListManagedDatabases after instance delete: expected NotFound")
	}
}

func TestElasticPoolMembershipBlocksDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "srv", Name: "pool"}); err != nil {
		t.Fatalf("CreateElasticPool: %v", err)
	}

	poolID := "/subscriptions/x/resourceGroups/x/providers/Microsoft.Sql/servers/srv/elasticPools/pool"
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db1", ClusterID: "srv", ElasticPoolID: poolID,
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The database round-trips its pool membership.
	got, _ := m.DescribeInstances(ctx, []string{"srv/db1"})
	if len(got) != 1 || got[0].ElasticPoolID != poolID {
		t.Fatalf("elasticPoolId not persisted: %+v", got)
	}

	// A non-empty pool cannot be deleted.
	if err := m.DeleteElasticPool(ctx, "srv", "pool"); err == nil {
		t.Error("DeleteElasticPool on non-empty pool: expected FailedPrecondition")
	}

	// After the database is removed, the pool deletes cleanly.
	if err := m.DeleteInstance(ctx, "srv/db1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if err := m.DeleteElasticPool(ctx, "srv", "pool"); err != nil {
		t.Errorf("DeleteElasticPool on empty pool: %v", err)
	}
}

// TestConcurrentSubResourceAccess exercises the mock under -race: concurrent
// mutators and readers, plus a caller mutating the Tags map returned from a
// managed-instance read (which must be a clone, not the stored map).
func TestConcurrentSubResourceAccess(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{
		Name: "mi", SubnetID: "/subnets/mi", Tags: map[string]string{"a": "b"},
	}); err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_, _ = m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{
				Server: "srv", Name: fmt.Sprintf("r%d", i),
				StartIPAddress: "10.0.0.1", EndIPAddress: "10.0.0.2",
			})
			_, _ = m.ListFirewallRules(ctx, "srv")

			if got, err := m.GetManagedInstance(ctx, "mi"); err == nil {
				// Mutating the returned Tags must not race the stored map.
				got.Tags["writer"] = "x"
			}
		}(i)
	}

	wg.Wait()
}

func TestManagedInstanceStateGuards(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{Name: "mi", SubnetID: "/subnets/mi"}); err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	// Stop, then a failover on a stopped instance is rejected (not silently started).
	if err := m.StopManagedInstance(ctx, "mi"); err != nil {
		t.Fatalf("StopManagedInstance: %v", err)
	}

	if err := m.FailoverManagedInstance(ctx, "mi"); err == nil {
		t.Error("FailoverManagedInstance on a stopped instance: expected FailedPrecondition")
	}

	got, _ := m.GetManagedInstance(ctx, "mi")
	if got.State != "Stopped" {
		t.Errorf("state after stop: got %q, want Stopped", got.State)
	}

	// Idempotent stop; start from stopped; failover once ready.
	if err := m.StopManagedInstance(ctx, "mi"); err != nil {
		t.Errorf("StopManagedInstance (idempotent): %v", err)
	}

	if err := m.StartManagedInstance(ctx, "mi"); err != nil {
		t.Fatalf("StartManagedInstance: %v", err)
	}

	if err := m.FailoverManagedInstance(ctx, "mi"); err != nil {
		t.Errorf("FailoverManagedInstance on a ready instance: %v", err)
	}

	if err := m.StartManagedInstance(ctx, "ghost"); err == nil {
		t.Error("StartManagedInstance on missing instance: expected NotFound")
	}
}

func TestSubResourceCRUDCoverage(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// VNet rules: create, get, list, delete.
	if _, err := m.CreateVNetRule(ctx, rdsdriver.VNetRuleConfig{Server: "srv", Name: "v1", SubnetID: "/subnets/a"}); err != nil {
		t.Fatalf("CreateVNetRule: %v", err)
	}

	if got, err := m.GetVNetRule(ctx, "srv", "v1"); err != nil || got.SubnetID != "/subnets/a" {
		t.Fatalf("GetVNetRule: %+v %v", got, err)
	}

	if vs, err := m.ListVNetRules(ctx, "srv"); err != nil || len(vs) != 1 {
		t.Fatalf("ListVNetRules: %d %v", len(vs), err)
	}

	if err := m.DeleteVNetRule(ctx, "srv", "v1"); err != nil {
		t.Fatalf("DeleteVNetRule: %v", err)
	}

	if err := m.DeleteVNetRule(ctx, "srv", "v1"); err == nil {
		t.Error("DeleteVNetRule again: expected NotFound")
	}

	// Elastic pools: create, get, list, update, delete.
	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "srv", Name: "p1", SKUName: "GP_Gen5", MaxCapacity: 4}); err != nil {
		t.Fatalf("CreateElasticPool: %v", err)
	}

	if got, err := m.GetElasticPool(ctx, "srv", "p1"); err != nil || got.SKUName != "GP_Gen5" {
		t.Fatalf("GetElasticPool: %+v %v", got, err)
	}

	if ps, err := m.ListElasticPools(ctx, "srv"); err != nil || len(ps) != 1 {
		t.Fatalf("ListElasticPools: %d %v", len(ps), err)
	}

	updated, err := m.UpdateElasticPool(ctx, rdsdriver.ElasticPoolConfig{
		Server: "srv", Name: "p1", SKUName: "GP_Gen5_8", SKUTier: "GeneralPurpose",
		MaxSizeBytes: 1 << 40, MinCapacity: 1, MaxCapacity: 8, Location: "westus",
	})
	if err != nil || updated.MaxCapacity != 8 || updated.SKUName != "GP_Gen5_8" {
		t.Fatalf("UpdateElasticPool: %+v %v", updated, err)
	}

	if err := m.DeleteElasticPool(ctx, "srv", "p1"); err != nil {
		t.Fatalf("DeleteElasticPool: %v", err)
	}

	// Failover groups: get, list, update, delete.
	if _, err := m.CreateFailoverGroup(ctx, rdsdriver.FailoverGroupConfig{Server: "srv", Name: "fg", PartnerServers: []string{"p"}, Databases: []string{"d1"}}); err != nil {
		t.Fatalf("CreateFailoverGroup: %v", err)
	}

	if got, err := m.GetFailoverGroup(ctx, "srv", "fg"); err != nil || len(got.Databases) != 1 {
		t.Fatalf("GetFailoverGroup: %+v %v", got, err)
	}

	if fgs, err := m.ListFailoverGroups(ctx, "srv"); err != nil || len(fgs) != 1 {
		t.Fatalf("ListFailoverGroups: %d %v", len(fgs), err)
	}

	upd, err := m.UpdateFailoverGroup(ctx, rdsdriver.FailoverGroupConfig{
		Server: "srv", Name: "fg", FailoverPolicy: "Automatic", GracePeriodMinutes: 60,
		PartnerServers: []string{"p2"}, Databases: []string{"d1", "d2"},
	})
	if err != nil || len(upd.Databases) != 2 || upd.FailoverPolicy != "Automatic" {
		t.Fatalf("UpdateFailoverGroup: %+v %v", upd, err)
	}

	if err := m.DeleteFailoverGroup(ctx, "srv", "fg"); err != nil {
		t.Fatalf("DeleteFailoverGroup: %v", err)
	}

	// AAD admin: set, get, list, delete.
	if _, err := m.SetAADAdmin(ctx, rdsdriver.AADAdminConfig{Server: "srv", Login: "admin@contoso.com", SID: "sid-1"}); err != nil {
		t.Fatalf("SetAADAdmin: %v", err)
	}

	if got, err := m.GetAADAdmin(ctx, "srv", ""); err != nil || got.Login != "admin@contoso.com" {
		t.Fatalf("GetAADAdmin: %+v %v", got, err)
	}

	if as, err := m.ListAADAdmins(ctx, "srv"); err != nil || len(as) != 1 {
		t.Fatalf("ListAADAdmins: %d %v", len(as), err)
	}

	if err := m.DeleteAADAdmin(ctx, "srv", ""); err != nil {
		t.Fatalf("DeleteAADAdmin: %v", err)
	}
}

func TestModifyInstanceMergesFields(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", ClusterID: "srv", AllocatedStorage: 10}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	out, err := m.ModifyInstance(ctx, "srv/db", rdsdriver.ModifyInstanceInput{AllocatedStorage: 50, EngineVersion: "12.0"})
	if err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	if out.AllocatedStorage != 50 || out.EngineVersion != "12.0" {
		t.Errorf("ModifyInstance merge: got storage=%d version=%q", out.AllocatedStorage, out.EngineVersion)
	}

	if _, err := m.ModifyInstance(ctx, "srv/ghost", rdsdriver.ModifyInstanceInput{}); err == nil {
		t.Error("ModifyInstance on missing instance: expected NotFound")
	}
}

func TestManagedInstanceAndDatabaseCRUD(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{Name: "mi", SubnetID: "/subnets/mi", VCores: 4, StorageGB: 32}); err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	upd, err := m.UpdateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{Name: "mi", VCores: 8})
	if err != nil || upd.VCores != 8 {
		t.Fatalf("UpdateManagedInstance: %+v %v", upd, err)
	}

	if mis, err := m.ListManagedInstances(ctx); err != nil || len(mis) != 1 {
		t.Fatalf("ListManagedInstances: %d %v", len(mis), err)
	}

	// Managed databases.
	if _, err := m.CreateManagedDatabase(ctx, rdsdriver.ManagedDatabaseConfig{Instance: "mi", Name: "mdb"}); err != nil {
		t.Fatalf("CreateManagedDatabase: %v", err)
	}

	if got, err := m.GetManagedDatabase(ctx, "mi", "mdb"); err != nil || got.Name != "mdb" {
		t.Fatalf("GetManagedDatabase: %+v %v", got, err)
	}

	if dbs, err := m.ListManagedDatabases(ctx, "mi"); err != nil || len(dbs) != 1 {
		t.Fatalf("ListManagedDatabases: %d %v", len(dbs), err)
	}

	if err := m.DeleteManagedDatabase(ctx, "mi", "mdb"); err != nil {
		t.Fatalf("DeleteManagedDatabase: %v", err)
	}

	// Deleting the instance cascades to its managed databases.
	if _, err := m.CreateManagedDatabase(ctx, rdsdriver.ManagedDatabaseConfig{Instance: "mi", Name: "mdb2"}); err != nil {
		t.Fatalf("CreateManagedDatabase 2: %v", err)
	}

	if err := m.DeleteManagedInstance(ctx, "mi"); err != nil {
		t.Fatalf("DeleteManagedInstance: %v", err)
	}

	if _, err := m.GetManagedInstance(ctx, "mi"); err == nil {
		t.Error("GetManagedInstance after delete: expected NotFound")
	}
}

func TestSnapshotsAndFirewallCoverage(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", ClusterID: "srv"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "s1", InstanceID: "srv/db"}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	// DescribeSnapshots by instance and by id.
	if snaps, err := m.DescribeSnapshots(ctx, nil, "srv/db"); err != nil || len(snaps) != 1 {
		t.Fatalf("DescribeSnapshots by instance: %d %v", len(snaps), err)
	}

	if snaps, err := m.DescribeSnapshots(ctx, []string{"s1"}, ""); err != nil || len(snaps) != 1 {
		t.Fatalf("DescribeSnapshots by id: %d %v", len(snaps), err)
	}

	// Server-level snapshot ops are unsupported on Azure SQL.
	if err := m.DeleteClusterSnapshot(ctx, "x"); err == nil {
		t.Error("DeleteClusterSnapshot: expected unsupported")
	}

	if _, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{}); err == nil {
		t.Error("RestoreClusterFromSnapshot: expected unsupported")
	}

	// Firewall get + delete.
	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{Server: "srv", Name: "r", StartIPAddress: "10.0.0.1", EndIPAddress: "10.0.0.9"}); err != nil {
		t.Fatalf("CreateFirewallRule: %v", err)
	}

	if got, err := m.GetFirewallRule(ctx, "srv", "r"); err != nil || got.EndIPAddress != "10.0.0.9" {
		t.Fatalf("GetFirewallRule: %+v %v", got, err)
	}

	if err := m.DeleteFirewallRule(ctx, "srv", "r"); err != nil {
		t.Fatalf("DeleteFirewallRule: %v", err)
	}

	if err := m.DeleteFirewallRule(ctx, "srv", "r"); err == nil {
		t.Error("DeleteFirewallRule again: expected NotFound")
	}
}

func TestDatabaseAndManagedInstanceEmitMetrics(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))

	m := New(opts)
	mon := monitor.New(opts)
	m.SetMonitoring(mon)

	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", ClusterID: "srv"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{Name: "mi", SubnetID: "/subnets/mi"}); err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	if names, err := mon.ListMetrics(ctx, "Microsoft.Sql/servers/databases"); err != nil || len(names) == 0 {
		t.Fatalf("database metrics: %v %v", names, err)
	}

	if names, err := mon.ListMetrics(ctx, "Microsoft.Sql/managedInstances"); err != nil || len(names) == 0 {
		t.Fatalf("managed-instance metrics: %v %v", names, err)
	}
}

func TestDescribeResultsDoNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv", Tags: map[string]string{"env": "prod"}}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db", ClusterID: "srv", Tags: map[string]string{"tier": "gold"},
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Mutating returned instance Tags must not corrupt the store.
	insts, err := m.DescribeInstances(ctx, []string{"srv/db"})
	requireNoError(t, err)
	insts[0].Tags["tier"] = "tampered"

	reread, err := m.DescribeInstances(ctx, []string{"srv/db"})
	requireNoError(t, err)
	if reread[0].Tags["tier"] != "gold" {
		t.Errorf("instance Tags aliased: got %q, want gold", reread[0].Tags["tier"])
	}

	// Same for clusters.
	clusters, err := m.DescribeClusters(ctx, []string{"srv"})
	requireNoError(t, err)
	clusters[0].Tags["env"] = "tampered"
	clusters[0].Members = append(clusters[0].Members, "phantom")

	rc, err := m.DescribeClusters(ctx, []string{"srv"})
	requireNoError(t, err)
	if rc[0].Tags["env"] != "prod" {
		t.Errorf("cluster Tags aliased: got %q, want prod", rc[0].Tags["env"])
	}
	if len(rc[0].Members) != 1 {
		t.Errorf("cluster Members aliased: got %v", rc[0].Members)
	}
}

func TestCreateDatabaseCopy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "srv", Name: "src", Collation: "SQL_Latin1_General_CP1_CI_AS",
		SKUName: "S3", SKUTier: "Standard", SKUCapacity: 100,
	}); err != nil {
		t.Fatalf("CreateDatabase source: %v", err)
	}

	// Bare-name source reference on the same server; inherits unset properties.
	copyDB, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "srv", Name: "copy", CreateMode: "Copy", SourceDatabaseID: "src",
	})
	if err != nil {
		t.Fatalf("CreateDatabase copy: %v", err)
	}

	assertEqual(t, "SQL_Latin1_General_CP1_CI_AS", copyDB.Collation)
	assertEqual(t, "S3", copyDB.SKUName)
	assertEqual(t, "Standard", copyDB.SKUTier)
	assertEqual(t, 100, copyDB.SKUCapacity)

	// The copy is independent: deleting the source does not remove it.
	if err := m.DeleteDatabase(ctx, "srv", "src"); err != nil {
		t.Fatalf("DeleteDatabase source: %v", err)
	}

	if _, err := m.GetDatabase(ctx, "srv", "copy"); err != nil {
		t.Fatalf("copy vanished after source delete: %v", err)
	}

	// Copy from a nonexistent source is NotFound.
	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "srv", Name: "orphan", CreateMode: "Copy",
		SourceDatabaseID: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/databases/ghost",
	}); err == nil {
		t.Error("copy from nonexistent source: expected NotFound")
	}

	// Copy without a source id is InvalidArgument.
	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "srv", Name: "nosrc", CreateMode: "Copy",
	}); err == nil {
		t.Error("copy without sourceDatabaseId: expected InvalidArgument")
	}
}

func TestCreateDatabaseValidatesElasticPool(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// Referencing a nonexistent pool is rejected.
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db1", ClusterID: "srv", ElasticPoolID: "ghost-pool",
	}); err == nil {
		t.Error("CreateInstance into a nonexistent pool: expected NotFound")
	}

	if _, err := m.CreateElasticPool(ctx, rdsdriver.ElasticPoolConfig{Server: "srv", Name: "pool1"}); err != nil {
		t.Fatalf("CreateElasticPool: %v", err)
	}

	// Bare name resolves.
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db1", ClusterID: "srv", ElasticPoolID: "pool1",
	}); err != nil {
		t.Fatalf("CreateInstance into an existing pool: %v", err)
	}

	// Full ARM ID resolves too.
	poolID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/servers/srv/elasticPools/pool1"
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db2", ClusterID: "srv", ElasticPoolID: poolID,
	}); err != nil {
		t.Fatalf("CreateInstance with pool ARM ID: %v", err)
	}

	// Moving a DB into a nonexistent pool via Modify is rejected.
	if _, err := m.ModifyInstance(ctx, "srv/db1", rdsdriver.ModifyInstanceInput{ElasticPoolID: "ghost"}); err == nil {
		t.Error("ModifyInstance into a nonexistent pool: expected NotFound")
	}
}
