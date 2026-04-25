package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	notifdriver "github.com/stackshy/cloudemu/notification/driver"
)

func newChaosNotification(t *testing.T) (notifdriver.Notification, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapNotification(cloudemu.NewAWS().SNS, e), e
}

func TestWrapNotificationCreateTopicChaos(t *testing.T) {
	n, e := newChaosNotification(t)
	ctx := context.Background()

	if _, err := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "ok"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("notification", time.Hour))

	if _, err := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "fail"}); err == nil {
		t.Error("expected chaos error on CreateTopic")
	}
}

func TestWrapNotificationDeleteTopicChaos(t *testing.T) {
	n, e := newChaosNotification(t)
	ctx := context.Background()
	tp, _ := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "del"})

	e.Apply(chaos.ServiceOutage("notification", time.Hour))

	if err := n.DeleteTopic(ctx, tp.ID); err == nil {
		t.Error("expected chaos error on DeleteTopic")
	}
}

func TestWrapNotificationGetTopicChaos(t *testing.T) {
	n, e := newChaosNotification(t)
	ctx := context.Background()
	tp, _ := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "g"})

	e.Apply(chaos.ServiceOutage("notification", time.Hour))

	if _, err := n.GetTopic(ctx, tp.ID); err == nil {
		t.Error("expected chaos error on GetTopic")
	}
}

func TestWrapNotificationListTopicsChaos(t *testing.T) {
	n, e := newChaosNotification(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("notification", time.Hour))

	if _, err := n.ListTopics(ctx); err == nil {
		t.Error("expected chaos error on ListTopics")
	}
}

func TestWrapNotificationSubscribeChaos(t *testing.T) {
	n, e := newChaosNotification(t)
	ctx := context.Background()
	tp, _ := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "sub"})

	e.Apply(chaos.ServiceOutage("notification", time.Hour))

	cfg := notifdriver.SubscriptionConfig{TopicID: tp.ID, Protocol: "email", Endpoint: "x@y.z"}
	if _, err := n.Subscribe(ctx, cfg); err == nil {
		t.Error("expected chaos error on Subscribe")
	}
}

func TestWrapNotificationUnsubscribeChaos(t *testing.T) {
	n, e := newChaosNotification(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("notification", time.Hour))

	if err := n.Unsubscribe(ctx, "any-subscription-id"); err == nil {
		t.Error("expected chaos error on Unsubscribe")
	}
}

func TestWrapNotificationPublishChaos(t *testing.T) {
	n, e := newChaosNotification(t)
	ctx := context.Background()
	tp, _ := n.CreateTopic(ctx, notifdriver.TopicConfig{Name: "pub"})

	e.Apply(chaos.ServiceOutage("notification", time.Hour))

	if _, err := n.Publish(ctx, notifdriver.PublishInput{TopicID: tp.ID, Message: "hi"}); err == nil {
		t.Error("expected chaos error on Publish")
	}
}
