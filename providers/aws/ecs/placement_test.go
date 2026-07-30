package ecs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// ecsException extracts the AWS exception name a driver error carries.
func ecsException(t *testing.T, err error) string {
	t.Helper()

	var ex interface{ ECSException() string }
	require.ErrorAs(t, err, &ex)

	return ex.ECSException()
}

// registerWeb registers a single-container task definition with the given
// container-level CPU units and memory (MiB) and returns nothing useful beyond
// registration.
func registerWeb(t *testing.T, m *Mock, cpu, memory int) {
	t.Helper()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img", CPU: cpu, Memory: memory}},
	})
	require.NoError(t, err)
}

func TestRunTaskEC2PlacementAndRelease(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	registerWeb(t, m, 256, 512)

	ci := m.SeedContainerInstance("prod", "i-fit", WithCapacity(1024, 2048))

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "web", LaunchType: launchEC2,
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)
	assert.Equal(t, statusRunning, tasks[0].LastStatus)
	assert.Equal(t, ci.ARN, tasks[0].ContainerInstanceARN)

	// The chosen instance had its remaining resources decremented by the task's
	// required footprint, and its running-task count incremented.
	after, _, err := m.DescribeContainerInstances(ctx, "prod", []string{ci.ARN})
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, 1024-256, after[0].RemainingCPU)
	assert.Equal(t, 2048-512, after[0].RemainingMemory)
	assert.Equal(t, 1, after[0].RunningTasksCount)

	// Stopping the task releases its reservation back to the instance.
	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "bye")
	require.NoError(t, err)

	released, _, err := m.DescribeContainerInstances(ctx, "prod", []string{ci.ARN})
	require.NoError(t, err)
	assert.Equal(t, 1024, released[0].RemainingCPU)
	assert.Equal(t, 2048, released[0].RemainingMemory)
	assert.Equal(t, 0, released[0].RunningTasksCount)

	// A second StopTask must not double-credit the instance.
	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "again")
	require.NoError(t, err)

	stable, _, err := m.DescribeContainerInstances(ctx, "prod", []string{ci.ARN})
	require.NoError(t, err)
	assert.Equal(t, 1024, stable[0].RemainingCPU)
	assert.Equal(t, 2048, stable[0].RemainingMemory)
}

func TestRunTaskEC2NoContainerInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 256, 512)

	// No container instances at all: a placement failure, not a RUNNING task.
	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "web", LaunchType: launchEC2,
	})
	require.NoError(t, err)
	assert.Empty(t, tasks)
	require.Len(t, failures, 1)
	assert.Equal(t, reasonNoInstances, failures[0].Reason)
	assert.Equal(t, noInstancesDetail, failures[0].Detail)
}

func TestRunTaskEC2InsufficientMemory(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 256, 512)

	// An instance that cannot satisfy the memory request yields RESOURCE:MEMORY.
	m.SeedContainerInstance("prod", "i-small", WithCapacity(1024, 256))

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "web", LaunchType: launchEC2,
	})
	require.NoError(t, err)
	assert.Empty(t, tasks)
	require.Len(t, failures, 1)
	assert.Equal(t, reasonResourceMem, failures[0].Reason)
}

func TestRunTaskEC2InsufficientCPU(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 256, 512)

	// Enough memory but not enough CPU yields RESOURCE:CPU.
	m.SeedContainerInstance("prod", "i-slow", WithCapacity(100, 4096))

	_, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "web", LaunchType: launchEC2,
	})
	require.NoError(t, err)
	require.Len(t, failures, 1)
	assert.Equal(t, reasonResourceCPU, failures[0].Reason)
}

func TestRunTaskEC2PartialCapacity(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 256, 512)

	// One instance fits exactly two tasks; a third has nowhere to go.
	m.SeedContainerInstance("prod", "i-two", WithCapacity(512, 1024))

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "web", LaunchType: launchEC2, Count: 3,
	})
	require.NoError(t, err)
	assert.Len(t, tasks, 2)
	require.Len(t, failures, 1)
	assert.Equal(t, reasonResourceMem, failures[0].Reason)
}

func TestRunTaskLaunchTypeMismatch(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	// The task def only supports FARGATE, so an EC2 launch is rejected up front.
	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchEC2,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
	assert.Empty(t, tasks)
	assert.Empty(t, failures)
}

func TestRunTaskFargate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	netCfg := &driver.NetworkConfiguration{
		AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-123"}},
	}

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)

	// Fargate tasks run without a container instance, echo a concrete platform
	// version, and carry a synthesized ENI attachment.
	assert.Equal(t, statusRunning, tasks[0].LastStatus)
	assert.Empty(t, tasks[0].ContainerInstanceARN)
	assert.Equal(t, "1.4.0", tasks[0].PlatformVersion)
	require.Len(t, tasks[0].Attachments, 1)
	assert.Equal(t, "ElasticNetworkInterface", tasks[0].Attachments[0].Type)

	var sawSubnet bool

	for _, d := range tasks[0].Attachments[0].Details {
		if d.Name == "subnetId" && d.Value == "subnet-123" {
			sawSubnet = true
		}
	}

	assert.True(t, sawSubnet, "ENI attachment should carry the requested subnet")
}

func TestRunTaskFargatePlatformVersionEcho(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, PlatformVersion: "1.3.0",
		NetworkConfiguration: &driver.NetworkConfiguration{
			AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "1.3.0", tasks[0].PlatformVersion)
}

func TestRunTaskFargateRequiresNetworkConfig(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	// A Fargate/awsvpc launch without networkConfiguration is a synchronous error.
	_, _, err = m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}

func TestRegisterTaskDefinitionValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// Empty containerDefinitions is a ClientException.
	_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{Family: "empty"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, excClient, ecsException(t, err))

	// A FARGATE-compatible task def missing cpu/memory is a ClientException.
	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, excClient, ecsException(t, err))

	// A FARGATE-compatible task def with the wrong network mode is a ClientException.
	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             "bridge",
		RequiresCompatibilities: []string{launchFargate},
	})
	require.Error(t, err)
	assert.Equal(t, excClient, ecsException(t, err))
}

func TestRunTaskExternalRunsUnplaced(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 256, 512)

	// EXTERNAL with no external instance runs the task unplaced (no failure).
	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "web", LaunchType: launchExternal,
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)
	assert.Equal(t, statusRunning, tasks[0].LastStatus)
	assert.Empty(t, tasks[0].ContainerInstanceARN)
}
