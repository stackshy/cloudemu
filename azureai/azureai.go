// Package azureai provides a portable Azure AI API with cross-cutting concerns.
// It wraps a driver.AzureAI (both ARM providers plus the data planes) with
// recording, metrics, rate limiting, error injection, and latency simulation —
// the same middle layer every other service ships (see bedrock/bedrock.go,
// sagemaker/sagemaker.go, vertexai/vertexai.go) so Azure AI participates in the
// three-layer design.
//
// The wrapper implements driver.AzureAI, so it is a drop-in replacement for the
// raw provider mock when constructing the SDK-compat server.
package azureai

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/azureai/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Compile-time check that the portable type implements the full service.
var _ driver.AzureAI = (*AzureAI)(nil)

// AzureAI is the portable Azure AI type wrapping a driver with cross-cutting
// concerns.
type AzureAI struct {
	drv      driver.AzureAI
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// New creates a portable AzureAI wrapping the given driver.
func New(d driver.AzureAI, opts ...Option) *AzureAI {
	a := &AzureAI{drv: d}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Option configures a portable AzureAI.
type Option func(*AzureAI)

// WithRecorder sets the call recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(a *AzureAI) { a.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(a *AzureAI) { a.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(a *AzureAI) { a.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(a *AzureAI) { a.injector = i } }

// WithLatency sets simulated latency applied to every call.
func WithLatency(d time.Duration) Option { return func(a *AzureAI) { a.latency = d } }

// do applies the cross-cutting concerns around a single driver call.
func (a *AzureAI) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if a.injector != nil {
		if err := a.injector.Check("azureai", op); err != nil {
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
		labels := map[string]string{"service": "azureai", "operation": op}
		a.metrics.Counter("calls_total", 1, labels)
		a.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			a.metrics.Counter("errors_total", 1, labels)
		}
	}

	a.rec(op, input, out, err, dur)

	return out, err
}

func (a *AzureAI) rec(op string, input, output any, err error, dur time.Duration) {
	if a.recorder != nil {
		a.recorder.Record("azureai", op, input, output, err, dur)
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
func (a *AzureAI) act(ctx context.Context, op string, input any, fn func() error) error {
	_, err := a.do(ctx, op, input, func() (any, error) { return nil, fn() })

	return err
}
