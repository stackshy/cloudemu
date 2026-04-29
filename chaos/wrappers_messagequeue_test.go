package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
)

func newChaosMessageQueue(t *testing.T) (mqdriver.MessageQueue, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapMessageQueue(cloudemu.NewAWS().SQS, e), e
}

func TestWrapMessageQueueCreateQueueChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()

	if _, err := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "ok"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	if _, err := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "fail"}); err == nil {
		t.Error("expected chaos error on CreateQueue")
	}
}

func TestWrapMessageQueueDeleteQueueChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()
	qi, _ := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "del"})

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	if err := q.DeleteQueue(ctx, qi.URL); err == nil {
		t.Error("expected chaos error on DeleteQueue")
	}
}

func TestWrapMessageQueueListQueuesChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	if _, err := q.ListQueues(ctx, ""); err == nil {
		t.Error("expected chaos error on ListQueues")
	}
}

func TestWrapMessageQueueSendMessageChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()
	qi, _ := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "send"})

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	if _, err := q.SendMessage(ctx, mqdriver.SendMessageInput{QueueURL: qi.URL, Body: "hi"}); err == nil {
		t.Error("expected chaos error on SendMessage")
	}
}

func TestWrapMessageQueueReceiveMessagesChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()
	qi, _ := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "recv"})

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	if _, err := q.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: qi.URL, MaxMessages: 1}); err == nil {
		t.Error("expected chaos error on ReceiveMessages")
	}
}

func TestWrapMessageQueueDeleteMessageChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()
	qi, _ := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "delm"})

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	if err := q.DeleteMessage(ctx, qi.URL, "rh"); err == nil {
		t.Error("expected chaos error on DeleteMessage")
	}
}

func TestWrapMessageQueueSendMessageBatchChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()
	qi, _ := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "sbatch"})

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	entries := []mqdriver.BatchSendEntry{{ID: "1", Body: "a"}}
	if _, err := q.SendMessageBatch(ctx, qi.URL, entries); err == nil {
		t.Error("expected chaos error on SendMessageBatch")
	}
}

func TestWrapMessageQueueDeleteMessageBatchChaos(t *testing.T) {
	q, e := newChaosMessageQueue(t)
	ctx := context.Background()
	qi, _ := q.CreateQueue(ctx, mqdriver.QueueConfig{Name: "dbatch"})

	e.Apply(chaos.ServiceOutage("messagequeue", time.Hour))

	entries := []mqdriver.BatchDeleteEntry{{ID: "1", ReceiptHandle: "rh"}}
	if _, err := q.DeleteMessageBatch(ctx, qi.URL, entries); err == nil {
		t.Error("expected chaos error on DeleteMessageBatch")
	}
}
