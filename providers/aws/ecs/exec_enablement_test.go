package ecs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

const wantExecNotEnabledMessage = "The execute command failed because execute command was not enabled when the task was run " +
	"or the execute command agent isn't running. Wait and try again or run a new task with execute command enabled and try again."

func runFargate(t *testing.T, m *Mock, netCfg *driver.NetworkConfiguration, enableExec bool) driver.Task {
	t.Helper()

	tasks, failures, err := m.RunTask(context.Background(), driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, EnableExecuteCommand: enableExec,
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)

	return tasks[0]
}

func TestExecuteCommand_NotEnabled(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	task := runFargate(t, m, netCfg, false)

	_, err := m.ExecuteCommand(context.Background(), driver.ExecuteCommandInput{
		Cluster: "prod", Task: task.ARN, Command: "ls", Interactive: true,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	var ce *errors.Error

	require.ErrorAs(t, err, &ce)
	assert.Equal(t, wantExecNotEnabledMessage, ce.Message)
}

func TestExecuteCommand_TaskNotRunning(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	task := runFargate(t, m, netCfg, true)

	_, err := m.StopTask(context.Background(), "prod", task.ARN, "bye")
	require.NoError(t, err)

	_, err = m.ExecuteCommand(context.Background(), driver.ExecuteCommandInput{
		Cluster: "prod", Task: task.ARN, Command: "ls", Interactive: true,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}

func TestExecuteCommand_ContainerAndInteractiveValidation(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	task := runFargate(t, m, netCfg, true)
	ctx := context.Background()

	_, err := m.ExecuteCommand(ctx, driver.ExecuteCommandInput{
		Cluster: "prod", Task: task.ARN, Container: "nope", Command: "ls", Interactive: true,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	_, err = m.ExecuteCommand(ctx, driver.ExecuteCommandInput{
		Cluster: "prod", Task: task.ARN, Command: "ls", Interactive: false,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	_, err = m.ExecuteCommand(ctx, driver.ExecuteCommandInput{
		Cluster: "ghost", Task: task.ARN, Command: "ls", Interactive: true,
	})
	require.Error(t, err)
	assert.Equal(t, excClusterNotFound, ecsException(t, err))
}

func TestExecuteCommand_EnabledRunning(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	task := runFargate(t, m, netCfg, true)

	res, err := m.ExecuteCommand(context.Background(), driver.ExecuteCommandInput{
		Cluster: "prod", Task: task.ARN, Command: "/bin/sh", Interactive: true,
	})
	require.NoError(t, err)
	assert.Equal(t, task.ARN, res.TaskARN)
	assert.Equal(t, "c", res.ContainerName)
	assert.Equal(t, task.Containers[0].ARN, res.ContainerARN, "the task's own container ARN, not a fresh one")
	assert.NotEmpty(t, res.Session.SessionID)
}

func TestEnableExecuteCommand_Propagation(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	ctx := context.Background()

	task := runFargate(t, m, netCfg, true)
	assert.True(t, task.EnableExecuteCommand)
	assert.False(t, runFargate(t, m, netCfg, false).EnableExecuteCommand)

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "on", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, DesiredCount: 1, EnableExecuteCommand: true,
	})
	require.NoError(t, err)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "off", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, DesiredCount: 1,
	})
	require.NoError(t, err)

	on, err := m.ListTasks(ctx, "prod", "", "", "on")
	require.NoError(t, err)
	require.Len(t, on, 1)
	assert.True(t, on[0].EnableExecuteCommand)

	off, err := m.ListTasks(ctx, "prod", "", "", "off")
	require.NoError(t, err)
	require.Len(t, off, 1)
	assert.False(t, off[0].EnableExecuteCommand)

	// Enabling on the service does not change running tasks; a new deployment does.
	enable := true
	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "off", Cluster: "prod", EnableExecuteCommand: &enable})
	require.NoError(t, err)

	off, err = m.ListTasks(ctx, "prod", "", "", "off")
	require.NoError(t, err)
	assert.False(t, off[0].EnableExecuteCommand, "existing task keeps its launch-time setting")

	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "off", Cluster: "prod", ForceNewDeployment: true})
	require.NoError(t, err)

	running, err := m.ListTasks(ctx, "prod", "", "RUNNING", "off")
	require.NoError(t, err)
	require.Len(t, running, 1)
	assert.True(t, running[0].EnableExecuteCommand)
}

func TestManagedAgents_ExecuteCommandAgent(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)

	on := runFargate(t, m, netCfg, true)
	require.Len(t, on.Containers[0].ManagedAgents, 1)
	assert.Equal(t, "ExecuteCommandAgent", on.Containers[0].ManagedAgents[0].Name)
	assert.Equal(t, statusRunning, on.Containers[0].ManagedAgents[0].LastStatus)
	assert.NotEmpty(t, on.Containers[0].ManagedAgents[0].LastStartedAt)

	off := runFargate(t, m, netCfg, false)
	assert.Empty(t, off.Containers[0].ManagedAgents)

	stopped, err := m.StopTask(context.Background(), "prod", on.ARN, "bye")
	require.NoError(t, err)
	assert.Equal(t, statusStopped, stopped.Containers[0].ManagedAgents[0].LastStatus)
}

func TestAwsvpcTask_ContainerNetworkInterfaces(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	task := runFargate(t, m, netCfg, false)

	require.Len(t, task.Attachments, 1)
	attachment := task.Attachments[0]
	require.NotEmpty(t, attachment.ID)

	var privateIP string

	for _, d := range attachment.Details {
		if d.Name == "privateIPv4Address" {
			privateIP = d.Value
		}
	}

	require.NotEmpty(t, privateIP)

	for _, c := range task.Containers {
		require.Len(t, c.NetworkInterfaces, 1)
		assert.Equal(t, attachment.ID, c.NetworkInterfaces[0].AttachmentID)
		assert.Equal(t, privateIP, c.NetworkInterfaces[0].PrivateIPv4Address)
	}
}

func TestBridgeTask_NoNetworkInterfaces(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 128, 128)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", LaunchType: launchExternal})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Empty(t, tasks[0].Containers[0].NetworkInterfaces)
}
