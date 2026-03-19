package pubsub

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/messagequeue/driver"
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
