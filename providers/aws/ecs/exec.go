package ecs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/container/containerengine"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// execNotEnabledMessage is the text AWS returns when ECS Exec was not enabled
// for the task or its agent is not running.
const execNotEnabledMessage = "The execute command failed because execute command was not enabled when the task was run " +
	"or the execute command agent isn't running. Wait and try again or run a new task with execute command enabled and try again."

// ExecuteCommand resolves a running task and returns a synthetic SSM session for
// the target container. The task must belong to the cluster, have been started
// with enableExecuteCommand, be RUNNING, and the call must be interactive and
// name an existing container (optional only for a single-container task); every
// other case is an InvalidParameterException. When the task is engine-backed
// the requested command is actually run inside the container via the configured
// ContainerEngine (its output travels the SSM data channel in real ECS, so it
// is not carried in this response).
func (m *Mock) ExecuteCommand(ctx context.Context, in driver.ExecuteCommandInput) (*driver.ExecuteCommandResult, error) {
	want := resolveClusterName(in.Cluster)
	if !m.clusterExists(want) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	stored, ok := m.resolveTask(in.Task)
	if !ok || clusterNameFromARN(stored.ClusterARN) != want {
		return nil, apiErrf(errors.NotFound, excInvalidParameter, "task %q not found", in.Task)
	}

	t := m.observedTask(stored)

	if err := validateExec(&t, &in); err != nil {
		return nil, err
	}

	container, err := execContainer(&t, in.Container)
	if err != nil {
		return nil, err
	}

	if handle, backed := m.taskHandle(t.ARN); backed {
		if _, err := containerengine.Exec(ctx, m.opts.ContainerEngine, handle, container.Name, execCommand(in.Command)); err != nil {
			return nil, apiErrf(errors.Internal, excInvalidParameter, "execute-command failed: %v", err)
		}
	}

	region := arnRegion(t.ClusterARN, m.opts.Region)
	sessionID := "ecs-execute-command-" + m.hexID()
	streamURL := "wss://ssmmessages." + region + ".amazonaws.com/v1/data-channel/" + sessionID + "?role=publish_subscribe"

	return &driver.ExecuteCommandResult{
		ClusterARN:    t.ClusterARN,
		ContainerARN:  container.ARN,
		ContainerName: container.Name,
		TaskARN:       t.ARN,
		Interactive:   in.Interactive,
		Session: driver.Session{
			SessionID:  sessionID,
			StreamURL:  streamURL,
			TokenValue: m.hexID() + m.hexID(),
		},
	}, nil
}

// validateExec checks the task-level preconditions of an ExecuteCommand call.
func validateExec(t *driver.Task, in *driver.ExecuteCommandInput) error {
	if !t.EnableExecuteCommand {
		return apiErrf(errors.InvalidArgument, excInvalidParameter, "%s", execNotEnabledMessage)
	}

	if t.LastStatus != statusRunning {
		return apiErrf(errors.FailedPrecondition, excInvalidParameter,
			"The execute command failed because the task is in %s state; it must be RUNNING.", t.LastStatus)
	}

	if !in.Interactive {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"The interactive parameter must be set to true for execute-command.")
	}

	return nil
}

// execContainer picks the container an ExecuteCommand call targets: the named
// one, or the only one when the call names none.
func execContainer(t *driver.Task, name string) (*driver.Container, error) {
	if name == "" {
		if len(t.Containers) != 1 {
			return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
				"The task has %d containers; specify the container to run the command in.", len(t.Containers))
		}

		return &t.Containers[0], nil
	}

	for i := range t.Containers {
		if t.Containers[i].Name == name {
			return &t.Containers[i], nil
		}
	}

	return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
		"The container %q does not exist in the task.", name)
}

// execCommand splits an execute-command command string into the argv the
// ContainerEngine expects. An empty command yields a nil argv.
func execCommand(cmd string) []string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil
	}

	return fields
}

// trailingID returns the last "/"-delimited segment of an ARN (the task id).
func trailingID(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			return arn[i+1:]
		}
	}

	return arn
}
