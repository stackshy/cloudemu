package sqs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
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
			name: "already exists with different attributes",
			cfg:  driver.QueueConfig{Name: "dup", VisibilityTimeout: 60},
			setup: func(m *Mock) {
				createStdQueue(m, "dup")
			},
			expectErr: true,
		},
		{
			name: "idempotent re-create with identical attributes",
			cfg:  driver.QueueConfig{Name: "idem"},
			setup: func(m *Mock) {
				createStdQueue(m, "idem")
			},
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

// TestCreateQueueVisibilityTimeoutDefault locks in that the 30s default lives at
// the provider layer (so the typed Go API and portable path get it), while an
// explicitly-set 0 still round-trips.
func TestCreateQueueVisibilityTimeoutDefault(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	// Typed API omits VisibilityTimeout: it must default to 30s, so a received
	// message stays invisible and an immediate second receive returns nothing.
	q := createStdQueue(m, "default-vis")
	if _, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "x"}); err != nil {
		requireNoError(t, err)
	}

	r1, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 1})
	requireNoError(t, err)
	r2, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 1})
	requireNoError(t, err)

	if len(r1) != 1 || len(r2) != 0 {
		t.Fatalf("default visibility should hide the received message: r1=%d r2=%d", len(r1), len(r2))
	}

	// Explicit 0 (as the wire handler flags it) round-trips: the received
	// message is immediately visible again.
	q0, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "zero-vis", VisibilityTimeout: 0, VisibilityTimeoutSet: true})
	requireNoError(t, err)
	if _, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q0.URL, Body: "y"}); err != nil {
		requireNoError(t, err)
	}

	z1, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q0.URL, MaxMessages: 1})
	requireNoError(t, err)
	z2, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q0.URL, MaxMessages: 1})
	requireNoError(t, err)

	if len(z1) != 1 || len(z2) != 1 {
		t.Fatalf("explicit 0 visibility should re-expose the message: z1=%d z2=%d", len(z1), len(z2))
	}
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

	t.Run("unknown handle is idempotent success", func(t *testing.T) {
		err := m.DeleteMessage(ctx, q.URL, "invalid-handle")
		requireNoError(t, err)
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

func TestSendMessageBatch(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "batch-queue")

	entries := []driver.BatchSendEntry{
		{ID: "e1", Body: "msg-1"},
		{ID: "e2", Body: "msg-2"},
		{ID: "e3", Body: "msg-3"},
	}

	result, err := m.SendMessageBatch(ctx, q.URL, entries)
	requireNoError(t, err)
	assertEqual(t, 3, len(result.Successful))
	assertEqual(t, 0, len(result.Failed))

	// Verify all messages can be received.
	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	requireNoError(t, err)
	assertEqual(t, 3, len(msgs))
}

func TestSendMessageBatchLimit(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "batch-limit-queue")

	entries := make([]driver.BatchSendEntry, 11)
	for i := range entries {
		entries[i] = driver.BatchSendEntry{ID: "e", Body: "x"}
	}

	_, err := m.SendMessageBatch(ctx, q.URL, entries)
	assertError(t, err, true)
}

func TestDeleteMessageBatch(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "batch-del-queue")

	// Send 3 messages.
	for i := 0; i < 3; i++ {
		_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "msg"})
	}

	// Receive them.
	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	requireNoError(t, err)
	assertEqual(t, 3, len(msgs))

	// Batch delete.
	delEntries := make([]driver.BatchDeleteEntry, len(msgs))
	for i, msg := range msgs {
		delEntries[i] = driver.BatchDeleteEntry{ID: msg.MessageID, ReceiptHandle: msg.ReceiptHandle}
	}

	result, err := m.DeleteMessageBatch(ctx, q.URL, delEntries)
	requireNoError(t, err)
	assertEqual(t, 3, len(result.Successful))
	assertEqual(t, 0, len(result.Failed))

	// Verify queue is empty.
	remaining, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	requireNoError(t, err)
	assertEqual(t, 0, len(remaining))
}

func TestReceiveMessagesWithOptions(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "opts-queue")

	for i := 0; i < 5; i++ {
		_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "msg"})
	}

	t.Run("MaxMessages limits results", func(t *testing.T) {
		msgs, err := m.ReceiveMessagesWithOptions(ctx, q.URL, driver.ReceiveOptions{MaxMessages: 2})
		requireNoError(t, err)
		assertEqual(t, 2, len(msgs))
	})

	t.Run("VisibilityTimeout override", func(t *testing.T) {
		// Wait for previous visibility to expire, then receive with short timeout.
		fc.Advance(31 * time.Second)
		msgs, err := m.ReceiveMessagesWithOptions(ctx, q.URL, driver.ReceiveOptions{
			MaxMessages:       10,
			VisibilityTimeout: 5,
		})
		requireNoError(t, err)
		assertEqual(t, 5, len(msgs))

		// Messages should be invisible now.
		msgs2, err := m.ReceiveMessagesWithOptions(ctx, q.URL, driver.ReceiveOptions{MaxMessages: 10})
		requireNoError(t, err)
		assertEqual(t, 0, len(msgs2))

		// After 6 seconds all messages become visible again.
		fc.Advance(6 * time.Second)
		msgs3, err := m.ReceiveMessagesWithOptions(ctx, q.URL, driver.ReceiveOptions{MaxMessages: 10})
		requireNoError(t, err)
		assertEqual(t, 5, len(msgs3))
	})

	t.Run("queue not found", func(t *testing.T) {
		_, err := m.ReceiveMessagesWithOptions(ctx, "https://nope", driver.ReceiveOptions{})
		assertError(t, err, true)
	})
}

func TestGetQueueAttributes(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name:              "attr-queue",
		DelaySeconds:      5,
		VisibilityTimeout: 45,
		MaxMessageSize:    1024,
		MessageRetention:  3600,
	})
	requireNoError(t, err)

	attrs, err := m.GetQueueAttributes(ctx, q.URL)
	requireNoError(t, err)
	assertEqual(t, 5, attrs.DelaySeconds)
	assertEqual(t, 45, attrs.VisibilityTimeout)
	assertEqual(t, 1024, attrs.MaximumMessageSize)
	assertEqual(t, 3600, attrs.MessageRetentionPeriod)
	assertEqual(t, 0, attrs.ApproximateMessageCount)
	assertEqual(t, false, attrs.FifoQueue)

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetQueueAttributes(ctx, "https://nope")
		assertError(t, err, true)
	})
}

func TestSetQueueAttributes(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "set-attr-queue")

	err := m.SetQueueAttributes(ctx, q.URL, map[string]int{
		"DelaySeconds":      10,
		"VisibilityTimeout": 60,
	})
	requireNoError(t, err)

	attrs, err := m.GetQueueAttributes(ctx, q.URL)
	requireNoError(t, err)
	assertEqual(t, 10, attrs.DelaySeconds)
	assertEqual(t, 60, attrs.VisibilityTimeout)

	t.Run("not found", func(t *testing.T) {
		err := m.SetQueueAttributes(ctx, "https://nope", map[string]int{"DelaySeconds": 1})
		assertError(t, err, true)
	})
}

func TestPurgeQueue(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "purge-queue")

	// Send several messages.
	for i := 0; i < 5; i++ {
		_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "msg"})
	}

	// Verify messages exist.
	info, _ := m.GetQueueInfo(ctx, q.URL)
	assertEqual(t, 5, info.ApproxMessageCount)

	// Purge.
	err := m.PurgeQueue(ctx, q.URL)
	requireNoError(t, err)

	// Verify empty.
	info, _ = m.GetQueueInfo(ctx, q.URL)
	assertEqual(t, 0, info.ApproxMessageCount)

	msgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	assertEqual(t, 0, len(msgs))

	t.Run("not found", func(t *testing.T) {
		err := m.PurgeQueue(ctx, "https://nope")
		assertError(t, err, true)
	})
}

func TestMetricsEmission(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	cw := cloudwatch.New(opts)
	m := New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()
	q := createSQSQueue(m, "metrics-queue")

	// Send a message - should emit NumberOfMessagesSent metric.
	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "hello"})
	requireNoError(t, err)

	// Query the metric.
	result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  "AWS/SQS",
		MetricName: "NumberOfMessagesSent",
		Dimensions: map[string]string{"QueueName": "metrics-queue"},
		StartTime:  fc.Now().Add(-time.Minute),
		EndTime:    fc.Now().Add(time.Minute),
		Period:     60,
		Stat:       "Sum",
	})
	requireNoError(t, err)
	assertGreaterThan(t, len(result.Values), 0)

	// Receive the message - should emit NumberOfMessagesReceived.
	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 1})
	requireNoError(t, err)
	assertEqual(t, 1, len(msgs))

	recvResult, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  "AWS/SQS",
		MetricName: "NumberOfMessagesReceived",
		Dimensions: map[string]string{"QueueName": "metrics-queue"},
		StartTime:  fc.Now().Add(-time.Minute),
		EndTime:    fc.Now().Add(time.Minute),
		Period:     60,
		Stat:       "Sum",
	})
	requireNoError(t, err)
	assertGreaterThan(t, len(recvResult.Values), 0)
}

func createSQSQueue(m *Mock, name string) *driver.QueueInfo {
	info, _ := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name})
	return info
}

func TestReceiveMessagesWithOptionsMetrics(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	cw := cloudwatch.New(opts)
	m := New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()
	q := createSQSQueue(m, "rwopt-queue")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "hello"})
	requireNoError(t, err)

	_, err = m.ReceiveMessagesWithOptions(ctx, q.URL, driver.ReceiveOptions{MaxMessages: 10})
	requireNoError(t, err)

	result, err := cw.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace:  "AWS/SQS",
		MetricName: "NumberOfMessagesReceived",
		Dimensions: map[string]string{"QueueName": "rwopt-queue"},
		StartTime:  fc.Now().Add(-time.Minute),
		EndTime:    fc.Now().Add(time.Minute),
		Period:     60,
		Stat:       "Sum",
	})
	requireNoError(t, err)
	assertGreaterThan(t, len(result.Values), 0)
}

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

func assertNotNegative(t *testing.T, n int) {
	t.Helper()
	if n < 0 {
		t.Errorf("expected non-negative, got %d", n)
	}
}

func assertGreaterThan(t *testing.T, actual, threshold int) {
	t.Helper()
	if actual <= threshold {
		t.Errorf("expected > %d, got %d", threshold, actual)
	}
}
