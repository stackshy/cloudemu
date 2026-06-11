package databricks

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/databricks/driver"
)

func TestInstancePoolLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	pool, err := m.CreateInstancePool(ctx, driver.InstancePoolConfig{Name: "p1", NodeTypeID: "nt", MaxCapacity: 5})
	requireNoError(t, err)
	assertNotEmpty(t, pool.ID)
	assertEqual(t, driver.PoolActive, pool.State)

	_, err = m.CreateInstancePool(ctx, driver.InstancePoolConfig{NodeTypeID: "nt"})
	assertError(t, err, true)

	got, err := m.GetInstancePool(ctx, pool.ID)
	requireNoError(t, err)
	assertEqual(t, "p1", got.Name)

	requireNoError(t, m.EditInstancePool(ctx, pool.ID, driver.InstancePoolConfig{Name: "p1e", NodeTypeID: "nt"}))

	edited, err := m.GetInstancePool(ctx, pool.ID)
	requireNoError(t, err)
	assertEqual(t, "p1e", edited.Name)

	pools, err := m.ListInstancePools(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(pools))

	requireNoError(t, m.DeleteInstancePool(ctx, pool.ID))
	_, err = m.GetInstancePool(ctx, pool.ID)
	assertError(t, err, true)
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	cl, err := m.CreateCluster(ctx, driver.ClusterConfig{Name: "c1", SparkVersion: "13.3", NodeTypeID: "nt", NumWorkers: 2})
	requireNoError(t, err)
	assertEqual(t, driver.ClusterRunning, cl.State)

	_, err = m.CreateCluster(ctx, driver.ClusterConfig{NodeTypeID: "nt"})
	assertError(t, err, true)

	requireNoError(t, m.DeleteCluster(ctx, cl.ID))
	got, err := m.GetCluster(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, driver.ClusterTerminated, got.State)

	requireNoError(t, m.StartCluster(ctx, cl.ID))
	got, err = m.GetCluster(ctx, cl.ID)
	requireNoError(t, err)
	assertEqual(t, driver.ClusterRunning, got.State)

	requireNoError(t, m.RestartCluster(ctx, cl.ID))
	requireNoError(t, m.EditCluster(ctx, cl.ID, driver.ClusterConfig{Name: "c1e", SparkVersion: "13.3", NodeTypeID: "nt", NumWorkers: 3}))

	clusters, err := m.ListClusters(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(clusters))

	requireNoError(t, m.PermanentDeleteCluster(ctx, cl.ID))
	_, err = m.GetCluster(ctx, cl.ID)
	assertError(t, err, true)
}

func TestJobLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	id, err := m.CreateJob(ctx, driver.JobConfig{Name: "j1", SettingsJSON: []byte(`{"name":"j1"}`)})
	requireNoError(t, err)
	if id == 0 {
		t.Fatal("expected non-zero job id")
	}

	_, err = m.CreateJob(ctx, driver.JobConfig{})
	assertError(t, err, true)

	job, err := m.GetJob(ctx, id)
	requireNoError(t, err)
	assertEqual(t, "j1", job.Name)

	requireNoError(t, m.UpdateJob(ctx, id, driver.JobConfig{Name: "j1u"}))
	job, err = m.GetJob(ctx, id)
	requireNoError(t, err)
	assertEqual(t, "j1u", job.Name)

	run1, err := m.RunJobNow(ctx, id)
	requireNoError(t, err)
	run2, err := m.RunJobNow(ctx, id)
	requireNoError(t, err)
	if run2 <= run1 {
		t.Fatalf("expected increasing run ids, got %d then %d", run1, run2)
	}

	jobs, err := m.ListJobs(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(jobs))

	requireNoError(t, m.DeleteJob(ctx, id))
	_, err = m.GetJob(ctx, id)
	assertError(t, err, true)
}

func TestPermissions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Unset → empty list, no error.
	empty, err := m.GetPermissions(ctx, "clusters", "c-1")
	requireNoError(t, err)
	assertEqual(t, 0, len(empty.AccessControlList))

	set, err := m.SetPermissions(ctx, "clusters", "c-1", []driver.AccessControl{
		{UserName: "alice", PermissionLevel: "CAN_MANAGE"},
	})
	requireNoError(t, err)
	assertEqual(t, 1, len(set.AccessControlList))

	// Update merges: replace alice, add bob.
	updated, err := m.UpdatePermissions(ctx, "clusters", "c-1", []driver.AccessControl{
		{UserName: "alice", PermissionLevel: "CAN_RESTART"},
		{GroupName: "eng", PermissionLevel: "CAN_ATTACH_TO"},
	})
	requireNoError(t, err)
	assertEqual(t, 2, len(updated.AccessControlList))
}
