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

// runExecution drives exec's containers to a terminal state, via the configured
// ContainerEngine when one is wired, or synthetically otherwise.
func (m *Mock) runExecution(ctx context.Context, exec *driver.Execution, containers []driver.Container) {
	if m.opts.ContainerEngine == nil {
		markSynthetic(exec)

		return
	}

	spec := config.ContainerRunSpec{
		Name:            exec.Name,
		Containers:      engineContainers(containers),
		RunToCompletion: true,
	}

	handle, statuses, err := containerengine.Run(ctx, m.opts.ContainerEngine, spec)
	if err != nil {
		markFailed(exec, "container run failed: "+err.Error(), nil)

		return
	}

	if handle == "" { // engine present but no-op (e.g. nothing to run)
		markSynthetic(exec)

		return
	}

	m.recordHandle(exec.JobName, handle)
	m.reflectStatuses(ctx, exec, handle, statuses)
}

// engineContainers maps a job's containers onto the engine's neutral spec. The
// engine models a single command list, so Cloud Run's command (entrypoint) and
// args are concatenated, entrypoint first.
func engineContainers(in []driver.Container) []config.ContainerSpec {
	out := make([]config.ContainerSpec, 0, len(in))

	for i := range in {
		cmd := append(append([]string(nil), in[i].Command...), in[i].Args...)
		out = append(out, config.ContainerSpec{
			Name:    in[i].Name,
			Image:   in[i].Image,
			Command: cmd,
			Env:     in[i].Env,
		})
	}

	return out
}

// reflectStatuses turns the engine's observed per-container statuses into the
// execution's task outcome and counts. A task succeeds only when every one of
// its containers exited 0; the highest exit code observed is the task exit code.
func (m *Mock) reflectStatuses(
	ctx context.Context, exec *driver.Execution, handle string, statuses []config.ContainerStatus,
) {
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
		markSucceeded(exec, cstats)

		return
	}

	markFailed(exec, m.failureMessage(ctx, handle, statuses, maxExit), cstats)
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

// markFailed records exec as failed across all its tasks with the given message.
func markFailed(exec *driver.Execution, msg string, cstats []driver.ContainerStatus) {
	exit := 1

	for _, c := range cstats {
		if c.ExitCode > exit {
			exit = c.ExitCode
		}
	}

	exec.Tasks = buildTasks(exec.TaskCount, taskFailed, exit, cstats)
	exec.FailedCount = exec.TaskCount
	exec.Conditions = []driver.Condition{{
		Type: condCompleted, State: stateFailed, Reason: "Failed", Message: msg,
	}}
	exec.LogURI = logURI(exec)
}

// buildTasks materializes count identical tasks reflecting the execution's
// observed outcome. CloudEmu runs the job's containers once per execution; the
// observed result stands for each of the TaskCount tasks.
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
