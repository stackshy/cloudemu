// Package eventbus provides a portable event bus API with cross-cutting concerns.
package eventbus

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/eventbus/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// EventBus is the portable event bus type wrapping a driver with cross-cutting concerns.
type EventBus struct {
	driver   driver.EventBus
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewEventBus creates a new portable EventBus wrapping the given driver.
func NewEventBus(d driver.EventBus, opts ...Option) *EventBus {
	eb := &EventBus{driver: d}
	for _, opt := range opts {
		opt(eb)
	}

	return eb
}

// Option configures a portable EventBus.
type Option func(*EventBus)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(eb *EventBus) { eb.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(eb *EventBus) { eb.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(eb *EventBus) { eb.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(eb *EventBus) { eb.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(eb *EventBus) { eb.latency = d } }

func (eb *EventBus) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if eb.injector != nil {
		if err := eb.injector.Check("eventbus", op); err != nil {
			eb.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if eb.limiter != nil {
		if err := eb.limiter.Allow(); err != nil {
			eb.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if eb.latency > 0 {
		time.Sleep(eb.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if eb.metrics != nil {
		labels := map[string]string{"service": "eventbus", "operation": op}
		eb.metrics.Counter("calls_total", 1, labels)
		eb.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			eb.metrics.Counter("errors_total", 1, labels)
		}
	}

	eb.rec(op, input, out, err, dur)

	return out, err
}

func (eb *EventBus) rec(op string, input, output any, err error, dur time.Duration) {
	if eb.recorder != nil {
		eb.recorder.Record("eventbus", op, input, output, err, dur)
	}
}

// CreateEventBus creates a new event bus.
func (eb *EventBus) CreateEventBus(ctx context.Context, config driver.EventBusConfig) (*driver.EventBusInfo, error) {
	out, err := eb.do(ctx, "CreateEventBus", config, func() (any, error) {
		return eb.driver.CreateEventBus(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EventBusInfo), nil
}

// DeleteEventBus deletes an event bus.
func (eb *EventBus) DeleteEventBus(ctx context.Context, name string) error {
	_, err := eb.do(ctx, "DeleteEventBus", name, func() (any, error) {
		return nil, eb.driver.DeleteEventBus(ctx, name)
	})

	return err
}

// GetEventBus retrieves event bus info.
func (eb *EventBus) GetEventBus(ctx context.Context, name string) (*driver.EventBusInfo, error) {
	out, err := eb.do(ctx, "GetEventBus", name, func() (any, error) {
		return eb.driver.GetEventBus(ctx, name)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.EventBusInfo), nil
}

// ListEventBuses lists all event buses.
func (eb *EventBus) ListEventBuses(ctx context.Context) ([]driver.EventBusInfo, error) {
	out, err := eb.do(ctx, "ListEventBuses", nil, func() (any, error) {
		return eb.driver.ListEventBuses(ctx)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.EventBusInfo), nil
}

// PutRule creates or updates a rule.
func (eb *EventBus) PutRule(ctx context.Context, config *driver.RuleConfig) (*driver.Rule, error) {
	out, err := eb.do(ctx, "PutRule", config, func() (any, error) {
		return eb.driver.PutRule(ctx, config)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Rule), nil
}

// DeleteRule deletes a rule.
func (eb *EventBus) DeleteRule(ctx context.Context, eventBus, ruleName string) error {
	_, err := eb.do(ctx, "DeleteRule", map[string]string{"eventBus": eventBus, "rule": ruleName}, func() (any, error) {
		return nil, eb.driver.DeleteRule(ctx, eventBus, ruleName)
	})

	return err
}

// GetRule retrieves a rule.
func (eb *EventBus) GetRule(ctx context.Context, eventBus, ruleName string) (*driver.Rule, error) {
	out, err := eb.do(ctx, "GetRule", map[string]string{"eventBus": eventBus, "rule": ruleName}, func() (any, error) {
		return eb.driver.GetRule(ctx, eventBus, ruleName)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.Rule), nil
}

// ListRules lists all rules for an event bus.
func (eb *EventBus) ListRules(ctx context.Context, eventBus string) ([]driver.Rule, error) {
	out, err := eb.do(ctx, "ListRules", eventBus, func() (any, error) {
		return eb.driver.ListRules(ctx, eventBus)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Rule), nil
}

// EnableRule enables a rule.
func (eb *EventBus) EnableRule(ctx context.Context, eventBus, ruleName string) error {
	_, err := eb.do(ctx, "EnableRule", map[string]string{"eventBus": eventBus, "rule": ruleName}, func() (any, error) {
		return nil, eb.driver.EnableRule(ctx, eventBus, ruleName)
	})

	return err
}

// DisableRule disables a rule.
func (eb *EventBus) DisableRule(ctx context.Context, eventBus, ruleName string) error {
	_, err := eb.do(ctx, "DisableRule", map[string]string{"eventBus": eventBus, "rule": ruleName}, func() (any, error) {
		return nil, eb.driver.DisableRule(ctx, eventBus, ruleName)
	})

	return err
}

// PutTargets adds targets to a rule.
func (eb *EventBus) PutTargets(
	ctx context.Context, eventBus, ruleName string, targets []driver.Target,
) error {
	_, err := eb.do(ctx, "PutTargets", map[string]string{"eventBus": eventBus, "rule": ruleName}, func() (any, error) {
		return nil, eb.driver.PutTargets(ctx, eventBus, ruleName, targets)
	})

	return err
}

// RemoveTargets removes targets from a rule.
func (eb *EventBus) RemoveTargets(ctx context.Context, eventBus, ruleName string, targetIDs []string) error {
	_, err := eb.do(ctx, "RemoveTargets", map[string]string{"eventBus": eventBus, "rule": ruleName}, func() (any, error) {
		return nil, eb.driver.RemoveTargets(ctx, eventBus, ruleName, targetIDs)
	})

	return err
}

// ListTargets lists targets for a rule.
func (eb *EventBus) ListTargets(ctx context.Context, eventBus, ruleName string) ([]driver.Target, error) {
	out, err := eb.do(ctx, "ListTargets", map[string]string{"eventBus": eventBus, "rule": ruleName}, func() (any, error) {
		return eb.driver.ListTargets(ctx, eventBus, ruleName)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Target), nil
}

// PutEvents publishes events to the event bus.
func (eb *EventBus) PutEvents(ctx context.Context, events []driver.Event) (*driver.PublishResult, error) {
	out, err := eb.do(ctx, "PutEvents", events, func() (any, error) {
		return eb.driver.PutEvents(ctx, events)
	})
	if err != nil {
		return nil, err
	}

	return out.(*driver.PublishResult), nil
}

// GetEventHistory retrieves event history for replay.
func (eb *EventBus) GetEventHistory(ctx context.Context, eventBus string, limit int) ([]driver.Event, error) {
	out, err := eb.do(ctx, "GetEventHistory", map[string]any{"eventBus": eventBus, "limit": limit}, func() (any, error) {
		return eb.driver.GetEventHistory(ctx, eventBus, limit)
	})
	if err != nil {
		return nil, err
	}

	return out.([]driver.Event), nil
}
