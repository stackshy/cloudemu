package compute

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Compute is the portable compute type wrapping a driver with cross-cutting concerns.
type Compute struct {
	driver   driver.Compute
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewCompute creates a new portable Compute wrapping the given driver.
func NewCompute(d driver.Compute, opts ...Option) *Compute {
	c := &Compute{driver: d}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Option configures a portable Compute.
type Option func(*Compute)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(c *Compute) { c.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(c *Compute) { c.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(c *Compute) { c.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(c *Compute) { c.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(c *Compute) { c.latency = d } }

func (c *Compute) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if c.injector != nil {
		if err := c.injector.Check("compute", op); err != nil {
			c.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.limiter != nil {
		if err := c.limiter.Allow(); err != nil {
			c.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if c.latency > 0 {
		time.Sleep(c.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if c.metrics != nil {
		labels := map[string]string{"service": "compute", "operation": op}
		c.metrics.Counter("calls_total", 1, labels)
		c.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			c.metrics.Counter("errors_total", 1, labels)
		}
	}

	c.record(op, input, out, err, dur)

	return out, err
}

func (c *Compute) record(op string, input, output any, err error, dur time.Duration) {
	if c.recorder != nil {
		c.recorder.Record("compute", op, input, output, err, dur)
	}
}

// RunInstances creates new VM instances.
//
//nolint:gocritic // config passed by value to match driver.Compute interface pattern
func (c *Compute) RunInstances(ctx context.Context, config driver.InstanceConfig, count int) ([]driver.Instance, error) {
	out, err := c.do(ctx, "RunInstances", config, func() (any, error) {
		return c.driver.RunInstances(ctx, config, count)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.Instance), nil
}

// StartInstances starts stopped instances.
func (c *Compute) StartInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "StartInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.StartInstances(ctx, instanceIDs)
	})

	return err
}

// StopInstances stops running instances.
func (c *Compute) StopInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "StopInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.StopInstances(ctx, instanceIDs)
	})

	return err
}

// RebootInstances reboots running instances.
func (c *Compute) RebootInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "RebootInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.RebootInstances(ctx, instanceIDs)
	})

	return err
}

// TerminateInstances terminates instances.
func (c *Compute) TerminateInstances(ctx context.Context, instanceIDs []string) error {
	_, err := c.do(ctx, "TerminateInstances", instanceIDs, func() (any, error) {
		return nil, c.driver.TerminateInstances(ctx, instanceIDs)
	})

	return err
}

// DescribeInstances describes instances.
func (c *Compute) DescribeInstances(ctx context.Context, instanceIDs []string, filters []driver.DescribeFilter) ([]driver.Instance, error) {
	out, err := c.do(ctx, "DescribeInstances", instanceIDs, func() (any, error) {
		return c.driver.DescribeInstances(ctx, instanceIDs, filters)
	})

	if err != nil {
		return nil, err
	}

	return out.([]driver.Instance), nil
}

// ModifyInstance modifies an instance.
func (c *Compute) ModifyInstance(ctx context.Context, instanceID string, input driver.ModifyInstanceInput) error {
	_, err := c.do(ctx, "ModifyInstance", input, func() (any, error) {
		return nil, c.driver.ModifyInstance(ctx, instanceID, input)
	})

	return err
}
