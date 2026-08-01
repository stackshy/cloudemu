package bigtable_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	bt "google.golang.org/api/bigtableadmin/v2"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const project = "p1"

func newSDKClient(t *testing.T) *bt.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Bigtable: cloud.Bigtable})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := bt.NewService(context.Background(), option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("bigtableadmin.NewService: %v", err)
	}

	return svc
}

func instanceParent() string { return "projects/" + project }

func createAppInstance(t *testing.T, svc *bt.Service) string {
	t.Helper()

	op, err := svc.Projects.Instances.Create(instanceParent(), &bt.CreateInstanceRequest{
		InstanceId: "app",
		Instance:   &bt.Instance{DisplayName: "App", Type: "PRODUCTION"},
		Clusters: map[string]bt.Cluster{
			"c1": {Location: "projects/" + project + "/locations/us-central1-a", ServeNodes: 3, DefaultStorageType: "SSD"},
		},
	}).Do()
	if err != nil {
		t.Fatalf("Instances.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("create instance op not done")
	}

	return "projects/" + project + "/instances/app"
}

func TestSDKInstanceAndClusterLifecycle(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)

	got, err := svc.Projects.Instances.Get(inst).Do()
	if err != nil || got.DisplayName != "App" {
		t.Fatalf("Instances.Get: %v %+v", err, got)
	}

	// Initial cluster is present.
	cl, err := svc.Projects.Instances.Clusters.List(inst).Do()
	if err != nil || len(cl.Clusters) != 1 || cl.Clusters[0].ServeNodes != 3 {
		t.Fatalf("Clusters.List: %v %+v", err, cl)
	}

	// Add a second cluster.
	op, err := svc.Projects.Instances.Clusters.Create(inst, &bt.Cluster{
		Location: "projects/" + project + "/locations/us-east1-b", ServeNodes: 5,
	}).ClusterId("c2").Do()
	if err != nil || !op.Done {
		t.Fatalf("Clusters.Create: %v", err)
	}

	list, _ := svc.Projects.Instances.List(instanceParent()).Do()
	if len(list.Instances) != 1 {
		t.Fatalf("Instances.List: got %d, want 1", len(list.Instances))
	}

	// Delete cascades clusters.
	if _, err := svc.Projects.Instances.Delete(inst).Do(); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	if _, err := svc.Projects.Instances.Get(inst).Do(); err == nil {
		t.Fatal("get after delete: expected error")
	}
}

func TestSDKTablesColumnFamiliesConsistency(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)

	tbl, err := svc.Projects.Instances.Tables.Create(inst, &bt.CreateTableRequest{
		TableId: "events",
		Table: &bt.Table{ColumnFamilies: map[string]bt.ColumnFamily{
			"cf1": {GcRule: &bt.GcRule{MaxNumVersions: 3}},
		}},
	}).Do()
	if err != nil || len(tbl.ColumnFamilies) != 1 {
		t.Fatalf("Tables.Create: %v %+v", err, tbl)
	}

	name := inst + "/tables/events"

	// Modify column families (add with maxAge, update cf1).
	mod, err := svc.Projects.Instances.Tables.ModifyColumnFamilies(name, &bt.ModifyColumnFamiliesRequest{
		Modifications: []*bt.Modification{
			{Id: "cf2", Create: &bt.ColumnFamily{GcRule: &bt.GcRule{MaxAge: "86400s"}}},
			{Id: "cf1", Update: &bt.ColumnFamily{GcRule: &bt.GcRule{MaxNumVersions: 1}}},
		},
	}).Do()
	if err != nil {
		t.Fatalf("ModifyColumnFamilies: %v", err)
	}

	if mod.ColumnFamilies["cf2"].GcRule.MaxAge != "86400s" || mod.ColumnFamilies["cf1"].GcRule.MaxNumVersions != 1 {
		t.Fatalf("column families wrong: %+v", mod.ColumnFamilies)
	}

	// Consistency token round-trips.
	tok, err := svc.Projects.Instances.Tables.GenerateConsistencyToken(name, &bt.GenerateConsistencyTokenRequest{}).Do()
	if err != nil || tok.ConsistencyToken == "" {
		t.Fatalf("GenerateConsistencyToken: %v %+v", err, tok)
	}

	chk, err := svc.Projects.Instances.Tables.CheckConsistency(name, &bt.CheckConsistencyRequest{
		ConsistencyToken: tok.ConsistencyToken,
	}).Do()
	if err != nil || !chk.Consistent {
		t.Fatalf("CheckConsistency: %v %+v", err, chk)
	}
}

func TestSDKBackupsRestoreAppProfilesIAM(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)
	cluster := inst + "/clusters/c1"

	if _, err := svc.Projects.Instances.Tables.Create(inst, &bt.CreateTableRequest{TableId: "src"}).Do(); err != nil {
		t.Fatalf("Tables.Create: %v", err)
	}

	// Create a backup (LRO).
	bop, err := svc.Projects.Instances.Clusters.Backups.Create(cluster, &bt.Backup{
		SourceTable: inst + "/tables/src", ExpireTime: "2030-01-01T00:00:00Z",
	}).BackupId("b1").Do()
	if err != nil || !bop.Done {
		t.Fatalf("Backups.Create: %v", err)
	}

	backups, err := svc.Projects.Instances.Clusters.Backups.List(cluster).Do()
	if err != nil || len(backups.Backups) != 1 {
		t.Fatalf("Backups.List: %v %+v", err, backups)
	}

	// Restore into a new table (LRO).
	rop, err := svc.Projects.Instances.Tables.Restore(inst, &bt.RestoreTableRequest{
		TableId: "restored", Backup: cluster + "/backups/b1",
	}).Do()
	if err != nil || !rop.Done {
		t.Fatalf("Tables.Restore: %v", err)
	}

	// App profile.
	if _, err := svc.Projects.Instances.AppProfiles.Create(inst, &bt.AppProfile{
		MultiClusterRoutingUseAny: &bt.MultiClusterRoutingUseAny{},
	}).AppProfileId("ap1").Do(); err != nil {
		t.Fatalf("AppProfiles.Create: %v", err)
	}

	profiles, err := svc.Projects.Instances.AppProfiles.List(inst).Do()
	if err != nil || len(profiles.AppProfiles) != 1 {
		t.Fatalf("AppProfiles.List: %v %+v", err, profiles)
	}

	// IAM policy round-trips on the instance.
	if _, err := svc.Projects.Instances.SetIamPolicy(inst, &bt.SetIamPolicyRequest{
		Policy: &bt.Policy{Bindings: []*bt.Binding{{Role: "roles/bigtable.user", Members: []string{"user:a@b.com"}}}},
	}).Do(); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	pol, err := svc.Projects.Instances.GetIamPolicy(inst, &bt.GetIamPolicyRequest{}).Do()
	if err != nil || len(pol.Bindings) != 1 || pol.Bindings[0].Role != "roles/bigtable.user" {
		t.Fatalf("GetIamPolicy: %v %+v", err, pol)
	}

	perms, err := svc.Projects.Instances.TestIamPermissions(inst, &bt.TestIamPermissionsRequest{
		Permissions: []string{"bigtable.tables.readRows"},
	}).Do()
	if err != nil || len(perms.Permissions) != 1 {
		t.Fatalf("TestIamPermissions: %v %+v", err, perms)
	}
}

func TestSDKGetListDeleteUpdatePaths(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)
	cluster := inst + "/clusters/c1"

	// Instance PUT (sync update) + PATCH (partial update, LRO).
	if _, err := svc.Projects.Instances.Update(inst, &bt.Instance{DisplayName: "Renamed", Type: "PRODUCTION"}).Do(); err != nil {
		t.Fatalf("Instances.Update: %v", err)
	}

	pop, err := svc.Projects.Instances.PartialUpdateInstance(inst, &bt.Instance{DisplayName: "Again"}).UpdateMask("displayName").Do()
	if err != nil || !pop.Done {
		t.Fatalf("PartialUpdateInstance: %v", err)
	}

	got, _ := svc.Projects.Instances.Get(inst).Do()
	if got.DisplayName != "Again" {
		t.Fatalf("partial update not applied: %q", got.DisplayName)
	}

	// Cluster get + update (scale) + memory layer.
	if _, err := svc.Projects.Instances.Clusters.Get(cluster).Do(); err != nil {
		t.Fatalf("Clusters.Get: %v", err)
	}

	if _, err := svc.Projects.Instances.Clusters.PartialUpdateCluster(cluster, &bt.Cluster{ServeNodes: 6}).UpdateMask("serveNodes").Do(); err != nil {
		t.Fatalf("Clusters.PartialUpdateCluster: %v", err)
	}

	if _, err := svc.Projects.Instances.Clusters.GetMemoryLayer(cluster).Do(); err != nil {
		t.Fatalf("Clusters.GetMemoryLayer: %v", err)
	}

	// Table get/list/patch/delete/undelete + dropRowRange.
	if _, err := svc.Projects.Instances.Tables.Create(inst, &bt.CreateTableRequest{TableId: "t"}).Do(); err != nil {
		t.Fatalf("Tables.Create: %v", err)
	}

	tname := inst + "/tables/t"
	if _, err := svc.Projects.Instances.Tables.Get(tname).Do(); err != nil {
		t.Fatalf("Tables.Get: %v", err)
	}

	if tl, err := svc.Projects.Instances.Tables.List(inst).Do(); err != nil || len(tl.Tables) != 1 {
		t.Fatalf("Tables.List: %v %+v", err, tl)
	}

	if _, err := svc.Projects.Instances.Tables.DropRowRange(tname, &bt.DropRowRangeRequest{DeleteAllDataFromTable: true}).Do(); err != nil {
		t.Fatalf("DropRowRange: %v", err)
	}

	if _, err := svc.Projects.Instances.Tables.Delete(tname).Do(); err != nil {
		t.Fatalf("Tables.Delete: %v", err)
	}

	if _, err := svc.Projects.Instances.Tables.Undelete(tname, &bt.UndeleteTableRequest{}).Do(); err != nil {
		t.Fatalf("Tables.Undelete: %v", err)
	}

	// Backup lifecycle: create, get, patch, copy, IAM, delete.
	if _, err := svc.Projects.Instances.Clusters.Backups.Create(cluster, &bt.Backup{
		SourceTable: tname, ExpireTime: "2030-01-01T00:00:00Z",
	}).BackupId("b1").Do(); err != nil {
		t.Fatalf("Backups.Create: %v", err)
	}

	bname := cluster + "/backups/b1"
	if _, err := svc.Projects.Instances.Clusters.Backups.Get(bname).Do(); err != nil {
		t.Fatalf("Backups.Get: %v", err)
	}

	if _, err := svc.Projects.Instances.Clusters.Backups.Patch(bname, &bt.Backup{ExpireTime: "2031-01-01T00:00:00Z"}).UpdateMask("expireTime").Do(); err != nil {
		t.Fatalf("Backups.Patch: %v", err)
	}

	if _, err := svc.Projects.Instances.Clusters.Backups.SetIamPolicy(bname, &bt.SetIamPolicyRequest{
		Policy: &bt.Policy{Bindings: []*bt.Binding{{Role: "roles/bigtable.viewer", Members: []string{"user:x@y.com"}}}},
	}).Do(); err != nil {
		t.Fatalf("Backups.SetIamPolicy: %v", err)
	}

	bp, err := svc.Projects.Instances.Clusters.Backups.GetIamPolicy(bname, &bt.GetIamPolicyRequest{}).Do()
	if err != nil || len(bp.Bindings) != 1 {
		t.Fatalf("Backups.GetIamPolicy: %v %+v", err, bp)
	}

	if _, err := svc.Projects.Instances.Clusters.Backups.Delete(bname).Do(); err != nil {
		t.Fatalf("Backups.Delete: %v", err)
	}

	// App profile get/patch/delete.
	if _, err := svc.Projects.Instances.AppProfiles.Create(inst, &bt.AppProfile{
		SingleClusterRouting: &bt.SingleClusterRouting{ClusterId: "c1"},
	}).AppProfileId("ap").Do(); err != nil {
		t.Fatalf("AppProfiles.Create: %v", err)
	}

	apname := inst + "/appProfiles/ap"
	if _, err := svc.Projects.Instances.AppProfiles.Get(apname).Do(); err != nil {
		t.Fatalf("AppProfiles.Get: %v", err)
	}

	if _, err := svc.Projects.Instances.AppProfiles.Patch(apname, &bt.AppProfile{
		MultiClusterRoutingUseAny: &bt.MultiClusterRoutingUseAny{},
	}).UpdateMask("multiClusterRoutingUseAny").Do(); err != nil {
		t.Fatalf("AppProfiles.Patch: %v", err)
	}

	if _, err := svc.Projects.Instances.AppProfiles.Delete(apname).Do(); err != nil {
		t.Fatalf("AppProfiles.Delete: %v", err)
	}

	// A second cluster lets us delete c1 (an instance must keep >= 1 cluster).
	if _, err := svc.Projects.Instances.Clusters.Create(inst, &bt.Cluster{
		Location: "projects/" + project + "/locations/us-east1-b", ServeNodes: 3,
	}).ClusterId("c2").Do(); err != nil {
		t.Fatalf("Clusters.Create c2: %v", err)
	}

	// Cluster delete.
	if _, err := svc.Projects.Instances.Clusters.Delete(cluster).Do(); err != nil {
		t.Fatalf("Clusters.Delete: %v", err)
	}
}

func TestSDKNotFound(t *testing.T) {
	svc := newSDKClient(t)

	_, err := svc.Projects.Instances.Get("projects/" + project + "/instances/ghost").Do()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("get missing instance: got %v, want not-found error", err)
	}
}
