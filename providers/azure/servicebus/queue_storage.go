package servicebus

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// Compile-time check that Mock implements the Azure-specific queue surface.
var _ driver.AzureQueueStorage = (*Mock)(nil)

// defaultMessageRetention is the Azure Queue Storage default message TTL.
const defaultMessageRetention = 7 * 24 * time.Hour

// maxQueueStorageMessages is the maximum number of messages Azure Queue Storage
// returns from a single Get Messages or Peek Messages call. This differs from
// Service Bus's receive cap, so Queue Storage clamps against its own limit.
const maxQueueStorageMessages = 32

// clampQueueStorageMessages bounds a requested message count to Azure Queue
// Storage's [1, 32] range, defaulting to 1 when unset or non-positive.
func clampQueueStorageMessages(maxMessages int) int {
	if maxMessages <= 0 {
		return 1
	}

	if maxMessages > maxQueueStorageMessages {
		return maxQueueStorageMessages
	}

	return maxMessages
}

// DequeueMessages retrieves up to maxMessages visible messages, hiding each for
// visibilityTimeout seconds and issuing a fresh pop receipt (Azure Get Messages).
func (m *Mock) DequeueMessages(
	_ context.Context, queueURL string, maxMessages, visibilityTimeout int,
) ([]driver.Message, error) {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	maxMsgs := clampQueueStorageMessages(maxMessages)

	visTimeout := visibilityTimeout
	if visTimeout == 0 {
		visTimeout = qd.visibilityTimeout
	}

	now := m.opts.Clock.Now()
	results, toRemove := m.collectVisibleMessages(qd, maxMsgs, visTimeout, now)

	removeByIndices(qd, toRemove)

	if results == nil {
		results = []driver.Message{}
	}

	m.emitMetric(qd.info.Name, map[string]float64{
		"OutgoingMessages": float64(len(results)), "ActiveMessages": float64(len(qd.messages)),
	})

	return results, nil
}

// messageExpiry returns when a message expires given the queue's retention.
func (qd *queueData) messageExpiry(msg *sbMessage) time.Time {
	if qd.messageRetention > 0 {
		return msg.SentAt.Add(time.Duration(qd.messageRetention) * time.Second)
	}

	return msg.SentAt.Add(defaultMessageRetention)
}

// PeekMessages returns up to maxMessages visible messages without changing
// their visibility or issuing pop receipts (Azure Peek Messages).
func (m *Mock) PeekMessages(_ context.Context, queueURL string, maxMessages int) ([]driver.AzureQueueMessage, error) {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	limit := clampQueueStorageMessages(maxMessages)
	now := m.opts.Clock.Now()

	out := make([]driver.AzureQueueMessage, 0, limit)

	for _, msg := range qd.messages {
		if len(out) >= limit {
			break
		}

		if msg.VisibleAt.After(now) {
			continue
		}

		out = append(out, driver.AzureQueueMessage{
			MessageID:    msg.ID,
			Body:         msg.Body,
			ReceiveCount: msg.ReceiveCount,
			InsertedAt:   msg.SentAt,
			ExpiresAt:    qd.messageExpiry(msg),
		})
	}

	return out, nil
}

// UpdateMessage updates a dequeued message's content and visibility, returning a
// fresh pop receipt (Azure Update Message). The supplied pop receipt must match
// the message's current receipt.
func (m *Mock) UpdateMessage(
	_ context.Context, queueURL, messageID, popReceipt string, visibilityTimeout int, body *string,
) (driver.AzureUpdateMessageResult, error) {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return driver.AzureUpdateMessageResult{}, cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	for _, msg := range qd.messages {
		if msg.ID != messageID || msg.ReceiptHandle != popReceipt {
			continue
		}

		now := m.opts.Clock.Now()

		if body != nil {
			msg.Body = *body
		}

		msg.VisibleAt = now.Add(time.Duration(visibilityTimeout) * time.Second)
		msg.ReceiptHandle = idgen.GenerateID("sb-lock-")

		return driver.AzureUpdateMessageResult{
			PopReceipt:      msg.ReceiptHandle,
			TimeNextVisible: msg.VisibleAt,
		}, nil
	}

	return driver.AzureUpdateMessageResult{}, cerrors.Newf(
		cerrors.NotFound, "message %q with the specified pop receipt was not found", messageID,
	)
}

// GetQueueMetadata reports the approximate visible-message count and the queue's
// user metadata (Azure Get Queue Metadata).
func (m *Mock) GetQueueMetadata(_ context.Context, queueURL string) (driver.AzureQueueMetadata, error) {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return driver.AzureQueueMetadata{}, cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	// x-ms-approximate-messages-count reflects the total messages in the queue,
	// counting both visible and invisible (in-flight) messages; it ignores
	// message visibility. See Get Queue Metadata (Azure Queue Storage) docs.
	count := len(qd.messages)

	meta := make(map[string]string, len(qd.metadata))
	for k, v := range qd.metadata {
		meta[k] = v
	}

	return driver.AzureQueueMetadata{ApproximateMessageCount: count, Metadata: meta}, nil
}

// SetQueueMetadata replaces the queue's user metadata (Azure Set Queue Metadata).
func (m *Mock) SetQueueMetadata(_ context.Context, queueURL string, metadata map[string]string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	meta := make(map[string]string, len(metadata))
	for k, v := range metadata {
		meta[k] = v
	}

	qd.metadata = meta

	return nil
}
