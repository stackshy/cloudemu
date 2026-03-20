// Package notification provides a portable notification API with cross-cutting concerns.
package notification

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/notification/driver"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
)

// Notification is the portable notification type wrapping a driver with cross-cutting concerns.
type Notification struct {
	driver   driver.Notification
	recorder *recorder.Recorder
	metrics  *metrics.Collector
	limiter  *ratelimit.Limiter
	injector *inject.Injector
	latency  time.Duration
}

// NewNotification creates a new portable Notification wrapping the given driver.
func NewNotification(d driver.Notification, opts ...Option) *Notification {
	n := &Notification{driver: d}
	for _, opt := range opts {
		opt(n)
	}

	return n
}

// Option configures a portable Notification.
type Option func(*Notification)

// WithRecorder sets the recorder.
func WithRecorder(r *recorder.Recorder) Option { return func(n *Notification) { n.recorder = r } }

// WithMetrics sets the metrics collector.
func WithMetrics(m *metrics.Collector) Option { return func(n *Notification) { n.metrics = m } }

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option { return func(n *Notification) { n.limiter = l } }

// WithErrorInjection sets the error injector.
func WithErrorInjection(i *inject.Injector) Option { return func(n *Notification) { n.injector = i } }

// WithLatency sets simulated latency.
func WithLatency(d time.Duration) Option { return func(n *Notification) { n.latency = d } }

func (n *Notification) do(_ context.Context, op string, input any, fn func() (any, error)) (any, error) {
	start := time.Now()

	if n.injector != nil {
		if err := n.injector.Check("notification", op); err != nil {
			n.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if n.limiter != nil {
		if err := n.limiter.Allow(); err != nil {
			n.rec(op, input, nil, err, time.Since(start))
			return nil, err
		}
	}

	if n.latency > 0 {
		time.Sleep(n.latency)
	}

	out, err := fn()
	dur := time.Since(start)

	if n.metrics != nil {
		labels := map[string]string{"service": "notification", "operation": op}
		n.metrics.Counter("calls_total", 1, labels)
		n.metrics.Histogram("call_duration", dur, labels)

		if err != nil {
			n.metrics.Counter("errors_total", 1, labels)
		}
	}

	n.rec(op, input, out, err, dur)

	return out, err
}

func (n *Notification) rec(op string, input, output any, err error, dur time.Duration) {
	if n.recorder != nil {
		n.recorder.Record("notification", op, input, output, err, dur)
	}
}

// CreateTopic creates a new notification topic.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (n *Notification) CreateTopic(ctx context.Context, config driver.TopicConfig) (*driver.TopicInfo, error) {
	out, err := n.do(ctx, "CreateTopic", config, func() (any, error) { return n.driver.CreateTopic(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.TopicInfo), nil
}

// DeleteTopic deletes a notification topic.
func (n *Notification) DeleteTopic(ctx context.Context, id string) error {
	_, err := n.do(ctx, "DeleteTopic", id, func() (any, error) { return nil, n.driver.DeleteTopic(ctx, id) })
	return err
}

// GetTopic retrieves topic info.
func (n *Notification) GetTopic(ctx context.Context, id string) (*driver.TopicInfo, error) {
	out, err := n.do(ctx, "GetTopic", id, func() (any, error) { return n.driver.GetTopic(ctx, id) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.TopicInfo), nil
}

// ListTopics lists all topics.
func (n *Notification) ListTopics(ctx context.Context) ([]driver.TopicInfo, error) {
	out, err := n.do(ctx, "ListTopics", nil, func() (any, error) { return n.driver.ListTopics(ctx) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.TopicInfo), nil
}

// Subscribe creates a subscription to a topic.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (n *Notification) Subscribe(ctx context.Context, config driver.SubscriptionConfig) (*driver.SubscriptionInfo, error) {
	out, err := n.do(ctx, "Subscribe", config, func() (any, error) { return n.driver.Subscribe(ctx, config) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.SubscriptionInfo), nil
}

// Unsubscribe removes a subscription.
func (n *Notification) Unsubscribe(ctx context.Context, subscriptionID string) error {
	_, err := n.do(ctx, "Unsubscribe", subscriptionID, func() (any, error) { return nil, n.driver.Unsubscribe(ctx, subscriptionID) })
	return err
}

// ListSubscriptions lists all subscriptions for a topic.
func (n *Notification) ListSubscriptions(ctx context.Context, topicID string) ([]driver.SubscriptionInfo, error) {
	out, err := n.do(ctx, "ListSubscriptions", topicID, func() (any, error) { return n.driver.ListSubscriptions(ctx, topicID) })
	if err != nil {
		return nil, err
	}

	return out.([]driver.SubscriptionInfo), nil
}

// Publish publishes a message to a topic.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (n *Notification) Publish(ctx context.Context, input driver.PublishInput) (*driver.PublishOutput, error) {
	out, err := n.do(ctx, "Publish", input, func() (any, error) { return n.driver.Publish(ctx, input) })
	if err != nil {
		return nil, err
	}

	return out.(*driver.PublishOutput), nil
}
