package servicebus

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingSink struct {
	calls    int
	binding  string
	queue    string
	lastBody string
}

func (s *recordingSink) DeliverFunctionTrigger(_ context.Context, bindingType, queueName string, body []byte) {
	s.calls++
	s.binding = bindingType
	s.queue = queueName
	s.lastBody = string(body)
}

func TestSendMessageDispatchesFunctionTriggerSink(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	sink := &recordingSink{}
	m.SetFunctionTriggerSink(sink, "queueTrigger")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "payload"})
	require.NoError(t, err)

	assert.Equal(t, 1, sink.calls)
	assert.Equal(t, "queueTrigger", sink.binding)
	assert.Equal(t, "test-queue", sink.queue, "the sink receives the queue name, not its URL")
	assert.Equal(t, "payload", sink.lastBody)
}

func TestSendMessageNoSinkIsNoop(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createStdQueue(t, m)

	// No sink wired: enqueue must still succeed.
	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "payload"})
	require.NoError(t, err)
}

// TestFIFODuplicateDoesNotDispatch proves a silently-accepted duplicate send
// (not re-enqueued) fires no trigger sink.
func TestFIFODuplicateDoesNotDispatch(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "jobs.fifo", FIFO: true})
	require.NoError(t, err)

	sink := &recordingSink{}
	m.SetFunctionTriggerSink(sink, "serviceBusTrigger")

	in := driver.SendMessageInput{QueueURL: info.URL, Body: "x", GroupID: "g", DeduplicationID: "d1"}

	_, err = m.SendMessage(ctx, in)
	require.NoError(t, err)

	// Same DeduplicationID within the window: accepted but not re-enqueued.
	_, err = m.SendMessage(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, 1, sink.calls, "duplicate send must not dispatch a second trigger")
}
