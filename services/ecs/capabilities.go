package ecs

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// The methods in this file expose the optional driver capabilities (task
// protection, task sets, service deployments, Service Connect namespaces)
// through the same error-injection, rate-limit, latency, metrics and recording
// pipeline as the primary operations. A driver that does not implement a
// capability answers FailedPrecondition.

// capabilityOf returns the optional capability T of the wrapped driver.
func capabilityOf[T any](e *ECS, op string) (T, error) {
	c, ok := e.driver.(T)
	if !ok {
		var zero T

		return zero, cerrors.Newf(cerrors.FailedPrecondition, "the ECS driver does not implement %s", op)
	}

	return c, nil
}

// GetTaskProtection reports the scale-in protection of service tasks.
func (e *ECS) GetTaskProtection(ctx context.Context, cluster string, tasks []string) (
	[]driver.ProtectedTask, []driver.Failure, error,
) {
	tp, err := capabilityOf[driver.TaskProtection](e, "GetTaskProtection")
	if err != nil {
		return nil, nil, err
	}

	return doBatch(ctx, e, "GetTaskProtection", tasks, func() ([]driver.ProtectedTask, []driver.Failure, error) {
		return tp.GetTaskProtection(ctx, cluster, tasks)
	})
}

// UpdateTaskProtection enables or disables scale-in protection on service tasks.
func (e *ECS) UpdateTaskProtection(ctx context.Context, in driver.UpdateTaskProtectionInput) (
	[]driver.ProtectedTask, []driver.Failure, error,
) {
	tp, err := capabilityOf[driver.TaskProtection](e, "UpdateTaskProtection")
	if err != nil {
		return nil, nil, err
	}

	return doBatch(ctx, e, "UpdateTaskProtection", in, func() ([]driver.ProtectedTask, []driver.Failure, error) {
		return tp.UpdateTaskProtection(ctx, in)
	})
}

// doOne runs one single-result capability operation through do(). Its closure takes no
// parameter so that the coverage generator does not count driver interfaces here.
func doOne[R any](ctx context.Context, e *ECS, op string, input any, call func() (R, error)) (R, error) {
	var zero R

	out, err := e.do(ctx, op, input, func() (any, error) { return call() })
	if err != nil {
		return zero, err
	}

	return out.(R), nil
}

// CreateTaskSet creates a task set in an EXTERNAL-controller service.
//
//nolint:gocritic // in is passed by value to mirror the driver interface; the copy is cheap for a mock.
func (e *ECS) CreateTaskSet(ctx context.Context, in driver.CreateTaskSetInput) (*driver.TaskSet, error) {
	c, err := capabilityOf[driver.TaskSets](e, "CreateTaskSet")
	if err != nil {
		return nil, err
	}

	return doOne(ctx, e, "CreateTaskSet", in, func() (*driver.TaskSet, error) {
		return c.CreateTaskSet(ctx, in)
	})
}

// UpdateTaskSet changes a task set's scale.
func (e *ECS) UpdateTaskSet(ctx context.Context, in driver.UpdateTaskSetInput) (*driver.TaskSet, error) {
	c, err := capabilityOf[driver.TaskSets](e, "UpdateTaskSet")
	if err != nil {
		return nil, err
	}

	return doOne(ctx, e, "UpdateTaskSet", in, func() (*driver.TaskSet, error) {
		return c.UpdateTaskSet(ctx, in)
	})
}

// DeleteTaskSet deletes a task set.
func (e *ECS) DeleteTaskSet(ctx context.Context, in driver.DeleteTaskSetInput) (*driver.TaskSet, error) {
	c, err := capabilityOf[driver.TaskSets](e, "DeleteTaskSet")
	if err != nil {
		return nil, err
	}

	return doOne(ctx, e, "DeleteTaskSet", in, func() (*driver.TaskSet, error) {
		return c.DeleteTaskSet(ctx, in)
	})
}

// DescribeTaskSets describes a service's task sets, returning failures for unresolved ids.
func (e *ECS) DescribeTaskSets(ctx context.Context, cluster, service string, ids []string) (
	[]driver.TaskSet, []driver.Failure, error,
) {
	c, err := capabilityOf[driver.TaskSets](e, "DescribeTaskSets")
	if err != nil {
		return nil, nil, err
	}

	return doBatch(ctx, e, "DescribeTaskSets", ids, func() ([]driver.TaskSet, []driver.Failure, error) {
		return c.DescribeTaskSets(ctx, cluster, service, ids)
	})
}

// UpdateServicePrimaryTaskSet promotes a task set to PRIMARY.
func (e *ECS) UpdateServicePrimaryTaskSet(ctx context.Context, cluster, service, primary string) (*driver.TaskSet, error) {
	c, err := capabilityOf[driver.TaskSets](e, "UpdateServicePrimaryTaskSet")
	if err != nil {
		return nil, err
	}

	return doOne(ctx, e, "UpdateServicePrimaryTaskSet", primary, func() (*driver.TaskSet, error) {
		return c.UpdateServicePrimaryTaskSet(ctx, cluster, service, primary)
	})
}

// deploymentPage is a page of ListServiceDeployments results threaded through do().
type deploymentPage struct {
	items []driver.ServiceDeployment
	next  string
}

// ListServiceDeployments lists a service's deployments, newest first.
//
//nolint:gocritic // in is passed by value to mirror the driver interface; the copy is cheap for a mock.
func (e *ECS) ListServiceDeployments(ctx context.Context, in driver.ListServiceDeploymentsInput) (
	items []driver.ServiceDeployment, next string, err error,
) {
	c, err := capabilityOf[driver.ServiceDeployments](e, "ListServiceDeployments")
	if err != nil {
		return nil, "", err
	}

	page, err := doOne(ctx, e, "ListServiceDeployments", in, func() (deploymentPage, error) {
		got, token, cerr := c.ListServiceDeployments(ctx, in)

		return deploymentPage{items: got, next: token}, cerr
	})
	if err != nil {
		return nil, "", err
	}

	return page.items, page.next, nil
}

// DescribeServiceDeployments describes deployments by ARN, returning failures for unresolved ARNs.
func (e *ECS) DescribeServiceDeployments(ctx context.Context, arns []string) (
	[]driver.ServiceDeployment, []driver.Failure, error,
) {
	c, err := capabilityOf[driver.ServiceDeployments](e, "DescribeServiceDeployments")
	if err != nil {
		return nil, nil, err
	}

	return doBatch(ctx, e, "DescribeServiceDeployments", arns, func() ([]driver.ServiceDeployment, []driver.Failure, error) {
		return c.DescribeServiceDeployments(ctx, arns)
	})
}

// DescribeServiceRevisions describes service revisions by ARN, returning failures for unresolved ARNs.
func (e *ECS) DescribeServiceRevisions(ctx context.Context, arns []string) (
	[]driver.ServiceRevision, []driver.Failure, error,
) {
	c, err := capabilityOf[driver.ServiceDeployments](e, "DescribeServiceRevisions")
	if err != nil {
		return nil, nil, err
	}

	return doBatch(ctx, e, "DescribeServiceRevisions", arns, func() ([]driver.ServiceRevision, []driver.Failure, error) {
		return c.DescribeServiceRevisions(ctx, arns)
	})
}

// StopServiceDeployment stops a deployment that has not completed.
func (e *ECS) StopServiceDeployment(ctx context.Context, arn, stopType string) (string, error) {
	c, err := capabilityOf[driver.ServiceDeployments](e, "StopServiceDeployment")
	if err != nil {
		return "", err
	}

	return doOne(ctx, e, "StopServiceDeployment", arn, func() (string, error) {
		return c.StopServiceDeployment(ctx, arn, stopType)
	})
}

// ListServicesByNamespace lists the services that joined a Service Connect namespace.
func (e *ECS) ListServicesByNamespace(ctx context.Context, namespace string, maxResults int, nextToken string) (
	arns []string, next string, err error,
) {
	c, err := capabilityOf[driver.ServiceNamespaces](e, "ListServicesByNamespace")
	if err != nil {
		return nil, "", err
	}

	page, err := doOne(ctx, e, "ListServicesByNamespace", namespace, func() (arnPage, error) {
		got, token, cerr := c.ListServicesByNamespace(ctx, namespace, maxResults, nextToken)

		return arnPage{items: got, next: token}, cerr
	})
	if err != nil {
		return nil, "", err
	}

	return page.items, page.next, nil
}

// arnPage is a page of ARNs threaded through do().
type arnPage struct {
	items []string
	next  string
}
