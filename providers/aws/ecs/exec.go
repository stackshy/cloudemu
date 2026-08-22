package ecs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/container/containerengine"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// ExecuteCommand resolves a running task and returns a synthetic SSM session for
// the target container. When the task is engine-backed the requested command is
// actually run inside the container via the configured ContainerEngine (its
// output travels the SSM data channel in real ECS, so it is not carried in this
// response). An unresolved task surfaces an InvalidParameterException.
func (m *Mock) ExecuteCommand(ctx context.Context, in driver.ExecuteCommandInput) (*driver.ExecuteCommandResult, error) {
	t, ok := m.resolveTask(in.Task)
	if !ok {
		return nil, apiErrf(errors.NotFound, excInvalidParameter, "task %q not found", in.Task)
	}

	containerName := in.Container
	if containerName == "" && len(t.Containers) > 0 {
		containerName = t.Containers[0].Name
	}

	if handle, backed := m.taskHandle(t.ARN); backed {
		if _, err := containerengine.Exec(ctx, m.opts.ContainerEngine, handle, containerName, execCommand(in.Command)); err != nil {
			return nil, apiErrf(errors.Internal, excInvalidParameter, "execute-command failed: %v", err)
		}
	}

	sessionID := "ecs-execute-command-" + m.hexID()
	streamURL := "wss://ssmmessages." + m.opts.Region + ".amazonaws.com/v1/data-channel/" + sessionID + "?role=publish_subscribe"
	containerARN := m.arn("container/" + clusterNameFromARN(t.ClusterARN) + "/" + trailingID(t.ARN) + "/" + idgen.GenerateID(""))

	return &driver.ExecuteCommandResult{
		ClusterARN:    t.ClusterARN,
		ContainerARN:  containerARN,
		ContainerName: containerName,
		TaskARN:       t.ARN,
		Interactive:   in.Interactive,
		Session: driver.Session{
			SessionID:  sessionID,
			StreamURL:  streamURL,
			TokenValue: m.hexID() + m.hexID(),
		},
	}, nil
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
