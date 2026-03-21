package messagequeue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/messagequeue/driver"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDriver implements driver.MessageQueue for testing the portable wrapper.
type mockDriver struct {
	queues   map[string]*driver.QueueInfo
	messages map[string][]driver.Message
	msgSeq   int
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		queues:   make(map[string]*driver.QueueInfo),
		messages: make(map[string][]driver.Message),
	}
}

func (m *mockDriver) CreateQueue(_ context.Context, config driver.QueueConfig) (*driver.QueueInfo, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name required")
	}

	url := "https://sqs.us-east-1.amazonaws.com/123456789012/" + config.Name

	if _, ok := m.queues[url]; ok {
		return nil, fmt.Errorf("already exists")
	}

	info := &driver.QueueInfo{URL: url, Name: config.Name, FIFO: config.FIFO}
	m.queues[url] = info

	return info, nil
}

func (m *mockDriver) DeleteQueue(_ context.Context, url string) error {
	if _, ok := m.queues[url]; !ok {
		return fmt.Errorf("not found")
	}

	delete(m.queues, url)
	delete(m.messages, url)

	return nil
}

func (m *mockDriver) GetQueueInfo(_ context.Context, url string) (*driver.QueueInfo, error) {
	info, ok := m.queues[url]
	if !ok {
		return nil, fmt.Errorf("not found")
	}

	return info, nil
}

func (m *mockDriver) ListQueues(_ context.Context, _ string) ([]driver.QueueInfo, error) {
	result := make([]driver.QueueInfo, 0, len(m.queues))
	for _, info := range m.queues {
		result = append(result, *info)
	}

	return result, nil
}

func (m *mockDriver) SendMessage(_ context.Context, input driver.SendMessageInput) (*driver.SendMessageOutput, error) {
	if _, ok := m.queues[input.QueueURL]; !ok {
		return nil, fmt.Errorf("queue not found")
	}

	m.msgSeq++
	msgID := fmt.Sprintf("msg-%d", m.msgSeq)
	msg := driver.Message{MessageID: msgID, Body: input.Body, ReceiptHandle: "rh-" + msgID}
	m.messages[input.QueueURL] = append(m.messages[input.QueueURL], msg)

	return &driver.SendMessageOutput{MessageID: msgID}, nil
}

func (m *mockDriver) ReceiveMessages(_ context.Context, input driver.ReceiveMessageInput) ([]driver.Message, error) {
	if _, ok := m.queues[input.QueueURL]; !ok {
		return nil, fmt.Errorf("queue not found")
	}

	msgs := m.messages[input.QueueURL]
	max := input.MaxMessages

	if max <= 0 || max > len(msgs) {
		max = len(msgs)
	}

	return msgs[:max], nil
}

func (m *mockDriver) DeleteMessage(_ context.Context, queueURL, _ string) error {
	if _, ok := m.queues[queueURL]; !ok {
		return fmt.Errorf("queue not found")
	}

	return nil
}

func (m *mockDriver) ChangeVisibility(_ context.Context, queueURL, _ string, _ int) error {
	if _, ok := m.queues[queueURL]; !ok {
		return fmt.Errorf("queue not found")
	}

	return nil
}

func (m *mockDriver) SendMessageBatch(
	_ context.Context, queue string, entries []driver.BatchSendEntry,
) (*driver.BatchSendResult, error) {
	if _, ok := m.queues[queue]; !ok {
		return nil, fmt.Errorf("queue not found")
	}

	result := &driver.BatchSendResult{}
	for _, e := range entries {
		m.msgSeq++
		msgID := fmt.Sprintf("msg-%d", m.msgSeq)
		result.Successful = append(result.Successful, driver.BatchSendResultEntry{ID: e.ID, MessageID: msgID})
	}

	return result, nil
}

func (m *mockDriver) DeleteMessageBatch(
	_ context.Context, queue string, entries []driver.BatchDeleteEntry,
) (*driver.BatchDeleteResult, error) {
	if _, ok := m.queues[queue]; !ok {
		return nil, fmt.Errorf("queue not found")
	}

	result := &driver.BatchDeleteResult{}
	for _, e := range entries {
		result.Successful = append(result.Successful, e.ID)
	}

	return result, nil
}

func (m *mockDriver) ReceiveMessagesWithOptions(
	_ context.Context, queue string, _ driver.ReceiveOptions,
) ([]driver.Message, error) {
	if _, ok := m.queues[queue]; !ok {
		return nil, fmt.Errorf("queue not found")
	}

	return m.messages[queue], nil
}

func (m *mockDriver) GetQueueAttributes(_ context.Context, queue string) (*driver.QueueAttributes, error) {
	if _, ok := m.queues[queue]; !ok {
		return nil, fmt.Errorf("queue not found")
	}

	return &driver.QueueAttributes{VisibilityTimeout: 30}, nil
}

func (m *mockDriver) SetQueueAttributes(_ context.Context, queue string, _ map[string]int) error {
	if _, ok := m.queues[queue]; !ok {
		return fmt.Errorf("queue not found")
	}

	return nil
}

func (m *mockDriver) PurgeQueue(_ context.Context, queue string) error {
	if _, ok := m.queues[queue]; !ok {
		return fmt.Errorf("queue not found")
	}

	m.messages[queue] = nil

	return nil
}

func newTestMQ(opts ...Option) *MQ {
	return NewMQ(newMockDriver(), opts...)
}

func setupQueue(t *testing.T, mq *MQ, name string) string {
	t.Helper()

	ctx := context.Background()

	info, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: name})
	require.NoError(t, err)

	return info.URL
}

func TestNewMQ(t *testing.T) {
	mq := newTestMQ()
	require.NotNil(t, mq)
	require.NotNil(t, mq.driver)
}

func TestCreateQueue(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: "my-queue"})
		require.NoError(t, err)
		assert.Equal(t, "my-queue", info.Name)
		assert.NotEmpty(t, info.URL)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := mq.CreateQueue(ctx, driver.QueueConfig{})
		require.Error(t, err)
	})
}

func TestDeleteQueue(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "del-queue")

	t.Run("success", func(t *testing.T) {
		err := mq.DeleteQueue(ctx, url)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := mq.DeleteQueue(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetQueueInfo(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "info-queue")

	t.Run("success", func(t *testing.T) {
		info, err := mq.GetQueueInfo(ctx, url)
		require.NoError(t, err)
		assert.Equal(t, "info-queue", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := mq.GetQueueInfo(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListQueues(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()

	queues, err := mq.ListQueues(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 0, len(queues))

	setupQueue(t, mq, "q-a")
	setupQueue(t, mq, "q-b")

	queues, err = mq.ListQueues(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 2, len(queues))
}

func TestSendAndReceiveMessage(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "msg-queue")

	out, err := mq.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "hello"})
	require.NoError(t, err)
	assert.NotEmpty(t, out.MessageID)

	msgs, err := mq.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, len(msgs))
	assert.Equal(t, "hello", msgs[0].Body)
}

func TestDeleteMessage(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "delmsg-queue")

	t.Run("success", func(t *testing.T) {
		err := mq.DeleteMessage(ctx, url, "rh-1")
		require.NoError(t, err)
	})

	t.Run("queue not found", func(t *testing.T) {
		err := mq.DeleteMessage(ctx, "nonexistent", "rh-1")
		require.Error(t, err)
	})
}

func TestChangeVisibility(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "vis-queue")

	t.Run("success", func(t *testing.T) {
		err := mq.ChangeVisibility(ctx, url, "rh-1", 60)
		require.NoError(t, err)
	})

	t.Run("queue not found", func(t *testing.T) {
		err := mq.ChangeVisibility(ctx, "nonexistent", "rh-1", 60)
		require.Error(t, err)
	})
}

func TestSendMessageBatch(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "batch-queue")

	entries := []driver.BatchSendEntry{
		{ID: "e1", Body: "msg1"},
		{ID: "e2", Body: "msg2"},
	}

	result, err := mq.SendMessageBatch(ctx, url, entries)
	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Successful))
}

func TestDeleteMessageBatch(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "delbatch-queue")

	entries := []driver.BatchDeleteEntry{
		{ID: "e1", ReceiptHandle: "rh-1"},
		{ID: "e2", ReceiptHandle: "rh-2"},
	}

	result, err := mq.DeleteMessageBatch(ctx, url, entries)
	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Successful))
}

func TestReceiveMessagesWithOptions(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "opts-queue")

	_, err := mq.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "hello"})
	require.NoError(t, err)

	msgs, err := mq.ReceiveMessagesWithOptions(ctx, url, driver.ReceiveOptions{MaxMessages: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, len(msgs))
}

func TestGetQueueAttributes(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "attr-queue")

	t.Run("success", func(t *testing.T) {
		attrs, err := mq.GetQueueAttributes(ctx, url)
		require.NoError(t, err)
		assert.Equal(t, 30, attrs.VisibilityTimeout)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := mq.GetQueueAttributes(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestSetQueueAttributes(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "setattr-queue")

	t.Run("success", func(t *testing.T) {
		err := mq.SetQueueAttributes(ctx, url, map[string]int{"VisibilityTimeout": 60})
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := mq.SetQueueAttributes(ctx, "nonexistent", map[string]int{"VisibilityTimeout": 60})
		require.Error(t, err)
	})
}

func TestPurgeQueue(t *testing.T) {
	mq := newTestMQ()
	ctx := context.Background()
	url := setupQueue(t, mq, "purge-queue")

	_, err := mq.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "hello"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := mq.PurgeQueue(ctx, url)
		require.NoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := mq.PurgeQueue(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestMQWithRecorder(t *testing.T) {
	rec := recorder.New()
	mq := newTestMQ(WithRecorder(rec))
	ctx := context.Background()

	_, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: "rec-queue"})
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 1)

	createCalls := rec.CallCountFor("messagequeue", "CreateQueue")
	assert.Equal(t, 1, createCalls)
}

func TestMQWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	mq := newTestMQ(WithRecorder(rec))
	ctx := context.Background()

	_, _ = mq.GetQueueInfo(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last)
	assert.NotNil(t, last.Error)
}

func TestMQWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	mq := newTestMQ(WithMetrics(mc))
	ctx := context.Background()

	_, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: "met-queue"})
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 1)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 1)
}

func TestMQWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	mq := newTestMQ(WithMetrics(mc))
	ctx := context.Background()

	_, _ = mq.GetQueueInfo(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestMQWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	mq := newTestMQ(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("messagequeue", "CreateQueue", injectedErr, inject.Always{})

	_, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: "fail-queue"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestMQWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	mq := newTestMQ(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("messagequeue", "SendMessage", injectedErr, inject.Always{})

	url := setupQueue(t, mq, "inj-queue")

	_, err := mq.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "hello"})
	require.Error(t, err)

	sendCalls := rec.CallsFor("messagequeue", "SendMessage")
	assert.Equal(t, 1, len(sendCalls))
	assert.NotNil(t, sendCalls[0].Error)
}

func TestMQWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	mq := newTestMQ(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("messagequeue", "CreateQueue", injectedErr, inject.Always{})

	_, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: "test"})
	require.Error(t, err)

	inj.Remove("messagequeue", "CreateQueue")

	_, err = mq.CreateQueue(ctx, driver.QueueConfig{Name: "test"})
	require.NoError(t, err)
}

func TestMQWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	mq := newTestMQ(WithLatency(latency))
	ctx := context.Background()

	_, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: "lat-queue"})
	require.NoError(t, err)
}

func TestMQAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	mq := NewMQ(newMockDriver(),
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := mq.CreateQueue(ctx, driver.QueueConfig{Name: "all-opts"})
	require.NoError(t, err)

	assert.Equal(t, 1, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 1, q.ByName("calls_total").Count())
}
