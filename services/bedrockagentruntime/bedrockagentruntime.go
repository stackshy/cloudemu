// Package bedrockagentruntime provides a portable Bedrock Agent runtime API
// with cross-cutting concerns. It wraps a driver.BedrockAgentRuntime with
// recording, metrics, rate limiting, error injection, and latency simulation.
package bedrockagentruntime

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/features/inject"
	"github.com/stackshy/cloudemu/v2/features/metrics"
	"github.com/stackshy/cloudemu/v2/features/ratelimit"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// BedrockAgentRuntime is the portable Bedrock Agent runtime type wrapping a
// driver with cross-cutting concerns.
type BedrockAgentRuntime struct {
	driver   driver.BedrockAgentRuntime
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewBedrockAgentRuntime creates a new portable BedrockAgentRuntime wrapping the
// given driver.
func NewBedrockAgentRuntime(d driver.BedrockAgentRuntime, opts ...Option) *BedrockAgentRuntime {
	b := &BedrockAgentRuntime{driver: d}
	for _, opt := range opts {
		opt(b)
	}

	return b
}

// Option configures a portable BedrockAgentRuntime.
type Option func(*BedrockAgentRuntime)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option {
	return func(b *BedrockAgentRuntime) { b.recorder = r }
}

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(b *BedrockAgentRuntime) { b.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(b *BedrockAgentRuntime) { b.limiter = l }
}

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option {
	return func(b *BedrockAgentRuntime) { b.injector = i }
}

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(b *BedrockAgentRuntime) { b.latency = d } }

func (b *BedrockAgentRuntime) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if b.injector != nil {
		if err := b.injector.Check("bedrockagentruntime", op); err != nil {
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
		labels := map[string]string{"service": "bedrockagentruntime", "operation": op}
		b.metrics.Counter("calls_total", 1, labels)
		b.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			b.metrics.Counter("errors_total", 1, labels)
		}
	}

	b.rec(op, input, out, err, dur)

	return out, err
}

func (b *BedrockAgentRuntime) rec(op string, input, output any, err error, dur time.Duration) {
	if b.recorder != nil {
		b.recorder.Record("bedrockagentruntime", op, input, output, err, dur)
	}
}

// InvokeAgent runs (emulated) agent inference and returns the assembled
// completion.
func (b *BedrockAgentRuntime) InvokeAgent(ctx context.Context, in driver.InvokeAgentInput) (*driver.InvokeAgentResult, error) {
	out, err := b.do(ctx, "InvokeAgent", in.AgentID, func() (any, error) { return b.driver.InvokeAgent(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.InvokeAgentResult), nil
}

// Retrieve queries a knowledge base and returns matching chunks.
func (b *BedrockAgentRuntime) Retrieve(ctx context.Context, in driver.RetrieveInput) (*driver.RetrieveResult, error) {
	out, err := b.do(ctx, "Retrieve", in.KnowledgeBaseID, func() (any, error) { return b.driver.Retrieve(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.RetrieveResult), nil
}

// RetrieveAndGenerate queries a knowledge base and generates an answer.
func (b *BedrockAgentRuntime) RetrieveAndGenerate(
	ctx context.Context, in driver.RetrieveAndGenerateInput,
) (*driver.RetrieveAndGenerateResult, error) {
	out, err := b.do(ctx, "RetrieveAndGenerate", in.SessionID, func() (any, error) {
		return b.driver.RetrieveAndGenerate(ctx, in)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.RetrieveAndGenerateResult), nil
}
