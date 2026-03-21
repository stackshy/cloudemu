// Package secrets provides a portable secret management API with cross-cutting concerns.
package secrets

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/secrets/driver"
)

// Secrets is the portable secrets type wrapping a driver with cross-cutting concerns.
type Secrets struct {
	driver   driver.Secrets
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewSecrets creates a new portable Secrets wrapping the given driver.
func NewSecrets(d driver.Secrets, opts ...Option) *Secrets {
	s := &Secrets{driver: d}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Option configures a portable Secrets.
type Option func(*Secrets)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(s *Secrets) { s.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(s *Secrets) { s.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(s *Secrets) { s.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(s *Secrets) { s.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(s *Secrets) { s.latency = d } }

func (s *Secrets) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if s.injector != nil {
		if err := s.injector.Check("secrets", op); err != nil {
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
		labels := map[string]string{"service": "secrets", "operation": op}
		s.metrics.Counter("calls_total", 1, labels)
		s.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			s.metrics.Counter("errors_total", 1, labels)
		}
	}

	s.rec(op, input, out, err, dur)

	return out, err
}

func (s *Secrets) rec(op string, input, output any, err error, dur time.Duration) {
	if s.recorder != nil {
		s.recorder.Record("secrets", op, input, output, err, dur)
	}
}

// CreateSecret creates a new secret with an initial value.
func (s *Secrets) CreateSecret(ctx context.Context, config driver.SecretConfig, value []byte) (*driver.SecretInfo, error) {
	out, err := s.do(ctx, "CreateSecret", config, func() (any, error) { return s.driver.CreateSecret(ctx, config, value) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.SecretInfo), nil
}

// DeleteSecret deletes a secret.
func (s *Secrets) DeleteSecret(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteSecret", name, func() (any, error) { return nil, s.driver.DeleteSecret(ctx, name) })
	return err
}

// GetSecret retrieves secret metadata.
func (s *Secrets) GetSecret(ctx context.Context, name string) (*driver.SecretInfo, error) {
	out, err := s.do(ctx, "GetSecret", name, func() (any, error) { return s.driver.GetSecret(ctx, name) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.SecretInfo), nil
}

// ListSecrets lists all secrets.
func (s *Secrets) ListSecrets(ctx context.Context) ([]driver.SecretInfo, error) {
	out, err := s.do(ctx, "ListSecrets", nil, func() (any, error) { return s.driver.ListSecrets(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.SecretInfo), nil
}

// PutSecretValue stores a new version of a secret value.
func (s *Secrets) PutSecretValue(ctx context.Context, name string, value []byte) (*driver.SecretVersion, error) {
	out, err := s.do(ctx, "PutSecretValue", name, func() (any, error) { return s.driver.PutSecretValue(ctx, name, value) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.SecretVersion), nil
}

// GetSecretValue retrieves a secret value by version. Empty versionID returns the current version.
func (s *Secrets) GetSecretValue(ctx context.Context, name, versionID string) (*driver.SecretVersion, error) {
	out, err := s.do(ctx, "GetSecretValue", map[string]string{"name": name, "versionID": versionID}, func() (any, error) {
		return s.driver.GetSecretValue(ctx, name, versionID)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.SecretVersion), nil
}

// ListSecretVersions lists all versions of a secret.
func (s *Secrets) ListSecretVersions(ctx context.Context, name string) ([]driver.SecretVersion, error) {
	out, err := s.do(ctx, "ListSecretVersions", name, func() (any, error) { return s.driver.ListSecretVersions(ctx, name) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.SecretVersion), nil
}
