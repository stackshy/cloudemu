package sqs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/messagequeue/driver"
)

func newTestMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	return New(opts), fc
}

func createStdQueue(m *Mock, name string) *driver.QueueInfo {
	info, _ := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name})
	return info
}

func createFIFOQueue(m *Mock, name string) *driver.QueueInfo {
	info, _ := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name, FIFO: true})
	return info
}

func TestCreateQueue(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.QueueConfig
		setup     func(m *Mock)
		expectErr bool
	}{
		{name: "standard queue", cfg: driver.QueueConfig{Name: "my-queue"}},
		{name: "FIFO queue", cfg: driver.QueueConfig{Name: "my-queue.fifo", FIFO: true}},
		{name: "empty name", cfg: driver.QueueConfig{}, expectErr: true},
		{name: "FIFO without suffix", cfg: driver.QueueConfig{Name: "bad", FIFO: true}, expectErr: true},
		{
			name: "already exists",
			cfg:  driver.QueueConfig{Name: "dup"},
			setup: func(m *Mock) {
				createStdQueue(m, "dup")
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}
			info, err := m.CreateQueue(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.URL)
			assertNotEmpty(t, info.ARN)
			assertEqual(t, tc.cfg.Name, info.Name)
		})
	}
}

func TestDeleteQueue(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "my-queue")

	t.Run("success", func(t *testing.T) {
		err := m.DeleteQueue(ctx, q.URL)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteQueue(ctx, "https://nope")
		assertError(t, err, true)
	})
}

func TestGetQueueInfo(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "my-queue")

	t.Run("found", func(t *testing.T) {
		info, err := m.GetQueueInfo(ctx, q.URL)
		requireNoError(t, err)
		assertEqual(t, "my-queue", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetQueueInfo(ctx, "https://nope")
		assertError(t, err, true)
	})
}

func TestListQueues(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createStdQueue(m, "alpha-queue")
	createStdQueue(m, "beta-queue")

	t.Run("all", func(t *testing.T) {
		queues, err := m.ListQueues(ctx, "")
		requireNoError(t, err)
		assertEqual(t, 2, len(queues))
	})

	t.Run("with prefix", func(t *testing.T) {
		queues, err := m.ListQueues(ctx, "alpha")
		requireNoError(t, err)
		assertEqual(t, 1, len(queues))
	})
}

func TestSendAndReceiveMessages(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "my-queue")

	t.Run("send and receive", func(t *testing.T) {
		out, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL:   q.URL,
			Body:       "hello",
			Attributes: map[string]string{"key": "val"},
		})
		requireNoError(t, err)
		assertNotEmpty(t, out.MessageID)

		msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL:    q.URL,
			MaxMessages: 10,
		})
		requireNoError(t, err)
		assertEqual(t, 1, len(msgs))
		assertEqual(t, "hello", msgs[0].Body)
		assertEqual(t, "val", msgs[0].Attributes["key"])
	})

	t.Run("send to nonexistent queue", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: "https://nope", Body: "x"})
		assertError(t, err, true)
	})

	t.Run("receive from nonexistent queue", func(t *testing.T) {
		_, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: "https://nope"})
		assertError(t, err, true)
	})
}

func TestDeleteMessage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "my-queue")

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "msg"})
	msgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 1})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteMessage(ctx, q.URL, msgs[0].ReceiptHandle)
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteMessage(ctx, q.URL, "invalid-handle")
		assertError(t, err, true)
	})

	t.Run("queue not found", func(t *testing.T) {
		err := m.DeleteMessage(ctx, "https://nope", "handle")
		assertError(t, err, true)
	})
}

func TestFIFODeduplication(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()
	q := createFIFOQueue(m, "dedup.fifo")

	t.Run("same dedup ID within window returns same message ID", func(t *testing.T) {
		out1, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL:        q.URL,
			Body:            "msg1",
			GroupID:         "g1",
			DeduplicationID: "dup-1",
		})
		requireNoError(t, err)

		out2, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL:        q.URL,
			Body:            "msg1-again",
			GroupID:         "g1",
			DeduplicationID: "dup-1",
		})
		requireNoError(t, err)
		assertEqual(t, out1.MessageID, out2.MessageID)
	})

	t.Run("different dedup ID creates new message", func(t *testing.T) {
		out3, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL:        q.URL,
			Body:            "msg2",
			GroupID:         "g1",
			DeduplicationID: "dup-2",
		})
		requireNoError(t, err)
		assertNotEmpty(t, out3.MessageID)
	})

	t.Run("after window expiry new message is created", func(t *testing.T) {
		fc.Advance(6 * time.Minute)

		out4, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL:        q.URL,
			Body:            "msg1-new",
			GroupID:         "g1",
			DeduplicationID: "dup-1",
		})
		requireNoError(t, err)
		assertNotEmpty(t, out4.MessageID)
	})
}

func TestFIFORequiresGroupAndDedup(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createFIFOQueue(m, "strict.fifo")

	t.Run("missing GroupID", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL:        q.URL,
			Body:            "x",
			DeduplicationID: "d1",
		})
		assertError(t, err, true)
	})

	t.Run("missing DeduplicationID", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: q.URL,
			Body:     "x",
			GroupID:  "g1",
		})
		assertError(t, err, true)
	})
}

func TestDeadLetterQueue(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	mainQ, _ := m.CreateQueue(ctx, driver.QueueConfig{
		Name: "main-queue",
		DeadLetterQueue: &driver.DeadLetterConfig{
			TargetQueueURL:  dlq.URL,
			MaxReceiveCount: 2,
		},
	})

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: mainQ.URL, Body: "fail-msg"})

	// Receive 1st time (ReceiveCount=1)
	msgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainQ.URL, MaxMessages: 1, VisibilityTimeout: 1})
	assertEqual(t, 1, len(msgs))

	// Wait for visibility timeout
	fc.Advance(2 * time.Second)

	// Receive 2nd time (ReceiveCount=2)
	msgs, _ = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainQ.URL, MaxMessages: 1, VisibilityTimeout: 1})
	assertEqual(t, 1, len(msgs))

	// Wait for visibility timeout
	fc.Advance(2 * time.Second)

	// 3rd receive should trigger DLQ move (ReceiveCount=3 > MaxReceiveCount=2)
	msgs, _ = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainQ.URL, MaxMessages: 1})
	assertEqual(t, 0, len(msgs))

	// Message should be in DLQ now
	dlqMsgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: dlq.URL, MaxMessages: 1})
	assertEqual(t, 1, len(dlqMsgs))
	assertEqual(t, "fail-msg", dlqMsgs[0].Body)
}

func TestChangeVisibility(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "vis-queue")

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "msg"})
	msgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 1, VisibilityTimeout: 60})

	t.Run("extend visibility", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, q.URL, msgs[0].ReceiptHandle, 120)
		requireNoError(t, err)

		// After 60 seconds message should still be invisible
		fc.Advance(61 * time.Second)
		result, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 1})
		assertEqual(t, 0, len(result))
	})

	t.Run("queue not found", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, "https://nope", "handle", 10)
		assertError(t, err, true)
	})

	t.Run("receipt handle not found", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, q.URL, "invalid", 10)
		assertError(t, err, true)
	})
}

func TestLambdaTrigger(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "trigger-queue")

	var triggered bool
	var triggeredBody string

	m.SetTrigger(q.URL, func(queueURL string, msg driver.Message) {
		triggered = true
		triggeredBody = msg.Body
	})

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "trigger-test"})

	assertEqual(t, true, triggered)
	assertEqual(t, "trigger-test", triggeredBody)

	m.RemoveTrigger(q.URL)
	triggered = false
	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "no-trigger"})
	assertEqual(t, false, triggered)
}

// --- test helpers ---

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
