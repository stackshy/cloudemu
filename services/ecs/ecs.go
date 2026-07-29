// Package ecs provides a portable Amazon ECS API that wraps a driver with the
// standard cross-cutting concerns (error injection, rate limiting, simulated
// latency, metrics, and recording), mirroring the other cloudemu services.
package ecs

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/ratelimit"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// ECS is the portable ECS type wrapping a driver with cross-cutting concerns.
type ECS struct {
	driver   driver.ECS
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewECS creates a new portable ECS wrapping the given driver.
func NewECS(d driver.ECS, opts ...Option) *ECS {
	e := &ECS{driver: d}
	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Option configures a portable ECS.
type Option func(*ECS)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(e *ECS) { e.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(e *ECS) { e.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(e *ECS) { e.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(e *ECS) { e.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(e *ECS) { e.latency = d } }

func (e *ECS) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if e.injector != nil {
		if err := e.injector.Check("ecs", op); err != nil {
			e.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if e.limiter != nil {
		if err := e.limiter.Allow(); err != nil {
			e.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if e.latency > 0 {
		time.Sleep(e.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if e.metrics != nil {
		labels := map[string]string{"service": "ecs", "operation": op}
		e.metrics.Counter("calls_total", 1, labels)
		e.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			e.metrics.Counter("errors_total", 1, labels)
		}
	}

	e.rec(op, input, out, err, dur)

	return out, err
}

func (e *ECS) rec(op string, input, output any, err error, dur time.Duration) {
	if e.recorder != nil {
		e.recorder.Record("ecs", op, input, output, err, dur)
	}
}

// CreateCluster creates a new cluster.
func (e *ECS) CreateCluster(ctx context.Context, in driver.CreateClusterInput) (*driver.Cluster, error) {
	out, err := e.do(ctx, "CreateCluster", in, func() (any, error) { return e.driver.CreateCluster(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Cluster), nil
}

// ListClusters lists all clusters.
func (e *ECS) ListClusters(ctx context.Context) ([]driver.Cluster, error) {
	out, err := e.do(ctx, "ListClusters", nil, func() (any, error) { return e.driver.ListClusters(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Cluster), nil
}

// DeleteCluster deletes (deactivates) a cluster.
func (e *ECS) DeleteCluster(ctx context.Context, id string) (*driver.Cluster, error) {
	out, err := e.do(ctx, "DeleteCluster", id, func() (any, error) { return e.driver.DeleteCluster(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Cluster), nil
}

// RegisterTaskDefinition registers a new task definition revision.
//
//nolint:gocritic // in is passed by value to mirror the driver.ECS interface; the copy is cheap for a mock.
func (e *ECS) RegisterTaskDefinition(ctx context.Context, in driver.RegisterTaskDefinitionInput) (*driver.TaskDefinition, error) {
	out, err := e.do(ctx, "RegisterTaskDefinition", in, func() (any, error) { return e.driver.RegisterTaskDefinition(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.TaskDefinition), nil
}

// DescribeTaskDefinition resolves a task definition by family, family:revision, or ARN.
func (e *ECS) DescribeTaskDefinition(ctx context.Context, id string) (*driver.TaskDefinition, error) {
	out, err := e.do(ctx, "DescribeTaskDefinition", id, func() (any, error) { return e.driver.DescribeTaskDefinition(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.TaskDefinition), nil
}

// DeregisterTaskDefinition marks a task definition revision INACTIVE.
func (e *ECS) DeregisterTaskDefinition(ctx context.Context, id string) (*driver.TaskDefinition, error) {
	out, err := e.do(ctx, "DeregisterTaskDefinition", id, func() (any, error) { return e.driver.DeregisterTaskDefinition(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.TaskDefinition), nil
}

// ListTaskDefinitions lists task definitions, optionally filtered by family prefix and status.
func (e *ECS) ListTaskDefinitions(ctx context.Context, familyPrefix, status string) ([]driver.TaskDefinition, error) {
	out, err := e.do(ctx, "ListTaskDefinitions", familyPrefix, func() (any, error) {
		return e.driver.ListTaskDefinitions(ctx, familyPrefix, status)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.TaskDefinition), nil
}

// StopTask stops a running task.
func (e *ECS) StopTask(ctx context.Context, cluster, task, reason string) (*driver.Task, error) {
	out, err := e.do(ctx, "StopTask", task, func() (any, error) { return e.driver.StopTask(ctx, cluster, task, reason) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Task), nil
}

// ListTasks lists tasks in a cluster, optionally filtered by family and desired status.
func (e *ECS) ListTasks(ctx context.Context, cluster, family, desiredStatus string) ([]driver.Task, error) {
	out, err := e.do(ctx, "ListTasks", cluster, func() (any, error) {
		return e.driver.ListTasks(ctx, cluster, family, desiredStatus)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Task), nil
}

// CreateService creates a new service.
//
//nolint:gocritic // in is passed by value to mirror the driver.ECS interface; the copy is cheap for a mock.
func (e *ECS) CreateService(ctx context.Context, in driver.CreateServiceInput) (*driver.Service, error) {
	out, err := e.do(ctx, "CreateService", in, func() (any, error) { return e.driver.CreateService(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Service), nil
}

// UpdateService updates mutable fields of a service.
func (e *ECS) UpdateService(ctx context.Context, in driver.UpdateServiceInput) (*driver.Service, error) {
	out, err := e.do(ctx, "UpdateService", in, func() (any, error) { return e.driver.UpdateService(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Service), nil
}

// ListServices lists services in a cluster.
func (e *ECS) ListServices(ctx context.Context, cluster string) ([]driver.Service, error) {
	out, err := e.do(ctx, "ListServices", cluster, func() (any, error) { return e.driver.ListServices(ctx, cluster) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Service), nil
}

// DeleteService deletes (deactivates) a service.
func (e *ECS) DeleteService(ctx context.Context, cluster, service string) (*driver.Service, error) {
	out, err := e.do(ctx, "DeleteService", service, func() (any, error) { return e.driver.DeleteService(ctx, cluster, service) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Service), nil
}

// ListContainerInstances lists container instances in a cluster.
func (e *ECS) ListContainerInstances(ctx context.Context, cluster string) ([]driver.ContainerInstance, error) {
	out, err := e.do(ctx, "ListContainerInstances", cluster, func() (any, error) { return e.driver.ListContainerInstances(ctx, cluster) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.ContainerInstance), nil
}

// batchResult carries the two-slice partial-success shape of the batch Describe
// and RunTask operations through the do() wrapper's single any return.
type batchResult[T any] struct {
	items    []T
	failures []driver.Failure
}

// doBatch runs a partial-success driver call (items + failures + error) through
// the do() wrapper, threading the two result slices through its single any
// return. It is a free function because Go methods cannot take type parameters.
func doBatch[T any](ctx context.Context, e *ECS, op string, input any,
	call func() ([]T, []driver.Failure, error),
) ([]T, []driver.Failure, error) {
	out, err := e.do(ctx, op, input, func() (any, error) {
		items, failures, derr := call()
		return batchResult[T]{items, failures}, derr
	})
	if err != nil {
		return nil, nil, err
	}

	r := out.(batchResult[T])

	return r.items, r.failures, nil
}

// DescribeClusters resolves clusters by name or ARN, returning failures for unresolved ids.
func (e *ECS) DescribeClusters(ctx context.Context, ids []string) ([]driver.Cluster, []driver.Failure, error) {
	return doBatch(ctx, e, "DescribeClusters", ids, func() ([]driver.Cluster, []driver.Failure, error) {
		return e.driver.DescribeClusters(ctx, ids)
	})
}

// RunTask runs count tasks from a task definition, returning failures for unresolved references.
//
//nolint:gocritic // in is passed by value to mirror the driver.ECS interface; the copy is cheap for a mock.
func (e *ECS) RunTask(ctx context.Context, in driver.RunTaskInput) ([]driver.Task, []driver.Failure, error) {
	return doBatch(ctx, e, "RunTask", in, func() ([]driver.Task, []driver.Failure, error) {
		return e.driver.RunTask(ctx, in)
	})
}

// DescribeTasks resolves tasks by id or ARN, returning failures for unresolved ids.
func (e *ECS) DescribeTasks(ctx context.Context, cluster string, ids []string) ([]driver.Task, []driver.Failure, error) {
	return doBatch(ctx, e, "DescribeTasks", ids, func() ([]driver.Task, []driver.Failure, error) {
		return e.driver.DescribeTasks(ctx, cluster, ids)
	})
}

// DescribeServices resolves services by name or ARN, returning failures for unresolved ids.
func (e *ECS) DescribeServices(ctx context.Context, cluster string, ids []string) ([]driver.Service, []driver.Failure, error) {
	return doBatch(ctx, e, "DescribeServices", ids, func() ([]driver.Service, []driver.Failure, error) {
		return e.driver.DescribeServices(ctx, cluster, ids)
	})
}

// DescribeContainerInstances resolves container instances by id or ARN, returning failures for unresolved ids.
func (e *ECS) DescribeContainerInstances(ctx context.Context, cluster string, ids []string) (
	[]driver.ContainerInstance, []driver.Failure, error,
) {
	return doBatch(ctx, e, "DescribeContainerInstances", ids, func() ([]driver.ContainerInstance, []driver.Failure, error) {
		return e.driver.DescribeContainerInstances(ctx, cluster, ids)
	})
}
