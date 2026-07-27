package ssm

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// SendCommand records a Run Command send and returns its command id.
//
// Nothing executes: an emulated instance has no guest operating system. The
// invocation is recorded as successful so a caller's send/poll loop runs to
// completion, but the script itself is never validated — see driver.RunCommand.
func (m *Mock) SendCommand(_ context.Context, cfg driver.CommandConfig) (string, error) {
	if len(cfg.InstanceIDs) == 0 {
		return "", errors.New(errors.InvalidArgument, "at least one instance id is required")
	}

	if cfg.DocumentName == "" {
		return "", errors.New(errors.InvalidArgument, "DocumentName is required")
	}

	commandID := idgen.GenerateID("")

	for _, instanceID := range cfg.InstanceIDs {
		m.commands.Set(commandKey(commandID, instanceID), driver.CommandInvocation{
			CommandID:    commandID,
			InstanceID:   instanceID,
			DocumentName: cfg.DocumentName,
			Status:       "Success",
			ResponseCode: 0,
		})
	}

	return commandID, nil
}

// GetCommandInvocation returns the recorded invocation for one instance.
//
// An unknown pair is InvocationDoesNotExist rather than a fabricated success:
// a caller polling a command it never sent has a real bug, and answering
// "Success" would bury it.
func (m *Mock) GetCommandInvocation(
	_ context.Context, commandID, instanceID string,
) (*driver.CommandInvocation, error) {
	inv, ok := m.commands.Get(commandKey(commandID, instanceID))
	if !ok {
		return nil, errors.Newf(errors.NotFound,
			"InvocationDoesNotExist: no invocation of command %q on instance %q",
			commandID, instanceID)
	}

	return &inv, nil
}

func commandKey(commandID, instanceID string) string {
	return commandID + "|" + instanceID
}
