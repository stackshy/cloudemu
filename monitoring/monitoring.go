// Package monitoring provides a portable monitoring API with cross-cutting concerns.
package monitoring

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Monitoring is the portable monitoring type wrapping a driver.
type Monitoring struct {
	driver   driver.Monitoring
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

func NewMonitoring(d driver.Monitoring, opts ...Option) *Monitoring {
	m := &Monitoring{driver: d}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

type Option func(*Monitoring)

func WithRecorder(r *recorder.Recorder) Option     { return func(m *Monitoring) { m.recorder = r } }
func WithMetrics(mc *metrics.Collector) Option     { return func(m *Monitoring) { m.metrics = mc } }
func WithRateLimiter(l *ratelimit.Limiter) Option  { return func(m *Monitoring) { m.limiter = l } }
func WithErrorInjection(i *inject.Injector) Option { return func(m *Monitoring) { m.injector = i } }
func WithLatency(d time.Duration) Option           { return func(m *Monitoring) { m.latency = d } }

func (m *Monitoring) do(ctx context.Context, op string, input interface{}, fn func() (interface{}, error)) (interface{}, error) {
	start := time.Now()
	if m.injector != nil {
		if err := m.injector.Check("monitoring", op); err != nil {
			m.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}
	if m.limiter != nil {
		if err := m.limiter.Allow(); err != nil {
			m.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
	out, err := fn()
	dur := time.Since(start)
	if m.metrics != nil {
		labels := map[string]string{"service": "monitoring", "operation": op}
		m.metrics.Counter("calls_total", 1, labels)
		m.metrics.Histogram("call_duration", dur, labels)
		if err != nil {
			m.metrics.Counter("errors_total", 1, labels)
		}
	}
	m.rec(op, input, out, err, dur)
	return out, err
}

func (m *Monitoring) rec(op string, input, output interface{}, err error, dur time.Duration) {
	if m.recorder != nil {
		m.recorder.Record("monitoring", op, input, output, err, dur)
	}
}

func (m *Monitoring) PutMetricData(ctx context.Context, data []driver.MetricDatum) error {
	_, err := m.do(ctx, "PutMetricData", data, func() (interface{}, error) { return nil, m.driver.PutMetricData(ctx, data) })
	return err
}
func (m *Monitoring) GetMetricData(ctx context.Context, input driver.GetMetricInput) (*driver.MetricDataResult, error) {
	out, err := m.do(ctx, "GetMetricData", input, func() (interface{}, error) { return m.driver.GetMetricData(ctx, input) })
	if err != nil {
		return nil, err
	}
	return out.(*driver.MetricDataResult), nil
}
func (m *Monitoring) ListMetrics(ctx context.Context, namespace string) ([]string, error) {
	out, err := m.do(ctx, "ListMetrics", namespace, func() (interface{}, error) { return m.driver.ListMetrics(ctx, namespace) })
	if err != nil {
		return nil, err
	}
	return out.([]string), nil
}
func (m *Monitoring) CreateAlarm(ctx context.Context, config driver.AlarmConfig) error {
	_, err := m.do(ctx, "CreateAlarm", config, func() (interface{}, error) { return nil, m.driver.CreateAlarm(ctx, config) })
	return err
}
func (m *Monitoring) DeleteAlarm(ctx context.Context, name string) error {
	_, err := m.do(ctx, "DeleteAlarm", name, func() (interface{}, error) { return nil, m.driver.DeleteAlarm(ctx, name) })
	return err
}
func (m *Monitoring) DescribeAlarms(ctx context.Context, names []string) ([]driver.AlarmInfo, error) {
	out, err := m.do(ctx, "DescribeAlarms", names, func() (interface{}, error) { return m.driver.DescribeAlarms(ctx, names) })
	if err != nil {
		return nil, err
	}
	return out.([]driver.AlarmInfo), nil
}
func (m *Monitoring) SetAlarmState(ctx context.Context, name, state, reason string) error {
	_, err := m.do(ctx, "SetAlarmState", name, func() (interface{}, error) { return nil, m.driver.SetAlarmState(ctx, name, state, reason) })
	return err
}
