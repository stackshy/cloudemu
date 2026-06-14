// Package sagemaker provides a portable Amazon SageMaker AI API with
// cross-cutting concerns. It wraps a driver.Service (the control plane plus the
// inference runtime) with recording, metrics, rate limiting, error injection,
// and latency simulation — the same middle layer every other service ships
// (see bedrock/bedrock.go) so SageMaker participates in the three-layer design.
//
// The wrapper implements driver.Service, so it is a drop-in replacement for the
// raw mock when constructing the SDK-compat server.
package sagemaker

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// Compile-time check that the portable type implements the full service.
var _ driver.Service = (*SageMaker)(nil)

// SageMaker is the portable SageMaker type wrapping a driver with cross-cutting
// concerns.
type SageMaker struct {
	drv      driver.Service
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// New creates a portable SageMaker wrapping the given driver.
func New(d driver.Service, opts ...Option) *SageMaker {
	s := &SageMaker{drv: d}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Option configures a portable SageMaker.
type Option func(*SageMaker)

// WithRecorder sets the call recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(s *SageMaker) { s.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(s *SageMaker) { s.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(s *SageMaker) { s.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(s *SageMaker) { s.injector = i } }

// WithLatency sets simulated latency applied to every call.
func WithLatency(d time.Duration) Option { return func(s *SageMaker) { s.latency = d } }

// do applies the cross-cutting concerns around a single driver call.
func (s *SageMaker) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if s.injector != nil {
		if err := s.injector.Check("sagemaker", op); err != nil {
			s.rec(op, input, nil, err, time.Since(start))

			return nil, err
		}
	}

	if s.limiter != nil {
		if err := s.limiter.Allow(); err != nil {
			s.rec(op, input, nil, err, time.Since(start))

			return nil, err
		}
	}

	if s.latency > 0 {
		time.Sleep(s.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if s.metrics != nil {
		labels := map[string]string{"service": "sagemaker", "operation": op}
		s.metrics.Counter("calls_total", 1, labels)
		s.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			s.metrics.Counter("errors_total", 1, labels)
		}
	}

	s.rec(op, input, out, err, dur)

	return out, err
}

func (s *SageMaker) rec(op string, input, output any, err error, dur time.Duration) {
	if s.recorder != nil {
		s.recorder.Record("sagemaker", op, input, output, err, dur)
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
