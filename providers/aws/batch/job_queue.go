package batch

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

const jqHealthyReason = "JobQueue Healthy"

//nolint:gocritic // clone intentionally takes a value to return an independent copy-on-write copy
func cloneJobQueue(q driver.JobQueue) driver.JobQueue {
	q.Tags = copyTags(q.Tags)
	q.ComputeEnvironmentOrder = cloneOrder(q.ComputeEnvironmentOrder)

	return q
}

func cloneOrder(in []driver.ComputeEnvironmentOrder) []driver.ComputeEnvironmentOrder {
	if in == nil {
		return nil
	}

	out := make([]driver.ComputeEnvironmentOrder, len(in))
	copy(out, in)

	return out
}

// CreateJobQueue creates a job queue and provisions it synchronously (VALID
// immediately). Every referenced compute environment must exist and be VALID,
// matching real Batch.
//
//nolint:gocritic // in is the driver CreateJobQueueInput, taken by value to match the interface
func (m *Mock) CreateJobQueue(_ context.Context, in driver.CreateJobQueueInput) (*driver.JobQueue, error) {
	if in.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "jobQueueName is required")
	}

	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}

	if len(in.ComputeEnvironmentOrder) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "computeEnvironmentOrder must list at least one compute environment")
	}

	if err := m.validateOrder(in.ComputeEnvironmentOrder); err != nil {
		return nil, err
	}

	m.policyBindMu.Lock()
	defer m.policyBindMu.Unlock()

	policyARN := ""

	if in.SchedulingPolicyARN != "" {
		arn, err := m.resolveSchedulingPolicy(in.SchedulingPolicyARN)
		if err != nil {
			return nil, err
		}

		policyARN = arn
	}

	state := in.State
	if state == "" {
		state = driver.StateEnabled
	}

	q := driver.JobQueue{
		Name:                    in.Name,
		ARN:                     m.arn(kindJobQueue, in.Name),
		State:                   state,
		Status:                  driver.StatusValid,
		StatusReason:            jqHealthyReason,
		Priority:                in.Priority,
		ComputeEnvironmentOrder: cloneOrder(in.ComputeEnvironmentOrder),
		SchedulingPolicyARN:     policyARN,
		Tags:                    copyTags(in.Tags),
	}

	if !m.jobQueues.SetIfAbsent(in.Name, q) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "job queue %q already exists", in.Name)
	}

	out := cloneJobQueue(q)

	return &out, nil
}

// DescribeJobQueues returns the named job queues (by name or ARN) in request
// order, or all of them (name order) when no filter is given.
func (m *Mock) DescribeJobQueues(_ context.Context, names []string) ([]driver.JobQueue, error) {
	return describeByNames(m.jobQueues, names, kindJobQueue, cloneJobQueue), nil
}

// UpdateJobQueue applies an in-place change; nil fields are left unchanged.
func (m *Mock) UpdateJobQueue(_ context.Context, in driver.UpdateJobQueueInput) (*driver.JobQueue, error) {
	name := nameFromARN(kindJobQueue, in.Name)

	if len(in.ComputeEnvironmentOrder) > 0 {
		if err := m.validateOrder(in.ComputeEnvironmentOrder); err != nil {
			return nil, err
		}
	}

	// Held across the read, the policy resolution and the write, so the policy
	// can't be deleted (and no other update can land) in between.
	m.policyBindMu.Lock()
	defer m.policyBindMu.Unlock()

	cur, ok := m.jobQueues.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "job queue %q does not exist", name)
	}

	policyARN, err := m.nextSchedulingPolicy(&cur, in.SchedulingPolicyARN)
	if err != nil {
		return nil, err
	}

	var updated driver.JobQueue

	ok = m.jobQueues.Update(name, func(q driver.JobQueue) driver.JobQueue {
		next := cloneJobQueue(q)
		if in.State != nil {
			next.State = *in.State
		}

		if in.Priority != nil {
			next.Priority = *in.Priority
		}

		next.SchedulingPolicyARN = policyARN

		if len(in.ComputeEnvironmentOrder) > 0 {
			next.ComputeEnvironmentOrder = cloneOrder(in.ComputeEnvironmentOrder)
		}

		updated = cloneJobQueue(next)

		return next
	})

	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "job queue %q does not exist", name)
	}

	return &updated, nil
}

// DeleteJobQueue removes a job queue. It must be DISABLED first, matching real
// Batch (Terraform disables the queue and waits for VALID before deleting).
func (m *Mock) DeleteJobQueue(_ context.Context, name string) error {
	name = nameFromARN(kindJobQueue, name)

	q, ok := m.jobQueues.Get(name)
	if !ok {
		return nil // delete is idempotent
	}

	if q.State != driver.StateDisabled {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"cannot delete job queue %q while it is %s; set its state to DISABLED first", name, q.State)
	}

	m.jobQueues.Delete(name)

	return nil
}

// validateOrder rejects an order that references a missing or non-VALID compute
// environment.
func (m *Mock) validateOrder(order []driver.ComputeEnvironmentOrder) error {
	for _, o := range order {
		ref := nameFromARN(kindComputeEnvironment, o.ComputeEnvironment)

		ce, ok := m.computeEnvs.Get(ref)
		if !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "compute environment %q does not exist", o.ComputeEnvironment)
		}

		if ce.Status != driver.StatusValid {
			return cerrors.Newf(cerrors.InvalidArgument,
				"compute environment %q is not VALID (status %s)", o.ComputeEnvironment, ce.Status)
		}
	}

	return nil
}

// nextSchedulingPolicy resolves the scheduling-policy ARN an UpdateJobQueue
// leaves on the queue. Per the CreateJobQueue reference, a FIFO queue (no
// policy) can't have one added, and a fair-share queue's policy can be replaced
// but not removed. A nil ref leaves the policy unchanged.
func (m *Mock) nextSchedulingPolicy(q *driver.JobQueue, ref *string) (string, error) {
	if ref == nil {
		return q.SchedulingPolicyARN, nil
	}

	switch {
	case *ref == "" && q.SchedulingPolicyARN == "":
		return "", nil
	case *ref == "":
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"the scheduling policy of job queue %q can be replaced but not removed", q.Name)
	case q.SchedulingPolicyARN == "":
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"job queue %q has no scheduling policy (FIFO) and can't have one added", q.Name)
	}

	return m.resolveSchedulingPolicy(*ref)
}
