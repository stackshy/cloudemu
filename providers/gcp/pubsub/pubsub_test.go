package pubsub

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
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	return New(opts), clk
}

func TestCreateQueue(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.QueueConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "standard queue", cfg: driver.QueueConfig{Name: "my-topic", Tags: map[string]string{"env": "test"}}},
		{name: "fifo queue", cfg: driver.QueueConfig{Name: "my-topic.fifo", FIFO: true}},
		{name: "empty name", cfg: driver.QueueConfig{}, wantErr: true, errSubstr: "required"},
		{name: "fifo without suffix", cfg: driver.QueueConfig{Name: "bad-fifo", FIFO: true}, wantErr: true, errSubstr: ".fifo"},
		{name: "duplicate", cfg: driver.QueueConfig{Name: "my-topic"}, wantErr: true, errSubstr: "already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := m.CreateQueue(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.cfg.Name, info.Name)
				assert.NotEmpty(t, info.URL)
				assert.NotEmpty(t, info.ARN)
			}
		})
	}
}

func TestDeleteQueue(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		url       string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", url: info.URL},
		{name: "not found", url: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteQueue(ctx, tt.url)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestSendAndReceiveMessages(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	t.Run("send and receive", func(t *testing.T) {
		out, sendErr := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "hello",
			Attributes: map[string]string{"key": "val"},
		})
		require.NoError(t, sendErr)
		assert.NotEmpty(t, out.MessageID)

		msgs, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 10,
		})
		require.NoError(t, recvErr)
		require.Len(t, msgs, 1)
		assert.Equal(t, "hello", msgs[0].Body)
		assert.Equal(t, "val", msgs[0].Attributes["key"])
	})

	t.Run("send to missing queue", func(t *testing.T) {
		_, sendErr := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: "missing", Body: "x"})
		require.Error(t, sendErr)
		assert.Contains(t, sendErr.Error(), "not found")
	})

	t.Run("receive from missing queue", func(t *testing.T) {
		_, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: "missing"})
		require.Error(t, recvErr)
		assert.Contains(t, recvErr.Error(), "not found")
	})

	t.Run("visibility timeout hides message", func(t *testing.T) {
		// The message from the first subtest should be invisible now (just received with default 30s timeout)
		msgs, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 10,
		})
		require.NoError(t, recvErr)
		assert.Empty(t, msgs)

		// Advance past visibility timeout
		clk.Advance(31 * time.Second)

		msgs, recvErr = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 10,
		})
		require.NoError(t, recvErr)
		assert.Len(t, msgs, 1)
	})
}

func TestDeleteMessage(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "msg1"})
	require.NoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: info.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	tests := []struct {
		name      string
		url       string
		handle    string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", url: info.URL, handle: msgs[0].ReceiptHandle},
		{name: "queue not found", url: "missing", handle: "x", wantErr: true, errSubstr: "not found"},
		{name: "handle not found", url: info.URL, handle: "bad-handle", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteMessage(ctx, tt.url, tt.handle)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestFIFODeduplication(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "fifo.fifo", FIFO: true})
	require.NoError(t, err)

	t.Run("deduplication within 5 min window", func(t *testing.T) {
		out1, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "msg1",
			GroupID: "g1", DeduplicationID: "dedup-1",
		})
		require.NoError(t, err)

		out2, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "msg1-dup",
			GroupID: "g1", DeduplicationID: "dedup-1",
		})
		require.NoError(t, err)
		assert.Equal(t, out1.MessageID, out2.MessageID)
	})

	t.Run("dedup expires after 5 min", func(t *testing.T) {
		clk.Advance(6 * time.Minute)

		out3, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "msg1-new",
			GroupID: "g1", DeduplicationID: "dedup-1",
		})
		require.NoError(t, err)
		// Should get a new message ID since window expired
		assert.NotEmpty(t, out3.MessageID)
	})

	t.Run("fifo requires GroupID", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "x",
			DeduplicationID: "d1",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GroupID")
	})

	t.Run("fifo requires DeduplicationID", func(t *testing.T) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "x",
			GroupID: "g1",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DeduplicationID")
	})
}

func TestDLQ(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	dlqInfo, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "dlq"})
	require.NoError(t, err)

	mainInfo, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name: "main",
		DeadLetterQueue: &driver.DeadLetterConfig{
			TargetQueueURL:  dlqInfo.URL,
			MaxReceiveCount: 2,
		},
	})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: mainInfo.URL, Body: "retry-me"})
	require.NoError(t, err)

	// Receive 1st time
	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	// Advance and receive 2nd time
	clk.Advance(31 * time.Second)
	msgs, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	// Advance and receive 3rd time - should exceed max receives and move to DLQ
	clk.Advance(31 * time.Second)
	msgs, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	assert.Empty(t, msgs)

	// DLQ should have the message
	dlqMsgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: dlqInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, dlqMsgs, 1)
	assert.Equal(t, "retry-me", dlqMsgs[0].Body)
}

func TestListQueues(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "alpha-queue"})
	require.NoError(t, err)
	_, err = m.CreateQueue(ctx, driver.QueueConfig{Name: "beta-queue"})
	require.NoError(t, err)

	t.Run("all queues", func(t *testing.T) {
		queues, listErr := m.ListQueues(ctx, "")
		require.NoError(t, listErr)
		assert.Len(t, queues, 2)
	})

	t.Run("with prefix", func(t *testing.T) {
		queues, listErr := m.ListQueues(ctx, "alpha")
		require.NoError(t, listErr)
		assert.Len(t, queues, 1)
		assert.Equal(t, "alpha-queue", queues[0].Name)
	})
}

func TestGetQueueInfo(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	tests := []struct {
		name      string
		url       string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", url: info.URL},
		{name: "not found", url: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qInfo, err := m.GetQueueInfo(ctx, tt.url)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				assert.Equal(t, "q1", qInfo.Name)
			}
		})
	}
}

func TestSendMessageBatch(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	t.Run("batch send success", func(t *testing.T) {
		result, batchErr := m.SendMessageBatch(ctx, info.URL, []driver.BatchSendEntry{
			{ID: "e1", Body: "msg1"},
			{ID: "e2", Body: "msg2"},
			{ID: "e3", Body: "msg3"},
		})
		require.NoError(t, batchErr)
		assert.Len(t, result.Successful, 3)
		assert.Empty(t, result.Failed)
	})

	t.Run("batch exceeds max size", func(t *testing.T) {
		entries := make([]driver.BatchSendEntry, 11)
		for i := range entries {
			entries[i] = driver.BatchSendEntry{ID: "e", Body: "x"}
		}
		_, batchErr := m.SendMessageBatch(ctx, info.URL, entries)
		require.Error(t, batchErr)
		assert.Contains(t, batchErr.Error(), "exceeds max")
	})

	t.Run("verify messages received", func(t *testing.T) {
		msgs, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 10,
		})
		require.NoError(t, recvErr)
		assert.Len(t, msgs, 3)
	})
}

func TestDeleteMessageBatch(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	// Send and receive messages to get receipt handles
	_, err = m.SendMessageBatch(ctx, info.URL, []driver.BatchSendEntry{
		{ID: "e1", Body: "msg1"},
		{ID: "e2", Body: "msg2"},
	})
	require.NoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
		QueueURL: info.URL, MaxMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	t.Run("batch delete success", func(t *testing.T) {
		entries := []driver.BatchDeleteEntry{
			{ID: "d1", ReceiptHandle: msgs[0].ReceiptHandle},
			{ID: "d2", ReceiptHandle: msgs[1].ReceiptHandle},
		}
		result, batchErr := m.DeleteMessageBatch(ctx, info.URL, entries)
		require.NoError(t, batchErr)
		assert.Len(t, result.Successful, 2)
		assert.Empty(t, result.Failed)
	})

	t.Run("batch delete with invalid handle", func(t *testing.T) {
		entries := []driver.BatchDeleteEntry{
			{ID: "d1", ReceiptHandle: "invalid-handle"},
		}
		result, batchErr := m.DeleteMessageBatch(ctx, info.URL, entries)
		require.NoError(t, batchErr)
		assert.Empty(t, result.Successful)
		assert.Len(t, result.Failed, 1)
	})
}

func TestGetQueueAttributes(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name:              "q1",
		VisibilityTimeout: 45,
		DelaySeconds:      5,
		MaxMessageSize:    1024,
		MessageRetention:  86400,
	})
	require.NoError(t, err)

	t.Run("returns correct attributes", func(t *testing.T) {
		attrs, attrErr := m.GetQueueAttributes(ctx, info.URL)
		require.NoError(t, attrErr)
		assert.Equal(t, 45, attrs.VisibilityTimeout)
		assert.Equal(t, 5, attrs.DelaySeconds)
		assert.Equal(t, 1024, attrs.MaximumMessageSize)
		assert.Equal(t, 86400, attrs.MessageRetentionPeriod)
		assert.False(t, attrs.FifoQueue)
	})

	t.Run("queue not found", func(t *testing.T) {
		_, attrErr := m.GetQueueAttributes(ctx, "missing")
		require.Error(t, attrErr)
		assert.Contains(t, attrErr.Error(), "not found")
	})
}

func TestSetQueueAttributes(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	t.Run("update attributes", func(t *testing.T) {
		require.NoError(t, m.SetQueueAttributes(ctx, info.URL, map[string]int{
			"VisibilityTimeout":  60,
			"DelaySeconds":       10,
			"MaximumMessageSize": 2048,
		}))

		attrs, attrErr := m.GetQueueAttributes(ctx, info.URL)
		require.NoError(t, attrErr)
		assert.Equal(t, 60, attrs.VisibilityTimeout)
		assert.Equal(t, 10, attrs.DelaySeconds)
		assert.Equal(t, 2048, attrs.MaximumMessageSize)
	})

	t.Run("queue not found", func(t *testing.T) {
		setErr := m.SetQueueAttributes(ctx, "missing", map[string]int{"VisibilityTimeout": 60})
		require.Error(t, setErr)
		assert.Contains(t, setErr.Error(), "not found")
	})
}

func TestPurgeQueue(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	// Send some messages
	for i := 0; i < 3; i++ {
		_, sendErr := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "msg",
		})
		require.NoError(t, sendErr)
	}

	t.Run("purge removes all messages", func(t *testing.T) {
		require.NoError(t, m.PurgeQueue(ctx, info.URL))

		msgs, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 10,
		})
		require.NoError(t, recvErr)
		assert.Empty(t, msgs)
	})

	t.Run("queue not found", func(t *testing.T) {
		purgeErr := m.PurgeQueue(ctx, "missing")
		require.Error(t, purgeErr)
		assert.Contains(t, purgeErr.Error(), "not found")
	})
}

func TestPubSubMetricsEmission(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	mon := &pubsubMonMock{data: make(map[string][]mondriver.MetricDatum)}
	m := New(opts)
	m.SetMonitoring(mon)

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	t.Run("SendMessage emits send metrics", func(t *testing.T) {
		_, sendErr := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "hello",
		})
		require.NoError(t, sendErr)

		assert.NotEmpty(t, mon.data["pubsub.googleapis.com/topic/send_message_operation_count"])
		assert.NotEmpty(t, mon.data["pubsub.googleapis.com/topic/byte_cost"])
	})

	t.Run("ReceiveMessages emits pull metrics", func(t *testing.T) {
		_, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 10,
		})
		require.NoError(t, recvErr)

		assert.NotEmpty(t, mon.data["pubsub.googleapis.com/subscription/pull_message_operation_count"])
	})
}

func TestReceiveMessagesWithOptionsMetrics(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	mon := &pubsubMonMock{data: make(map[string][]mondriver.MetricDatum)}
	m := New(opts)
	m.SetMonitoring(mon)

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "rwopt-queue"})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "hello"})
	require.NoError(t, err)

	_, err = m.ReceiveMessagesWithOptions(ctx, info.URL, driver.ReceiveOptions{MaxMessages: 10})
	require.NoError(t, err)

	assert.NotEmpty(t, mon.data["pubsub.googleapis.com/subscription/pull_message_operation_count"])
}

type pubsubMonMock struct {
	data map[string][]mondriver.MetricDatum
}

func (m *pubsubMonMock) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	for _, d := range data {
		key := d.Namespace + "/" + d.MetricName
		m.data[key] = append(m.data[key], d)
	}

	return nil
}

func (m *pubsubMonMock) GetMetricData(
	_ context.Context, _ mondriver.GetMetricInput,
) (*mondriver.MetricDataResult, error) {
	return &mondriver.MetricDataResult{}, nil
}

func (m *pubsubMonMock) ListMetrics(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (m *pubsubMonMock) CreateAlarm(_ context.Context, _ mondriver.AlarmConfig) error {
	return nil
}

func (m *pubsubMonMock) DeleteAlarm(_ context.Context, _ string) error {
	return nil
}

func (m *pubsubMonMock) DescribeAlarms(_ context.Context, _ []string) ([]mondriver.AlarmInfo, error) {
	return nil, nil
}

func (m *pubsubMonMock) SetAlarmState(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *pubsubMonMock) CreateNotificationChannel(_ context.Context, _ mondriver.NotificationChannelConfig) (*mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (m *pubsubMonMock) DeleteNotificationChannel(_ context.Context, _ string) error {
	return nil
}

func (m *pubsubMonMock) GetNotificationChannel(_ context.Context, _ string) (*mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (m *pubsubMonMock) ListNotificationChannels(_ context.Context) ([]mondriver.NotificationChannelInfo, error) {
	return nil, nil
}

func (m *pubsubMonMock) GetAlarmHistory(_ context.Context, _ string, _ int) ([]mondriver.AlarmHistoryEntry, error) {
	return nil, nil
}

func TestSendMessageBatchFIFOWithDedup(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "fifo.fifo", FIFO: true})
	require.NoError(t, err)

	t.Run("batch send with dedup", func(t *testing.T) {
		result, batchErr := m.SendMessageBatch(ctx, info.URL, []driver.BatchSendEntry{
			{ID: "e1", Body: "msg1", GroupID: "g1", DeduplicationID: "dedup-1"},
			{ID: "e2", Body: "msg2", GroupID: "g1", DeduplicationID: "dedup-2"},
			{ID: "e3", Body: "msg1-dup", GroupID: "g1", DeduplicationID: "dedup-1"}, // duplicate
		})
		require.NoError(t, batchErr)
		assert.Len(t, result.Successful, 3) // all succeed, dedup returns existing ID
		assert.Empty(t, result.Failed)
	})

	t.Run("batch send to FIFO without GroupID fails", func(t *testing.T) {
		result, batchErr := m.SendMessageBatch(ctx, info.URL, []driver.BatchSendEntry{
			{ID: "e1", Body: "msg1", DeduplicationID: "d1"}, // missing GroupID
		})
		require.NoError(t, batchErr)
		assert.Empty(t, result.Successful)
		assert.Len(t, result.Failed, 1)
	})
}

func TestDeleteMessageBatchInvalidHandles(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	t.Run("all invalid handles", func(t *testing.T) {
		entries := []driver.BatchDeleteEntry{
			{ID: "d1", ReceiptHandle: "bad-handle-1"},
			{ID: "d2", ReceiptHandle: "bad-handle-2"},
		}
		result, batchErr := m.DeleteMessageBatch(ctx, info.URL, entries)
		require.NoError(t, batchErr)
		assert.Empty(t, result.Successful)
		assert.Len(t, result.Failed, 2)
	})

	t.Run("batch delete exceeds max size", func(t *testing.T) {
		entries := make([]driver.BatchDeleteEntry, 11)
		for i := range entries {
			entries[i] = driver.BatchDeleteEntry{ID: "d", ReceiptHandle: "h"}
		}
		_, batchErr := m.DeleteMessageBatch(ctx, info.URL, entries)
		require.Error(t, batchErr)
		assert.Contains(t, batchErr.Error(), "exceeds max")
	})
}

func TestReceiveMessagesWithOptions(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	// Send 5 messages
	for i := 0; i < 5; i++ {
		_, sendErr := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "msg",
		})
		require.NoError(t, sendErr)
	}

	t.Run("MaxMessages limits results", func(t *testing.T) {
		msgs, recvErr := m.ReceiveMessagesWithOptions(ctx, info.URL, driver.ReceiveOptions{
			MaxMessages: 2,
		})
		require.NoError(t, recvErr)
		assert.Len(t, msgs, 2)
	})

	t.Run("VisibilityTimeout override", func(t *testing.T) {
		// Receive with short visibility timeout of 5 seconds
		clk.Advance(31 * time.Second) // make previously received messages visible again
		msgs, recvErr := m.ReceiveMessagesWithOptions(ctx, info.URL, driver.ReceiveOptions{
			MaxMessages:       1,
			VisibilityTimeout: 5,
		})
		require.NoError(t, recvErr)
		assert.Len(t, msgs, 1)

		// After 6 seconds, message should be visible again
		clk.Advance(6 * time.Second)
		msgs2, recvErr := m.ReceiveMessagesWithOptions(ctx, info.URL, driver.ReceiveOptions{
			MaxMessages: 10,
		})
		require.NoError(t, recvErr)
		assert.GreaterOrEqual(t, len(msgs2), 1)
	})

	t.Run("queue not found", func(t *testing.T) {
		_, recvErr := m.ReceiveMessagesWithOptions(ctx, "missing", driver.ReceiveOptions{MaxMessages: 1})
		require.Error(t, recvErr)
		assert.Contains(t, recvErr.Error(), "not found")
	})

	t.Run("default max messages is 1", func(t *testing.T) {
		clk.Advance(31 * time.Second)
		msgs, recvErr := m.ReceiveMessagesWithOptions(ctx, info.URL, driver.ReceiveOptions{})
		require.NoError(t, recvErr)
		assert.Len(t, msgs, 1)
	})
}

func TestSetQueueAttributesVarious(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	t.Run("set MessageRetentionPeriod", func(t *testing.T) {
		require.NoError(t, m.SetQueueAttributes(ctx, info.URL, map[string]int{
			"MessageRetentionPeriod": 172800,
		}))

		attrs, attrErr := m.GetQueueAttributes(ctx, info.URL)
		require.NoError(t, attrErr)
		assert.Equal(t, 172800, attrs.MessageRetentionPeriod)
	})

	t.Run("set multiple attributes at once", func(t *testing.T) {
		require.NoError(t, m.SetQueueAttributes(ctx, info.URL, map[string]int{
			"DelaySeconds":       15,
			"VisibilityTimeout":  90,
			"MaximumMessageSize": 4096,
		}))

		attrs, attrErr := m.GetQueueAttributes(ctx, info.URL)
		require.NoError(t, attrErr)
		assert.Equal(t, 15, attrs.DelaySeconds)
		assert.Equal(t, 90, attrs.VisibilityTimeout)
		assert.Equal(t, 4096, attrs.MaximumMessageSize)
	})
}

func TestGetQueueAttributesMessageCounts(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	// Send 3 messages
	for i := 0; i < 3; i++ {
		_, sendErr := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: info.URL, Body: "msg",
		})
		require.NoError(t, sendErr)
	}

	t.Run("all visible initially", func(t *testing.T) {
		attrs, attrErr := m.GetQueueAttributes(ctx, info.URL)
		require.NoError(t, attrErr)
		assert.Equal(t, 3, attrs.ApproximateMessageCount)
		assert.Equal(t, 0, attrs.ApproximateNotVisibleCount)
	})

	t.Run("after receiving some become not visible", func(t *testing.T) {
		_, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 2,
		})
		require.NoError(t, recvErr)

		attrs, attrErr := m.GetQueueAttributes(ctx, info.URL)
		require.NoError(t, attrErr)
		assert.Equal(t, 1, attrs.ApproximateMessageCount)
		assert.Equal(t, 2, attrs.ApproximateNotVisibleCount)
	})

	t.Run("FIFO queue flag", func(t *testing.T) {
		fifoInfo, fifoErr := m.CreateQueue(ctx, driver.QueueConfig{Name: "fifo.fifo", FIFO: true})
		require.NoError(t, fifoErr)

		attrs, attrErr := m.GetQueueAttributes(ctx, fifoInfo.URL)
		require.NoError(t, attrErr)
		assert.True(t, attrs.FifoQueue)
	})
}

func TestPurgeQueueEmpty(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	// Purge an already-empty queue should succeed without error
	require.NoError(t, m.PurgeQueue(ctx, info.URL))

	msgs, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
		QueueURL: info.URL, MaxMessages: 10,
	})
	require.NoError(t, recvErr)
	assert.Empty(t, msgs)
}

func TestChangeVisibility(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "msg1"})
	require.NoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: info.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	t.Run("extend visibility", func(t *testing.T) {
		require.NoError(t, m.ChangeVisibility(ctx, info.URL, msgs[0].ReceiptHandle, 60))

		// After 31 seconds message should still be invisible (extended to 60)
		clk.Advance(31 * time.Second)
		visible, recvErr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: info.URL, MaxMessages: 1,
		})
		require.NoError(t, recvErr)
		assert.Empty(t, visible)
	})

	t.Run("queue not found", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, "missing", "handle", 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("handle not found", func(t *testing.T) {
		err := m.ChangeVisibility(ctx, info.URL, "bad-handle", 10)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestPubSubMetricsDeleteMessage(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	mon := &pubsubMonMock{data: make(map[string][]mondriver.MetricDatum)}
	m := New(opts)
	m.SetMonitoring(mon)

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "hello"})
	require.NoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: info.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	require.NoError(t, m.DeleteMessage(ctx, info.URL, msgs[0].ReceiptHandle))
	assert.NotEmpty(t, mon.data["pubsub.googleapis.com/subscription/ack_message_operation_count"])
}

func TestClampMaxMessages(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{name: "zero defaults to 1", input: 0, want: 1},
		{name: "negative defaults to 1", input: -5, want: 1},
		{name: "normal value", input: 5, want: 5},
		{name: "exceeds max clamped to 10", input: 15, want: 10},
		{name: "exactly max", input: 10, want: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clampMaxMessages(tt.input))
		})
	}
}

func TestTriggerOnSendMessage(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "q1"})
	require.NoError(t, err)

	var triggered bool
	var triggeredBody string

	m.SetTrigger(info.URL, func(queueURL string, message driver.Message) {
		triggered = true
		triggeredBody = message.Body
	})

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "trigger-me"})
	require.NoError(t, err)
	assert.True(t, triggered)
	assert.Equal(t, "trigger-me", triggeredBody)

	// Remove trigger
	m.RemoveTrigger(info.URL)
	triggered = false

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: info.URL, Body: "no-trigger"})
	require.NoError(t, err)
	assert.False(t, triggered)
}
