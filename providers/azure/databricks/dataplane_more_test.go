package databricks

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

func TestJobRunLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	id, err := m.CreateJob(ctx, driver.JobConfig{Name: "j1"})
	requireNoError(t, err)

	runID, err := m.RunJobNow(ctx, id)
	requireNoError(t, err)

	run, err := m.GetRun(ctx, runID)
	requireNoError(t, err)
	assertEqual(t, driver.RunTerminated, run.LifeCycleState)
	assertEqual(t, driver.ResultSuccess, run.ResultState)
	assertEqual(t, id, run.JobID)

	runs, err := m.ListRuns(ctx, id)
	requireNoError(t, err)
	assertEqual(t, 1, len(runs))

	all, err := m.ListRuns(ctx, 0)
	requireNoError(t, err)
	assertEqual(t, 1, len(all))

	out, err := m.GetRunOutput(ctx, runID)
	requireNoError(t, err)
	assertEqual(t, runID, out.Run.RunID)

	requireNoError(t, m.CancelRun(ctx, runID))
	run, err = m.GetRun(ctx, runID)
	requireNoError(t, err)
	assertEqual(t, driver.ResultCanceled, run.ResultState)

	_, err = m.GetRun(ctx, 999999)
	assertError(t, err, true)
}

func TestClusterPolicyLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	p, err := m.CreateClusterPolicy(ctx, driver.ClusterPolicyConfig{Name: "p1", Definition: "{}"})
	requireNoError(t, err)
	assertNotEmpty(t, p.PolicyID)

	_, err = m.CreateClusterPolicy(ctx, driver.ClusterPolicyConfig{})
	assertError(t, err, true)

	got, err := m.GetClusterPolicy(ctx, p.PolicyID)
	requireNoError(t, err)
	assertEqual(t, "p1", got.Name)

	requireNoError(t, m.EditClusterPolicy(ctx, p.PolicyID, driver.ClusterPolicyConfig{Name: "p1e", Definition: "{}"}))
	got, err = m.GetClusterPolicy(ctx, p.PolicyID)
	requireNoError(t, err)
	assertEqual(t, "p1e", got.Name)

	policies, err := m.ListClusterPolicies(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(policies))

	requireNoError(t, m.DeleteClusterPolicy(ctx, p.PolicyID))
	_, err = m.GetClusterPolicy(ctx, p.PolicyID)
	assertError(t, err, true)
}

func TestLibraries(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cl, err := m.CreateCluster(ctx, driver.ClusterConfig{Name: "c1", SparkVersion: "13.3", NodeTypeID: "nt", NumWorkers: 1})
	requireNoError(t, err)

	lib := driver.LibrarySpec{PypiPackage: "requests"}

	// Install on a missing cluster fails.
	assertError(t, m.InstallLibraries(ctx, "missing", []driver.LibrarySpec{lib}), true)

	requireNoError(t, m.InstallLibraries(ctx, cl.ID, []driver.LibrarySpec{lib}))

	statuses, err := m.ClusterLibraryStatuses(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, 1, len(statuses))
	assertEqual(t, driver.LibraryInstalled, statuses[0].Status)

	// Re-install the same library updates in place (no duplicate).
	requireNoError(t, m.InstallLibraries(ctx, cl.ID, []driver.LibrarySpec{lib}))
	statuses, err = m.ClusterLibraryStatuses(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, 1, len(statuses))

	requireNoError(t, m.UninstallLibraries(ctx, cl.ID, []driver.LibrarySpec{lib}))
	statuses, err = m.ClusterLibraryStatuses(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, driver.LibraryUninstallOnRestart, statuses[0].Status)

	all, err := m.AllClusterLibraryStatuses(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(all))
}
