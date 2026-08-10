package alloydb

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/monitoring"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-central1"),
		config.WithProjectID("mock-project"),
	)

	return New(opts)
}

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

func TestClusterInstanceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cluster, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c1", MasterUsername: "admin"})
	requireNoError(t, err)
	assertEqual(t, "c1", cluster.ID)
	assertEqual(t, "available", cluster.State)
	assertEqual(t, defaultPort, cluster.Port)

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c1"}); err == nil {
		t.Error("duplicate cluster: expected AlreadyExists")
	}

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "i1", ClusterID: "c1", InstanceClass: "4-vcpu"})
	requireNoError(t, err)
	assertEqual(t, "i1", inst.ID)
	assertEqual(t, "c1", inst.ClusterID)

	// Instance on a missing cluster is rejected.
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "x", ClusterID: "ghost"}); err == nil {
		t.Error("instance on missing cluster: expected NotFound")
	}

	// Cluster records the instance as a member.
	got, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)
	if len(got[0].Members) != 1 || got[0].Members[0] != "i1" {
		t.Errorf("cluster members: got %v", got[0].Members)
	}

	// Bare + composite instance lookup both work.
	byBare, err := m.DescribeInstances(ctx, []string{"i1"})
	requireNoError(t, err)
	assertEqual(t, 1, len(byBare))

	byComposite, err := m.DescribeInstances(ctx, []string{"c1/i1"})
	requireNoError(t, err)
	assertEqual(t, 1, len(byComposite))

	// Reboot (restart) works; start/stop are unsupported.
	requireNoError(t, m.RebootInstance(ctx, "c1/i1"))

	if err := m.StartInstance(ctx, "c1/i1"); err == nil {
		t.Error("StartInstance: expected unsupported")
	}

	if err := m.StopCluster(ctx, "c1"); err == nil {
		t.Error("StopCluster: expected unsupported")
	}

	// Modify instance machine size.
	upd, err := m.ModifyInstance(ctx, "c1/i1", rdsdriver.ModifyInstanceInput{InstanceClass: "8-vcpu"})
	requireNoError(t, err)
	assertEqual(t, "8-vcpu", upd.InstanceClass)

	requireNoError(t, m.DeleteInstance(ctx, "c1/i1"))

	after, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)
	assertEqual(t, 0, len(after[0].Members))
}

func TestCascadeDeleteCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustCreateCluster(m, ctx, "c1"))

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "i1", ClusterID: "c1"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateUser(ctx, rdsdriver.UserConfig{Instance: "c1", Name: "u1"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "c1", Name: "d1"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	requireNoError(t, m.DeleteCluster(ctx, "c1"))

	insts, err := m.DescribeInstances(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 0, len(insts))

	if _, err := m.ListUsers(ctx, "c1"); err == nil {
		t.Error("ListUsers after cluster delete: expected cluster NotFound")
	}
}

func TestBackupAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustCreateCluster(m, ctx, "src"))

	snap, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "b1", ClusterID: "src"})
	requireNoError(t, err)
	assertEqual(t, "available", snap.State)
	assertEqual(t, "src", snap.ClusterID)

	// Backup of a missing cluster is rejected.
	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "x", ClusterID: "ghost"}); err == nil {
		t.Error("backup of missing cluster: expected NotFound")
	}

	snaps, err := m.DescribeClusterSnapshots(ctx, nil, "src")
	requireNoError(t, err)
	assertEqual(t, 1, len(snaps))

	restored, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{NewClusterID: "restored", SnapshotID: "b1"})
	requireNoError(t, err)
	assertEqual(t, "restored", restored.ID)

	requireNoError(t, m.DeleteClusterSnapshot(ctx, "b1"))

	if err := m.DeleteClusterSnapshot(ctx, "b1"); err == nil {
		t.Error("delete backup again: expected NotFound")
	}
}

func TestInstanceSnapshotsUnsupported(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "s", InstanceID: "i"}); err == nil {
		t.Error("CreateSnapshot: expected unsupported")
	}

	snaps, err := m.DescribeSnapshots(ctx, nil, "i")
	requireNoError(t, err)
	assertEqual(t, 0, len(snaps))

	if _, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{}); err == nil {
		t.Error("RestoreInstanceFromSnapshot: expected unsupported")
	}
}

func TestUsersAndDatabasesCRUD(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, mustCreateCluster(m, ctx, "c1"))

	if _, err := m.CreateUser(ctx, rdsdriver.UserConfig{Instance: "c1", Name: "u1", Host: "%"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if got, err := m.GetUser(ctx, "c1", "u1"); err != nil || got.Name != "u1" {
		t.Fatalf("GetUser: %+v %v", got, err)
	}

	if us, err := m.ListUsers(ctx, "c1"); err != nil || len(us) != 1 {
		t.Fatalf("ListUsers: %d %v", len(us), err)
	}

	if _, err := m.UpdateUser(ctx, rdsdriver.UserConfig{Instance: "c1", Name: "u1", Host: "10.0.0.0/8"}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	requireNoError(t, m.DeleteUser(ctx, "c1", "u1"))

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "c1", Name: "d1"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if got, err := m.GetDatabase(ctx, "c1", "d1"); err != nil || got.Charset != "UTF8" {
		t.Fatalf("GetDatabase: %+v %v", got, err)
	}

	if dbs, err := m.ListDatabases(ctx, "c1"); err != nil || len(dbs) != 1 {
		t.Fatalf("ListDatabases: %d %v", len(dbs), err)
	}

	requireNoError(t, m.DeleteDatabase(ctx, "c1", "d1"))

	// Names with '/' are rejected.
	if _, err := m.CreateUser(ctx, rdsdriver.UserConfig{Instance: "c1", Name: "a/b"}); err == nil {
		t.Error("CreateUser with '/': expected InvalidArgument")
	}
}

func TestResultsDoNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c1", Tags: map[string]string{"env": "prod"}}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)

	got[0].Tags["env"] = "tampered"
	got[0].Members = append(got[0].Members, "phantom")

	reread, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)

	if reread[0].Tags["env"] != "prod" {
		t.Errorf("cluster Tags aliased: got %q", reread[0].Tags["env"])
	}

	if len(reread[0].Members) != 0 {
		t.Errorf("cluster Members aliased: got %v", reread[0].Members)
	}
}

func TestInstanceMetricsEmitted(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithProjectID("p"))

	m := New(opts)
	mon := monitoring.New(opts)
	m.SetMonitoring(mon)

	ctx := context.Background()
	requireNoError(t, mustCreateCluster(m, ctx, "c1"))

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "i1", ClusterID: "c1"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	names, err := mon.ListMetrics(ctx, metricsNamespace)
	requireNoError(t, err)

	if len(names) == 0 {
		t.Fatal("expected AlloyDB instance metrics to be emitted")
	}
}

func mustCreateCluster(m *Mock, ctx context.Context, id string) error {
	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: id})

	return err
}
