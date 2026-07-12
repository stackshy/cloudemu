// Package parameterstore provides a portable SSM Parameter Store API with
// cross-cutting concerns (recorder, metrics, rate limiting, error injection,
// simulated latency) wrapping a driver.
package parameterstore

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/parameterstore/driver"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// ParameterStore is the portable Parameter Store type wrapping a driver with
// cross-cutting concerns.
type ParameterStore struct {
	driver   driver.ParameterStore
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// New creates a new portable ParameterStore wrapping the given driver.
func New(d driver.ParameterStore, opts ...Option) *ParameterStore {
	p := &ParameterStore{driver: d}
	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Option configures a portable ParameterStore.
type Option func(*ParameterStore)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(p *ParameterStore) { p.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(p *ParameterStore) { p.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(p *ParameterStore) { p.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(p *ParameterStore) { p.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(p *ParameterStore) { p.latency = d } }

func (p *ParameterStore) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if p.injector != nil {
		if err := p.injector.Check("ssm", op); err != nil {
			p.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if p.limiter != nil {
		if err := p.limiter.Allow(); err != nil {
			p.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if p.latency > 0 {
		time.Sleep(p.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if p.metrics != nil {
		labels := map[string]string{"service": "ssm", "operation": op}
		p.metrics.Counter("calls_total", 1, labels)
		p.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			p.metrics.Counter("errors_total", 1, labels)
		}
	}

	p.rec(op, input, out, err, dur)

	return out, err
}

func (p *ParameterStore) rec(op string, input, output any, err error, dur time.Duration) {
	if p.recorder != nil {
		p.recorder.Record("ssm", op, input, output, err, dur)
	}
}

// PutParameter creates or updates a parameter, returning the new version and tier.
func (p *ParameterStore) PutParameter(ctx context.Context, cfg driver.PutConfig) (int64, string, error) {
	type result struct {
		version int64
		tier    string
	}

	out, err := p.do(ctx, "PutParameter", cfg, func() (any, error) {
		v, tier, err := p.driver.PutParameter(ctx, cfg)
		return result{v, tier}, err
	})
	if err != nil {
		return 0, "", err
	}

	r := out.(result)

	return r.version, r.tier, nil
}

// GetParameter retrieves a parameter by name (optionally with a version or label selector).
func (p *ParameterStore) GetParameter(ctx context.Context, name string, withDecryption bool) (*driver.Parameter, error) {
	out, err := p.do(ctx, "GetParameter", name, func() (any, error) {
		return p.driver.GetParameter(ctx, name, withDecryption)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Parameter), nil
}

// GetParameters retrieves multiple parameters by name.
func (p *ParameterStore) GetParameters(ctx context.Context, names []string, withDecryption bool) ([]driver.Parameter, []string, error) {
	type result struct {
		found   []driver.Parameter
		invalid []string
	}

	out, err := p.do(ctx, "GetParameters", names, func() (any, error) {
		found, invalid, err := p.driver.GetParameters(ctx, names, withDecryption)
		return result{found, invalid}, err
	})
	if err != nil {
		return nil, nil, err
	}

	r := out.(result)

	return r.found, r.invalid, nil
}

// GetParametersByPath retrieves parameters under a hierarchical path.
func (p *ParameterStore) GetParametersByPath(ctx context.Context, in driver.GetByPathInput) ([]driver.Parameter, error) {
	out, err := p.do(ctx, "GetParametersByPath", in, func() (any, error) {
		return p.driver.GetParametersByPath(ctx, in)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Parameter), nil
}

// DeleteParameter deletes a parameter by name.
func (p *ParameterStore) DeleteParameter(ctx context.Context, name string) error {
	_, err := p.do(ctx, "DeleteParameter", name, func() (any, error) {
		return nil, p.driver.DeleteParameter(ctx, name)
	})

	return err
}

// DeleteParameters deletes multiple parameters, returning deleted and invalid names.
func (p *ParameterStore) DeleteParameters(ctx context.Context, names []string) ([]string, []string, error) {
	type result struct {
		deleted []string
		invalid []string
	}

	out, err := p.do(ctx, "DeleteParameters", names, func() (any, error) {
		deleted, invalid, err := p.driver.DeleteParameters(ctx, names)
		return result{deleted, invalid}, err
	})
	if err != nil {
		return nil, nil, err
	}

	r := out.(result)

	return r.deleted, r.invalid, nil
}

// DescribeParameters lists parameter metadata.
func (p *ParameterStore) DescribeParameters(ctx context.Context) ([]driver.ParameterMetadata, error) {
	out, err := p.do(ctx, "DescribeParameters", nil, func() (any, error) {
		return p.driver.DescribeParameters(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.ParameterMetadata), nil
}

// GetParameterHistory returns every version of a parameter, oldest first.
func (p *ParameterStore) GetParameterHistory(ctx context.Context, name string) ([]driver.Parameter, error) {
	out, err := p.do(ctx, "GetParameterHistory", name, func() (any, error) {
		return p.driver.GetParameterHistory(ctx, name)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Parameter), nil
}

// LabelParameterVersion attaches labels to a specific version (0 = latest).
func (p *ParameterStore) LabelParameterVersion(ctx context.Context, name string, version int64, labels []string) (int64, []string, error) {
	type result struct {
		applied int64
		invalid []string
	}

	out, err := p.do(ctx, "LabelParameterVersion", map[string]any{"name": name, "version": version}, func() (any, error) {
		applied, invalid, err := p.driver.LabelParameterVersion(ctx, name, version, labels)
		return result{applied, invalid}, err
	})
	if err != nil {
		return 0, nil, err
	}

	r := out.(result)

	return r.applied, r.invalid, nil
}
