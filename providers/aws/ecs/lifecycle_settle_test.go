package ecs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// settleEpoch is a fixed base time so the FakeClock-driven settle assertions
// below are deterministic.
var settleEpoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test fixture

// newSettleMock builds an ECS mock with AsyncSettle enabled and a FakeClock the
// test can advance to observe the task lifecycle transients.
func newSettleMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(settleEpoch)
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAsyncSettle())

	return New(opts), fc
}

// registerSimpleTaskDef registers a minimal single-container task definition
// and returns its ARN.
func registerSimpleTaskDef(t *testing.T, m *Mock, ctx context.Context, family string) string {
	t.Helper()

	td, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family: family,
		ContainerDefinitions: []driver.ContainerDefinition{
			{Name: "app", Image: "nginx:latest"},
		},
	})
	require.NoError(t, err)

	return td.ARN
}

// TestRunTaskEC2SettlesPendingToRunning verifies an EC2-launched task reports
// the PENDING launch transient until its settle window elapses, then RUNNING,
// when AsyncSettle is enabled.
func TestRunTaskEC2SettlesPendingToRunning(t *testing.T) {
	m, fc := newSettleMock()
	ctx := context.Background()

	tdARN := registerSimpleTaskDef(t, m, ctx, "web")
	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	_, err = m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{
		Cluster: "prod",
		TotalResources: []driver.Resource{
			{Name: "CPU", Type: "INTEGER", IntegerValue: 4096},
			{Name: "MEMORY", Type: "INTEGER", IntegerValue: 8192},
		},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: tdARN})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)
	assert.Equal(t, statusPending, tasks[0].LastStatus)

	taskARN := tasks[0].ARN

	described, _, err := m.DescribeTasks(ctx, "prod", []string{taskARN})
	require.NoError(t, err)
	require.Len(t, described, 1)
	assert.Equal(t, statusPending, described[0].LastStatus)

	fc.Advance(settle.DefaultECSTaskStartSettle - time.Millisecond)
	described, _, err = m.DescribeTasks(ctx, "prod", []string{taskARN})
	require.NoError(t, err)
	assert.Equal(t, statusPending, described[0].LastStatus)

	fc.Advance(time.Millisecond)
	described, _, err = m.DescribeTasks(ctx, "prod", []string{taskARN})
	require.NoError(t, err)
	assert.Equal(t, statusRunning, described[0].LastStatus)
}

// TestRunTaskFargateSettlesProvisioningToRunning verifies a Fargate task
// reports PROVISIONING (not the EC2 PENDING) as its launch transient.
func TestRunTaskFargateSettlesProvisioningToRunning(t *testing.T) {
	m, fc := newSettleMock()
	ctx := context.Background()

	td, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "web",
		RequiresCompatibilities: []string{launchFargate},
		NetworkMode:             networkModeAwsvpc,
		CPU:                     "256",
		Memory:                  "512",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "app", Image: "nginx:latest"}},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		TaskDefinition: td.ARN,
		LaunchType:     launchFargate,
		NetworkConfiguration: &driver.NetworkConfiguration{
			AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
		},
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)
	assert.Equal(t, taskStatusProvisioning, tasks[0].LastStatus)

	fc.Advance(settle.DefaultECSTaskStartSettle)
	described, _, err := m.DescribeTasks(ctx, "default", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, statusRunning, described[0].LastStatus)
}

// TestStopTaskSettlesStoppingToStopped verifies StopTask reports the STOPPING
// transient before STOPPED, matching real ECS: desiredStatus flips to STOPPED
// synchronously but lastStatus lags until the container actually stops.
func TestStopTaskSettlesStoppingToStopped(t *testing.T) {
	m, fc := newSettleMock()
	ctx := context.Background()

	td, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "web",
		RequiresCompatibilities: []string{launchFargate},
		NetworkMode:             networkModeAwsvpc,
		CPU:                     "256",
		Memory:                  "512",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "app", Image: "nginx:latest"}},
	})
	require.NoError(t, err)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		TaskDefinition: td.ARN,
		LaunchType:     launchFargate,
		NetworkConfiguration: &driver.NetworkConfiguration{
			AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
		},
	})
	require.NoError(t, err)
	fc.Advance(settle.DefaultECSTaskStartSettle)

	stopped, err := m.StopTask(ctx, "default", tasks[0].ARN, "user request")
	require.NoError(t, err)
	assert.Equal(t, taskStatusDeprovisioning, stopped.LastStatus)
	assert.Equal(t, statusStopped, stopped.DesiredStatus)
	assert.Equal(t, "user request", stopped.StoppedReason)

	described, _, err := m.DescribeTasks(ctx, "default", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, taskStatusDeprovisioning, described[0].LastStatus)

	fc.Advance(settle.DefaultECSTaskStopSettle)
	described, _, err = m.DescribeTasks(ctx, "default", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, statusStopped, described[0].LastStatus)
}

// TestRepeatedStopTaskDoesNotRestartSettleWindow verifies StopTask is
// idempotent on an already-stopped task: once a task has settled to STOPPED, a
// second StopTask call must not restart the stop-settle window — neither its
// own response nor an immediate DescribeTasks may report the
// DEPROVISIONING/STOPPING transient again for a terminal task.
func TestRepeatedStopTaskDoesNotRestartSettleWindow(t *testing.T) {
	m, fc := newSettleMock()
	ctx := context.Background()

	td, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "web",
		RequiresCompatibilities: []string{launchFargate},
		NetworkMode:             networkModeAwsvpc,
		CPU:                     "256",
		Memory:                  "512",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "app", Image: "nginx:latest"}},
	})
	require.NoError(t, err)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		TaskDefinition: td.ARN,
		LaunchType:     launchFargate,
		NetworkConfiguration: &driver.NetworkConfiguration{
			AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
		},
	})
	require.NoError(t, err)
	fc.Advance(settle.DefaultECSTaskStartSettle)

	_, err = m.StopTask(ctx, "default", tasks[0].ARN, "first stop")
	require.NoError(t, err)

	fc.Advance(settle.DefaultECSTaskStopSettle)
	described, _, err := m.DescribeTasks(ctx, "default", []string{tasks[0].ARN})
	require.NoError(t, err)
	require.Equal(t, statusStopped, described[0].LastStatus)

	// A second StopTask on the already-stopped task must be a clean idempotent
	// no-op: its own response reports STOPPED (not a fresh DEPROVISIONING), and
	// an immediate DescribeTasks agrees — the task never "un-stops".
	again, err := m.StopTask(ctx, "default", tasks[0].ARN, "second stop")
	require.NoError(t, err)
	assert.Equal(t, statusStopped, again.LastStatus)

	described, _, err = m.DescribeTasks(ctx, "default", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, statusStopped, described[0].LastStatus)
}

// TestServiceReconciliationUnaffectedBySettle verifies a service's
// running/pending counts reflect the tasks' already-final state immediately —
// the settle transient is a read-time overlay on DescribeTasks/ListTasks only,
// never on the scheduler's own internal bookkeeping.
func TestServiceReconciliationUnaffectedBySettle(t *testing.T) {
	m, _ := newSettleMock()
	ctx := context.Background()

	td, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "web",
		RequiresCompatibilities: []string{launchFargate},
		NetworkMode:             networkModeAwsvpc,
		CPU:                     "256",
		Memory:                  "512",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "app", Image: "nginx:latest"}},
	})
	require.NoError(t, err)
	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "api", Cluster: "prod", TaskDefinition: td.ARN, DesiredCount: 2, LaunchType: launchFargate,
		NetworkConfiguration: &driver.NetworkConfiguration{
			AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, svc.RunningCount)
	assert.Equal(t, 0, svc.PendingCount)

	// Meanwhile a direct DescribeTasks poll for one of the service's tasks still
	// observes the PROVISIONING launch transient.
	tasks, err := m.ListTasks(ctx, "prod", "", "", "api")
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	assert.Equal(t, taskStatusProvisioning, tasks[0].LastStatus)
}

// TestForceDeregisterInstanceClearsSettleWindow verifies a task force-stopped
// by deregistering its container instance mid-launch-transient reports STOPPED
// immediately, not a stale PENDING/PROVISIONING.
func TestForceDeregisterInstanceClearsSettleWindow(t *testing.T) {
	m, _ := newSettleMock()
	ctx := context.Background()

	tdARN := registerSimpleTaskDef(t, m, ctx, "web")
	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	ci, err := m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{
		Cluster: "prod",
		TotalResources: []driver.Resource{
			{Name: "CPU", Type: "INTEGER", IntegerValue: 4096},
			{Name: "MEMORY", Type: "INTEGER", IntegerValue: 8192},
		},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: tdARN})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Equal(t, statusPending, tasks[0].LastStatus)

	_, err = m.DeregisterContainerInstance(ctx, "prod", ci.ARN, true)
	require.NoError(t, err)

	described, _, err := m.DescribeTasks(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, statusStopped, described[0].LastStatus)
}

// TestAsyncSettleDefaultOffTaskLifecycle guards the blast radius: with the
// default options (AsyncSettle off) RunTask/StopTask report their final
// RUNNING/STOPPED status immediately, exactly as before this change.
func TestAsyncSettleDefaultOffTaskLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	tdARN := registerSimpleTaskDef(t, m, ctx, "web")
	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	_, err = m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{
		Cluster: "prod",
		TotalResources: []driver.Resource{
			{Name: "CPU", Type: "INTEGER", IntegerValue: 4096},
			{Name: "MEMORY", Type: "INTEGER", IntegerValue: 8192},
		},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: tdARN})
	require.NoError(t, err)
	require.Empty(t, failures)
	assert.Equal(t, statusRunning, tasks[0].LastStatus)

	stopped, err := m.StopTask(ctx, "prod", tasks[0].ARN, "done")
	require.NoError(t, err)
	assert.Equal(t, statusStopped, stopped.LastStatus)
}
