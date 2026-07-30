package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// ExecuteCommand resolves a running task and returns a synthetic SSM session for
// the target container. An unresolved task surfaces an InvalidParameterException.
func (m *Mock) ExecuteCommand(_ context.Context, in driver.ExecuteCommandInput) (*driver.ExecuteCommandResult, error) {
	t, ok := m.resolveTask(in.Task)
	if !ok {
		return nil, apiErrf(errors.NotFound, excInvalidParameter, "task %q not found", in.Task)
	}

	containerName := in.Container
	if containerName == "" && len(t.Containers) > 0 {
		containerName = t.Containers[0].Name
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

// trailingID returns the last "/"-delimited segment of an ARN (the task id).
func trailingID(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			return arn[i+1:]
		}
	}

	return arn
}
