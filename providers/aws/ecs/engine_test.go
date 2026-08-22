package ecs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// fakeContainerEngine is an in-process config.ContainerEngine that records calls
// and returns canned results, so the ECS provider wiring can be exercised
// without Docker.
type fakeContainerEngine struct {
	ranSpec   config.ContainerRunSpec
	handle    string
	statuses  []config.ContainerStatus
	runErr    error
	logs      map[string]string
	execCalls []fakeExec
	stopped   []string
}

type fakeExec struct {
	container string
	cmd       []string
}

//nolint:gocritic // spec is the by-value DTO defined by the ContainerEngine contract.
func (f *fakeContainerEngine) Run(_ context.Context, spec config.ContainerRunSpec) (string, error) {
	f.ranSpec = spec

	return f.handle, f.runErr
}

func (f *fakeContainerEngine) Status(_ context.Context, _ string) ([]config.ContainerStatus, error) {
	return f.statuses, nil
}

func (f *fakeContainerEngine) Logs(_ context.Context, _, container string, _ int) (string, error) {
	return f.logs[container], nil
}

func (f *fakeContainerEngine) Exec(_ context.Context, _, container string, cmd []string) (config.ExecResult, error) {
	f.execCalls = append(f.execCalls, fakeExec{container: container, cmd: cmd})

	return config.ExecResult{Stdout: "ok"}, nil
}

func (f *fakeContainerEngine) Stop(_ context.Context, handle string) error {
	f.stopped = append(f.stopped, handle)

	return nil
}

func newEngineTestMock(eng config.ContainerEngine) *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithContainerEngine(eng)))
}

// registerAndRun registers a single-container task definition and runs one task
// against the default cluster, returning the launched task.
func registerAndRun(t *testing.T, m *Mock, cd driver.ContainerDefinition) driver.Task {
	t.Helper()

	ctx := context.Background()

	_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{cd},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		TaskDefinition: "web", LaunchType: "EXTERNAL", Count: 1,
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)

	return tasks[0]
}

func TestRunTaskEngineBackedPopulatesStatus(t *testing.T) {
	eng := &fakeContainerEngine{
		handle:   "h-123",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited", ExitCode: 7}},
	}
	m := newEngineTestMock(eng)

	task := registerAndRun(t, m, driver.ContainerDefinition{
		Name: "app", Image: "busybox:latest", Command: []string{"echo", "hi"},
		Environment: []driver.KeyValue{{Name: "K", Value: "V"}},
	})

	// The engine received the task's containers, run to completion for RunTask.
	assert.True(t, eng.ranSpec.RunToCompletion)
	require.Len(t, eng.ranSpec.Containers, 1)
	assert.Equal(t, "busybox:latest", eng.ranSpec.Containers[0].Image)
	assert.Equal(t, []string{"echo", "hi"}, eng.ranSpec.Containers[0].Command)
	assert.Equal(t, map[string]string{"K": "V"}, eng.ranSpec.Containers[0].Env)

	// The observed exit code / status / runtime id are reflected onto the task.
	require.Len(t, task.Containers, 1)
	assert.Equal(t, 7, task.Containers[0].ExitCode)
	assert.Equal(t, statusStopped, task.Containers[0].LastStatus)
	assert.Equal(t, "h-123", task.Containers[0].RuntimeID)

	// A run-to-completion task whose containers all exited rolls up to STOPPED.
	assert.Equal(t, statusStopped, task.LastStatus)
	assert.Equal(t, "EssentialContainerExited", task.StopCode)
}

// registerAndRunMulti registers a two-container task definition (main essential,
// sidecar non-essential) and runs one task, returning it.
func registerAndRunMulti(t *testing.T, m *Mock, mainEssential, sidecarEssential bool) driver.Task {
	t.Helper()

	ctx := context.Background()

	_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family: "web",
		ContainerDefinitions: []driver.ContainerDefinition{
			{Name: "main", Image: "app:latest", Essential: mainEssential},
			{Name: "sidecar", Image: "log:latest", Essential: sidecarEssential},
		},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		TaskDefinition: "web", LaunchType: "EXTERNAL", Count: 1,
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)

	return tasks[0]
}

func TestEssentialContainerExitStopsTask(t *testing.T) {
	eng := &fakeContainerEngine{
		handle: "h-ess",
		statuses: []config.ContainerStatus{
			{Name: "main", State: "exited", ExitCode: 0},
			{Name: "sidecar", State: "running"},
		},
	}
	m := newEngineTestMock(eng)

	task := registerAndRunMulti(t, m, true, false)

	// The essential container exited → the task is STOPPED even though the
	// non-essential sidecar is still running, and every container is marked STOPPED.
	assert.Equal(t, statusStopped, task.LastStatus)
	assert.Equal(t, statusStopped, task.DesiredStatus)
	assert.Equal(t, "EssentialContainerExited", task.StopCode)

	for _, c := range task.Containers {
		assert.Equal(t, statusStopped, c.LastStatus)
	}
}

func TestEssentialExitReapsEngineWorkload(t *testing.T) {
	eng := &fakeContainerEngine{
		handle: "h-reap",
		statuses: []config.ContainerStatus{
			{Name: "main", State: "exited", ExitCode: 0},
			{Name: "sidecar", State: "running"},
		},
	}
	m := newEngineTestMock(eng)

	task := registerAndRunMulti(t, m, true, false)

	// The task is terminal: the essential main exited while the non-essential
	// sidecar was still sleeping.
	assert.Equal(t, statusStopped, task.LastStatus)
	assert.Equal(t, "EssentialContainerExited", task.StopCode)

	// The engine workload was torn down immediately — no container lingers.
	assert.Equal(t, []string{"h-reap"}, eng.stopped)

	// The handle is dropped, so a later StopTask does not re-stop the workload.
	_, err := m.StopTask(context.Background(), "", task.ARN, "bye")
	require.NoError(t, err)
	assert.Equal(t, []string{"h-reap"}, eng.stopped)
}

func TestNonEssentialContainerExitKeepsTaskRunning(t *testing.T) {
	eng := &fakeContainerEngine{
		handle: "h-non",
		statuses: []config.ContainerStatus{
			{Name: "main", State: "running"},
			{Name: "sidecar", State: "exited", ExitCode: 0},
		},
	}
	m := newEngineTestMock(eng)

	task := registerAndRunMulti(t, m, true, false)

	// A non-essential container exiting does not stop the task, and a genuinely
	// running task must keep its engine workload (no reap).
	assert.Equal(t, statusRunning, task.LastStatus)
	assert.NotEqual(t, "EssentialContainerExited", task.StopCode)
	assert.Empty(t, eng.stopped)
}

func TestRunTaskEngineRunFailureStopsTask(t *testing.T) {
	eng := &fakeContainerEngine{runErr: errors.New("image pull failed")}
	m := newEngineTestMock(eng)

	task := registerAndRun(t, m, driver.ContainerDefinition{Name: "app", Image: "bad"})

	assert.Equal(t, statusStopped, task.LastStatus)
	assert.Equal(t, "TaskFailedToStart", task.StopCode)
	require.Len(t, task.Containers, 1)
	assert.Equal(t, "image pull failed", task.Containers[0].Reason)
}

func TestStopTaskStopsEngineWorkload(t *testing.T) {
	eng := &fakeContainerEngine{
		handle:   "h-stop",
		statuses: []config.ContainerStatus{{Name: "app", State: "running"}},
	}
	m := newEngineTestMock(eng)
	ctx := context.Background()

	task := registerAndRun(t, m, driver.ContainerDefinition{Name: "app", Image: "img"})

	_, err := m.StopTask(ctx, "", task.ARN, "bye")
	require.NoError(t, err)
	assert.Equal(t, []string{"h-stop"}, eng.stopped)

	// A repeated StopTask does not stop the engine again (handle dropped).
	_, err = m.StopTask(ctx, "", task.ARN, "bye")
	require.NoError(t, err)
	assert.Len(t, eng.stopped, 1)
}

func TestExecuteCommandExecsEngine(t *testing.T) {
	eng := &fakeContainerEngine{
		handle:   "h-exec",
		statuses: []config.ContainerStatus{{Name: "app", State: "running"}},
	}
	m := newEngineTestMock(eng)
	ctx := context.Background()

	task := registerAndRun(t, m, driver.ContainerDefinition{Name: "app", Image: "img"})

	res, err := m.ExecuteCommand(ctx, driver.ExecuteCommandInput{
		Task: task.ARN, Container: "app", Command: "ls -la /tmp", Interactive: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "app", res.ContainerName)
	require.Len(t, eng.execCalls, 1)
	assert.Equal(t, "app", eng.execCalls[0].container)
	assert.Equal(t, []string{"ls", "-la", "/tmp"}, eng.execCalls[0].cmd)
}

func TestNilEngineLeavesTaskSynthetic(t *testing.T) {
	m := newTestMock()

	task := registerAndRun(t, m, driver.ContainerDefinition{Name: "app", Image: "img"})

	require.Len(t, task.Containers, 1)
	assert.Equal(t, statusRunning, task.Containers[0].LastStatus)
	assert.Equal(t, 0, task.Containers[0].ExitCode)
	assert.Empty(t, task.Containers[0].RuntimeID)
	assert.Equal(t, statusRunning, task.LastStatus)

	// ExecuteCommand stays purely synthetic with no engine wired.
	res, err := m.ExecuteCommand(context.Background(), driver.ExecuteCommandInput{Task: task.ARN})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Session.SessionID)
}

func TestAwslogsSurfacedToCloudWatch(t *testing.T) {
	eng := &fakeContainerEngine{
		handle:   "h-logs",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited"}},
		logs:     map[string]string{"app": "line one\nline two\n"},
	}
	m := newEngineTestMock(eng)
	ctx := context.Background()

	cwl := cloudwatchlogs.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithRegion("us-east-1"),
	))
	m.SetLogSink(cwl)

	task := registerAndRun(t, m, driver.ContainerDefinition{
		Name:  "app",
		Image: "img",
		LogConfiguration: &driver.LogConfiguration{
			LogDriver: "awslogs",
			Options: map[string]string{
				"awslogs-group":         "/ecs/web",
				"awslogs-stream-prefix": "ecs",
			},
		},
	})

	stream := "ecs/app/" + trailingID(task.ARN)
	events, err := cwl.GetLogEvents(ctx, &logdriver.LogQueryInput{LogGroup: "/ecs/web", LogStream: stream})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "line one", events[0].Message)
	assert.Equal(t, "line two", events[1].Message)
}
