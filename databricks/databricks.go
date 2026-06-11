// Package databricks provides a portable analytics-workspace API with
// cross-cutting concerns. It wraps a driver.Databricks with recording,
// metrics, rate limiting, error injection, and latency simulation.
package databricks

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/databricks/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Databricks is the portable workspace type wrapping a driver with
// cross-cutting concerns.
type Databricks struct {
	driver   driver.Databricks
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewDatabricks creates a new portable Databricks wrapping the given driver.
func NewDatabricks(d driver.Databricks, opts ...Option) *Databricks {
	b := &Databricks{driver: d}
	for _, opt := range opts {
		opt(b)
	}

	return b
}

// Option configures a portable Databricks.
type Option func(*Databricks)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(b *Databricks) { b.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(b *Databricks) { b.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(b *Databricks) { b.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(b *Databricks) { b.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(b *Databricks) { b.latency = d } }

func (b *Databricks) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if b.injector != nil {
		if err := b.injector.Check("databricks", op); err != nil {
			b.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.limiter != nil {
		if err := b.limiter.Allow(); err != nil {
			b.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if b.latency > 0 {
		time.Sleep(b.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if b.metrics != nil {
		labels := map[string]string{"service": "databricks", "operation": op}
		b.metrics.Counter("calls_total", 1, labels)
		b.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			b.metrics.Counter("errors_total", 1, labels)
		}
	}

	b.rec(op, input, out, err, dur)

	return out, err
}

func (b *Databricks) rec(op string, input, output any, err error, dur time.Duration) {
	if b.recorder != nil {
		b.recorder.Record("databricks", op, input, output, err, dur)
	}
}

// CreateWorkspace creates a new managed workspace.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Databricks) CreateWorkspace(ctx context.Context, cfg driver.WorkspaceConfig) (*driver.Workspace, error) {
	out, err := b.do(ctx, "CreateWorkspace", cfg, func() (any, error) { return b.driver.CreateWorkspace(ctx, cfg) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Workspace), nil
}

// GetWorkspace retrieves a workspace by resource group and name.
func (b *Databricks) GetWorkspace(ctx context.Context, resourceGroup, name string) (*driver.Workspace, error) {
	out, err := b.do(ctx, "GetWorkspace", name, func() (any, error) { return b.driver.GetWorkspace(ctx, resourceGroup, name) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.Workspace), nil
}

// DeleteWorkspace deletes a workspace by resource group and name.
func (b *Databricks) DeleteWorkspace(ctx context.Context, resourceGroup, name string) error {
	_, err := b.do(ctx, "DeleteWorkspace", name, func() (any, error) {
		return nil, b.driver.DeleteWorkspace(ctx, resourceGroup, name)
	})

	return err
}

// UpdateWorkspaceTags replaces a workspace's tags.
func (b *Databricks) UpdateWorkspaceTags(
	ctx context.Context, resourceGroup, name string, tags map[string]string,
) (*driver.Workspace, error) {
	out, err := b.do(ctx, "UpdateWorkspaceTags", name, func() (any, error) {
		return b.driver.UpdateWorkspaceTags(ctx, resourceGroup, name, tags)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Workspace), nil
}

// ListWorkspacesByResourceGroup lists workspaces in a resource group.
func (b *Databricks) ListWorkspacesByResourceGroup(ctx context.Context, resourceGroup string) ([]driver.Workspace, error) {
	out, err := b.do(ctx, "ListWorkspacesByResourceGroup", resourceGroup, func() (any, error) {
		return b.driver.ListWorkspacesByResourceGroup(ctx, resourceGroup)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Workspace), nil
}

// ListWorkspaces lists all workspaces in the subscription.
func (b *Databricks) ListWorkspaces(ctx context.Context) ([]driver.Workspace, error) {
	out, err := b.do(ctx, "ListWorkspaces", nil, func() (any, error) { return b.driver.ListWorkspaces(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.Workspace), nil
}
