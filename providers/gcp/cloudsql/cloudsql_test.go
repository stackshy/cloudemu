package cloudsql

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

func TestCreateInstance(t *testing.T) {
	tests := []struct {
		name      string
		cfg       rdsdriver.InstanceConfig
		expectErr bool
	}{
		{
			name: "MySQL default",
			cfg: rdsdriver.InstanceConfig{
				ID:     "orders",
				Engine: "MYSQL_8_0",
			},
		},
		{
			name: "Postgres explicit tier and storage",
			cfg: rdsdriver.InstanceConfig{
				ID:               "analytics",
				Engine:           "POSTGRES_15",
				InstanceClass:    "db-custom-2-8192",
				AllocatedStorage: 200,
				StorageType:      "PD_HDD",
			},
		},
		{
			name:      "missing identifier",
			cfg:       rdsdriver.InstanceConfig{Engine: "MYSQL_8_0"},
			expectErr: true,
		},
		{
			name:      "missing engine",
			cfg:       rdsdriver.InstanceConfig{ID: "x"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			inst, err := m.CreateInstance(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.ID, inst.ID)
			assertEqual(t, "available", inst.State)
			assertNotEmpty(t, inst.ARN)
			assertNotEmpty(t, inst.Endpoint)

			if inst.Port == 0 {
				t.Errorf("expected default port to be set")
			}
		})
	}
}

func TestPortDefaults(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mysql, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "m", Engine: "MYSQL_8_0"})
	requireNoError(t, err)
	assertEqual(t, 3306, mysql.Port)

	pg, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "p", Engine: "POSTGRES_15"})
	requireNoError(t, err)
	assertEqual(t, 5432, pg.Port)

	ms, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "s", Engine: "SQLSERVER_2019_STANDARD"})
	requireNoError(t, err)
	assertEqual(t, 1433, ms.Port)
}

func TestInstanceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "MYSQL_8_0"})
	requireNoError(t, err)

	requireNoError(t, m.StopInstance(ctx, "db1"))
	insts, err := m.DescribeInstances(ctx, []string{"db1"})
	requireNoError(t, err)
	assertEqual(t, "stopped", insts[0].State)

	// Idempotent stop.
	requireNoError(t, m.StopInstance(ctx, "db1"))

	requireNoError(t, m.StartInstance(ctx, "db1"))
	requireNoError(t, m.RebootInstance(ctx, "db1"))

	requireNoError(t, m.DeleteInstance(ctx, "db1"))

	if _, err := m.DescribeInstances(ctx, []string{"db1"}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1", Engine: "MYSQL_8_0"})
	requireNoError(t, err)

	updated, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "db-custom-4-16384",
		AllocatedStorage: 500,
		Tags:             map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	assertEqual(t, "db-custom-4-16384", updated.InstanceClass)
	assertEqual(t, 500, updated.AllocatedStorage)
	assertEqual(t, "prod", updated.Tags["env"])
}

func TestSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "src",
		Engine:           "POSTGRES_15",
		AllocatedStorage: 100,
	})
	requireNoError(t, err)

	snap, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap1", InstanceID: "src"})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)
	assertEqual(t, 100, snap.AllocatedStorage)

	restored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "restored",
		SnapshotID:    "snap1",
	})
	requireNoError(t, err)
	assertEqual(t, "restored", restored.ID)
	assertEqual(t, 100, restored.AllocatedStorage)
	assertEqual(t, "POSTGRES_15", restored.Engine)
	assertEqual(t, 5432, restored.Port)

	requireNoError(t, m.DeleteSnapshot(ctx, "snap1"))
}

func TestRestoreBackupInPlace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "src",
		Engine:           "POSTGRES_15",
		AllocatedStorage: 100,
	})
	requireNoError(t, err)

	_, err = m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "target",
		Engine:           "MYSQL_8_0",
		AllocatedStorage: 20,
	})
	requireNoError(t, err)

	_, err = m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap1", InstanceID: "src"})
	requireNoError(t, err)

	// Restoring in place keeps the target's identity but adopts the backup's
	// engine/version/storage — and does NOT create a new instance.
	restored, err := m.RestoreBackup(ctx, "target", "snap1")
	requireNoError(t, err)
	assertEqual(t, "target", restored.ID)
	assertEqual(t, 100, restored.AllocatedStorage)
	assertEqual(t, "POSTGRES_15", restored.Engine)

	instances, err := m.DescribeInstances(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 2, len(instances))

	// Missing target and missing backup both surface NotFound.
	if _, err := m.RestoreBackup(ctx, "ghost", "snap1"); err == nil {
		t.Error("RestoreBackup onto missing instance: expected NotFound")
	}

	if _, err := m.RestoreBackup(ctx, "target", "ghost"); err == nil {
		t.Error("RestoreBackup with missing backup: expected NotFound")
	}
}

func TestClusterOpsUnsupported(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c", Engine: "x"}); err == nil {
		t.Fatal("CreateCluster should be unsupported on Cloud SQL")
	}

	clusters, err := m.DescribeClusters(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 0, len(clusters))

	if err := m.StartCluster(ctx, "c"); err == nil {
		t.Fatal("StartCluster should be unsupported on Cloud SQL")
	}

	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "s", ClusterID: "c"}); err == nil {
		t.Fatal("CreateClusterSnapshot should be unsupported on Cloud SQL")
	}

	csnaps, err := m.DescribeClusterSnapshots(ctx, nil, "")
	requireNoError(t, err)
	assertEqual(t, 0, len(csnaps))
}

// Hand-rolled helpers per CLAUDE.md (provider tests don't use testify).

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error, got nil")
	case !expectErr && err != nil:
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

func TestSubResourcesRequireInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "ghost", Name: "db"}); err == nil {
		t.Error("CreateDatabase on missing instance: expected error")
	}

	if _, err := m.CreateUser(ctx, rdsdriver.UserConfig{Instance: "ghost", Name: "u"}); err == nil {
		t.Error("CreateUser on missing instance: expected error")
	}

	if _, err := m.CreateSslCert(ctx, rdsdriver.SslCertConfig{Instance: "ghost", CommonName: "c"}); err == nil {
		t.Error("CreateSslCert on missing instance: expected error")
	}
}

func TestUpdateDatabase(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "inst", Engine: "POSTGRES_15"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "inst", Name: "app"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	updated, err := m.UpdateDatabase(ctx, rdsdriver.DatabaseConfig{
		Server: "inst", Name: "app", Charset: "LATIN1", Collation: "en_US.ISO8859-1",
	})
	requireNoError(t, err)
	assertEqual(t, "LATIN1", updated.Charset)
	assertEqual(t, "en_US.ISO8859-1", updated.Collation)

	got, err := m.GetDatabase(ctx, "inst", "app")
	requireNoError(t, err)
	assertEqual(t, "LATIN1", got.Charset)
	assertEqual(t, "en_US.ISO8859-1", got.Collation)

	if _, err := m.UpdateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "inst", Name: "ghost"}); err == nil {
		t.Error("UpdateDatabase on missing database: expected error")
	}
}

func TestInstanceSettingsRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	created, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "s",
		Engine:           "POSTGRES_15",
		MultiAZ:          true,
		GCPDatabaseFlags: `[{"name":"max_connections","value":"100"}]`,
		GCPBackupConfig:  `{"enabled":true}`,
		GCPIPConfig:      `{"ipv4Enabled":true}`,
	})
	requireNoError(t, err)
	assertEqual(t, true, created.MultiAZ)

	got, err := m.DescribeInstances(ctx, []string{"s"})
	requireNoError(t, err)
	assertEqual(t, true, got[0].MultiAZ)
	assertEqual(t, `[{"name":"max_connections","value":"100"}]`, got[0].GCPDatabaseFlags)
	assertEqual(t, `{"enabled":true}`, got[0].GCPBackupConfig)
	assertEqual(t, `{"ipv4Enabled":true}`, got[0].GCPIPConfig)

	falseVal := false

	if _, err := m.ModifyInstance(ctx, "s", rdsdriver.ModifyInstanceInput{
		MultiAZ:          &falseVal,
		GCPDatabaseFlags: `[{"name":"work_mem","value":"64MB"}]`,
	}); err != nil {
		t.Fatalf("ModifyInstance: %v", err)
	}

	after, err := m.DescribeInstances(ctx, []string{"s"})
	requireNoError(t, err)
	assertEqual(t, false, after[0].MultiAZ)
	assertEqual(t, `[{"name":"work_mem","value":"64MB"}]`, after[0].GCPDatabaseFlags)
	// Untouched blobs are preserved (patch merges).
	assertEqual(t, `{"enabled":true}`, after[0].GCPBackupConfig)
}

func TestCloneAndCascade(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "src", Engine: "POSTGRES_15"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "src", Name: "app"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if _, err := m.CreateUser(ctx, rdsdriver.UserConfig{Instance: "src", Name: "u"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	clone, err := m.CloneInstance(ctx, "src", "dst")
	if err != nil {
		t.Fatalf("CloneInstance: %v", err)
	}

	if clone.ID != "dst" || clone.Engine != "POSTGRES_15" {
		t.Errorf("clone: got id=%q engine=%q", clone.ID, clone.Engine)
	}

	if _, err := m.CloneInstance(ctx, "src", "dst"); err == nil {
		t.Error("clone onto existing instance: expected AlreadyExists")
	}

	// Deleting the source cascades to its children but leaves the clone.
	if err := m.DeleteInstance(ctx, "src"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if _, err := m.ListDatabases(ctx, "src"); err == nil {
		t.Error("ListDatabases after instance delete: expected NotFound")
	}

	if _, err := m.DescribeInstances(ctx, []string{"dst"}); err != nil {
		t.Errorf("clone should survive source delete: %v", err)
	}
}

func TestCloudSQLReplicaAndFailoverActions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "i", Engine: "POSTGRES_15"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Failover is valid on a primary, not on a replica.
	if err := m.FailoverInstance(ctx, "i"); err != nil {
		t.Errorf("FailoverInstance: %v", err)
	}

	// Promote requires an actual replica.
	if err := m.PromoteReplica(ctx, "i"); err == nil {
		t.Error("PromoteReplica on a non-replica: expected FailedPrecondition")
	}

	// Create a replica of i, then promote it — it detaches and the primary
	// loses it from its replica list.
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "r", Engine: "POSTGRES_15", MasterInstanceName: "i",
	}); err != nil {
		t.Fatalf("CreateInstance replica: %v", err)
	}

	if err := m.FailoverInstance(ctx, "r"); err == nil {
		t.Error("FailoverInstance on a replica: expected FailedPrecondition")
	}

	if err := m.PromoteReplica(ctx, "r"); err != nil {
		t.Fatalf("PromoteReplica: %v", err)
	}

	got, _ := m.DescribeInstances(ctx, []string{"r"})
	if got[0].ReadReplicaSource != "" {
		t.Errorf("promoted replica still has master %q", got[0].ReadReplicaSource)
	}

	primary, _ := m.DescribeInstances(ctx, []string{"i"})
	if len(primary[0].ReadReplicaTargets) != 0 {
		t.Errorf("primary still lists promoted replica: %v", primary[0].ReadReplicaTargets)
	}

	if err := m.FailoverInstance(ctx, "ghost"); err == nil {
		t.Error("FailoverInstance on missing instance: expected NotFound")
	}
}

func TestDeleteInstanceUnlinksReplicas(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	mk := func(id, master string) {
		_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
			ID: id, Engine: "POSTGRES_15", MasterInstanceName: master,
		})
		if err != nil {
			t.Fatalf("CreateInstance %q: %v", id, err)
		}
	}

	mk("master", "")
	mk("replica", "master")

	// Deleting the replica drops it from the master's target list.
	if err := m.DeleteInstance(ctx, "replica"); err != nil {
		t.Fatalf("DeleteInstance replica: %v", err)
	}

	got, _ := m.DescribeInstances(ctx, []string{"master"})
	if len(got[0].ReadReplicaTargets) != 0 {
		t.Errorf("master still lists a deleted replica: %v", got[0].ReadReplicaTargets)
	}

	// Deleting the master clears the source pointer on its surviving replica.
	mk("replica2", "master")

	if err := m.DeleteInstance(ctx, "master"); err != nil {
		t.Fatalf("DeleteInstance master: %v", err)
	}

	got, _ = m.DescribeInstances(ctx, []string{"replica2"})
	if got[0].ReadReplicaSource != "" {
		t.Errorf("replica still points at a deleted master: %q", got[0].ReadReplicaSource)
	}
}

func TestSubResourceCRUDCoverage(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "inst", Engine: "MYSQL_8_0"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Databases.
	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "inst", Name: "app"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if got, err := m.GetDatabase(ctx, "inst", "app"); err != nil || got.Name != "app" {
		t.Fatalf("GetDatabase: %+v %v", got, err)
	}

	if err := m.DeleteDatabase(ctx, "inst", "app"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	// Users: create, get, list, update, delete.
	if _, err := m.CreateUser(ctx, rdsdriver.UserConfig{Instance: "inst", Name: "u1", Host: "%", Password: "p1"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if got, err := m.GetUser(ctx, "inst", "u1"); err != nil || got.Name != "u1" {
		t.Fatalf("GetUser: %+v %v", got, err)
	}

	if us, err := m.ListUsers(ctx, "inst"); err != nil || len(us) != 1 {
		t.Fatalf("ListUsers: %d %v", len(us), err)
	}

	if _, err := m.UpdateUser(ctx, rdsdriver.UserConfig{Instance: "inst", Name: "u1", Host: "%", Password: "p2"}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	if _, err := m.UpdateUser(ctx, rdsdriver.UserConfig{Instance: "inst", Name: "ghost", Host: "%"}); err == nil {
		t.Error("UpdateUser on missing user: expected NotFound")
	}

	if err := m.DeleteUser(ctx, "inst", "u1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// SSL certs: create, get, list, delete.
	cert, err := m.CreateSslCert(ctx, rdsdriver.SslCertConfig{Instance: "inst", CommonName: "client"})
	if err != nil {
		t.Fatalf("CreateSslCert: %v", err)
	}

	if got, err := m.GetSslCert(ctx, "inst", cert.Sha1Fingerprint); err != nil || got.CommonName != "client" {
		t.Fatalf("GetSslCert: %+v %v", got, err)
	}

	if cs, err := m.ListSslCerts(ctx, "inst"); err != nil || len(cs) != 1 {
		t.Fatalf("ListSslCerts: %d %v", len(cs), err)
	}

	if err := m.DeleteSslCert(ctx, "inst", cert.Sha1Fingerprint); err != nil {
		t.Fatalf("DeleteSslCert: %v", err)
	}

	// Snapshot describe + delete-snapshot NotFound.
	if _, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "b1", InstanceID: "inst"}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if snaps, err := m.DescribeSnapshots(ctx, nil, "inst"); err != nil || len(snaps) != 1 {
		t.Fatalf("DescribeSnapshots: %d %v", len(snaps), err)
	}
}

func TestClusterOpsAndMonitoringCoverage(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithProjectID("p"))

	m := New(opts)
	mon := monitoring.New(opts)
	m.SetMonitoring(mon)

	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "inst", Engine: "MYSQL_8_0"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Cluster and cluster-snapshot ops are unsupported on Cloud SQL.
	if _, err := m.ModifyCluster(ctx, "x", rdsdriver.ModifyInstanceInput{}); err == nil {
		t.Error("ModifyCluster: expected unsupported")
	}

	if err := m.DeleteCluster(ctx, "x"); err == nil {
		t.Error("DeleteCluster: expected unsupported")
	}

	if err := m.StopCluster(ctx, "x"); err == nil {
		t.Error("StopCluster: expected unsupported")
	}

	if err := m.DeleteClusterSnapshot(ctx, "x"); err == nil {
		t.Error("DeleteClusterSnapshot: expected unsupported")
	}

	if _, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{}); err == nil {
		t.Error("RestoreClusterFromSnapshot: expected unsupported")
	}
}

func TestDescribeInstancesResultDoesNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "inst", Engine: "MYSQL_8_0", Tags: map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	got, err := m.DescribeInstances(ctx, []string{"inst"})
	requireNoError(t, err)

	// Mutating the returned Tags map must not corrupt the store.
	got[0].Tags["env"] = "tampered"
	got[0].Tags["injected"] = "x"

	reread, err := m.DescribeInstances(ctx, []string{"inst"})
	requireNoError(t, err)

	if reread[0].Tags["env"] != "prod" {
		t.Errorf("store Tags aliased: got %q, want prod", reread[0].Tags["env"])
	}

	if _, ok := reread[0].Tags["injected"]; ok {
		t.Error("store Tags aliased: injected key leaked into store")
	}
}

func TestChildNameRejectsSlash(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "inst", Engine: "MYSQL_8_0"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// A '/' in a child name would collide with the "{instance}/{name}" key and
	// create a row unreachable via single-segment GET/DELETE.
	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "inst", Name: "a/b"}); err == nil {
		t.Error("CreateDatabase with '/' in name: expected InvalidArgument")
	}

	if _, err := m.CreateUser(ctx, rdsdriver.UserConfig{Instance: "inst", Name: "a/b"}); err == nil {
		t.Error("CreateUser with '/' in name: expected InvalidArgument")
	}
}
