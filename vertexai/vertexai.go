// Package vertexai provides a portable Google Cloud Vertex AI API with
// cross-cutting concerns. It wraps a driver.VertexAI (the control plane plus the
// prediction and generateContent runtimes) with recording, metrics, rate
// limiting, error injection, and latency simulation — the same middle layer
// every other service ships (see bedrock/bedrock.go, sagemaker/sagemaker.go) so
// Vertex AI participates in the three-layer design.
//
// The wrapper implements driver.VertexAI, so it is a drop-in replacement for the
// raw mock when constructing the SDK-compat server.
package vertexai

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/vertexai/driver"
)

// Compile-time check that the portable type implements the full service.
var _ driver.VertexAI = (*VertexAI)(nil)

// VertexAI is the portable Vertex AI type wrapping a driver with cross-cutting
// concerns.
type VertexAI struct {
	drv      driver.VertexAI
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// New creates a portable VertexAI wrapping the given driver.
func New(d driver.VertexAI, opts ...Option) *VertexAI {
	v := &VertexAI{drv: d}
	for _, opt := range opts {
		opt(v)
	}

	return v
}

// Option configures a portable VertexAI.
type Option func(*VertexAI)

// WithRecorder sets the call recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(v *VertexAI) { v.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(v *VertexAI) { v.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(v *VertexAI) { v.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(v *VertexAI) { v.injector = i } }

// WithLatency sets simulated latency applied to every call.
func WithLatency(d time.Duration) Option { return func(v *VertexAI) { v.latency = d } }

// do applies the cross-cutting concerns around a single driver call.
func (v *VertexAI) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if v.injector != nil {
		if err := v.injector.Check("vertexai", op); err != nil {
			v.rec(op, input, nil, err, time.Since(start))

			return nil, err
		}
	}

	if v.limiter != nil {
		if err := v.limiter.Allow(); err != nil {
			v.rec(op, input, nil, err, time.Since(start))

			return nil, err
		}
	}

	if v.latency > 0 {
		time.Sleep(v.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if v.metrics != nil {
		labels := map[string]string{"service": "vertexai", "operation": op}
		v.metrics.Counter("calls_total", 1, labels)
		v.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			v.metrics.Counter("errors_total", 1, labels)
		}
	}

	v.rec(op, input, out, err, dur)

	return out, err
}

func (v *VertexAI) rec(op string, input, output any, err error, dur time.Duration) {
	if v.recorder != nil {
		v.recorder.Record("vertexai", op, input, output, err, dur)
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

// opPair bundles a long-running Operation with the typed resource it returns so
// the (op, resource, error) driver methods can flow through the single-value
// do() pipeline.
type opPair[T any] struct {
	op  *driver.Operation
	res T
}
