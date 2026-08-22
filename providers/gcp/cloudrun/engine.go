package cloudrun

import (
	"context"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
	"github.com/stackshy/cloudemu/v2/services/container/containerengine"
)

const engineStateExited = "exited"

// RunJob creates one Execution of the named job and runs it to completion. When
// a ContainerEngine is wired the execution runs real containers and reflects
// their exit codes; otherwise it is a synthetic success.
func (m *Mock) RunJob(ctx context.Context, name string) (*driver.Execution, error) {
	jobName := lastSegment(name)

	m.mu.Lock()

	job, ok := m.jobs.Get(jobName)
	if !ok {
		m.mu.Unlock()

		return nil, cerrors.Newf(cerrors.NotFound, "job %q not found", jobName)
	}

	job.ExecutionCount++
	now := m.opts.Clock.Now()
	exec := &driver.Execution{
		Name:       jobName + "-" + newID(execSuffixBytes),
		JobName:    jobName,
		UID:        newID(uidBytes),
		Generation: 1,
		CreateTime: now,
		StartTime:  now,
		TaskCount:  job.TaskCount,
		Containers: cloneContainers(job.Containers),
	}
	containers := cloneContainers(job.Containers)

	m.mu.Unlock()

	// Run outside the lock: an engine-backed run blocks until the containers
	// exit (RunToCompletion), and must not hold the store mutex meanwhile.
	m.runExecution(ctx, exec, containers)

	m.mu.Lock()
	exec.CompletionTime = m.opts.Clock.Now()
	m.executions.Set(exec.Name, exec)
	m.mu.Unlock()

	return cloneExecution(exec), nil
}

// runExecution drives exec's tasks to a terminal state, via the configured
// ContainerEngine when one is wired, or synthetically otherwise. Cloud Run runs
// TaskCount tasks per execution; each task is a real, independent container run
// (with the per-task CLOUD_RUN_TASK_* env the real service injects), and the
// execution's counts aggregate the observed per-task outcomes.
func (m *Mock) runExecution(ctx context.Context, exec *driver.Execution, containers []driver.Container) {
	if m.opts.ContainerEngine == nil {
		markSynthetic(exec)

		return
	}

	tasks := make([]driver.Task, 0, exec.TaskCount)
	succeeded := 0
	failMsg := ""

	for idx := 0; idx < exec.TaskCount; idx++ {
		task, msg := m.runTask(ctx, exec, containers, idx)
		tasks = append(tasks, task)

		if task.State == taskSucceeded {
			succeeded++
		} else if failMsg == "" {
			failMsg = msg
		}
	}

	aggregateExecution(exec, tasks, succeeded, failMsg)
}

// runTask runs one task's containers to completion and returns its outcome and,
// on failure, a message describing why. taskIndex/taskCount are injected into the
// containers' environment as CLOUD_RUN_TASK_INDEX/CLOUD_RUN_TASK_COUNT.
func (m *Mock) runTask(
	ctx context.Context, exec *driver.Execution, containers []driver.Container, taskIndex int,
) (task driver.Task, failMsg string) {
	spec := config.ContainerRunSpec{
		Name:            exec.Name + "-" + strconv.Itoa(taskIndex),
		Containers:      engineContainers(containers, taskIndex, exec.TaskCount),
		RunToCompletion: true,
	}

	handle, statuses, err := containerengine.Run(ctx, m.opts.ContainerEngine, spec)
	if err != nil {
		msg := "container run failed: " + err.Error()

		return driver.Task{Index: taskIndex, State: taskFailed, ExitCode: 1}, msg
	}

	if handle == "" { // engine present but no-op (e.g. nothing to run)
		return driver.Task{Index: taskIndex, State: taskSucceeded}, ""
	}

	m.recordHandle(exec.JobName, handle)

	return m.taskOutcome(ctx, handle, statuses, taskIndex)
}

// taskOutcome turns one task's observed container statuses into its outcome. The
// task succeeds only when every container exited 0; the highest exit code
// observed is the task exit code (at least 1 on failure).
func (m *Mock) taskOutcome(
	ctx context.Context, handle string, statuses []config.ContainerStatus, taskIndex int,
) (task driver.Task, failMsg string) {
	cstats := make([]driver.ContainerStatus, 0, len(statuses))
	maxExit := 0
	allExited := true

	for _, s := range statuses {
		cstats = append(cstats, driver.ContainerStatus{Name: s.Name, State: s.State, ExitCode: s.ExitCode})

		if s.ExitCode > maxExit {
			maxExit = s.ExitCode
		}

		if s.State != engineStateExited {
			allExited = false
		}
	}

	if allExited && maxExit == 0 {
		return driver.Task{Index: taskIndex, State: taskSucceeded, Containers: cstats}, ""
	}

	exit := maxExit
	if exit == 0 {
		exit = 1
	}

	return driver.Task{Index: taskIndex, State: taskFailed, ExitCode: exit, Containers: cstats},
		m.failureMessage(ctx, handle, statuses, maxExit)
}

// aggregateExecution rolls the per-task outcomes up onto the execution: its task
// list, counts, terminal condition, and log URI. The execution succeeds only when
// every task succeeded.
func aggregateExecution(exec *driver.Execution, tasks []driver.Task, succeeded int, failMsg string) {
	exec.Tasks = tasks
	exec.SucceededCount = succeeded
	exec.FailedCount = len(tasks) - succeeded
	exec.LogURI = logURI(exec)

	if exec.FailedCount == 0 {
		exec.Conditions = []driver.Condition{{Type: condCompleted, State: stateSucceeded, Reason: "Completed"}}

		return
	}

	exec.Conditions = []driver.Condition{{Type: condCompleted, State: stateFailed, Reason: "Failed", Message: failMsg}}
}

// engineContainers maps a job's containers onto the engine's neutral spec. The
// engine models a single command list, so Cloud Run's command (entrypoint) and
// args are concatenated, entrypoint first. taskIndex/taskCount are overlaid onto
// each container's env as the CLOUD_RUN_TASK_* variables.
func engineContainers(in []driver.Container, taskIndex, taskCount int) []config.ContainerSpec {
	out := make([]config.ContainerSpec, 0, len(in))

	for i := range in {
		cmd := append(append([]string(nil), in[i].Command...), in[i].Args...)
		out = append(out, config.ContainerSpec{
			Name:    in[i].Name,
			Image:   in[i].Image,
			Command: cmd,
			Env:     taskEnv(in[i].Env, taskIndex, taskCount),
		})
	}

	return out
}

// taskEnv copies a container's env and overlays the CLOUD_RUN_TASK_INDEX and
// CLOUD_RUN_TASK_COUNT variables the real Cloud Run injects for each task.
func taskEnv(base map[string]string, taskIndex, taskCount int) map[string]string {
	const injectedVars = 2 // CLOUD_RUN_TASK_INDEX + CLOUD_RUN_TASK_COUNT

	env := make(map[string]string, len(base)+injectedVars)
	for k, v := range base {
		env[k] = v
	}

	env["CLOUD_RUN_TASK_INDEX"] = strconv.Itoa(taskIndex)
	env["CLOUD_RUN_TASK_COUNT"] = strconv.Itoa(taskCount)

	return env
}

// failureMessage builds a task-failure condition message, enriching it with the
// captured output of the containers that failed so a caller sees why.
func (m *Mock) failureMessage(
	ctx context.Context, handle string, statuses []config.ContainerStatus, maxExit int,
) string {
	var b strings.Builder

	b.WriteString("task failed with exit code ")
	b.WriteString(strconv.Itoa(maxExit))

	for _, s := range statuses {
		if s.State == engineStateExited && s.ExitCode == 0 {
			continue
		}

		logs, err := containerengine.Logs(ctx, m.opts.ContainerEngine, handle, s.Name, 0)
		if err != nil || logs == "" {
			continue
		}

		b.WriteString("\n[")
		b.WriteString(s.Name)
		b.WriteString("] ")
		b.WriteString(strings.TrimRight(logs, "\n"))
	}

	return b.String()
}

// markSucceeded records exec as fully succeeded: every task ran the containers
// to a clean exit.
func markSucceeded(exec *driver.Execution, cstats []driver.ContainerStatus) {
	exec.Tasks = buildTasks(exec.TaskCount, taskSucceeded, 0, cstats)
	exec.SucceededCount = exec.TaskCount
	exec.Conditions = []driver.Condition{{Type: condCompleted, State: stateSucceeded, Reason: "Completed"}}
	exec.LogURI = logURI(exec)
}

// markSynthetic records a clean success without any container statuses — the
// no-engine path, where the execution is a stub.
func markSynthetic(exec *driver.Execution) {
	markSucceeded(exec, nil)
}

// buildTasks materializes count identical succeeded tasks for the no-engine
// synthetic path, where there are no observed container statuses to aggregate.
func buildTasks(count int, state string, exitCode int, cstats []driver.ContainerStatus) []driver.Task {
	tasks := make([]driver.Task, 0, count)
	for i := 0; i < count; i++ {
		tasks = append(tasks, driver.Task{
			Index:      i,
			State:      state,
			ExitCode:   exitCode,
			Containers: append([]driver.ContainerStatus(nil), cstats...),
		})
	}

	return tasks
}

// recordHandle appends an engine handle to the job's handle list so DeleteJob
// can stop the containers later.
func (m *Mock) recordHandle(jobName, handle string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	handles, _ := m.engineHandles.Get(jobName)
	m.engineHandles.Set(jobName, append(handles, handle))
}

// stopHandles tears down each engine-backed workload; best-effort.
func (m *Mock) stopHandles(ctx context.Context, handles []string) {
	for _, h := range handles {
		_ = containerengine.Stop(ctx, m.opts.ContainerEngine, h)
	}
}

func logURI(exec *driver.Execution) string {
	return "https://console.cloud.google.com/run/jobs/executions/" + exec.Name + "/logs"
}
