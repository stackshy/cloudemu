package ecs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// registerFargate registers a FARGATE task definition with the given task-level
// cpu/memory strings.
func registerFargate(t *testing.T, m *Mock, cpu, memory string) {
	t.Helper()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     cpu,
		Memory:                  memory,
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)
}

// #1 — an unsupported Fargate cpu/memory pairing is rejected up front.
func TestRunTaskFargateInvalidCPUMemoryCombo(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	registerFargate(t, m, "512", "512") // 512 vCPU requires >= 1024 MiB

	netCfg := &driver.NetworkConfiguration{
		AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
	}

	_, _, err = m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	// A valid pairing still succeeds.
	registerFargate(t, m, "256", "512")

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
}

// #4 — a DAEMON service rejects a caller-supplied desiredCount.
func TestCreateServiceDaemonRejectsDesiredCount(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 128, 256)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		Cluster: "prod", ServiceName: "svc", TaskDefinition: "web",
		SchedulingStrategy: schedDaemon, DesiredCount: 2,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}

// #3 — repeated UpdateService does not grow the deployments list.
func TestUpdateServiceDeploymentsDoNotAccumulate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	m.SeedContainerInstance("prod", "i-1")
	registerWeb(t, m, 128, 256)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		Cluster: "prod", ServiceName: "svc", TaskDefinition: "web", DesiredCount: 1,
	})
	require.NoError(t, err)

	for range 3 {
		_, err = m.UpdateService(ctx, driver.UpdateServiceInput{
			Cluster: "prod", Service: "svc", ForceNewDeployment: true,
		})
		require.NoError(t, err)
	}

	svcs, _, err := m.DescribeServices(ctx, "prod", []string{"svc"})
	require.NoError(t, err)
	require.Len(t, svcs, 1)
	assert.Len(t, svcs[0].Deployments, 1, "deployments must not accumulate across updates")
	assert.Equal(t, deploymentPrimary, svcs[0].Deployments[0].Status)
}

// #6c — force-deregistering an instance stops the tasks placed on it.
func TestForceDeregisterStopsTasks(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	ci := m.SeedContainerInstance("prod", "i-1")
	registerWeb(t, m, 128, 256)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)
	require.Equal(t, statusRunning, tasks[0].LastStatus)

	_, err = m.DeregisterContainerInstance(ctx, "prod", ci.ARN, true)
	require.NoError(t, err)

	got, _, err := m.DescribeTasks(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, statusStopped, got[0].LastStatus, "task on a force-deregistered instance must be stopped")
}

// #6d — RunTask with count <= 0 defaults to a single task.
func TestRunTaskDefaultsCountToOne(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	m.SeedContainerInstance("prod", "i-1")
	registerWeb(t, m, 128, 256)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 0})
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
}
