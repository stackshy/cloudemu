package databricks

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/databricks/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	azuredbx "github.com/stackshy/cloudemu/providers/azure/databricks"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDataPlane(opts ...DataPlaneOption) *DataPlane {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))

	return NewDataPlane(azuredbx.New(o), opts...)
}

func TestDataPlaneLifecycle(t *testing.T) {
	dp := newTestDataPlane()
	ctx := context.Background()

	pool, err := dp.CreateInstancePool(ctx, driver.InstancePoolConfig{Name: "p1", NodeTypeID: "nt"})
	require.NoError(t, err)
	assert.Equal(t, driver.PoolActive, pool.State)

	_, err = dp.GetInstancePool(ctx, pool.ID)
	require.NoError(t, err)

	pools, err := dp.ListInstancePools(ctx)
	require.NoError(t, err)
	assert.Len(t, pools, 1)

	require.NoError(t, dp.EditInstancePool(ctx, pool.ID, driver.InstancePoolConfig{Name: "p1e", NodeTypeID: "nt"}))
	require.NoError(t, dp.DeleteInstancePool(ctx, pool.ID))

	cl, err := dp.CreateCluster(ctx, driver.ClusterConfig{Name: "c1", SparkVersion: "13.3", NodeTypeID: "nt", NumWorkers: 1})
	require.NoError(t, err)
	assert.Equal(t, driver.ClusterRunning, cl.State)

	require.NoError(t, dp.DeleteCluster(ctx, cl.ID))
	require.NoError(t, dp.StartCluster(ctx, cl.ID))
	require.NoError(t, dp.RestartCluster(ctx, cl.ID))
	require.NoError(t, dp.EditCluster(ctx, cl.ID, driver.ClusterConfig{Name: "c1", SparkVersion: "13.3", NodeTypeID: "nt", NumWorkers: 2}))

	clusters, err := dp.ListClusters(ctx)
	require.NoError(t, err)
	assert.Len(t, clusters, 1)

	require.NoError(t, dp.PermanentDeleteCluster(ctx, cl.ID))

	id, err := dp.CreateJob(ctx, driver.JobConfig{Name: "j1"})
	require.NoError(t, err)

	_, err = dp.GetJob(ctx, id)
	require.NoError(t, err)

	jobsList, err := dp.ListJobs(ctx)
	require.NoError(t, err)
	assert.Len(t, jobsList, 1)

	require.NoError(t, dp.UpdateJob(ctx, id, driver.JobConfig{Name: "j1u"}))
	require.NoError(t, dp.ResetJob(ctx, id, driver.JobConfig{Name: "j1r"}))

	runID, err := dp.RunJobNow(ctx, id)
	require.NoError(t, err)
	assert.NotZero(t, runID)

	require.NoError(t, dp.DeleteJob(ctx, id))

	_, err = dp.SetPermissions(ctx, "clusters", "c-1", []driver.AccessControl{{UserName: "a", PermissionLevel: "CAN_MANAGE"}})
	require.NoError(t, err)

	got, err := dp.GetPermissions(ctx, "clusters", "c-1")
	require.NoError(t, err)
	assert.Len(t, got.AccessControlList, 1)

	_, err = dp.UpdatePermissions(ctx, "clusters", "c-1", []driver.AccessControl{{GroupName: "g", PermissionLevel: "CAN_RESTART"}})
	require.NoError(t, err)
}

func TestDataPlaneCrossCutting(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	dp := newTestDataPlane(WithDataPlaneRecorder(rec), WithDataPlaneMetrics(mc), WithDataPlaneErrorInjection(inj))

	_, err := dp.ListClusters(context.Background())
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(rec.Calls()), 1)
	assert.Equal(t, "databricks-dataplane", rec.Calls()[0].Service)
	assert.GreaterOrEqual(t, metrics.NewQuery(mc).ByName("calls_total").Count(), 1)

	inj.Set("databricks-dataplane", "ListClusters", fmt.Errorf("boom"), inject.Always{})
	_, err = dp.ListClusters(context.Background())
	require.Error(t, err)
}
