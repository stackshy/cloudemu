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
)

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func newTestNotification(opts ...Option) *Notification {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewNotification(sns.New(o), opts...)
}

func createTopicVia(t *testing.T, n *Notification, name string) *driver.TopicInfo {
	t.Helper()

	info, err := n.CreateTopic(context.Background(), driver.TopicConfig{Name: name})
	requireNoError(t, err)

	return info
}

func TestCreateTopicPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	info, err := n.CreateTopic(ctx, driver.TopicConfig{Name: "test-topic", DisplayName: "Test"})
	requireNoError(t, err)

	assertNotEmpty(t, info.ID)
	assertNotEmpty(t, info.ARN)
	assertEqual(t, "test-topic", info.Name)
	assertEqual(t, "Test", info.DisplayName)
}

func TestCreateTopicPortableError(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	_, err := n.CreateTopic(ctx, driver.TopicConfig{})
	assertError(t, err, true)
}

func TestDeleteTopicPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	info := createTopicVia(t, n, "del-topic")

	err := n.DeleteTopic(ctx, info.ARN)
	requireNoError(t, err)
}

func TestDeleteTopicPortableNotFound(t *testing.T) {
	n := newTestNotification()

	err := n.DeleteTopic(context.Background(), "arn:aws:sns:us-east-1:123456789012:nope")
	assertError(t, err, true)
}

func TestGetTopicPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	created := createTopicVia(t, n, "get-topic")

	info, err := n.GetTopic(ctx, created.ARN)
	requireNoError(t, err)
	assertEqual(t, "get-topic", info.Name)
}

func TestGetTopicPortableNotFound(t *testing.T) {
	n := newTestNotification()

	_, err := n.GetTopic(context.Background(), "arn:aws:sns:us-east-1:123456789012:nope")
	assertError(t, err, true)
}

func TestListTopicsPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topics, err := n.ListTopics(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(topics))

	createTopicVia(t, n, "t1")
	createTopicVia(t, n, "t2")

	topics, err = n.ListTopics(ctx)
	requireNoError(t, err)
	assertEqual(t, 2, len(topics))
}

func TestSubscribePortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "sub-topic")

	sub, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.ARN, Protocol: "email", Endpoint: "a@b.com",
	})
	requireNoError(t, err)

	assertNotEmpty(t, sub.ID)
	assertEqual(t, "email", sub.Protocol)
	assertEqual(t, "confirmed", sub.Status)
}

func TestSubscribePortableError(t *testing.T) {
	n := newTestNotification()

	_, err := n.Subscribe(context.Background(), driver.SubscriptionConfig{
		TopicID: "arn:aws:sns:us-east-1:123456789012:nope", Protocol: "email", Endpoint: "a@b.com",
	})
	assertError(t, err, true)
}

func TestUnsubscribePortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "unsub-topic")

	sub, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.ARN, Protocol: "email", Endpoint: "a@b.com",
	})
	requireNoError(t, err)

	err = n.Unsubscribe(ctx, sub.ID)
	requireNoError(t, err)
}

func TestUnsubscribePortableNotFound(t *testing.T) {
	n := newTestNotification()

	err := n.Unsubscribe(context.Background(), "nonexistent-sub")
	assertError(t, err, true)
}

func TestListSubscriptionsPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "list-subs")

	_, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.ARN, Protocol: "email", Endpoint: "x@y.com",
	})
	requireNoError(t, err)

	subs, err := n.ListSubscriptions(ctx, topic.ARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(subs))
}

func TestListSubscriptionsPortableNotFound(t *testing.T) {
	n := newTestNotification()

	_, err := n.ListSubscriptions(context.Background(), "arn:aws:sns:us-east-1:123456789012:nope")
	assertError(t, err, true)
}

func TestPublishPortable(t *testing.T) {
	n := newTestNotification()
	ctx := context.Background()

	topic := createTopicVia(t, n, "pub-topic")

	out, err := n.Publish(ctx, driver.PublishInput{
		TopicID: topic.ARN,
		Message: "hello",
	})
	requireNoError(t, err)
	assertNotEmpty(t, out.MessageID)
}

func TestPublishPortableError(t *testing.T) {
	n := newTestNotification()

	_, err := n.Publish(context.Background(), driver.PublishInput{
		TopicID: "arn:aws:sns:us-east-1:123456789012:nope",
		Message: "hello",
	})
	assertError(t, err, true)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))
	ctx := context.Background()

	_, _ = n.CreateTopic(ctx, driver.TopicConfig{Name: "rec-topic"})

	assertEqual(t, 1, rec.CallCount())

	calls := rec.CallsFor("notification", "CreateTopic")
	assertEqual(t, 1, len(calls))
	assertEqual(t, "notification", calls[0].Service)
	assertEqual(t, "CreateTopic", calls[0].Operation)
}

func TestWithRecorderMultipleOps(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))
	ctx := context.Background()

	topic, err := n.CreateTopic(ctx, driver.TopicConfig{Name: "rec-multi"})
	requireNoError(t, err)

	_, _ = n.GetTopic(ctx, topic.ARN)
	_, _ = n.ListTopics(ctx)
	_ = n.DeleteTopic(ctx, topic.ARN)

	assertEqual(t, 4, rec.CallCount())
	assertEqual(t, 1, rec.CallCountFor("notification", "CreateTopic"))
	assertEqual(t, 1, rec.CallCountFor("notification", "GetTopic"))
	assertEqual(t, 1, rec.CallCountFor("notification", "ListTopics"))
	assertEqual(t, 1, rec.CallCountFor("notification", "DeleteTopic"))
}

func TestWithRecorderRecordsErrors(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))

	_, _ = n.CreateTopic(context.Background(), driver.TopicConfig{})

	calls := rec.CallsFor("notification", "CreateTopic")
	assertEqual(t, 1, len(calls))

	if calls[0].Error == nil {
		t.Error("expected recorded error to be non-nil")
	}
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
	assertEqual(t, 1, callsCount)

	durCount := q.ByName("call_duration").
		ByLabel("service", "notification").
		ByLabel("operation", "CreateTopic").
		Count()
	assertEqual(t, 1, durCount)
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
	assertEqual(t, 1, errCount)
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
	assertEqual(t, 0, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	injectedErr := fmt.Errorf("injected failure")
	inj.Set("notification", "CreateTopic", injectedErr, inject.Always{})

	n := newTestNotification(WithErrorInjection(inj))

	_, err := n.CreateTopic(context.Background(), driver.TopicConfig{Name: "inj-topic"})
	assertError(t, err, true)
	assertEqual(t, injectedErr.Error(), err.Error())
}

func TestWithErrorInjectionSelectiveOps(t *testing.T) {
	inj := inject.NewInjector()
	injectedErr := fmt.Errorf("publish broken")
	inj.Set("notification", "Publish", injectedErr, inject.Always{})

	n := newTestNotification(WithErrorInjection(inj))
	ctx := context.Background()

	// CreateTopic should succeed (not injected).
	topic, err := n.CreateTopic(ctx, driver.TopicConfig{Name: "sel-topic"})
	requireNoError(t, err)

	// Publish should fail (injected).
	_, err = n.Publish(ctx, driver.PublishInput{
		TopicID: topic.ARN,
		Message: "hello",
	})
	assertError(t, err, true)
	assertEqual(t, injectedErr.Error(), err.Error())
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	inj := inject.NewInjector()
	rec := recorder.New()
	injectedErr := fmt.Errorf("injected")
	inj.Set("notification", "GetTopic", injectedErr, inject.Always{})

	n := newTestNotification(WithErrorInjection(inj), WithRecorder(rec))

	_, _ = n.GetTopic(context.Background(), "any-id")

	calls := rec.CallsFor("notification", "GetTopic")
	assertEqual(t, 1, len(calls))

	if calls[0].Error == nil {
		t.Error("expected recorded error to be non-nil")
	}
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
	assertEqual(t, 0, callsCount)
}

func TestWithLatency(t *testing.T) {
	n := newTestNotification(WithLatency(1 * time.Millisecond))
	ctx := context.Background()

	start := time.Now()

	_, _ = n.CreateTopic(ctx, driver.TopicConfig{Name: "lat-topic"})

	elapsed := time.Since(start)

	if elapsed < 1*time.Millisecond {
		t.Errorf("expected at least 1ms latency, got %v", elapsed)
	}
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
	requireNoError(t, err)
	assertNotEmpty(t, topic.ID)

	assertEqual(t, 1, rec.CallCount())

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").
		ByLabel("service", "notification").
		Count()
	assertEqual(t, 1, callsCount)
}

func TestSubscribeViaPortableAllPaths(t *testing.T) {
	rec := recorder.New()
	n := newTestNotification(WithRecorder(rec))
	ctx := context.Background()

	topic := createTopicVia(t, n, "full-flow")

	sub, err := n.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.ARN, Protocol: "email", Endpoint: "u@e.com",
	})
	requireNoError(t, err)

	subs, err := n.ListSubscriptions(ctx, topic.ARN)
	requireNoError(t, err)
	assertEqual(t, 1, len(subs))

	err = n.Unsubscribe(ctx, sub.ID)
	requireNoError(t, err)

	out, err := n.Publish(ctx, driver.PublishInput{
		TopicID: topic.ARN, Message: "test msg",
	})
	requireNoError(t, err)
	assertNotEmpty(t, out.MessageID)

	// Verify all operations were recorded.
	assertEqual(t, 5, rec.CallCount())
	assertEqual(t, 1, rec.CallCountFor("notification", "Subscribe"))
	assertEqual(t, 1, rec.CallCountFor("notification", "ListSubscriptions"))
	assertEqual(t, 1, rec.CallCountFor("notification", "Unsubscribe"))
	assertEqual(t, 1, rec.CallCountFor("notification", "Publish"))
}
