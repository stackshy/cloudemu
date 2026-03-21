package notification

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/notification/driver"
	"github.com/stackshy/cloudemu/providers/aws/sns"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestNotification(opts ...Option) *Notification {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewNotification(sns.New(o), opts...)
}

func createTopicVia(t *testing.T, n *Notification, name string) *driver.TopicInfo {
	t.Helper()

	info, err := n.CreateTopic(context.Background(), driver.TopicConfig{Name: name})
	require.NoError(t, err)

	return info
}

func TestCreateTopicPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	info, err := n.CreateTopic(ctx, driver.TopicConfig{Name: "test-topic", DisplayName: "Test"})
	require.NoError(t, err)

	assert.NotEmpty(t, info.ID)
	assert.NotEmpty(t, info.ResourceID)
	assert.Equal(t, "test-topic", info.Name)
	assert.Equal(t, "Test", info.DisplayName)
}

func TestCreateTopicPortableError(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	_, err := n.CreateTopic(ctx, driver.TopicConfig{})
	require.Error(t, err)
}

func TestDeleteTopicPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	info := createTopicVia(t, n, "del-topic")

	err := n.DeleteTopic(ctx, info.Name)
	require.NoError(t, err)
}

func TestDeleteTopicPortableNotFound(t *testing.T) {
	n := newTestNotification()

	err := n.DeleteTopic(context.Background(), "arn:aws:sns:us-east-1:123456789012:nope")
	require.Error(t, err)
}

func TestGetTopicPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	created := createTopicVia(t, n, "get-topic")

	info, err := n.GetTopic(ctx, created.Name)
	require.NoError(t, err)
	assert.Equal(t, "get-topic", info.Name)
}

func TestGetTopicPortableNotFound(t *testing.T) {
	n := newTestNotification()

	_, err := n.GetTopic(context.Background(), "arn:aws:sns:us-east-1:123456789012:nope")
	require.Error(t, err)
}

func TestListTopicsPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topics, err := n.ListTopics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(topics))

	createTopicVia(t, n, "t1")
	createTopicVia(t, n, "t2")

	topics, err = n.ListTopics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(topics))
}

func TestSubscribePortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "sub-topic")

	sub, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "a@b.com",
	})
	require.NoError(t, err)

	assert.NotEmpty(t, sub.ID)
	assert.Equal(t, "email", sub.Protocol)
	assert.Equal(t, "confirmed", sub.Status)
}

func TestSubscribePortableError(t *testing.T) {
	n := newTestNotification()

	_, err := n.Subscribe(context.Background(), driver.SubscriptionConfig{
		TopicID: "arn:aws:sns:us-east-1:123456789012:nope", Protocol: "email", Endpoint: "a@b.com",
	})
	require.Error(t, err)
}

func TestUnsubscribePortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "unsub-topic")

	sub, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "a@b.com",
	})
	require.NoError(t, err)

	err = n.Unsubscribe(ctx, sub.ID)
	require.NoError(t, err)
}

func TestUnsubscribePortableNotFound(t *testing.T) {
	n := newTestNotification()

	err := n.Unsubscribe(context.Background(), "nonexistent-sub")
	require.Error(t, err)
}

func TestListSubscriptionsPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "list-subs")

	_, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "x@y.com",
	})
	require.NoError(t, err)

	subs, err := n.ListSubscriptions(ctx, topic.Name)
	require.NoError(t, err)
	assert.Equal(t, 1, len(subs))
}

func TestListSubscriptionsPortableNotFound(t *testing.T) {
	n := newTestNotification()

	_, err := n.ListSubscriptions(context.Background(), "arn:aws:sns:us-east-1:123456789012:nope")
	require.Error(t, err)
}

func TestPublishPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "pub-topic")

	out, err := n.Publish(ctx, driver.PublishInput{
		TopicID: topic.Name,
		Message: "hello",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.MessageID)
}

func TestPublishPortableError(t *testing.T) {
	n := newTestNotification()

	_, err := n.Publish(context.Background(), driver.PublishInput{
		TopicID: "arn:aws:sns:us-east-1:123456789012:nope",
		Message: "hello",
	})
	require.Error(t, err)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))
	ctx := context.Background()

	_, _ = n.CreateTopic(ctx, driver.TopicConfig{Name: "rec-topic"})

	assert.Equal(t, 1, rec.CallCount())

	calls := rec.CallsFor("notification", "CreateTopic")
	assert.Equal(t, 1, len(calls))
	assert.Equal(t, "notification", calls[0].Service)
	assert.Equal(t, "CreateTopic", calls[0].Operation)
}

func TestWithRecorderMultipleOps(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))
	ctx := context.Background()

	topic, err := n.CreateTopic(ctx, driver.TopicConfig{Name: "rec-multi"})
	require.NoError(t, err)

	_, _ = n.GetTopic(ctx, topic.Name)
	_, _ = n.ListTopics(ctx)
	_ = n.DeleteTopic(ctx, topic.Name)

	assert.Equal(t, 4, rec.CallCount())
	assert.Equal(t, 1, rec.CallCountFor("notification", "CreateTopic"))
	assert.Equal(t, 1, rec.CallCountFor("notification", "GetTopic"))
	assert.Equal(t, 1, rec.CallCountFor("notification", "ListTopics"))
	assert.Equal(t, 1, rec.CallCountFor("notification", "DeleteTopic"))
}

func TestWithRecorderRecordsErrors(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))

	_, _ = n.CreateTopic(context.Background(), driver.TopicConfig{})

	calls := rec.CallsFor("notification", "CreateTopic")
	assert.Equal(t, 1, len(calls))
	assert.NotNil(t, calls[0].Error)
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	n := newTestNotification(WithMetrics(mc))
	ctx := context.Background()

	_, _ = n.CreateTopic(ctx, driver.TopicConfig{Name: "met-topic"})

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").
		ByLabel("service", "notification").
		ByLabel("operation", "CreateTopic").
		Count()
	assert.Equal(t, 1, callsCount)

	durCount := q.ByName("call_duration").
		ByLabel("service", "notification").
		ByLabel("operation", "CreateTopic").
		Count()
	assert.Equal(t, 1, durCount)
}

func TestWithMetricsRecordsErrors(t *testing.T) {
	mc := metrics.NewCollector()
	n := newTestNotification(WithMetrics(mc))

	_, _ = n.CreateTopic(context.Background(), driver.TopicConfig{})

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").
		ByLabel("service", "notification").
		ByLabel("operation", "CreateTopic").
		Count()
	assert.Equal(t, 1, errCount)
}

func TestWithMetricsNoErrorMetricOnSuccess(t *testing.T) {
	mc := metrics.NewCollector()
	n := newTestNotification(WithMetrics(mc))

	_, _ = n.CreateTopic(context.Background(), driver.TopicConfig{Name: "ok"})

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").
		ByLabel("service", "notification").
		ByLabel("operation", "CreateTopic").
		Count()
	assert.Equal(t, 0, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	injectedErr := fmt.Errorf("injected failure")
	inj.Set("notification", "CreateTopic", injectedErr, inject.Always{})

	n := newTestNotification(WithErrorInjection(inj))

	_, err := n.CreateTopic(context.Background(), driver.TopicConfig{Name: "inj-topic"})
	require.Error(t, err)
	assert.Equal(t, injectedErr.Error(), err.Error())
}

func TestWithErrorInjectionSelectiveOps(t *testing.T) {
	inj := inject.NewInjector()
	injectedErr := fmt.Errorf("publish broken")
	inj.Set("notification", "Publish", injectedErr, inject.Always{})

	n := newTestNotification(WithErrorInjection(inj))
	ctx := context.Background()

	// CreateTopic should succeed (not injected).
	topic, err := n.CreateTopic(ctx, driver.TopicConfig{Name: "sel-topic"})
	require.NoError(t, err)

	// Publish should fail (injected).
	_, err = n.Publish(ctx, driver.PublishInput{
		TopicID: topic.Name,
		Message: "hello",
	})
	require.Error(t, err)
	assert.Equal(t, injectedErr.Error(), err.Error())
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	inj := inject.NewInjector()
	rec := recorder.New()
	injectedErr := fmt.Errorf("injected")
	inj.Set("notification", "GetTopic", injectedErr, inject.Always{})

	n := newTestNotification(WithErrorInjection(inj), WithRecorder(rec))

	_, _ = n.GetTopic(context.Background(), "any-id")

	calls := rec.CallsFor("notification", "GetTopic")
	assert.Equal(t, 1, len(calls))
	assert.NotNil(t, calls[0].Error)
}

func TestWithErrorInjectionAndMetrics(t *testing.T) {
	inj := inject.NewInjector()
	mc := metrics.NewCollector()
	injectedErr := fmt.Errorf("injected")
	inj.Set("notification", "DeleteTopic", injectedErr, inject.Always{})

	n := newTestNotification(WithErrorInjection(inj), WithMetrics(mc))

	_ = n.DeleteTopic(context.Background(), "any-id")

	// The injected error should NOT record metrics since it returns before the driver call.
	q := metrics.NewQuery(mc)

	// calls_total should not be recorded for injected errors.
	callsCount := q.ByName("calls_total").
		ByLabel("service", "notification").
		ByLabel("operation", "DeleteTopic").
		Count()
	assert.Equal(t, 0, callsCount)
}

func TestWithLatency(t *testing.T) {
	n := newTestNotification(WithLatency(1 * time.Millisecond))
	ctx := context.Background()

	start := time.Now()

	_, _ = n.CreateTopic(ctx, driver.TopicConfig{Name: "lat-topic"})

	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 1*time.Millisecond)
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()

	n := newTestNotification(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
	)
	ctx := context.Background()

	topic, err := n.CreateTopic(ctx, driver.TopicConfig{Name: "composed"})
	require.NoError(t, err)
	assert.NotEmpty(t, topic.ID)

	assert.Equal(t, 1, rec.CallCount())

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").
		ByLabel("service", "notification").
		Count()
	assert.Equal(t, 1, callsCount)
}

func TestSubscribeViaPortableAllPaths(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))
	ctx := context.Background()

	topic := createTopicVia(t, n, "full-flow")

	sub, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "u@e.com",
	})
	require.NoError(t, err)

	subs, err := n.ListSubscriptions(ctx, topic.Name)
	require.NoError(t, err)
	assert.Equal(t, 1, len(subs))

	err = n.Unsubscribe(ctx, sub.ID)
	require.NoError(t, err)

	out, err := n.Publish(ctx, driver.PublishInput{
		TopicID: topic.Name, Message: "test msg",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, out.MessageID)

	// Verify all operations were recorded.
	assert.Equal(t, 5, rec.CallCount())
	assert.Equal(t, 1, rec.CallCountFor("notification", "Subscribe"))
	assert.Equal(t, 1, rec.CallCountFor("notification", "ListSubscriptions"))
	assert.Equal(t, 1, rec.CallCountFor("notification", "Unsubscribe"))
	assert.Equal(t, 1, rec.CallCountFor("notification", "Publish"))
}
