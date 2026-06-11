package databricks_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/compute"
	"github.com/databricks/databricks-sdk-go/service/iam"
	"github.com/databricks/databricks-sdk-go/service/jobs"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{DatabricksDataPlane: cloud.Databricks})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:        ts.URL,
		Token:       "test-token",
		Credentials: config.PatCredentials{},
	})
	if err != nil {
		t.Fatalf("new workspace client: %v", err)
	}

	return w
}

func TestSDKInstancePoolLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.InstancePools.Create(ctx, compute.CreateInstancePool{
		InstancePoolName: "pool-1",
		NodeTypeId:       "Standard_DS3_v2",
		MinIdleInstances: 1,
		MaxCapacity:      5,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.InstancePoolId == "" {
		t.Fatal("expected instance pool id")
	}

	got, err := w.InstancePools.GetByInstancePoolId(ctx, created.InstancePoolId)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.InstancePoolName != "pool-1" || got.State != compute.InstancePoolStateActive {
		t.Fatalf("unexpected pool: %+v", got)
	}

	pools, err := w.InstancePools.ListAll(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(pools) != 1 {
		t.Fatalf("got %d pools, want 1", len(pools))
	}

	if err = w.InstancePools.Edit(ctx, compute.EditInstancePool{
		InstancePoolId:   created.InstancePoolId,
		InstancePoolName: "pool-1-edited",
		NodeTypeId:       "Standard_DS3_v2",
	}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if err = w.InstancePools.DeleteByInstancePoolId(ctx, created.InstancePoolId); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestSDKClusterLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
		ClusterName:  "cluster-1",
		SparkVersion: "13.3.x-scala2.12",
		NodeTypeId:   "Standard_DS3_v2",
		NumWorkers:   2,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id := wait.ClusterId
	if id == "" {
		t.Fatal("expected cluster id")
	}

	got, err := w.Clusters.GetByClusterId(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.State != compute.StateRunning {
		t.Fatalf("got state %q, want RUNNING", got.State)
	}

	clusters, err := w.Clusters.ListAll(ctx, compute.ListClustersRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(clusters) != 1 {
		t.Fatalf("got %d clusters, want 1", len(clusters))
	}

	if _, err = w.Clusters.Delete(ctx, compute.DeleteCluster{ClusterId: id}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	terminated, err := w.Clusters.GetByClusterId(ctx, id)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}

	if terminated.State != compute.StateTerminated {
		t.Fatalf("got state %q, want TERMINATED", terminated.State)
	}

	if _, err = w.Clusters.Start(ctx, compute.StartCluster{ClusterId: id}); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestSDKJobLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Jobs.Create(ctx, jobs.CreateJob{Name: "job-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.JobId == 0 {
		t.Fatal("expected job id")
	}

	got, err := w.Jobs.GetByJobId(ctx, created.JobId)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Settings == nil || got.Settings.Name != "job-1" {
		t.Fatalf("unexpected job settings: %+v", got.Settings)
	}

	run, err := w.Jobs.RunNow(ctx, jobs.RunNow{JobId: created.JobId})
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	if run.Response.RunId == 0 {
		t.Fatal("expected run id")
	}

	if err = w.Jobs.DeleteByJobId(ctx, created.JobId); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestSDKPermissions(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	set, err := w.Permissions.Set(ctx, iam.SetObjectPermissions{
		RequestObjectType: "clusters",
		RequestObjectId:   "cluster-123",
		AccessControlList: []iam.AccessControlRequest{
			{UserName: "alice@example.com", PermissionLevel: iam.PermissionLevelCanManage},
		},
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	if len(set.AccessControlList) != 1 {
		t.Fatalf("got %d acl entries, want 1", len(set.AccessControlList))
	}

	got, err := w.Permissions.Get(ctx, iam.GetPermissionRequest{
		RequestObjectType: "clusters",
		RequestObjectId:   "cluster-123",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.AccessControlList) != 1 || got.AccessControlList[0].UserName != "alice@example.com" {
		t.Fatalf("unexpected permissions: %+v", got.AccessControlList)
	}
}
