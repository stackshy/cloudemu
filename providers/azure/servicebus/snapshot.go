package servicebus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// serviceBusSnapshot is the full serialized state of the Service Bus mock: every
// queue keyed by its URL. queueData is built almost entirely from unexported
// fields, so it is promoted to an exported snapshot form (its message elements,
// sbMessage, are already all-exported and serialize directly). The mutexes, the
// wired function triggers, the monitoring backend, and *config.Options are
// intentionally not captured.
type serviceBusSnapshot struct {
	Queues map[string]*queueSnapshot `json:"queues,omitempty"`
}

// queueSnapshot mirrors queueData, promoting its unexported attribute fields,
// message list, and dedup indexes to exported forms.
type queueSnapshot struct {
	Info                   driver.QueueInfo         `json:"info"`
	Messages               []*sbMessage             `json:"messages,omitempty"`
	DelaySeconds           int                      `json:"delaySeconds,omitempty"`
	VisibilityTimeout      int                      `json:"visibilityTimeout,omitempty"`
	MaxMessageSize         int                      `json:"maxMessageSize,omitempty"`
	MessageRetention       int                      `json:"messageRetention,omitempty"`
	CreatedAt              time.Time                `json:"createdAt"`
	LastModifiedAt         time.Time                `json:"lastModifiedAt"`
	DeduplicationIndex     map[string]time.Time     `json:"deduplicationIndex,omitempty"`
	RequiresDupDetection   bool                     `json:"requiresDupDetection,omitempty"`
	DupDetectionWindow     time.Duration            `json:"dupDetectionWindow,omitempty"`
	DedupByMessageID       map[string]time.Time     `json:"dedupByMessageId,omitempty"`
	DLQConfig              *driver.DeadLetterConfig `json:"dlqConfig,omitempty"`
	DeadLetterOnExpiration bool                     `json:"deadLetterOnExpiration,omitempty"`
	Metadata               map[string]string        `json:"metadata,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Service Bus message bodies are the queue state, not bulk object assets.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := serviceBusSnapshot{Queues: make(map[string]*queueSnapshot, m.queues.Len())}

	for url, qd := range m.queues.All() {
		snap.Queues[url] = snapshotQueue(qd)
	}

	if len(snap.Queues) == 0 {
		snap.Queues = nil
	}

	return json.Marshal(snap)
}

func snapshotQueue(qd *queueData) *queueSnapshot {
	qd.mu.Lock()
	defer qd.mu.Unlock()

	qs := &queueSnapshot{
		Info: qd.info, DelaySeconds: qd.delaySeconds, VisibilityTimeout: qd.visibilityTimeout,
		MaxMessageSize: qd.maxMessageSize, MessageRetention: qd.messageRetention,
		CreatedAt: qd.createdAt, LastModifiedAt: qd.lastModifiedAt,
		DeduplicationIndex: qd.deduplicationIndex, RequiresDupDetection: qd.requiresDupDetection,
		DupDetectionWindow: qd.dupDetectionWindow, DedupByMessageID: qd.dedupByMessageID,
		DLQConfig: qd.dlqConfig, DeadLetterOnExpiration: qd.deadLetterOnExpiration,
		Metadata: qd.metadata,
	}

	if len(qd.messages) > 0 {
		qs.Messages = make([]*sbMessage, len(qd.messages))
		copy(qs.Messages, qd.messages)
	}

	return qs
}

// Restore rebuilds every queue under its original URL with its messages,
// attributes, and dedup state intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap serviceBusSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}

	for url, qs := range snap.Queues {
		m.queues.Set(url, restoreQueue(qs))
	}

	return nil
}

func restoreQueue(qs *queueSnapshot) *queueData {
	qd := &queueData{
		info: qs.Info, delaySeconds: qs.DelaySeconds, visibilityTimeout: qs.VisibilityTimeout,
		maxMessageSize: qs.MaxMessageSize, messageRetention: qs.MessageRetention,
		createdAt: qs.CreatedAt, lastModifiedAt: qs.LastModifiedAt,
		deduplicationIndex: qs.DeduplicationIndex, requiresDupDetection: qs.RequiresDupDetection,
		dupDetectionWindow: qs.DupDetectionWindow, dedupByMessageID: qs.DedupByMessageID,
		dlqConfig: qs.DLQConfig, deadLetterOnExpiration: qs.DeadLetterOnExpiration,
		metadata: qs.Metadata,
	}

	if qd.deduplicationIndex == nil {
		qd.deduplicationIndex = make(map[string]time.Time)
	}

	if qd.dedupByMessageID == nil {
		qd.dedupByMessageID = make(map[string]time.Time)
	}

	if len(qs.Messages) > 0 {
		qd.messages = make([]*sbMessage, len(qs.Messages))
		copy(qd.messages, qs.Messages)
	}

	return qd
}
