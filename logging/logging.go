// Package logging provides a portable logging API with cross-cutting concerns.
package logging

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/logging/driver"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Logging is the portable logging type wrapping a driver with cross-cutting concerns.
type Logging struct {
	driver   driver.Logging
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewLogging creates a new portable Logging wrapping the given driver.
func NewLogging(d driver.Logging, opts ...Option) *Logging {
	l := &Logging{driver: d}
	for _, opt := range opts {
		opt(l)
	}

	return l
}

// Option configures a portable Logging.
type Option func(*Logging)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(l *Logging) { l.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(l *Logging) { l.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(rl *ratelimit.Limiter) Option { return func(l *Logging) { l.limiter = rl } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(l *Logging) { l.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(l *Logging) { l.latency = d } }

func (l *Logging) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if l.injector != nil {
		if err := l.injector.Check("logging", op); err != nil {
			l.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if l.limiter != nil {
		if err := l.limiter.Allow(); err != nil {
			l.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if l.latency > 0 {
		time.Sleep(l.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if l.metrics != nil {
		labels := map[string]string{"service": "logging", "operation": op}
		l.metrics.Counter("calls_total", 1, labels)
		l.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			l.metrics.Counter("errors_total", 1, labels)
		}
	}

	l.rec(op, input, out, err, dur)

	return out, err
}

func (l *Logging) rec(op string, input, output any, err error, dur time.Duration) {
	if l.recorder != nil {
		l.recorder.Record("logging", op, input, output, err, dur)
	}
}

// CreateLogGroup creates a new log group.
func (l *Logging) CreateLogGroup(ctx context.Context, config driver.LogGroupConfig) (*driver.LogGroupInfo, error) {
	out, err := l.do(ctx, "CreateLogGroup", config, func() (any, error) { return l.driver.CreateLogGroup(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.LogGroupInfo), nil
}

// DeleteLogGroup deletes a log group.
func (l *Logging) DeleteLogGroup(ctx context.Context, name string) error {
	_, err := l.do(ctx, "DeleteLogGroup", name, func() (any, error) { return nil, l.driver.DeleteLogGroup(ctx, name) })
	return err
}

// GetLogGroup retrieves log group info.
func (l *Logging) GetLogGroup(ctx context.Context, name string) (*driver.LogGroupInfo, error) {
	out, err := l.do(ctx, "GetLogGroup", name, func() (any, error) { return l.driver.GetLogGroup(ctx, name) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.LogGroupInfo), nil
}

// ListLogGroups lists all log groups.
func (l *Logging) ListLogGroups(ctx context.Context) ([]driver.LogGroupInfo, error) {
	out, err := l.do(ctx, "ListLogGroups", nil, func() (any, error) { return l.driver.ListLogGroups(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.LogGroupInfo), nil
}

// CreateLogStream creates a new log stream in a log group.
func (l *Logging) CreateLogStream(ctx context.Context, logGroup, streamName string) (*driver.LogStreamInfo, error) {
	out, err := l.do(ctx, "CreateLogStream", map[string]string{"logGroup": logGroup, "stream": streamName}, func() (any, error) {
		return l.driver.CreateLogStream(ctx, logGroup, streamName)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.LogStreamInfo), nil
}

// DeleteLogStream deletes a log stream from a log group.
func (l *Logging) DeleteLogStream(ctx context.Context, logGroup, streamName string) error {
	_, err := l.do(ctx, "DeleteLogStream", map[string]string{"logGroup": logGroup, "stream": streamName}, func() (any, error) {
		return nil, l.driver.DeleteLogStream(ctx, logGroup, streamName)
	})

	return err
}

// ListLogStreams lists all log streams in a log group.
func (l *Logging) ListLogStreams(ctx context.Context, logGroup string) ([]driver.LogStreamInfo, error) {
	out, err := l.do(ctx, "ListLogStreams", logGroup, func() (any, error) { return l.driver.ListLogStreams(ctx, logGroup) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.LogStreamInfo), nil
}

// PutLogEvents writes log events to a stream.
func (l *Logging) PutLogEvents(ctx context.Context, logGroup, streamName string, events []driver.LogEvent) error {
	_, err := l.do(ctx, "PutLogEvents", map[string]string{"logGroup": logGroup, "stream": streamName}, func() (any, error) {
		return nil, l.driver.PutLogEvents(ctx, logGroup, streamName, events)
	})

	return err
}

// GetLogEvents retrieves log events matching the query.
func (l *Logging) GetLogEvents(ctx context.Context, input *driver.LogQueryInput) ([]driver.LogEvent, error) {
	out, err := l.do(ctx, "GetLogEvents", input, func() (any, error) { return l.driver.GetLogEvents(ctx, input) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.LogEvent), nil
}
