package ssm

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// InstanceResolver is the slice of the compute mock this package needs to
// check that a Run Command target exists.
type InstanceResolver interface {
	DescribeInstances(ctx context.Context, instanceIDs []string,
		filters []computedriver.DescribeFilter, opts ...computedriver.DescribeInstancesOptions) ([]computedriver.Instance, error)
}

// SetInstanceResolver wires the compute mock in so SendCommand can reject a
// target that does not exist. Without it, targets are not validated.
func (m *Mock) SetInstanceResolver(r InstanceResolver) {
	m.instanceResolver = r
}

// SendCommand records a Run Command send and returns its command id.
//
// Nothing executes: an emulated instance has no guest operating system. The
// invocation is recorded as successful so a caller's send/poll loop runs to
// completion, but the script itself is never validated — see driver.RunCommand.
func (m *Mock) SendCommand(ctx context.Context, cfg driver.CommandConfig) (string, error) {
	if len(cfg.InstanceIDs) == 0 {
		return "", errors.New(errors.InvalidArgument, "at least one instance id is required")
	}

	if cfg.DocumentName == "" {
		return "", errors.New(errors.InvalidArgument, "DocumentName is required")
	}

	// Real SSM answers InvalidInstanceId when a target is not a managed
	// instance, and that is the single most common Run Command failure during
	// bring-up. Accepting any id hides it until the caller runs for real.
	if err := m.checkTargets(ctx, cfg.InstanceIDs); err != nil {
		return "", err
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

// checkTargets rejects instance ids the compute mock does not know.
func (m *Mock) checkTargets(ctx context.Context, instanceIDs []string) error {
	if m.instanceResolver == nil {
		return nil
	}

	// SSM Run Command is an internal/system caller: a managed (service-owned)
	// instance is a valid target even when the account hides managed resources
	// from the public Describe API. Opt in so hiding doesn't spuriously report
	// InvalidInstanceId for a real, running instance.
	found, err := m.instanceResolver.DescribeInstances(ctx, instanceIDs, nil,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
	if err != nil {
		return errors.Newf(errors.NotFound,
			"InvalidInstanceId: %v", err)
	}

	known := make(map[string]bool, len(found))
	for i := range found {
		known[found[i].ID] = true
	}

	for _, id := range instanceIDs {
		if !known[id] {
			return errors.Newf(errors.NotFound,
				"InvalidInstanceId: instance %q is not a managed instance", id)
		}
	}

	return nil
}

func commandKey(commandID, instanceID string) string {
	return commandID + "|" + instanceID
}
