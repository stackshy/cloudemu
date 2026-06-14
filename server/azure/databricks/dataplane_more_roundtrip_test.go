package databricks_test

import (
	"context"
	"testing"

	"github.com/databricks/databricks-sdk-go/service/compute"
	"github.com/databricks/databricks-sdk-go/service/jobs"
)

func TestSDKJobRuns(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Jobs.Create(ctx, jobs.CreateJob{Name: "job-runs"})
	if err != nil {
		t.Fatalf("Create job: %v", err)
	}

	run, err := w.Jobs.RunNow(ctx, jobs.RunNow{JobId: created.JobId})
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	runID := run.Response.RunId

	got, err := w.Jobs.GetRun(ctx, jobs.GetRunRequest{RunId: runID})
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	if got.State == nil || got.State.ResultState != jobs.RunResultStateSuccess {
		t.Fatalf("expected SUCCESS result, got %+v", got.State)
	}

	runs, err := w.Jobs.ListRunsAll(ctx, jobs.ListRunsRequest{JobId: created.JobId})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}

	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}

	out, err := w.Jobs.GetRunOutput(ctx, jobs.GetRunOutputRequest{RunId: runID})
	if err != nil {
		t.Fatalf("GetRunOutput: %v", err)
	}

	if out.NotebookOutput == nil || out.NotebookOutput.Result == "" {
		t.Fatalf("expected notebook output, got %+v", out)
	}

	if _, err = w.Jobs.CancelRun(ctx, jobs.CancelRun{RunId: runID}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	canceled, err := w.Jobs.GetRun(ctx, jobs.GetRunRequest{RunId: runID})
	if err != nil {
		t.Fatalf("GetRun after cancel: %v", err)
	}

	if canceled.State.ResultState != jobs.RunResultStateCanceled {
		t.Fatalf("expected CANCELED, got %q", canceled.State.ResultState)
	}
}

func TestSDKClusterPolicies(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.ClusterPolicies.Create(ctx, compute.CreatePolicy{
		Name:       "policy-1",
		Definition: `{"spark_version":{"type":"fixed","value":"13.3.x-scala2.12"}}`,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.PolicyId == "" {
		t.Fatal("expected policy id")
	}

	got, err := w.ClusterPolicies.Get(ctx, compute.GetClusterPolicyRequest{PolicyId: created.PolicyId})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != "policy-1" {
		t.Fatalf("got name %q, want policy-1", got.Name)
	}

	if err = w.ClusterPolicies.Edit(ctx, compute.EditPolicy{
		PolicyId:   created.PolicyId,
		Name:       "policy-1-edited",
		Definition: got.Definition,
	}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	policies, err := w.ClusterPolicies.ListAll(ctx, compute.ListClusterPoliciesRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(policies) != 1 {
		t.Fatalf("got %d policies, want 1", len(policies))
	}

	if err = w.ClusterPolicies.Delete(ctx, compute.DeletePolicy{PolicyId: created.PolicyId}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestSDKLibraries(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
		ClusterName:  "lib-cluster",
		SparkVersion: "13.3.x-scala2.12",
		NodeTypeId:   "Standard_DS3_v2",
		NumWorkers:   1,
	})
	if err != nil {
		t.Fatalf("Create cluster: %v", err)
	}

	clusterID := wait.ClusterId

	if err = w.Libraries.Install(ctx, compute.InstallLibraries{
		ClusterId: clusterID,
		Libraries: []compute.Library{{Pypi: &compute.PythonPyPiLibrary{Package: "requests"}}},
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	status, err := w.Libraries.ClusterStatusByClusterId(ctx, clusterID)
	if err != nil {
		t.Fatalf("ClusterStatus: %v", err)
	}

	if len(status.LibraryStatuses) != 1 || status.LibraryStatuses[0].Status != compute.LibraryInstallStatusInstalled {
		t.Fatalf("expected one INSTALLED library, got %+v", status.LibraryStatuses)
	}

	if err = w.Libraries.Uninstall(ctx, compute.UninstallLibraries{
		ClusterId: clusterID,
		Libraries: []compute.Library{{Pypi: &compute.PythonPyPiLibrary{Package: "requests"}}},
	}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	after, err := w.Libraries.ClusterStatusByClusterId(ctx, clusterID)
	if err != nil {
		t.Fatalf("ClusterStatus after uninstall: %v", err)
	}

	if after.LibraryStatuses[0].Status != compute.LibraryInstallStatusUninstallOnRestart {
		t.Fatalf("expected UNINSTALL_ON_RESTART, got %q", after.LibraryStatuses[0].Status)
	}
}
