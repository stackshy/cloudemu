// Package serverless provides a portable serverless functions API with cross-cutting concerns.
package serverless

import (
	"context"
	"time"

	"github.com/NitinKumar004/cloudemu/inject"
	"github.com/NitinKumar004/cloudemu/metrics"
	"github.com/NitinKumar004/cloudemu/ratelimit"
	"github.com/NitinKumar004/cloudemu/recorder"
	"github.com/NitinKumar004/cloudemu/serverless/driver"
)

// Serverless is the portable serverless type wrapping a driver.
type Serverless struct {
	driver   driver.Serverless
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewServerless creates a new portable Serverless.
func NewServerless(d driver.Serverless, opts ...Option) *Serverless {
	s := &Serverless{driver: d}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type Option func(*Serverless)

func WithRecorder(r *recorder.Recorder) Option     { return func(s *Serverless) { s.recorder = r } }
func WithMetrics(m *metrics.Collector) Option      { return func(s *Serverless) { s.metrics = m } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(s *Serverless) { s.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(s *Serverless) { s.injector = i } }
func WithLatency(d time.Duration) Option           { return func(s *Serverless) { s.latency = d } }

func (s *Serverless) do(ctx context.Context, op string, input interface{}, fn func() (interface{}, error)) (interface{}, error) {
	start := time.Now()
	if s.injector != nil {
		if err := s.injector.Check("serverless", op); err != nil {
			s.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}
	if s.limiter != nil {
		if err := s.limiter.Allow(); err != nil {
			s.record(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}
	if s.latency > 0 {
		time.Sleep(s.latency)
	}
	out, err := fn()
	dur := time.Since(start)
	if s.metrics != nil {
		labels := map[string]string{"service": "serverless", "operation": op}
		s.metrics.Counter("calls_total", 1, labels)
		s.metrics.Histogram("call_duration", dur, labels)
		if err != nil {
			s.metrics.Counter("errors_total", 1, labels)
		}
	}
	s.record(op, input, out, err, dur)
	return out, err
}

func (s *Serverless) record(op string, input, output interface{}, err error, dur time.Duration) {
	if s.recorder != nil {
		s.recorder.Record("serverless", op, input, output, err, dur)
	}
}

func (s *Serverless) CreateFunction(ctx context.Context, config driver.FunctionConfig) (*driver.FunctionInfo, error) {
	out, err := s.do(ctx, "CreateFunction", config, func() (interface{}, error) { return s.driver.CreateFunction(ctx, config) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.FunctionInfo), nil
}

func (s *Serverless) DeleteFunction(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteFunction", name, func() (interface{}, error) { return nil, s.driver.DeleteFunction(ctx, name) })
	return err
}

func (s *Serverless) GetFunction(ctx context.Context, name string) (*driver.FunctionInfo, error) {
	out, err := s.do(ctx, "GetFunction", name, func() (interface{}, error) { return s.driver.GetFunction(ctx, name) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.FunctionInfo), nil
}

func (s *Serverless) ListFunctions(ctx context.Context) ([]driver.FunctionInfo, error) {
	out, err := s.do(ctx, "ListFunctions", nil, func() (interface{}, error) { return s.driver.ListFunctions(ctx) })
	if err != nil {
		return nil, err
	}
	return out.([]driver.FunctionInfo), nil
}

func (s *Serverless) UpdateFunction(ctx context.Context, name string, config driver.FunctionConfig) (*driver.FunctionInfo, error) {
	out, err := s.do(ctx, "UpdateFunction", config, func() (interface{}, error) { return s.driver.UpdateFunction(ctx, name, config) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.FunctionInfo), nil
}

func (s *Serverless) Invoke(ctx context.Context, input driver.InvokeInput) (*driver.InvokeOutput, error) {
	out, err := s.do(ctx, "Invoke", input, func() (interface{}, error) { return s.driver.Invoke(ctx, input) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.InvokeOutput), nil
}

func (s *Serverless) RegisterHandler(name string, handler driver.HandlerFunc) {
	s.driver.RegisterHandler(name, handler)
}
