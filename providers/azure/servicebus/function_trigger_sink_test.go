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

// recordingTopicSink extends recordingSink with the optional
// TopicFunctionTriggerSink method so dispatchFunctionTrigger's type assertion
// picks it for a topic-subscription backing queue.
type recordingTopicSink struct {
	recordingSink
	topicCalls int
	binding    string
	topic      string
	sub        string
	lastBody   string
}

func (s *recordingTopicSink) DeliverTopicFunctionTrigger(
	_ context.Context, bindingType, topicName, subscriptionName string, body []byte,
) {
	s.topicCalls++
	s.binding = bindingType
	s.topic = topicName
	s.sub = subscriptionName
	s.lastBody = string(body)
}

// createSubQueue creates a queue named after the "{ns}/{topic}/subscriptions/{sub}"
// convention server/azure/servicebus's createSubQueue uses for a topic
// subscription's backing store.
func createSubQueue(t *testing.T, m *Mock, topic, sub string) string {
	t.Helper()

	ctx := context.Background()
	info, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "test-ns/" + topic + "/subscriptions/" + sub})
	require.NoError(t, err)

	return info.URL
}

// TestSendMessageDispatchesTopicFunctionTriggerSink proves a message sent to a
// topic subscription's backing queue dispatches DeliverTopicFunctionTrigger
// with the parsed topic and subscription, not DeliverFunctionTrigger.
func TestSendMessageDispatchesTopicFunctionTriggerSink(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createSubQueue(t, m, "orders", "all")

	sink := &recordingTopicSink{}
	m.SetFunctionTriggerSink(sink, "serviceBusTrigger")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "payload"})
	require.NoError(t, err)

	assert.Equal(t, 1, sink.topicCalls)
	assert.Equal(t, "serviceBusTrigger", sink.binding)
	assert.Equal(t, "orders", sink.topic)
	assert.Equal(t, "all", sink.sub)
	assert.Equal(t, "payload", sink.lastBody)
	assert.Equal(t, 0, sink.calls, "a subscription delivery must not also fire the queue-shaped trigger")
}

// TestSendMessageTopicFallsBackToQueueSinkWithoutTopicSupport proves a sink that
// only implements FunctionTriggerSink still fires (with the raw composite
// queue name) when dispatching a subscription-backed send, so a queue-only test
// double or sink keeps working unchanged.
func TestSendMessageTopicFallsBackToQueueSinkWithoutTopicSupport(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()
	url := createSubQueue(t, m, "orders", "all")

	sink := &recordingSink{}
	m.SetFunctionTriggerSink(sink, "serviceBusTrigger")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "payload"})
	require.NoError(t, err)

	assert.Equal(t, 1, sink.calls)
	assert.Equal(t, "test-ns/orders/subscriptions/all", sink.queue)
}

// TestSendMessageTopicDuplicateDoesNotDispatch mirrors
// TestFIFODuplicateDoesNotDispatch for the topic/subscription dispatch path.
func TestSendMessageTopicDuplicateDoesNotDispatch(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	info, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name:                       "test-ns/orders/subscriptions/all",
		RequiresDuplicateDetection: true,
	})
	require.NoError(t, err)

	sink := &recordingTopicSink{}
	m.SetFunctionTriggerSink(sink, "serviceBusTrigger")

	in := driver.SendMessageInput{QueueURL: info.URL, Body: "x", SystemProperties: map[string]string{"MessageId": "m1"}}

	_, err = m.SendMessage(ctx, in)
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, 1, sink.topicCalls, "duplicate send must not dispatch a second topic trigger")
}
