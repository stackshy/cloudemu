// Package bedrock provides a portable foundation-model API with cross-cutting
// concerns. It wraps a driver.Bedrock with recording, metrics, rate limiting,
// error injection, and latency simulation.
package bedrock

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/bedrock/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Bedrock is the portable foundation-model type wrapping a driver with
// cross-cutting concerns.
type Bedrock struct {
	driver   driver.Bedrock
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewBedrock creates a new portable Bedrock wrapping the given driver.
func NewBedrock(d driver.Bedrock, opts ...Option) *Bedrock {
	b := &Bedrock{driver: d}
	for _, opt := range opts {
		opt(b)
	}

	return b
}

// Option configures a portable Bedrock.
type Option func(*Bedrock)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(b *Bedrock) { b.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(b *Bedrock) { b.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(b *Bedrock) { b.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(b *Bedrock) { b.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(b *Bedrock) { b.latency = d } }

func (b *Bedrock) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if b.injector != nil {
		if err := b.injector.Check("bedrock", op); err != nil {
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
		labels := map[string]string{"service": "bedrock", "operation": op}
		b.metrics.Counter("calls_total", 1, labels)
		b.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			b.metrics.Counter("errors_total", 1, labels)
		}
	}

	b.rec(op, input, out, err, dur)

	return out, err
}

func (b *Bedrock) rec(op string, input, output any, err error, dur time.Duration) {
	if b.recorder != nil {
		b.recorder.Record("bedrock", op, input, output, err, dur)
	}
}

// ListFoundationModels lists the available foundation models.
func (b *Bedrock) ListFoundationModels(ctx context.Context) ([]driver.FoundationModel, error) {
	out, err := b.do(ctx, "ListFoundationModels", nil, func() (any, error) { return b.driver.ListFoundationModels(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.FoundationModel), nil
}

// GetFoundationModel retrieves a single foundation model by ID.
func (b *Bedrock) GetFoundationModel(ctx context.Context, modelID string) (*driver.FoundationModel, error) {
	out, err := b.do(ctx, "GetFoundationModel", modelID, func() (any, error) { return b.driver.GetFoundationModel(ctx, modelID) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.FoundationModel), nil
}

// CreateModelCustomizationJob starts a model-customization (fine-tuning) job.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (b *Bedrock) CreateModelCustomizationJob(ctx context.Context, cfg driver.CustomizationJobConfig) (*driver.CustomizationJob, error) {
	out, err := b.do(ctx, "CreateModelCustomizationJob", cfg, func() (any, error) {
		return b.driver.CreateModelCustomizationJob(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.CustomizationJob), nil
}

// GetModelCustomizationJob retrieves a customization job by ARN or name.
func (b *Bedrock) GetModelCustomizationJob(ctx context.Context, jobIdentifier string) (*driver.CustomizationJob, error) {
	out, err := b.do(ctx, "GetModelCustomizationJob", jobIdentifier, func() (any, error) {
		return b.driver.GetModelCustomizationJob(ctx, jobIdentifier)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.CustomizationJob), nil
}

// ListModelCustomizationJobs lists all customization jobs.
func (b *Bedrock) ListModelCustomizationJobs(ctx context.Context) ([]driver.CustomizationJob, error) {
	out, err := b.do(ctx, "ListModelCustomizationJobs", nil, func() (any, error) {
		return b.driver.ListModelCustomizationJobs(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.CustomizationJob), nil
}

// ListCustomModels lists all custom models.
func (b *Bedrock) ListCustomModels(ctx context.Context) ([]driver.CustomModel, error) {
	out, err := b.do(ctx, "ListCustomModels", nil, func() (any, error) { return b.driver.ListCustomModels(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.CustomModel), nil
}

// GetCustomModel retrieves a custom model by ARN or name.
func (b *Bedrock) GetCustomModel(ctx context.Context, modelIdentifier string) (*driver.CustomModel, error) {
	out, err := b.do(ctx, "GetCustomModel", modelIdentifier, func() (any, error) {
		return b.driver.GetCustomModel(ctx, modelIdentifier)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.CustomModel), nil
}

// DeleteCustomModel deletes a custom model by ARN or name.
func (b *Bedrock) DeleteCustomModel(ctx context.Context, modelIdentifier string) error {
	_, err := b.do(ctx, "DeleteCustomModel", modelIdentifier, func() (any, error) {
		return nil, b.driver.DeleteCustomModel(ctx, modelIdentifier)
	})

	return err
}

// InvokeModel runs (emulated) inference against a model with a native payload.
func (b *Bedrock) InvokeModel(ctx context.Context, in driver.InvokeModelInput) (*driver.InvokeModelResult, error) {
	out, err := b.do(ctx, "InvokeModel", in.ModelID, func() (any, error) { return b.driver.InvokeModel(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.InvokeModelResult), nil
}

// Converse runs (emulated) inference using the structured Converse API.
func (b *Bedrock) Converse(ctx context.Context, in driver.ConverseInput) (*driver.ConverseOutput, error) {
	out, err := b.do(ctx, "Converse", in.ModelID, func() (any, error) { return b.driver.Converse(ctx, in) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.ConverseOutput), nil
}
