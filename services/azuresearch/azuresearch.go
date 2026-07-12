// Package azuresearch provides a portable Azure AI Search API with
// cross-cutting concerns. It wraps a driver.AzureSearch (ARM control plane +
// search data plane) with recording, metrics, rate limiting, error injection,
// and latency simulation — the same middle layer every other service ships, so
// Azure AI Search participates in the three-layer design.
package azuresearch

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/ratelimit"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	"github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
)

// Compile-time check that the portable type implements the full service.
var _ driver.AzureSearch = (*AzureSearch)(nil)

// AzureSearch is the portable Azure AI Search type wrapping a driver with
// cross-cutting concerns.
type AzureSearch struct {
	drv      driver.AzureSearch
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// New creates a portable AzureSearch wrapping the given driver.
func New(d driver.AzureSearch, opts ...Option) *AzureSearch {
	a := &AzureSearch{drv: d}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Option configures a portable AzureSearch.
type Option func(*AzureSearch)

// WithRecorder sets the call recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(a *AzureSearch) { a.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(a *AzureSearch) { a.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(a *AzureSearch) { a.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(a *AzureSearch) { a.injector = i } }

// WithLatency sets simulated latency applied to every call.
func WithLatency(d time.Duration) Option { return func(a *AzureSearch) { a.latency = d } }

// do applies the cross-cutting concerns around a single driver call.
func (a *AzureSearch) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if a.injector != nil {
		if err := a.injector.Check("azuresearch", op); err != nil {
			a.rec(op, input, nil, err, time.Since(start))

			return nil, err
		}
	}

	if a.limiter != nil {
		if err := a.limiter.Allow(); err != nil {
			a.rec(op, input, nil, err, time.Since(start))

			return nil, err
		}
	}

	if a.latency > 0 {
		time.Sleep(a.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if a.metrics != nil {
		labels := map[string]string{"service": "azuresearch", "operation": op}
		a.metrics.Counter("calls_total", 1, labels)
		a.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			a.metrics.Counter("errors_total", 1, labels)
		}
	}

	a.rec(op, input, out, err, dur)

	return out, err
}

func (a *AzureSearch) rec(op string, input, output any, err error, dur time.Duration) {
	if a.recorder != nil {
		a.recorder.Record("azuresearch", op, input, output, err, dur)
	}
}

// cast converts the do() result to T, short-circuiting on error.
func cast[T any](out any, err error) (T, error) {
	if err != nil {
		var zero T

		return zero, err
	}

	return out.(T), nil //nolint:forcetypeassert // do() returns exactly the driver's typed result
}

// act runs an error-only driver call through the pipeline.
func (a *AzureSearch) act(ctx context.Context, op string, input any, fn func() error) error {
	_, err := a.do(ctx, op, input, func() (any, error) { return nil, fn() })

	return err
}
