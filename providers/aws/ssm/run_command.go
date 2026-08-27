package ssm

import (
	"context"
	"strings"

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
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) SendCommand(ctx context.Context, cfg driver.CommandConfig) (string, error) {
	// Real SSM accepts EITHER explicit InstanceIds OR tag/attribute Targets;
	// supplying neither is a ValidationException.
	if len(cfg.InstanceIDs) == 0 && len(cfg.Targets) == 0 {
		return "", errors.New(errors.InvalidArgument, "either instance IDs or targets must be specified")
	}

	if cfg.DocumentName == "" {
		return "", errors.New(errors.InvalidArgument, "DocumentName is required")
	}

	// Real SSM answers InvalidInstanceId when an explicitly listed target is not
	// a managed instance, and that is the single most common Run Command failure
	// during bring-up. Accepting any id hides it until the caller runs for real.
	if err := m.checkTargets(ctx, cfg.InstanceIDs); err != nil {
		return "", err
	}

	// Resolve tag/attribute Targets to concrete instance ids. Unlike an explicit
	// id, a Target that matches nothing is not an error — real SSM accepts the
	// command with a TargetCount of zero.
	resolved := m.resolveTargets(ctx, cfg.Targets)
	instanceIDs := dedupeStrings(append(append([]string{}, cfg.InstanceIDs...), resolved...))

	commandID := idgen.GenerateID("")

	for _, instanceID := range instanceIDs {
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

// resolveTargets maps SSM Targets to the instance ids they select. Multiple
// targets are AND-combined, matching real SSM. Resolution needs the compute
// mock; without it (or on a lookup error) no ids are resolved, which still
// yields an accepted command.
func (m *Mock) resolveTargets(ctx context.Context, targets []driver.CommandTarget) []string {
	if len(targets) == 0 || m.instanceResolver == nil {
		return nil
	}

	filters := make([]computedriver.DescribeFilter, 0, len(targets))

	for _, t := range targets {
		name, ok := targetFilterName(t.Key)
		if !ok {
			// A documented Run Command target key the emulator cannot resolve
			// (resource-groups:Name, resource-groups:ResourceTypeFilters, tag-key).
			// Targets are AND-combined, so an unresolvable one must select nothing.
			// Forwarding the raw key as an EC2 describe-filter name instead falls
			// into matchesTagFilter's default branch, which matches every instance
			// unconditionally — fanning the command out to the whole fleet.
			return nil
		}

		filters = append(filters, computedriver.DescribeFilter{
			Name: name, Values: t.Values,
		})
	}

	found, err := m.instanceResolver.DescribeInstances(ctx, nil, filters,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: true})
	if err != nil {
		return nil
	}

	ids := make([]string, 0, len(found))
	for i := range found {
		ids = append(ids, found[i].ID)
	}

	return ids
}

// targetFilterName maps a supported SSM Target Key to the equivalent EC2
// describe-filter name, reporting whether the key is one the emulator can
// resolve. Only the two Run Command keys the EC2 matcher actually understands
// are supported: the "InstanceIds" pseudo-key (selects by instance id) and
// "tag:<name>" (passes through unchanged). Other documented keys such as
// resource-groups:Name / resource-groups:ResourceTypeFilters, and the bare
// "tag-key" form, are reported unsupported so the caller can decline to forward
// them — the EC2 matcher would otherwise treat them as an unrestricted match.
func targetFilterName(key string) (string, bool) {
	switch {
	case key == "InstanceIds":
		return "instance-id", true
	case strings.HasPrefix(key, "tag:") && len(key) > len("tag:"):
		return key, true
	default:
		return "", false
	}
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if seen[s] {
			continue
		}

		seen[s] = true

		out = append(out, s)
	}

	return out
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
