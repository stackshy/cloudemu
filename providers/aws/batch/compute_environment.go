package batch

import (
	"context"
	"encoding/json"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

const ceHealthyReason = "ComputeEnvironment Healthy"

//nolint:gocritic // clone intentionally takes a value to return an independent copy-on-write copy
func cloneComputeEnvironment(ce driver.ComputeEnvironment) driver.ComputeEnvironment {
	ce.ComputeResources = copyRaw(ce.ComputeResources)
	ce.Tags = copyTags(ce.Tags)
	ce.UnmanagedvCpus = copyInt32(ce.UnmanagedvCpus)

	return ce
}

// CreateComputeEnvironment creates a compute environment and provisions it
// synchronously: it is VALID immediately (real Batch transitions
// CREATING -> VALID asynchronously, which Terraform waits on).
//
//nolint:gocritic // in is the driver CreateComputeEnvironmentInput, taken by value to match the interface
func (m *Mock) CreateComputeEnvironment(
	_ context.Context, in driver.CreateComputeEnvironmentInput,
) (*driver.ComputeEnvironment, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "computeEnvironmentName is required")
	}

	if in.Type != driver.CETypeManaged && in.Type != driver.CETypeUnmanaged {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "type must be MANAGED or UNMANAGED, got %q", in.Type)
	}

	if in.Type == driver.CETypeManaged && len(in.ComputeResources) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "computeResources is required for a MANAGED compute environment")
	}

	state := in.State
	if state == "" {
		state = driver.StateEnabled
	}

	uuid := idgen.UUID()
	ce := driver.ComputeEnvironment{
		Name:             in.Name,
		ARN:              m.arn(kindComputeEnvironment, in.Name),
		EcsClusterARN:    m.ecsClusterARN(in.Name, uuid),
		Type:             in.Type,
		State:            state,
		Status:           driver.StatusValid,
		StatusReason:     ceHealthyReason,
		ComputeResources: copyRaw(in.ComputeResources),
		ServiceRole:      in.ServiceRole,
		UUID:             uuid,
		UnmanagedvCpus:   copyInt32(in.UnmanagedvCpus),
		Tags:             copyTags(in.Tags),
	}

	if !m.computeEnvs.SetIfAbsent(in.Name, ce) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "compute environment %q already exists", in.Name)
	}

	out := cloneComputeEnvironment(ce)

	return &out, nil
}

// DescribeComputeEnvironments returns the named compute environments (by name or
// ARN) in request order, or all of them (name order) when no filter is given.
func (m *Mock) DescribeComputeEnvironments(_ context.Context, names []string) ([]driver.ComputeEnvironment, error) {
	return describeByNames(m.computeEnvs, names, kindComputeEnvironment, cloneComputeEnvironment), nil
}

// UpdateComputeEnvironment applies an in-place change. computeResources members
// are shallow-merged over the stored resources so unchanged nested fields are
// preserved.
func (m *Mock) UpdateComputeEnvironment(
	_ context.Context, in driver.UpdateComputeEnvironmentInput,
) (*driver.ComputeEnvironment, error) {
	name := nameFromARN(kindComputeEnvironment, in.Name)

	var updated driver.ComputeEnvironment

	var mergeErr error

	ok := m.computeEnvs.Update(name, func(ce driver.ComputeEnvironment) driver.ComputeEnvironment {
		next := cloneComputeEnvironment(ce)
		if in.State != "" {
			next.State = in.State
		}

		if in.ServiceRole != "" {
			next.ServiceRole = in.ServiceRole
		}

		merged, err := mergeComputeResources(next.ComputeResources, in.ComputeResources)
		if err != nil {
			mergeErr = err

			return ce
		}

		next.ComputeResources = merged
		updated = cloneComputeEnvironment(next)

		return next
	})

	if mergeErr != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid computeResources: %v", mergeErr)
	}

	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "compute environment %q does not exist", name)
	}

	return &updated, nil
}

// DeleteComputeEnvironment removes a compute environment. It must be DISABLED
// and unreferenced by any job queue, matching real Batch (Terraform disables the
// environment and destroys dependent queues first).
func (m *Mock) DeleteComputeEnvironment(_ context.Context, name string) error {
	name = nameFromARN(kindComputeEnvironment, name)

	ce, ok := m.computeEnvs.Get(name)
	if !ok {
		return nil // delete is idempotent
	}

	if ce.State != driver.StateDisabled {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"cannot delete compute environment %q while it is %s; set its state to DISABLED first", name, ce.State)
	}

	if q := m.referencingQueue(name, ce.ARN); q != "" {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"cannot delete compute environment %q while job queue %q still references it", name, q)
	}

	m.computeEnvs.Delete(name)

	return nil
}

// referencingQueue returns the name of a job queue that still lists the given
// compute environment (by name or ARN) in its order, or "" if none does.
func (m *Mock) referencingQueue(name, arn string) string {
	queues := m.jobQueues.SortedValues()
	for i := range queues {
		for _, o := range queues[i].ComputeEnvironmentOrder {
			ref := nameFromARN(kindComputeEnvironment, o.ComputeEnvironment)
			if ref == name || o.ComputeEnvironment == arn {
				return queues[i].Name
			}
		}
	}

	return ""
}

// mergeComputeResources overlays the non-null top-level members of a
// ComputeResourceUpdate onto the stored computeResources, preserving fields the
// update did not mention.
func mergeComputeResources(base, update json.RawMessage) (json.RawMessage, error) {
	if len(update) == 0 {
		return base, nil
	}

	baseMap := map[string]json.RawMessage{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &baseMap); err != nil {
			return nil, fmt.Errorf("stored computeResources: %w", err)
		}
	}

	updMap := map[string]json.RawMessage{}
	if err := json.Unmarshal(update, &updMap); err != nil {
		return nil, fmt.Errorf("update computeResources: %w", err)
	}

	for k, v := range updMap {
		baseMap[k] = v
	}

	return json.Marshal(baseMap)
}
