package databricks_test

import (
	"context"
	"testing"

	"github.com/databricks/databricks-sdk-go/service/compute"
	"github.com/databricks/databricks-sdk-go/service/jobs"
)

func TestSDKClusterResizePinMetadata(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
		ClusterName:  "c-extra",
		SparkVersion: "13.3.x-scala2.12",
		NodeTypeId:   "Standard_DS3_v2",
		NumWorkers:   1,
	})
	if err != nil {
		t.Fatalf("Create cluster: %v", err)
	}

	id := wait.ClusterId

	if _, err = w.Clusters.Resize(ctx, compute.ResizeCluster{ClusterId: id, NumWorkers: 3}); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	resized, err := w.Clusters.GetByClusterId(ctx, id)
	if err != nil {
		t.Fatalf("Get after resize: %v", err)
	}

	if resized.NumWorkers != 3 {
		t.Fatalf("got %d workers, want 3", resized.NumWorkers)
	}

	if err = w.Clusters.Pin(ctx, compute.PinCluster{ClusterId: id}); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	if err = w.Clusters.Unpin(ctx, compute.UnpinCluster{ClusterId: id}); err != nil {
		t.Fatalf("Unpin: %v", err)
	}

	nodeTypes, err := w.Clusters.ListNodeTypes(ctx)
	if err != nil {
		t.Fatalf("ListNodeTypes: %v", err)
	}

	if len(nodeTypes.NodeTypes) == 0 {
		t.Fatal("expected node types")
	}

	versions, err := w.Clusters.SparkVersions(ctx)
	if err != nil {
		t.Fatalf("SparkVersions: %v", err)
	}

	if len(versions.Versions) == 0 {
		t.Fatal("expected spark versions")
	}

	zones, err := w.Clusters.ListZones(ctx)
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}

	if len(zones.Zones) == 0 || zones.DefaultZone == "" {
		t.Fatalf("expected zones + default, got %+v", zones)
	}
}

func TestSDKRunSubmitRepairDelete(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	submit, err := w.Jobs.Submit(ctx, jobs.SubmitRun{RunName: "one-time"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	runID := submit.Response.RunId
	if runID == 0 {
		t.Fatal("expected a submitted run id")
	}

	repair, err := w.Jobs.RepairRun(ctx, jobs.RepairRun{RunId: runID})
	if err != nil {
		t.Fatalf("RepairRun: %v", err)
	}

	if repair.Response.RepairId == 0 {
		t.Fatal("expected a repair id")
	}

	if err = w.Jobs.DeleteRun(ctx, jobs.DeleteRun{RunId: runID}); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}

	if _, err = w.Jobs.GetRun(ctx, jobs.GetRunRequest{RunId: runID}); err == nil {
		t.Fatal("expected error after DeleteRun")
	}
}

func TestSDKCancelAllRuns(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	job, err := w.Jobs.Create(ctx, jobs.CreateJob{Name: "cancel-all-job"})
	if err != nil {
		t.Fatalf("Create job: %v", err)
	}

	if _, err = w.Jobs.RunNow(ctx, jobs.RunNow{JobId: job.JobId}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	if err = w.Jobs.CancelAllRuns(ctx, jobs.CancelAllRuns{JobId: job.JobId}); err != nil {
		t.Fatalf("CancelAllRuns: %v", err)
	}

	runs, err := w.Jobs.ListRunsAll(ctx, jobs.ListRunsRequest{JobId: job.JobId})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}

	for _, run := range runs {
		if run.State != nil && run.State.ResultState != jobs.RunResultStateCanceled {
			t.Fatalf("expected CANCELED run, got %q", run.State.ResultState)
		}
	}
}
