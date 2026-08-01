package bigtable

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

const proj = "projects/p1"

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-central1"), config.WithAccountID("p1"))

	return New(opts)
}

func mustInstance(t *testing.T, m *Mock, id string) string {
	t.Helper()

	name := proj + "/instances/" + id
	if _, _, err := m.CreateInstance(context.Background(), btdriver.CreateInstanceConfig{
		Name: name, DisplayName: id,
		Clusters: []btdriver.CreateClusterConfig{{
			Name: name + "/clusters/c1", Location: "us-central1-a", ServeNodes: 3,
		}},
	}); err != nil {
		t.Fatalf("CreateInstance %s: %v", id, err)
	}

	return name
}

func TestInstanceLifecycleWithInitialCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// An instance requires at least one cluster.
	if _, _, err := m.CreateInstance(ctx, btdriver.CreateInstanceConfig{Name: proj + "/instances/none"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("instance with no clusters: got %v, want InvalidArgument", err)
	}

	inst := mustInstance(t, m, "app")

	got, err := m.GetInstance(ctx, inst)
	if err != nil || got.State != btdriver.StateReady {
		t.Fatalf("GetInstance: %v %+v", err, got)
	}

	// The initial cluster exists.
	clusters, _ := m.ListClusters(ctx, inst)
	if len(clusters) != 1 || clusters[0].ServeNodes != 3 {
		t.Fatalf("initial cluster wrong: %+v", clusters)
	}

	list, _ := m.ListInstances(ctx, "p1")
	if len(list) != 1 {
		t.Fatalf("ListInstances: got %d, want 1", len(list))
	}

	// Cascade delete removes clusters.
	if err := m.DeleteInstance(ctx, inst); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if c, _ := m.ListClusters(ctx, inst); len(c) != 0 {
		t.Fatalf("clusters survived instance delete: %+v", c)
	}
}

func TestClusterParentValidationAndUpdate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	// Cluster under a missing instance is rejected.
	if _, _, err := m.CreateCluster(ctx, btdriver.CreateClusterConfig{
		Name: proj + "/instances/ghost/clusters/c9", Location: "us-central1-a", ServeNodes: 3,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("cluster under missing instance: got %v, want InvalidArgument", err)
	}

	// Scale the cluster up.
	c, _, err := m.UpdateCluster(ctx, inst+"/clusters/c1", 5, nil)
	if err != nil || c.ServeNodes != 5 {
		t.Fatalf("UpdateCluster: %v %+v", err, c)
	}

	// Oversized serve-node count is rejected.
	if _, _, err := m.CreateCluster(ctx, btdriver.CreateClusterConfig{
		Name: inst + "/clusters/big", Location: "us-central1-b", ServeNodes: maxServeNodes + 1,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("oversized cluster: got %v, want InvalidArgument", err)
	}
}

func TestTablesAndColumnFamilies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	tbl, err := m.CreateTable(ctx, btdriver.CreateTableConfig{
		Parent: inst, TableID: "events",
		ColumnFamilies: map[string]btdriver.ColumnFamily{"cf1": {GCRule: &btdriver.GCRule{MaxNumVersions: 3}}},
	})
	if err != nil || len(tbl.ColumnFamilies) != 1 {
		t.Fatalf("CreateTable: %v %+v", err, tbl)
	}

	name := inst + "/tables/events"

	// Modify column families: add, update, drop.
	updated, err := m.ModifyColumnFamilies(ctx, name, []btdriver.ColumnFamilyModification{
		{ID: "cf2", Create: &btdriver.ColumnFamily{GCRule: &btdriver.GCRule{MaxAgeSeconds: 86400}}},
		{ID: "cf1", Update: &btdriver.ColumnFamily{GCRule: &btdriver.GCRule{MaxNumVersions: 1}}},
	})
	if err != nil {
		t.Fatalf("ModifyColumnFamilies: %v", err)
	}

	if updated.ColumnFamilies["cf1"].GCRule.MaxNumVersions != 1 || updated.ColumnFamilies["cf2"].GCRule.MaxAgeSeconds != 86400 {
		t.Fatalf("column families wrong: %+v", updated.ColumnFamilies)
	}

	// Dropping an unknown family errors.
	if _, err := m.ModifyColumnFamilies(ctx, name, []btdriver.ColumnFamilyModification{{ID: "ghost", Drop: true}}); !cerrors.IsNotFound(err) {
		t.Fatalf("drop unknown family: got %v, want NotFound", err)
	}

	// Consistency token round-trips.
	tok, err := m.GenerateConsistencyToken(ctx, name)
	if err != nil || tok == "" {
		t.Fatalf("GenerateConsistencyToken: %v %q", err, tok)
	}

	consistent, _ := m.CheckConsistency(ctx, name, tok)
	if !consistent {
		t.Fatal("expected consistent")
	}
}

func TestTableDeleteProtectionAndUndelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{Parent: inst, TableID: "t", DeletionProtection: true}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	name := inst + "/tables/t"

	// Deletion protection blocks delete.
	if err := m.DeleteTable(ctx, name); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete protected table: got %v, want FailedPrecondition", err)
	}

	// Clear protection, delete, then undelete.
	no := false
	if _, _, err := m.UpdateTable(ctx, name, &no); err != nil {
		t.Fatalf("UpdateTable: %v", err)
	}

	if err := m.DeleteTable(ctx, name); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if _, err := m.GetTable(ctx, name); !cerrors.IsNotFound(err) {
		t.Fatalf("get soft-deleted table: got %v, want NotFound", err)
	}

	if _, _, err := m.UndeleteTable(ctx, name); err != nil {
		t.Fatalf("UndeleteTable: %v", err)
	}

	if _, err := m.GetTable(ctx, name); err != nil {
		t.Fatalf("get after undelete: %v", err)
	}
}

func TestBackupsAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")
	cluster := inst + "/clusters/c1"

	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{Parent: inst, TableID: "src"}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	_, _, err := m.CreateBackup(ctx, btdriver.CreateBackupConfig{
		Parent: cluster, BackupID: "b1", SourceTable: inst + "/tables/src",
		ExpireTime: m.opts.Clock.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	backupName := cluster + "/backups/b1"

	backups, _ := m.ListBackups(ctx, cluster)
	if len(backups) != 1 {
		t.Fatalf("ListBackups: got %d, want 1", len(backups))
	}

	// Restore into a new table.
	if _, _, err := m.RestoreTable(ctx, inst, "restored", backupName); err != nil {
		t.Fatalf("RestoreTable: %v", err)
	}

	rt, err := m.GetTable(ctx, inst+"/tables/restored")
	if err != nil || rt.SourceBackup != backupName {
		t.Fatalf("restored table wrong: %v %+v", err, rt)
	}

	// A second cluster lets us delete c1 (an instance must keep >= 1 cluster).
	if _, _, err := m.CreateCluster(ctx, btdriver.CreateClusterConfig{
		Name: inst + "/clusters/c2", Location: "us-east1-b", ServeNodes: 3,
	}); err != nil {
		t.Fatalf("CreateCluster c2: %v", err)
	}

	// Deleting the cluster removes its backups.
	if err := m.DeleteCluster(ctx, cluster); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if b, _ := m.ListBackups(ctx, cluster); len(b) != 0 {
		t.Fatalf("backups survived cluster delete: %+v", b)
	}
}

func TestAppProfilesAndIAM(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	if _, err := m.CreateAppProfile(ctx, btdriver.CreateAppProfileConfig{
		Parent: inst, AppProfileID: "ap1", MultiClusterRoutingAny: true,
	}); err != nil {
		t.Fatalf("CreateAppProfile: %v", err)
	}

	profiles, _ := m.ListAppProfiles(ctx, inst)
	if len(profiles) != 1 || !profiles[0].MultiClusterRoutingAny {
		t.Fatalf("ListAppProfiles: %+v", profiles)
	}

	// IAM policy round-trips per resource.
	if _, err := m.SetIamPolicy(ctx, inst, btdriver.Policy{
		Bindings: []btdriver.Binding{{Role: "roles/bigtable.admin", Members: []string{"user:a@b.com"}}},
	}); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	pol, err := m.GetIamPolicy(ctx, inst)
	if err != nil || len(pol.Bindings) != 1 || pol.Bindings[0].Role != "roles/bigtable.admin" {
		t.Fatalf("GetIamPolicy: %v %+v", err, pol)
	}

	perms, _ := m.TestIamPermissions(ctx, inst, []string{"bigtable.tables.readRows"})
	if len(perms) != 1 {
		t.Fatalf("TestIamPermissions: %+v", perms)
	}
}

func TestRemainingProviderOps(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")
	cluster := inst + "/clusters/c1"

	// Instance update (sync) + partial update (LRO) + operation get.
	if _, err := m.UpdateInstance(ctx, inst, btdriver.UpdateInstanceConfig{DisplayName: "New", Labels: map[string]string{"k": "v"}}); err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}

	_, op, err := m.PartialUpdateInstance(ctx, inst, btdriver.UpdateInstanceConfig{Type: "DEVELOPMENT"})
	if err != nil {
		t.Fatalf("PartialUpdateInstance: %v", err)
	}

	if got, err := m.GetOperation(ctx, op.Name); err != nil || !got.Done {
		t.Fatalf("GetOperation: %v %+v", err, got)
	}

	// Memory layer on a missing cluster errors.
	if err := m.GetClusterMemoryLayer(ctx, inst+"/clusters/ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetClusterMemoryLayer missing: got %v, want NotFound", err)
	}

	// App profile get/update/delete.
	if _, err := m.CreateAppProfile(ctx, btdriver.CreateAppProfileConfig{Parent: inst, AppProfileID: "ap", SingleClusterID: "c1"}); err != nil {
		t.Fatalf("CreateAppProfile: %v", err)
	}

	apname := inst + "/appProfiles/ap"
	if _, err := m.GetAppProfile(ctx, apname); err != nil {
		t.Fatalf("GetAppProfile: %v", err)
	}

	if _, _, err := m.UpdateAppProfile(ctx, apname, btdriver.CreateAppProfileConfig{Parent: inst, AppProfileID: "ap", MultiClusterRoutingAny: true}); err != nil {
		t.Fatalf("UpdateAppProfile: %v", err)
	}

	if err := m.DeleteAppProfile(ctx, apname); err != nil {
		t.Fatalf("DeleteAppProfile: %v", err)
	}

	// Backup get/update/delete/copy.
	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{Parent: inst, TableID: "src"}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if _, _, err := m.CreateBackup(ctx, btdriver.CreateBackupConfig{
		Parent: cluster, BackupID: "b1", SourceTable: inst + "/tables/src",
		ExpireTime: m.opts.Clock.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	bname := cluster + "/backups/b1"
	if _, err := m.GetBackup(ctx, bname); err != nil {
		t.Fatalf("GetBackup: %v", err)
	}

	if _, err := m.UpdateBackup(ctx, bname, m.opts.Clock.Now().Add(48*time.Hour)); err != nil {
		t.Fatalf("UpdateBackup: %v", err)
	}

	if _, _, err := m.CopyBackup(ctx, btdriver.CopyBackupConfig{
		Parent: cluster, BackupID: "b2", SourceBackup: bname, ExpireTime: m.opts.Clock.Now().Add(72 * time.Hour),
	}); err != nil {
		t.Fatalf("CopyBackup: %v", err)
	}

	if err := m.DeleteBackup(ctx, bname); err != nil {
		t.Fatalf("DeleteBackup: %v", err)
	}
}

func TestTableResultDoesNotAliasStore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{
		Parent: inst, TableID: "t",
		ColumnFamilies: map[string]btdriver.ColumnFamily{"cf1": {GCRule: &btdriver.GCRule{MaxNumVersions: 3}}},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	name := inst + "/tables/t"
	got, _ := m.GetTable(ctx, name)
	got.ColumnFamilies["cf1"] = btdriver.ColumnFamily{GCRule: &btdriver.GCRule{MaxNumVersions: 99}}

	again, _ := m.GetTable(ctx, name)
	if again.ColumnFamilies["cf1"].GCRule.MaxNumVersions != 3 {
		t.Fatal("returned table aliases the store (clone-on-read broken)")
	}
}

func TestUpdateClusterAutoscalingDoesNotAlias(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")
	name := inst + "/clusters/c1"

	as := &btdriver.Autoscaling{MinServeNodes: 2, MaxServeNodes: 10, CPUTargetPct: 60}
	if _, _, err := m.UpdateCluster(ctx, name, 0, as); err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	// Mutating the caller's struct after the call must not reach the store.
	as.MaxServeNodes = 999

	got, _ := m.GetCluster(ctx, name)
	if got.Autoscaling == nil || got.Autoscaling.MaxServeNodes != 10 {
		t.Fatalf("autoscaling aliased the caller's struct: %+v", got.Autoscaling)
	}
}

func TestTestIamPermissionsIntersectsPolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	// No policy set -> nothing granted.
	if perms, _ := m.TestIamPermissions(ctx, inst, []string{"bigtable.tables.readRows"}); len(perms) != 0 {
		t.Fatalf("no-policy grant: got %+v, want none", perms)
	}

	// A viewer can read table metadata but cannot mutate rows.
	if _, err := m.SetIamPolicy(ctx, inst, btdriver.Policy{
		Bindings: []btdriver.Binding{{Role: "roles/bigtable.viewer", Members: []string{"user:a@b.com"}}},
	}); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	perms, _ := m.TestIamPermissions(ctx, inst, []string{"bigtable.tables.get", "bigtable.tables.mutateRows"})
	if len(perms) != 1 || perms[0] != "bigtable.tables.get" {
		t.Fatalf("viewer intersection wrong: %+v", perms)
	}
}

func TestDeleteLastClusterRejected(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	// The sole cluster cannot be deleted.
	if err := m.DeleteCluster(ctx, inst+"/clusters/c1"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete last cluster: got %v, want FailedPrecondition", err)
	}

	// With a second cluster present, deleting one is allowed.
	if _, _, err := m.CreateCluster(ctx, btdriver.CreateClusterConfig{
		Name: inst + "/clusters/c2", Location: "us-central1-b", ServeNodes: 3,
	}); err != nil {
		t.Fatalf("CreateCluster c2: %v", err)
	}

	if err := m.DeleteCluster(ctx, inst+"/clusters/c1"); err != nil {
		t.Fatalf("DeleteCluster c1: %v", err)
	}
}

func TestCreateInstanceIsAtomicOnBadCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	name := proj + "/instances/atomic"

	// A bad cluster in the initial set must fail the whole create with no
	// half-built instance left behind.
	_, _, err := m.CreateInstance(ctx, btdriver.CreateInstanceConfig{
		Name: name,
		Clusters: []btdriver.CreateClusterConfig{
			{Name: name + "/clusters/ok", Location: "us-central1-a", ServeNodes: 3},
			{Name: name + "/clusters/bad", Location: "us-central1-b", ServeNodes: maxServeNodes + 1},
		},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad initial cluster: got %v, want InvalidArgument", err)
	}

	if _, err := m.GetInstance(ctx, name); !cerrors.IsNotFound(err) {
		t.Fatalf("instance survived failed create: got %v, want NotFound", err)
	}
}

func TestRestorePreservesColumnFamilies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")
	cluster := inst + "/clusters/c1"

	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{
		Parent: inst, TableID: "src",
		ColumnFamilies: map[string]btdriver.ColumnFamily{"cf": {GCRule: &btdriver.GCRule{MaxNumVersions: 5}}},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if _, _, err := m.CreateBackup(ctx, btdriver.CreateBackupConfig{
		Parent: cluster, BackupID: "b1", SourceTable: inst + "/tables/src",
		ExpireTime: m.opts.Clock.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	if _, _, err := m.RestoreTable(ctx, inst, "restored", cluster+"/backups/b1"); err != nil {
		t.Fatalf("RestoreTable: %v", err)
	}

	rt, err := m.GetTable(ctx, inst+"/tables/restored")
	if err != nil {
		t.Fatalf("GetTable restored: %v", err)
	}

	cf, ok := rt.ColumnFamilies["cf"]
	if !ok || cf.GCRule == nil || cf.GCRule.MaxNumVersions != 5 {
		t.Fatalf("restored table lost column families: %+v", rt.ColumnFamilies)
	}
}

func TestCreateBackupSourceTableValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")
	other := mustInstance(t, m, "other")
	cluster := inst + "/clusters/c1"

	// A source table in a different instance is rejected.
	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{Parent: other, TableID: "t"}); err != nil {
		t.Fatalf("CreateTable other: %v", err)
	}

	if _, _, err := m.CreateBackup(ctx, btdriver.CreateBackupConfig{
		Parent: cluster, BackupID: "x", SourceTable: other + "/tables/t",
		ExpireTime: m.opts.Clock.Now().Add(time.Hour),
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("cross-instance source: got %v, want InvalidArgument", err)
	}

	// A soft-deleted source table is rejected.
	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{Parent: inst, TableID: "gone"}); err != nil {
		t.Fatalf("CreateTable gone: %v", err)
	}

	if err := m.DeleteTable(ctx, inst+"/tables/gone"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if _, _, err := m.CreateBackup(ctx, btdriver.CreateBackupConfig{
		Parent: cluster, BackupID: "y", SourceTable: inst + "/tables/gone",
		ExpireTime: m.opts.Clock.Now().Add(time.Hour),
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("deleted source: got %v, want InvalidArgument", err)
	}
}

func TestAppProfileRoutingRequiresExactlyOnePolicy(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	// Neither routing policy set.
	if _, err := m.CreateAppProfile(ctx, btdriver.CreateAppProfileConfig{
		Parent: inst, AppProfileID: "none",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("no routing: got %v, want InvalidArgument", err)
	}

	// Both routing policies set.
	if _, err := m.CreateAppProfile(ctx, btdriver.CreateAppProfileConfig{
		Parent: inst, AppProfileID: "both", MultiClusterRoutingAny: true, SingleClusterID: "c1",
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("both routing: got %v, want InvalidArgument", err)
	}
}

func TestNestedGCRuleSurvivesCloneOnRead(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	inst := mustInstance(t, m, "app")

	nested := &btdriver.GCRule{Union: []btdriver.GCRule{
		{MaxNumVersions: 1},
		{Intersection: []btdriver.GCRule{{MaxNumVersions: 2}, {MaxAgeSeconds: 3600}}},
	}}

	if _, err := m.CreateTable(ctx, btdriver.CreateTableConfig{
		Parent: inst, TableID: "t",
		ColumnFamilies: map[string]btdriver.ColumnFamily{"cf": {GCRule: nested}},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	name := inst + "/tables/t"

	// Mutating a deeply nested node of the returned tree must not reach the store.
	got, _ := m.GetTable(ctx, name)
	got.ColumnFamilies["cf"].GCRule.Union[1].Intersection[0].MaxNumVersions = 99

	again, _ := m.GetTable(ctx, name)
	if again.ColumnFamilies["cf"].GCRule.Union[1].Intersection[0].MaxNumVersions != 2 {
		t.Fatal("nested GC rule aliases the store (deep clone broken)")
	}
}
