package ecs

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// TestStopTask_ReconcilesOwningService guards the ECS "service-count-vs-overlay
// divergence" fix: stopping a service-owned task directly (not via the
// service's own drain) must reconcile the owning service's live counts and
// launch a replacement to converge back to desiredCount, mirroring real ECS's
// scheduler reconciliation pass.
func TestStopTask_ReconcilesOwningService(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// Generous EC2 capacity so both the initial convergence and the
	// replacement task launched by reconciliation always fit.
	m.SeedContainerInstance("prod", "i-svc", WithCapacity(81920, 163840))

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName:    "web-svc",
		Cluster:        "prod",
		TaskDefinition: "web:1",
		DesiredCount:   2,
	})
	require.NoError(t, err)
	require.Equal(t, 2, svc.RunningCount)
	require.Equal(t, 0, svc.PendingCount)

	tasks, err := m.ListTasks(ctx, "prod", "", "", "web-svc")
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	stopped, err := m.StopTask(ctx, "prod", tasks[0].ARN, "manual test stop")
	require.NoError(t, err)
	assert.Equal(t, statusStopped, stopped.LastStatus)

	// The service must self-heal synchronously: a replacement task launches
	// and the live counts converge back to desiredCount, exactly as real
	// ECS's scheduler does on its next reconciliation pass.
	found, _, err := m.DescribeServices(ctx, "prod", []string{"web-svc"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, 2, found[0].DesiredCount)
	assert.Equal(t, 2, found[0].RunningCount)
	assert.Equal(t, 0, found[0].PendingCount)
	require.Len(t, found[0].Deployments, 1)
	assert.Equal(t, 2, found[0].Deployments[0].RunningCount)
	assert.Equal(t, rolloutCompleted, found[0].Deployments[0].RolloutState)

	// Exactly 2 tasks are actually RUNNING (the untouched original plus the
	// replacement) — the stopped task must not still be counted.
	running, err := m.ListTasks(ctx, "prod", "", statusRunning, "web-svc")
	require.NoError(t, err)
	assert.Len(t, running, 2)

	all, err := m.ListTasks(ctx, "prod", "", "", "web-svc")
	require.NoError(t, err)
	assert.Len(t, all, 3, "the stopped task, the untouched original, and the replacement")

	// A repeated StopTask on the already-stopped task is idempotent: it must
	// not trigger a second reconciliation (which would over-converge).
	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "second stop")
	require.NoError(t, err)

	found, _, err = m.DescribeServices(ctx, "prod", []string{"web-svc"})
	require.NoError(t, err)
	assert.Equal(t, 2, found[0].RunningCount)
}

// TestDeleteService_DrainDoesNotDoubleReconcile guards that DeleteService's
// own drain (which stops every task itself) doesn't race StopTask's
// reconciliation: drainService must not launch a replacement task for a
// service that's being deliberately torn down.
func TestDeleteService_DrainDoesNotDoubleReconcile(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	m.SeedContainerInstance("prod", "i-svc", WithCapacity(81920, 163840))

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName:    "web-svc",
		Cluster:        "prod",
		TaskDefinition: "web:1",
		DesiredCount:   2,
	})
	require.NoError(t, err)

	deleted, err := m.DeleteService(ctx, "prod", "web-svc", true)
	require.NoError(t, err)
	assert.Equal(t, statusInactive, deleted.Status)
	assert.Equal(t, 0, deleted.RunningCount)

	running, err := m.ListTasks(ctx, "prod", "", statusRunning, "web-svc")
	require.NoError(t, err)
	assert.Empty(t, running, "delete must not relaunch replacement tasks for a service being torn down")
}

// TestCreateService_AvailabilityZoneRebalancingDefaultsToDisabled guards that
// a fresh service always echoes availabilityZoneRebalancing (real ECS never
// leaves it unset), defaulting to "DISABLED" when the caller doesn't specify
// one — this is what lets a Terraform apply immediately followed by a plan
// come back clean instead of showing a perpetual 1-attribute diff.
func TestCreateService_AvailabilityZoneRebalancingDefaultsToDisabled(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "default-azr", Cluster: "prod", TaskDefinition: "web", DesiredCount: 0,
	})
	require.NoError(t, err)
	assert.Equal(t, "DISABLED", svc.AvailabilityZoneRebalancing)

	explicit, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "explicit-azr", Cluster: "prod", TaskDefinition: "web", DesiredCount: 0,
		AvailabilityZoneRebalancing: "ENABLED",
	})
	require.NoError(t, err)
	assert.Equal(t, "ENABLED", explicit.AvailabilityZoneRebalancing)
}

// TestUpdateService_AvailabilityZoneRebalancing guards that UpdateService
// accepts and stores an explicit availabilityZoneRebalancing change, leaving
// it untouched when the caller omits the field.
func TestUpdateService_AvailabilityZoneRebalancing(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web-svc", Cluster: "prod", TaskDefinition: "web", DesiredCount: 0,
	})
	require.NoError(t, err)
	require.Equal(t, "DISABLED", svc.AvailabilityZoneRebalancing)

	// Omitting the field on update leaves the current setting unchanged.
	unchanged, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web-svc", Cluster: "prod"})
	require.NoError(t, err)
	assert.Equal(t, "DISABLED", unchanged.AvailabilityZoneRebalancing)

	updated, err := m.UpdateService(ctx, driver.UpdateServiceInput{
		Service: "web-svc", Cluster: "prod", AvailabilityZoneRebalancing: "ENABLED",
	})
	require.NoError(t, err)
	assert.Equal(t, "ENABLED", updated.AvailabilityZoneRebalancing)
}

// TestStopTask_ConcurrentSameServiceNoOverLaunch guards the per-service
// reconcile lock: reconcileServiceAfterStop's read-live-counts ->
// decide-shortfall -> launch-replacements -> commit sequence must be atomic
// per service, or two concurrent StopTask calls on two different RUNNING
// tasks of the same service can each read the pre-replacement counts, each
// independently compute the full shortfall, and each launch a replacement —
// leaving the service permanently over-provisioned above desiredCount (with
// nothing to ever scale it back down). Repeated iterations under -race make
// this reliably catch a regression of the lock.
func TestStopTask_ConcurrentSameServiceNoOverLaunch(t *testing.T) {
	const iterations = 20

	for iter := range iterations {
		m := newTestMock()
		ctx := context.Background()

		_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
		require.NoError(t, err)

		_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
			Family:               "web",
			ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		})
		require.NoError(t, err)

		// Generous EC2 capacity so both the initial convergence and any
		// replacement tasks always fit.
		m.SeedContainerInstance("prod", "i-race", WithCapacity(81920, 163840))

		svc, err := m.CreateService(ctx, driver.CreateServiceInput{
			ServiceName:    "web-svc",
			Cluster:        "prod",
			TaskDefinition: "web:1",
			DesiredCount:   2,
		})
		require.NoError(t, err)
		require.Equal(t, 2, svc.RunningCount)

		tasks, err := m.ListTasks(ctx, "prod", "", "", "web-svc")
		require.NoError(t, err)
		require.Len(t, tasks, 2)

		var wg sync.WaitGroup

		wg.Add(len(tasks))

		for _, task := range tasks {
			go func(taskARN string) {
				defer wg.Done()

				_, stopErr := m.StopTask(ctx, "prod", taskARN, "concurrent stop")
				assert.NoError(t, stopErr)
			}(task.ARN)
		}

		wg.Wait()

		running, err := m.ListTasks(ctx, "prod", "", statusRunning, "web-svc")
		require.NoError(t, err)

		pending, err := m.ListTasks(ctx, "prod", "", statusPending, "web-svc")
		require.NoError(t, err)

		assert.Equalf(t, 2, len(running)+len(pending),
			"iteration %d: service must never over-launch above desiredCount=2 (got %d running + %d pending)",
			iter, len(running), len(pending))

		found, _, err := m.DescribeServices(ctx, "prod", []string{"web-svc"})
		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.Equalf(t, 2, found[0].RunningCount+found[0].PendingCount,
			"iteration %d: service bookkeeping must match live counts", iter)
	}
}

// TestStopTask_StandaloneTaskNoServiceGroupNoop guards the reconcile path for
// a standalone task started via RunTask (no owning service, so no
// "service:"-prefixed Group): serviceNameFromGroup must report false and
// reconcileServiceAfterStop must no-op cleanly on StopTask — no crash, no
// accidental relaunch, no phantom service.
func TestStopTask_StandaloneTaskNoServiceGroupNoop(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	m.SeedContainerInstance("prod", "i-standalone")

	// A plain RunTask (no service) carries no "service:" Group.
	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)
	assert.Empty(t, tasks[0].Group)

	stopped, err := m.StopTask(ctx, "prod", tasks[0].ARN, "standalone stop")
	require.NoError(t, err)
	assert.Equal(t, statusStopped, stopped.LastStatus)

	// No service was created, so nothing to relaunch: exactly the one
	// now-stopped task exists, and no service surfaces it.
	all, err := m.ListTasks(ctx, "prod", "", "", "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, statusStopped, all[0].LastStatus)

	svcs, err := m.ListServices(ctx, "prod")
	require.NoError(t, err)
	assert.Empty(t, svcs)

	// A repeated StopTask on the already-stopped standalone task is also a
	// clean no-op (the reconcile path's early return handles it both times).
	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "standalone stop again")
	require.NoError(t, err)
}
