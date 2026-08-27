package servicebus

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScheduledMessageSurvivesUntilEnqueue confirms a message scheduled for a
// future enqueue time with a TTL shorter than the schedule delay is still
// delivered once it becomes active (rather than being silently reaped before it
// is ever visible). Real Azure computes expires-at-utc from the enqueued time,
// not the submit time.
func TestScheduledMessageSurvivesUntilEnqueue(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	url := createStdQueue(t, m)

	// Scheduled 60s out with a 30s TTL: on real Azure the message materializes at
	// +60s and lives until +90s. The buggy path expired it at submit+30s (+30s),
	// before it ever became visible.
	_, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: url, Body: "scheduled", DelaySeconds: 60, MessageTTLSeconds: ttlPtr(30),
	})
	require.NoError(t, err)

	// Advance past the scheduled enqueue time (and past submit+TTL).
	clk.Advance(61 * time.Second)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1, "scheduled message must survive until its enqueue time")
	assert.Equal(t, "scheduled", msgs[0].Body)

	// TTL is measured from the scheduled enqueue time: expires-at-utc = enqueue + TTL.
	wantExpiry := clk.Now().Add((90 - 61) * time.Second) // T0+90s
	assert.Equal(t, wantExpiry, msgs[0].ExpiresAt, "TTL must count from the scheduled enqueue time")
}

// TestScheduledMessageExpiresAfterEnqueuePlusTTL confirms the scheduled
// message's TTL clock runs from the enqueue time: it is gone only after
// enqueue+TTL, not before.
func TestScheduledMessageExpiresAfterEnqueuePlusTTL(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	url := createStdQueue(t, m)

	_, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: url, Body: "scheduled", DelaySeconds: 60, MessageTTLSeconds: ttlPtr(30),
	})
	require.NoError(t, err)

	// Past enqueue+TTL (T0+90s): the message has now genuinely expired.
	clk.Advance(91 * time.Second)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	require.NoError(t, err)
	assert.Empty(t, msgs, "scheduled message expires at enqueue+TTL")
}

// TestNonScheduledTTLCountsFromSubmit confirms the fix does not change a
// non-scheduled message: with no delay, the enqueue time equals the submit time,
// so the TTL still counts from submit.
func TestNonScheduledTTLCountsFromSubmit(t *testing.T) {
	ctx := context.Background()

	send := func(m *Mock, url string) {
		_, err := m.SendMessage(ctx, driver.SendMessageInput{
			QueueURL: url, Body: "plain", MessageTTLSeconds: ttlPtr(30),
		})
		require.NoError(t, err)
	}

	// Present just before submit+TTL.
	m, clk := newTestMock()
	url := createStdQueue(t, m)
	send(m, url)
	clk.Advance(29 * time.Second)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, clk.Now().Add(time.Second), msgs[0].ExpiresAt, "non-scheduled TTL counts from submit")

	// Gone just after submit+TTL.
	m2, clk2 := newTestMock()
	url2 := createStdQueue(t, m2)
	send(m2, url2)
	clk2.Advance(31 * time.Second)

	msgs2, err := m2.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url2, MaxMessages: 1})
	require.NoError(t, err)
	assert.Empty(t, msgs2, "non-scheduled message expires at submit+TTL")
}
