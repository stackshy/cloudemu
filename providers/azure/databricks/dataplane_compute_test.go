package databricks

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

func TestClusterResizePinMetadata(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cl, err := m.CreateCluster(ctx, driver.ClusterConfig{Name: "c1", SparkVersion: "13.3", NodeTypeID: "nt", NumWorkers: 1})
	requireNoError(t, err)

	requireNoError(t, m.ResizeCluster(ctx, cl.ID, 4, 0, 0))
	got, err := m.GetCluster(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, int32(4), got.NumWorkers)

	requireNoError(t, m.PinCluster(ctx, cl.ID))
	got, err = m.GetCluster(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, true, got.Pinned)

	requireNoError(t, m.UnpinCluster(ctx, cl.ID))
	got, err = m.GetCluster(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, false, got.Pinned)

	// not-found paths
	assertError(t, m.ResizeCluster(ctx, "missing", 1, 0, 0), true)
	assertError(t, m.PinCluster(ctx, "missing"), true)

	nt, err := m.ListNodeTypes(ctx)
	requireNoError(t, err)
	if len(nt) == 0 {
		t.Fatal("expected seeded node types")
	}

	sv, err := m.ListSparkVersions(ctx)
	requireNoError(t, err)
	if len(sv) == 0 {
		t.Fatal("expected seeded spark versions")
	}

	zones, def, err := m.ListZones(ctx)
	requireNoError(t, err)
	if len(zones) == 0 || def == "" {
		t.Fatalf("expected zones + default, got %v / %q", zones, def)
	}
}

func TestRunSubmitRepairCancelDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	runID, err := m.SubmitRun(ctx, "one-time")
	requireNoError(t, err)

	run, err := m.GetRun(ctx, runID)
	requireNoError(t, err)
	assertEqual(t, driver.ResultSuccess, run.ResultState)

	repairID, err := m.RepairRun(ctx, runID)
	requireNoError(t, err)
	if repairID == 0 {
		t.Fatal("expected non-zero repair id")
	}

	_, err = m.RepairRun(ctx, 999999)
	assertError(t, err, true)

	requireNoError(t, m.DeleteRun(ctx, runID))
	_, err = m.GetRun(ctx, runID)
	assertError(t, err, true)
	assertError(t, m.DeleteRun(ctx, runID), true)
}

func TestCancelAllRuns(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	id, err := m.CreateJob(ctx, driver.JobConfig{Name: "j"})
	requireNoError(t, err)

	r1, err := m.RunJobNow(ctx, id)
	requireNoError(t, err)
	_, err = m.RunJobNow(ctx, id)
	requireNoError(t, err)

	requireNoError(t, m.CancelAllRuns(ctx, id))

	runs, err := m.ListRuns(ctx, id)
	requireNoError(t, err)
	assertEqual(t, 2, len(runs))

	for _, run := range runs {
		assertEqual(t, driver.ResultCanceled, run.ResultState)
	}

	got, err := m.GetRun(ctx, r1)
	requireNoError(t, err)
	assertEqual(t, driver.ResultCanceled, got.ResultState)
}
