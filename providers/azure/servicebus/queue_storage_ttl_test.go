package servicebus

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ttlPtr returns a pointer to seconds, for SendMessageInput.MessageTTLSeconds.
func ttlPtr(seconds int) *int {
	return &seconds
}

// TestMessageTTLExpiry confirms a message sent with a short Azure Queue
// Storage messagettl is lazily dropped from Peek/Dequeue Messages once it
// expires, while a messagettl=-1 message survives indefinitely.
func TestMessageTTLExpiry(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	url := createStdQueue(t, m)

	_, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: url, Body: "expires-soon", MessageTTLSeconds: ttlPtr(5),
	})
	require.NoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: url, Body: "never-expires", MessageTTLSeconds: ttlPtr(-1),
	})
	require.NoError(t, err)

	// Before the TTL elapses, both messages are visible via Peek.
	peeked, err := m.PeekMessages(ctx, url, 10)
	require.NoError(t, err)
	assert.Len(t, peeked, 2)

	clk.Advance(10 * time.Second)

	// After the TTL elapses, only the never-expiring message remains.
	peeked, err = m.PeekMessages(ctx, url, 10)
	require.NoError(t, err)
	require.Len(t, peeked, 1)
	assert.Equal(t, "never-expires", peeked[0].Body)

	msgs, err := m.DequeueMessages(ctx, url, 10, 30)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "never-expires", msgs[0].Body)
}

// TestMessageTTLUnsetNeverExpires confirms a message sent through the generic
// cross-cloud SendMessage path (Service Bus dataplane; MessageTTLSeconds left
// nil) keeps its existing unlimited-retention behavior rather than picking up
// Queue Storage's 7-day default.
func TestMessageTTLUnsetNeverExpires(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	url := createStdQueue(t, m)

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: "plain"})
	require.NoError(t, err)

	clk.Advance(365 * 24 * time.Hour) // a full year later

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 10})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "plain", msgs[0].Body)
}
