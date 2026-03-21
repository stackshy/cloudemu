package servicebus

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-ns"))

	return New(opts), clk
}

func createStdQueue(t *testing.T, m *Mock) string {
	t.Helper()

	ctx := context.Background()
	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "test-queue", Tags: map[string]string{"env": "test"}})
	require.NoError(t, err)

	return info.URL
}

func TestCreateQueue(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		cfg     driver.QueueConfig
		wantErr bool
		errMsg  string
	}{
		{name: "standard queue", cfg: driver.QueueConfig{Name: "my-queue"}},
		{name: "FIFO queue", cfg: driver.QueueConfig{Name: "my-queue.fifo", FIFO: true}},
		{name: "empty name", cfg: driver.QueueConfig{Name: ""}, wantErr: true, errMsg: "queue name is required"},
		{name: "FIFO without suffix", cfg: driver.QueueConfig{Name: "bad-fifo", FIFO: true}, wantErr: true, errMsg: "must end with .fifo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newTestMock()

			info, err := m.CreateQueue(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, info.Name)
				assert.NotEmpty(t, info.URL)
				assert.NotEmpty(t, info.ARN)
				assert.Equal(t, tt.cfg.FIFO, info.FIFO)
			}
		})
	}
}

func TestCreateQueueDuplicate(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	_, err = m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestDeleteQueue(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{name: "success", url: url},
		{name: "not found", url: "https://missing.servicebus.windows.net/q", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteQueue(ctx, tt.url)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetQueueInfo(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	t.Run("success", func(t *testing.T) {
		info, err := m.GetQueueInfo(ctx, url)
		require.NoError(t, err)
		assert.Equal(t, "test-queue", info.Name)
		assert.Equal(t, 0, info.ApproxMessageCount)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetQueueInfo(ctx, "https://missing.servicebus.windows.net/q")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestListQueues(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, _ = m.CreateQueue(ctx, driver.QueueConfig{Name: "alpha-queue"})
	_, _ = m.CreateQueue(ctx, driver.QueueConfig{Name: "beta-queue"})

	t.Run("all queues", func(t *testing.T) {
		queues, err := m.ListQueues(ctx, "")
		require.NoError(t, err)
		assert.Len(t, queues, 2)
	})

	t.Run("with prefix", func(t *testing.T) {
		queues, err := m.ListQueues(ctx, "alpha")
		require.NoError(t, err)
		assert.Len(t, queues, 1)
		assert.Equal(t, "alpha-queue", queues[0].Name)
	})
}

func TestSendAndReceiveMessage(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	t.Run("send and receive", func(t *testing.T) {
		out, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL:   url,
			Body:       "hello world",
			Attributes: map[string]string{"key": "val"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, out.MessageID)

		msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 10})
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		assert.Equal(t, "hello world", msgs[0].Body)
		assert.Equal(t, "val", msgs[0].Attributes["key"])
		assert.NotEmpty(t, msgs[0].ReceiptHandle)
	})

	t.Run("send to nonexistent queue", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: "bad-url", Body: "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("receive from nonexistent queue", func(t *testing.T) {
		_, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: "bad-url"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteMessage(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "msg"})
	msgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	require.Len(t, msgs, 1)

	tests := []struct {
		name    string
		url     string
		handle  string
		wantErr bool
		errMsg  string
	}{
		{name: "success", url: url, handle: msgs[0].ReceiptHandle},
		{name: "invalid handle", url: url, handle: "bad-handle", wantErr: true, errMsg: "not found"},
		{name: "queue not found", url: "bad-url", handle: "x", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteMessage(ctx, tt.url, tt.handle)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestFIFODeduplication(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "fifo-queue.fifo", FIFO: true})
	require.NoError(t, err)
	url := info.URL

	t.Run("duplicate within window returns same ID", func(t *testing.T) {
		out1, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: url, Body: "msg1", GroupID: "g1", DeduplicationID: "dedup1",
		})
		require.NoError(t, err)

		out2, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: url, Body: "msg1-dup", GroupID: "g1", DeduplicationID: "dedup1",
		})
		require.NoError(t, err)

		assert.Equal(t, out1.MessageID, out2.MessageID)
	})

	t.Run("after window allows new message", func(t *testing.T) {
		clk.Advance(6 * time.Minute)

		out3, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: url, Body: "msg1-new", GroupID: "g1", DeduplicationID: "dedup1",
		})
		require.NoError(t, err)
		// Should get a new message ID since window has passed
		assert.NotEmpty(t, out3.MessageID)
	})

	t.Run("FIFO requires GroupID", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: url, Body: "msg", DeduplicationID: "d1",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GroupID")
	})

	t.Run("FIFO requires DeduplicationID", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: url, Body: "msg", GroupID: "g1",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DeduplicationID")
	})
}

func TestDeadLetterQueue(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	// Create DLQ first
	dlqInfo, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "dlq"})
	require.NoError(t, err)

	// Create main queue with DLQ config (maxReceiveCount=1)
	mainInfo, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name: "main-queue",
		DeadLetterQueue: &driver.DeadLetterConfig{
			TargetQueueURL:  dlqInfo.URL,
			MaxReceiveCount: 1,
		},
	})
	require.NoError(t, err)

	// Send a message
	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: mainInfo.URL, Body: "will-fail"})
	require.NoError(t, err)

	// First receive: receiveCount becomes 1, message is returned
	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1, VisibilityTimeout: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "will-fail", msgs[0].Body)

	// Let visibility timeout expire
	clk.Advance(2 * time.Second)

	// Second receive: receiveCount becomes 2 > maxReceiveCount(1), message moves to DLQ
	msgs, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	assert.Empty(t, msgs)

	// Check DLQ has the message
	dlqMsgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: dlqInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, dlqMsgs, 1)
	assert.Equal(t, "will-fail", dlqMsgs[0].Body)
}

func TestChangeVisibility(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()
	url := createStdQueue(t, m)

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "msg"})
	msgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1, VisibilityTimeout: 60})
	require.Len(t, msgs, 1)

	t.Run("extend visibility", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, url, msgs[0].ReceiptHandle, 120)
		require.NoError(t, err)

		// After 60s (original timeout), message should still be invisible
		clk.Advance(61 * time.Second)
		received, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
		assert.Empty(t, received)
	})

	t.Run("queue not found", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, "bad-url", "handle", 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("handle not found", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, url, "bad-handle", 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestMessageCountInQueueInfo(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m1"})
	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m2"})

	info, err := m.GetQueueInfo(ctx, url)
	require.NoError(t, err)
	assert.Equal(t, 2, info.ApproxMessageCount)
}

func TestTrigger(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	var triggered bool
	var triggerBody string

	m.SetTrigger(url, func(_ string, msg driver.Message) {
		triggered = true
		triggerBody = msg.Body
	})

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "trigger-msg"})
	require.NoError(t, err)
	assert.True(t, triggered)
	assert.Equal(t, "trigger-msg", triggerBody)

	// Remove trigger and verify no more triggers
	triggered = false
	m.RemoveTrigger(url)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "no-trigger"})
	require.NoError(t, err)
	assert.False(t, triggered)
}

func TestSendMessageBatch(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	t.Run("successful batch", func(t *testing.T) {
		entries := []driver.BatchSendEntry{
			{ID: "e1", Body: "msg1"},
			{ID: "e2", Body: "msg2"},
			{ID: "e3", Body: "msg3"},
		}

		result, err := m.SendMessageBatch(ctx, url, entries)
		require.NoError(t, err)
		assert.Len(t, result.Successful, 3)
		assert.Empty(t, result.Failed)

		for _, entry := range result.Successful {
			assert.NotEmpty(t, entry.MessageID)
		}
	})

	t.Run("batch exceeds max size", func(t *testing.T) {
		entries := make([]driver.BatchSendEntry, 11)
		for i := range entries {
			entries[i] = driver.BatchSendEntry{ID: "e", Body: "m"}
		}

		_, err := m.SendMessageBatch(ctx, url, entries)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds max")
	})

	t.Run("batch with invalid queue", func(t *testing.T) {
		entries := []driver.BatchSendEntry{{ID: "e1", Body: "msg1"}}

		result, err := m.SendMessageBatch(ctx, "bad-url", entries)
		require.NoError(t, err)
		assert.Len(t, result.Failed, 1)
		assert.Equal(t, "e1", result.Failed[0].ID)
	})
}

func TestDeleteMessageBatch(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	// Send messages
	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m1"})
	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m2"})

	// Receive messages
	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 10})
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	t.Run("successful batch delete", func(t *testing.T) {
		entries := []driver.BatchDeleteEntry{
			{ID: "d1", ReceiptHandle: msgs[0].ReceiptHandle},
			{ID: "d2", ReceiptHandle: msgs[1].ReceiptHandle},
		}

		result, err := m.DeleteMessageBatch(ctx, url, entries)
		require.NoError(t, err)
		assert.Len(t, result.Successful, 2)
		assert.Empty(t, result.Failed)
	})

	t.Run("batch with invalid handles", func(t *testing.T) {
		entries := []driver.BatchDeleteEntry{
			{ID: "d1", ReceiptHandle: "bad-handle"},
		}

		result, err := m.DeleteMessageBatch(ctx, url, entries)
		require.NoError(t, err)
		assert.Len(t, result.Failed, 1)
	})

	t.Run("batch exceeds max size", func(t *testing.T) {
		entries := make([]driver.BatchDeleteEntry, 11)
		for i := range entries {
			entries[i] = driver.BatchDeleteEntry{ID: "d", ReceiptHandle: "h"}
		}

		_, err := m.DeleteMessageBatch(ctx, url, entries)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds max")
	})
}

func TestGetQueueAttributes(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	t.Run("success", func(t *testing.T) {
		attrs, err := m.GetQueueAttributes(ctx, url)
		require.NoError(t, err)
		assert.Equal(t, defaultVisibilityTimeout, attrs.VisibilityTimeout)
		assert.Equal(t, 0, attrs.ApproximateMessageCount)
		assert.False(t, attrs.FifoQueue)
		assert.False(t, attrs.CreatedAt.IsZero())
	})

	t.Run("with messages", func(t *testing.T) {
		_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m1"})
		_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m2"})

		attrs, err := m.GetQueueAttributes(ctx, url)
		require.NoError(t, err)
		assert.Equal(t, 2, attrs.ApproximateMessageCount)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := m.GetQueueAttributes(ctx, "bad-url")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestSetQueueAttributes(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	t.Run("update attributes", func(t *testing.T) {
		err := m.SetQueueAttributes(ctx, url, map[string]int{
			"DelaySeconds":      5,
			"VisibilityTimeout": 60,
			"MaximumMessageSize": 1024,
		})
		require.NoError(t, err)

		attrs, err := m.GetQueueAttributes(ctx, url)
		require.NoError(t, err)
		assert.Equal(t, 5, attrs.DelaySeconds)
		assert.Equal(t, 60, attrs.VisibilityTimeout)
		assert.Equal(t, 1024, attrs.MaximumMessageSize)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.SetQueueAttributes(ctx, "bad-url", map[string]int{"DelaySeconds": 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestPurgeQueue(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m1"})
	_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "m2"})

	t.Run("purge removes all messages", func(t *testing.T) {
		err := m.PurgeQueue(ctx, url)
		require.NoError(t, err)

		info, err := m.GetQueueInfo(ctx, url)
		require.NoError(t, err)
		assert.Equal(t, 0, info.ApproxMessageCount)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.PurgeQueue(ctx, "bad-url")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestServiceBusMetricsEmission(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-ns"))
	m := New(opts)

	mon := &sbMetricsCollector{}
	m.SetMonitoring(mon)

	ctx := context.Background()
	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "metric-queue"})
	require.NoError(t, err)

	t.Run("SendMessage emits metrics", func(t *testing.T) {
		mon.reset()
		_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "hello"})
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.ServiceBus/namespaces", "IncomingMessages"))
		assert.True(t, mon.hasMetric("Microsoft.ServiceBus/namespaces", "Size"))
	})

	t.Run("ReceiveMessages emits metrics", func(t *testing.T) {
		mon.reset()
		_, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: info.URL, MaxMessages: 10})
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.ServiceBus/namespaces", "OutgoingMessages"))
	})

	t.Run("DeleteMessage emits metrics", func(t *testing.T) {
		_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "to-delete"})
		msgs, _ := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: info.URL, MaxMessages: 1})
		require.NotEmpty(t, msgs)

		mon.reset()
		err := m.DeleteMessage(ctx, info.URL, msgs[0].ReceiptHandle)
		require.NoError(t, err)
		assert.True(t, mon.hasMetric("Microsoft.ServiceBus/namespaces", "CompletedMessages"))
	})
}

func TestReceiveMessagesWithOptionsMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("test-ns"))
	m := New(opts)

	mon := &sbMetricsCollector{}
	m.SetMonitoring(mon)

	ctx := context.Background()
	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "rwopt-queue"})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "hello"})
	require.NoError(t, err)

	mon.reset()
	_, err = m.ReceiveMessagesWithOptions(ctx, info.URL, driver.ReceiveOptions{MaxMessages: 10})
	require.NoError(t, err)
	assert.True(t, mon.hasMetric("Microsoft.ServiceBus/namespaces", "OutgoingMessages"))
}

type sbMetricsCollector struct {
	data []mondriver.MetricDatum
}

func (c *sbMetricsCollector) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	c.data = append(c.data, data...)
	return nil
}

func (c *sbMetricsCollector) GetMetricData(_ context.Context, _ mondriver.GetMetricInput) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (c *sbMetricsCollector) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (c *sbMetricsCollector) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (c *sbMetricsCollector) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (c *sbMetricsCollector) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (c *sbMetricsCollector) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c *sbMetricsCollector) reset() {
	c.data = nil
}

func (c *sbMetricsCollector) hasMetric(namespace, metricName string) bool {
	for _, d := range c.data {
		if d.Namespace == namespace && d.MetricName == metricName {
			return true
		}
	}
	return false
}
