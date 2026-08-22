package ecs

import (
	"context"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/container/containerengine"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

const (
	// logDriverAwslogs is the ECS awslogs log driver that ships container output
	// to CloudWatch Logs.
	logDriverAwslogs   = "awslogs"
	optAwslogsGroup    = "awslogs-group"
	optAwslogsPrefix   = "awslogs-stream-prefix"
	engineStateExited  = "exited"
	engineStateRunning = "running"
)

// backTaskWithEngine runs the task's containers on the configured
// ContainerEngine (when one is wired) and reflects the observed per-container
// state — LastStatus, ExitCode, RuntimeID — back onto the task. It records the
// engine handle so StopTask/ExecuteCommand can reach the workload later, and
// surfaces any awslogs-configured container output into CloudWatch Logs. When no
// engine is configured it is a no-op and the task keeps its synthetic RUNNING
// containers.
func (m *Mock) backTaskWithEngine(ctx context.Context, task *driver.Task, spec *taskSpec) {
	if m.opts.ContainerEngine == nil {
		return
	}

	runSpec := config.ContainerRunSpec{
		Name:            trailingID(task.ARN),
		Containers:      engineContainers(spec.td),
		RunToCompletion: spec.runToCompletion,
	}

	handle, statuses, err := containerengine.Run(ctx, m.opts.ContainerEngine, runSpec)
	if err != nil {
		markEngineFailure(task, err)

		return
	}

	if handle == "" {
		return
	}

	m.engineHandles.Set(task.ARN, handle)
	terminal := applyStatuses(task, spec.td, handle, statuses)
	m.surfaceLogs(ctx, task, spec.td, handle)

	// A standalone RunToCompletion task that reached its terminal state (an
	// essential container exited) is torn down at once, just as real ECS kills
	// the remaining containers the instant the essential one exits — so no
	// sidecar or exited container lingers until the engine's Close(). Reaping
	// after surfaceLogs keeps the captured output intact.
	if terminal {
		m.reapEngine(ctx, task.ARN)
	}
}

// reapEngine stops the engine-backed workload behind a task and drops its
// handle, leaving zero running or exited containers. Dropping the handle makes a
// later StopTask a no-op, so a double stop is safe.
func (m *Mock) reapEngine(ctx context.Context, taskARN string) {
	handle, backed := m.engineHandles.Get(taskARN)
	if !backed {
		return
	}

	_ = containerengine.Stop(ctx, m.opts.ContainerEngine, handle)
	m.engineHandles.Delete(taskARN)
}

// engineContainers maps a task definition's container definitions onto the
// engine's neutral container spec.
func engineContainers(td *driver.TaskDefinition) []config.ContainerSpec {
	out := make([]config.ContainerSpec, 0, len(td.ContainerDefinitions))

	for i := range td.ContainerDefinitions {
		cd := &td.ContainerDefinitions[i]
		out = append(out, config.ContainerSpec{
			Name:    cd.Name,
			Image:   cd.Image,
			Command: append([]string(nil), cd.Command...),
			Env:     keyValueEnv(cd.Environment),
		})
	}

	return out
}

// keyValueEnv flattens a container definition's environment entries into the
// engine's env map.
func keyValueEnv(env []driver.KeyValue) map[string]string {
	if len(env) == 0 {
		return nil
	}

	out := make(map[string]string, len(env))
	for _, kv := range env {
		out[kv.Name] = kv.Value
	}

	return out
}

// applyStatuses reflects the engine's observed per-container status onto the
// task's containers (matched by name) and rolls the task-level LastStatus up.
// Real ECS stops a task the moment an *essential* container exits (and kills the
// rest), so the task is STOPPED as soon as any essential container has exited,
// regardless of whether non-essential containers are still running.
// It returns whether the task reached its terminal STOPPED state (an essential
// container exited), so the caller can reap the engine workload.
func applyStatuses(task *driver.Task, td *driver.TaskDefinition, handle string, statuses []config.ContainerStatus) bool {
	byName := make(map[string]config.ContainerStatus, len(statuses))
	for _, s := range statuses {
		byName[s.Name] = s
	}

	essential := essentialContainers(td)
	essentialExited := false

	for i := range task.Containers {
		s, ok := byName[task.Containers[i].Name]
		if !ok {
			continue
		}

		task.Containers[i].LastStatus = ecsStatusFromEngine(s.State)
		task.Containers[i].ExitCode = s.ExitCode
		task.Containers[i].RuntimeID = handle

		if s.State == engineStateExited && essential[task.Containers[i].Name] {
			essentialExited = true
		}
	}

	if essentialExited {
		stopTaskOnEssentialExit(task)
	}

	return essentialExited
}

// stopTaskOnEssentialExit flips the task (and every container, since real ECS
// kills the remaining containers) to STOPPED with the essential-exit reason.
func stopTaskOnEssentialExit(task *driver.Task) {
	task.LastStatus = statusStopped
	task.DesiredStatus = statusStopped
	task.StoppedReason = "Essential container in task exited"
	task.StopCode = "EssentialContainerExited"

	for i := range task.Containers {
		task.Containers[i].LastStatus = statusStopped
	}
}

// essentialContainers returns the set of container names ECS treats as
// essential. Containers explicitly flagged Essential form that set; when no
// container is flagged, real ECS treats every container as essential, so the
// full set is returned.
func essentialContainers(td *driver.TaskDefinition) map[string]bool {
	out := make(map[string]bool, len(td.ContainerDefinitions))

	anyEssential := false

	for i := range td.ContainerDefinitions {
		if td.ContainerDefinitions[i].Essential {
			out[td.ContainerDefinitions[i].Name] = true
			anyEssential = true
		}
	}

	if !anyEssential {
		for i := range td.ContainerDefinitions {
			out[td.ContainerDefinitions[i].Name] = true
		}
	}

	return out
}

// ecsStatusFromEngine maps an engine container state onto the ECS lastStatus
// vocabulary.
func ecsStatusFromEngine(state string) string {
	switch state {
	case engineStateRunning:
		return statusRunning
	case engineStateExited:
		return statusStopped
	default:
		return strings.ToUpper(state)
	}
}

// markEngineFailure records an engine Run failure on the task: the task is
// STOPPED and every container carries the failure reason, mirroring a task that
// could not start its containers.
func markEngineFailure(task *driver.Task, err error) {
	task.LastStatus = statusStopped
	task.DesiredStatus = statusStopped
	task.StoppedReason = err.Error()
	task.StopCode = "TaskFailedToStart"

	for i := range task.Containers {
		task.Containers[i].LastStatus = statusStopped
		task.Containers[i].Reason = err.Error()
	}
}

// surfaceLogs pushes each awslogs-configured container's captured engine output
// into CloudWatch Logs under the configured group and a
// "<prefix>/<container>/<taskId>" stream, mirroring the ECS awslogs driver. It
// is best-effort: a missing log sink, driver, group, or empty output skips the
// container.
func (m *Mock) surfaceLogs(ctx context.Context, task *driver.Task, td *driver.TaskDefinition, handle string) {
	if m.logs == nil {
		return
	}

	taskID := trailingID(task.ARN)

	for i := range td.ContainerDefinitions {
		cd := &td.ContainerDefinitions[i]

		lc := cd.LogConfiguration
		if lc == nil || lc.LogDriver != logDriverAwslogs {
			continue
		}

		group := lc.Options[optAwslogsGroup]
		if group == "" {
			continue
		}

		text, err := containerengine.Logs(ctx, m.opts.ContainerEngine, handle, cd.Name, 0)
		if err != nil || text == "" {
			continue
		}

		m.pushAwslogs(ctx, group, awslogsStream(lc.Options[optAwslogsPrefix], cd.Name, taskID), text)
	}
}

// pushAwslogs ensures the group/stream exist and writes the captured output as
// one CloudWatch Logs event per line. Group/stream creation errors (e.g. the
// group already exists) are ignored so surfacing stays best-effort.
func (m *Mock) pushAwslogs(ctx context.Context, group, stream, text string) {
	_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: group})
	_, _ = m.logs.CreateLogStream(ctx, group, stream)

	events := logEvents(text, m.opts.Clock.Now())
	if len(events) == 0 {
		return
	}

	_ = m.logs.PutLogEvents(ctx, group, stream, events)
}

// awslogsStream builds the CloudWatch Logs stream name ECS uses for an awslogs
// container: "<prefix>/<container>/<taskId>", dropping an empty prefix.
func awslogsStream(prefix, container, taskID string) string {
	if prefix == "" {
		return container + "/" + taskID
	}

	return prefix + "/" + container + "/" + taskID
}

// logEvents splits captured container output into one log event per non-empty
// line, all stamped at the given time.
func logEvents(text string, now time.Time) []logdriver.LogEvent {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	events := make([]logdriver.LogEvent, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		events = append(events, logdriver.LogEvent{Timestamp: now, Message: line})
	}

	return events
}

// taskHandle returns the engine handle backing a task, if the task is
// engine-backed.
func (m *Mock) taskHandle(taskARN string) (string, bool) {
	return m.engineHandles.Get(taskARN)
}
