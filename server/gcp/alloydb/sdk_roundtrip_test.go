package alloydb_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	alloydb "google.golang.org/api/alloydb/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2/config"
	alloyprov "github.com/stackshy/cloudemu/v2/providers/gcp/alloydb"
	alloysrv "github.com/stackshy/cloudemu/v2/server/gcp/alloydb"
)

const (
	testProject  = "mock-project"
	testLocation = "us-central1"
)

func newSDKClient(t *testing.T) *alloydb.Service {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion(testLocation), config.WithProjectID(testProject))

	h := alloysrv.New(alloyprov.New(opts))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	svc, err := alloydb.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

func parent() string {
	return "projects/" + testProject + "/locations/" + testLocation
}

func TestSDKAlloyDBClusterAndInstance(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	op, err := svc.Projects.Locations.Clusters.Create(parent(), &alloydb.Cluster{
		DatabaseVersion: "POSTGRES_15",
		Network:         "default",
		InitialUser:     &alloydb.UserPassword{User: "postgres", Password: "secret"},
	}).ClusterId("c1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	if !op.Done {
		t.Error("create cluster: expected done operation")
	}

	got, err := svc.Projects.Locations.Clusters.Get(parent() + "/clusters/c1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.Get: %v", err)
	}

	if got.ClusterType != "PRIMARY" || got.DatabaseVersion != "POSTGRES_15" {
		t.Errorf("cluster: got type=%q version=%q", got.ClusterType, got.DatabaseVersion)
	}

	list, err := svc.Projects.Locations.Clusters.List(parent()).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Clusters.List: %v", err)
	}

	if len(list.Clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(list.Clusters))
	}

	// Instance: PRIMARY.
	if _, err := svc.Projects.Locations.Clusters.Instances.Create(parent()+"/clusters/c1", &alloydb.Instance{
		InstanceType:  "PRIMARY",
		MachineConfig: &alloydb.MachineConfig{CpuCount: 4},
	}).InstanceId("i1").Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Create: %v", err)
	}

	inst, err := svc.Projects.Locations.Clusters.Instances.Get(parent() + "/clusters/c1/instances/i1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if inst.InstanceType != "PRIMARY" || inst.MachineConfig == nil || inst.MachineConfig.CpuCount != 4 {
		t.Errorf("instance: got %+v", inst)
	}

	// Failover + restart custom methods.
	if _, err := svc.Projects.Locations.Clusters.Instances.Failover(parent()+"/clusters/c1/instances/i1",
		&alloydb.FailoverInstanceRequest{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Failover: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Instances.Restart(parent()+"/clusters/c1/instances/i1",
		&alloydb.RestartInstanceRequest{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Restart: %v", err)
	}

	// Delete instance + cluster.
	if _, err := svc.Projects.Locations.Clusters.Instances.Delete(parent() + "/clusters/c1/instances/i1").Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Delete(parent() + "/clusters/c1").Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Delete: %v", err)
	}
}

func TestSDKAlloyDBBackupUserSecondary(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Clusters.Create(parent(), &alloydb.Cluster{DatabaseVersion: "POSTGRES_15"}).
		ClusterId("primary").Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Create primary: %v", err)
	}

	// User.
	if _, err := svc.Projects.Locations.Clusters.Users.Create(parent()+"/clusters/primary", &alloydb.User{}).
		UserId("appuser").Context(ctx).Do(); err != nil {
		t.Fatalf("Users.Create: %v", err)
	}

	ulist, err := svc.Projects.Locations.Clusters.Users.List(parent() + "/clusters/primary").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Users.List: %v", err)
	}

	if len(ulist.Users) != 1 {
		t.Fatalf("got %d users, want 1", len(ulist.Users))
	}

	// Backup.
	if _, err := svc.Projects.Locations.Backups.Create(parent(), &alloydb.Backup{
		ClusterName: parent() + "/clusters/primary",
	}).BackupId("b1").Context(ctx).Do(); err != nil {
		t.Fatalf("Backups.Create: %v", err)
	}

	blist, err := svc.Projects.Locations.Backups.List(parent()).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Backups.List: %v", err)
	}

	if len(blist.Backups) != 1 {
		t.Fatalf("got %d backups, want 1", len(blist.Backups))
	}

	// Secondary cluster + promote.
	if _, err := svc.Projects.Locations.Clusters.Createsecondary(parent(), &alloydb.Cluster{
		SecondaryConfig: &alloydb.SecondaryConfig{PrimaryClusterName: parent() + "/clusters/primary"},
	}).ClusterId("secondary").Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Createsecondary: %v", err)
	}

	sec, err := svc.Projects.Locations.Clusters.Get(parent() + "/clusters/secondary").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get secondary: %v", err)
	}

	if sec.ClusterType != "SECONDARY" {
		t.Errorf("secondary cluster type: got %q", sec.ClusterType)
	}

	if _, err := svc.Projects.Locations.Clusters.Promote(parent()+"/clusters/secondary",
		&alloydb.PromoteClusterRequest{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Promote: %v", err)
	}
}

func TestSDKAlloyDBWireErrorMapping(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	// 404 — missing cluster.
	_, err := svc.Projects.Locations.Clusters.Get(parent() + "/clusters/ghost").Context(ctx).Do()
	assertStatus(t, err, 404)

	// Create a primary, then a duplicate → 409.
	if _, err := svc.Projects.Locations.Clusters.Create(parent(), &alloydb.Cluster{}).
		ClusterId("dup").Context(ctx).Do(); err != nil {
		t.Fatalf("Clusters.Create: %v", err)
	}

	_, err = svc.Projects.Locations.Clusters.Create(parent(), &alloydb.Cluster{}).
		ClusterId("dup").Context(ctx).Do()
	assertStatus(t, err, 409)

	// Secondary from a missing primary → 404.
	_, err = svc.Projects.Locations.Clusters.Createsecondary(parent(), &alloydb.Cluster{
		SecondaryConfig: &alloydb.SecondaryConfig{PrimaryClusterName: parent() + "/clusters/ghost"},
	}).ClusterId("sec").Context(ctx).Do()
	assertStatus(t, err, 404)
}

func assertStatus(t *testing.T, err error, want int) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected an error with status %d, got nil", want)
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("expected a googleapi.Error, got %T: %v", err, err)
	}

	if gerr.Code != want {
		t.Errorf("status: got %d, want %d", gerr.Code, want)
	}
}

func TestSDKAlloyDBCoverageExtras(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Clusters.Create(parent(), &alloydb.Cluster{DatabaseVersion: "POSTGRES_15"}).
		ClusterId("c1").Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	// Patch cluster.
	if _, err := svc.Projects.Locations.Clusters.Patch(parent()+"/clusters/c1", &alloydb.Cluster{DatabaseVersion: "POSTGRES_16"}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("patch cluster: %v", err)
	}

	// Instance create, list, patch.
	if _, err := svc.Projects.Locations.Clusters.Instances.Create(parent()+"/clusters/c1", &alloydb.Instance{
		InstanceType: "READ_POOL", ReadPoolConfig: &alloydb.ReadPoolConfig{NodeCount: 2},
	}).InstanceId("pool").Context(ctx).Do(); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	ilist, err := svc.Projects.Locations.Clusters.Instances.List(parent() + "/clusters/c1").Context(ctx).Do()
	if err != nil || len(ilist.Instances) != 1 {
		t.Fatalf("list instances: %d %v", len(ilist.Instances), err)
	}

	if _, err := svc.Projects.Locations.Clusters.Instances.Patch(parent()+"/clusters/c1/instances/pool", &alloydb.Instance{
		Labels: map[string]string{"env": "prod"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("patch instance: %v", err)
	}

	// User get + delete.
	if _, err := svc.Projects.Locations.Clusters.Users.Create(parent()+"/clusters/c1", &alloydb.User{}).
		UserId("u1").Context(ctx).Do(); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Users.Get(parent() + "/clusters/c1/users/u1").Context(ctx).Do(); err != nil {
		t.Fatalf("get user: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Users.Delete(parent() + "/clusters/c1/users/u1").Context(ctx).Do(); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	// Backup get + delete.
	if _, err := svc.Projects.Locations.Backups.Create(parent(), &alloydb.Backup{ClusterName: parent() + "/clusters/c1"}).
		BackupId("b1").Context(ctx).Do(); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	if _, err := svc.Projects.Locations.Backups.Get(parent() + "/backups/b1").Context(ctx).Do(); err != nil {
		t.Fatalf("get backup: %v", err)
	}

	// Restore a new cluster from the backup.
	if _, err := svc.Projects.Locations.Clusters.Restore(parent(), &alloydb.RestoreClusterRequest{
		ClusterId:    "restored",
		BackupSource: &alloydb.BackupSource{BackupName: parent() + "/backups/b1"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("restore cluster: %v", err)
	}

	if _, err := svc.Projects.Locations.Backups.Delete(parent() + "/backups/b1").Context(ctx).Do(); err != nil {
		t.Fatalf("delete backup: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Delete(parent() + "/clusters/c1").Context(ctx).Do(); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}

	// Operations poll (always done).
	op, err := svc.Projects.Locations.Operations.Get(parent() + "/operations/op-x").Context(ctx).Do()
	if err != nil || !op.Done {
		t.Fatalf("operations.get: %+v %v", op, err)
	}
}

func TestSDKAlloyDBUserPatchAndGuards(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Clusters.Create(parent(), &alloydb.Cluster{DatabaseVersion: "POSTGRES_15"}).
		ClusterId("c1").Context(ctx).Do(); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	if _, err := svc.Projects.Locations.Clusters.Users.Create(parent()+"/clusters/c1", &alloydb.User{}).
		UserId("u1").Context(ctx).Do(); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Users.Patch (UpdateUser) is now routed (was 405).
	got, err := svc.Projects.Locations.Clusters.Users.Patch(parent()+"/clusters/c1/users/u1",
		&alloydb.User{Password: "newpass"}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Users.Patch: %v", err)
	}

	if got.Name == "" {
		t.Error("patched user has empty name")
	}

	// 400 — READ_POOL instance without a node count.
	_, err = svc.Projects.Locations.Clusters.Instances.Create(parent()+"/clusters/c1", &alloydb.Instance{
		InstanceType: "READ_POOL",
	}).InstanceId("bad").Context(ctx).Do()
	assertStatus(t, err, 400)
}

func TestAlloyDBRawGetPromoteRejected(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion(testLocation), config.WithProjectID(testProject))
	ts := httptest.NewServer(alloysrv.New(alloyprov.New(opts)))
	t.Cleanup(ts.Close)

	// A GET on the :promote custom method must not trigger the state change.
	resp, err := ts.Client().Get(ts.URL + "/v1/" + parent() + "/clusters/c1:promote")
	if err != nil {
		t.Fatalf("GET :promote: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 405 {
		t.Errorf("GET :promote: status %d, want 405", resp.StatusCode)
	}
}
