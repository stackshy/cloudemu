package servicebus

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeadLetterMissingStorePreservesMessage guards the latent data-loss bug:
// when a message exceeds maxReceiveCount but its dead-letter store is absent, it
// must stay on the main queue rather than being silently dropped.
func TestDeadLetterMissingStorePreservesMessage(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	mainInfo, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name: "guarded",
		DeadLetterQueue: &driver.DeadLetterConfig{
			TargetQueueURL:  "https://missing.example/nope",
			MaxReceiveCount: 1,
		},
	})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: mainInfo.URL, Body: "keep-me"})
	require.NoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1, VisibilityTimeout: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	clk.Advance(2 * time.Second)

	// Second receive would exceed maxReceiveCount, but the DLQ store is missing.
	msgs, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	assert.Empty(t, msgs)

	// The message is preserved rather than lost.
	info, err := m.GetQueueInfo(ctx, mainInfo.URL)
	require.NoError(t, err)
	assert.Equal(t, 1, info.ApproxMessageCount)
}

// TestSendPreservesSystemProperties confirms brokered-message system properties
// survive a send/receive round-trip through the driver.
func TestSendPreservesSystemProperties(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	url := createStdQueue(t, m)

	_, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL:         url,
		Body:             "b",
		SystemProperties: map[string]string{"MessageId": "m-9", "Label": "L"},
	})
	require.NoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "m-9", msgs[0].SystemProperties["MessageId"])
	assert.Equal(t, "L", msgs[0].SystemProperties["Label"])
}

// TestDeadLetterOnExpiration confirms an expired message is routed to the DLQ
// (not dropped) when deadLetteringOnMessageExpiration is enabled.
func TestDeadLetterOnExpiration(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	dlqInfo, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "expiry-dlq"})
	require.NoError(t, err)

	mainInfo, err := m.CreateQueue(ctx, driver.QueueConfig{
		Name:                   "expiry-main",
		DeadLetterQueue:        &driver.DeadLetterConfig{TargetQueueURL: dlqInfo.URL, MaxReceiveCount: 10},
		DeadLetterOnExpiration: true,
	})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: mainInfo.URL, Body: "expiring", MessageTTLSeconds: ttlPtr(5),
	})
	require.NoError(t, err)

	clk.Advance(6 * time.Second)

	// The reaping receive on the main queue routes the expired message to the DLQ.
	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: mainInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	assert.Empty(t, msgs)

	dlqMsgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: dlqInfo.URL, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, dlqMsgs, 1)
	assert.Equal(t, "expiring", dlqMsgs[0].Body)
}
