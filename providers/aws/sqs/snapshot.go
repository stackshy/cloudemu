package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// sqsSnapshot is the full serialized state of the SQS mock: every queue keyed by
// its URL and every in-flight message-move task keyed by its handle. Both stored
// value types (queueData, moveTask) are built almost entirely from unexported
// fields, so each is promoted to an exported snapshot form — a generic memstore
// dump would silently lose that state. The mutexes, the wired Lambda
// event-source invoker, the monitoring backend, and *config.Options are
// intentionally not captured.
type sqsSnapshot struct {
	Queues    map[string]*queueSnapshot    `json:"queues,omitempty"`
	MoveTasks map[string]*moveTaskSnapshot `json:"moveTasks,omitempty"`
}

// queueSnapshot mirrors queueData, promoting its unexported attribute fields,
// message list, dedup index, and sequence counter to exported forms.
type queueSnapshot struct {
	Info               driver.QueueInfo         `json:"info"`
	Messages           []*messageSnapshot       `json:"messages,omitempty"`
	DelaySeconds       int                      `json:"delaySeconds,omitempty"`
	VisibilityTimeout  int                      `json:"visibilityTimeout,omitempty"`
	MaxMessageSize     int                      `json:"maxMessageSize,omitempty"`
	MessageRetention   int                      `json:"messageRetention,omitempty"`
	ReceiveWaitTime    int                      `json:"receiveWaitTime,omitempty"`
	ContentBasedDedup  bool                     `json:"contentBasedDedup,omitempty"`
	RedrivePolicy      string                   `json:"redrivePolicy,omitempty"`
	RedriveAllowPolicy string                   `json:"redriveAllowPolicy,omitempty"`
	Policy             string                   `json:"policy,omitempty"`
	KMSMasterKeyID     string                   `json:"kmsMasterKeyId,omitempty"`
	CreatedAt          time.Time                `json:"createdAt"`
	LastModifiedAt     time.Time                `json:"lastModifiedAt"`
	DeduplicationIndex map[string]time.Time     `json:"deduplicationIndex,omitempty"`
	DLQConfig          *driver.DeadLetterConfig `json:"dlqConfig,omitempty"`
	SeqCounter         uint64                   `json:"seqCounter,omitempty"`
}

// messageSnapshot mirrors sqsMessage, promoting its one unexported field
// (sourceQueueURL) to an exported one so it survives JSON.
type messageSnapshot struct {
	ID                string                                  `json:"id"`
	Body              string                                  `json:"body,omitempty"`
	GroupID           string                                  `json:"groupId,omitempty"`
	DeduplicationID   string                                  `json:"deduplicationId,omitempty"`
	Attributes        map[string]string                       `json:"attributes,omitempty"`
	MessageAttributes map[string]driver.MessageAttributeValue `json:"messageAttributes,omitempty"`
	SystemAttributes  map[string]string                       `json:"systemAttributes,omitempty"`
	SenderID          string                                  `json:"senderId,omitempty"`
	SequenceNumber    string                                  `json:"sequenceNumber,omitempty"`
	ReceiptHandle     string                                  `json:"receiptHandle,omitempty"`
	VisibleAt         time.Time                               `json:"visibleAt"`
	SentAt            time.Time                               `json:"sentAt"`
	FirstReceivedAt   time.Time                               `json:"firstReceivedAt"`
	ReceiveCount      int                                     `json:"receiveCount,omitempty"`
	SourceQueueURL    string                                  `json:"sourceQueueUrl,omitempty"`
}

// moveTaskSnapshot is the exported form of moveTask (all its fields are
// unexported).
type moveTaskSnapshot struct {
	Handle        string    `json:"handle"`
	SourceARN     string    `json:"sourceArn,omitempty"`
	SourceURL     string    `json:"sourceUrl,omitempty"`
	DestARN       string    `json:"destArn,omitempty"`
	MaxRate       int       `json:"maxRate,omitempty"`
	Status        string    `json:"status,omitempty"`
	Moved         int64     `json:"moved,omitempty"`
	ToMove        int64     `json:"toMove,omitempty"`
	FailureReason string    `json:"failureReason,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// SQS message bodies are always captured (they are the queue state, not bulk
// object assets).
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := sqsSnapshot{
		Queues:    make(map[string]*queueSnapshot, m.queues.Len()),
		MoveTasks: make(map[string]*moveTaskSnapshot, m.moveTasks.Len()),
	}

	for url, qd := range m.queues.All() {
		snap.Queues[url] = snapshotQueue(qd)
	}

	for handle, task := range m.moveTasks.All() {
		snap.MoveTasks[handle] = &moveTaskSnapshot{
			Handle: task.handle, SourceARN: task.sourceARN, SourceURL: task.sourceURL,
			DestARN: task.destARN, MaxRate: task.maxRate, Status: task.status,
			Moved: task.moved, ToMove: task.toMove, FailureReason: task.failureReason,
			StartedAt: task.startedAt,
		}
	}

	if len(snap.Queues) == 0 {
		snap.Queues = nil
	}

	if len(snap.MoveTasks) == 0 {
		snap.MoveTasks = nil
	}

	return json.Marshal(snap)
}

func snapshotQueue(qd *queueData) *queueSnapshot {
	qd.mu.Lock()
	defer qd.mu.Unlock()

	qs := &queueSnapshot{
		Info: qd.info, DelaySeconds: qd.delaySeconds, VisibilityTimeout: qd.visibilityTimeout,
		MaxMessageSize: qd.maxMessageSize, MessageRetention: qd.messageRetention,
		ReceiveWaitTime: qd.receiveWaitTime, ContentBasedDedup: qd.contentBasedDedup,
		RedrivePolicy: qd.redrivePolicy, RedriveAllowPolicy: qd.redriveAllowPolicy,
		Policy: qd.policy, KMSMasterKeyID: qd.kmsMasterKeyID, CreatedAt: qd.createdAt,
		LastModifiedAt: qd.lastModifiedAt, DeduplicationIndex: qd.deduplicationIndex,
		DLQConfig: qd.dlqConfig, SeqCounter: qd.seqCounter.Load(),
	}

	if len(qd.messages) > 0 {
		qs.Messages = make([]*messageSnapshot, 0, len(qd.messages))
		for _, msg := range qd.messages {
			qs.Messages = append(qs.Messages, snapshotMessage(msg))
		}
	}

	return qs
}

func snapshotMessage(msg *sqsMessage) *messageSnapshot {
	return &messageSnapshot{
		ID: msg.ID, Body: msg.Body, GroupID: msg.GroupID, DeduplicationID: msg.DeduplicationID,
		Attributes: msg.Attributes, MessageAttributes: msg.MessageAttributes,
		SystemAttributes: msg.SystemAttributes, SenderID: msg.SenderID,
		SequenceNumber: msg.SequenceNumber, ReceiptHandle: msg.ReceiptHandle,
		VisibleAt: msg.VisibleAt, SentAt: msg.SentAt, FirstReceivedAt: msg.FirstReceivedAt,
		ReceiveCount: msg.ReceiveCount, SourceQueueURL: msg.sourceQueueURL,
	}
}

// Restore rebuilds every queue under its original URL (and every move task under
// its handle), with messages, attributes, dedup state, and sequence counter
// intact, so receive-handles and cross-queue DLQ references still resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap sqsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("sqs: parse snapshot: %w", err)
	}

	for url, qs := range snap.Queues {
		m.queues.Set(url, restoreQueue(qs))
	}

	for handle, ts := range snap.MoveTasks {
		m.moveTasks.Set(handle, &moveTask{
			handle: ts.Handle, sourceARN: ts.SourceARN, sourceURL: ts.SourceURL,
			destARN: ts.DestARN, maxRate: ts.MaxRate, status: ts.Status,
			moved: ts.Moved, toMove: ts.ToMove, failureReason: ts.FailureReason,
			startedAt: ts.StartedAt,
		})
	}

	return nil
}

func restoreQueue(qs *queueSnapshot) *queueData {
	qd := &queueData{
		info: qs.Info, delaySeconds: qs.DelaySeconds, visibilityTimeout: qs.VisibilityTimeout,
		maxMessageSize: qs.MaxMessageSize, messageRetention: qs.MessageRetention,
		receiveWaitTime: qs.ReceiveWaitTime, contentBasedDedup: qs.ContentBasedDedup,
		redrivePolicy: qs.RedrivePolicy, redriveAllowPolicy: qs.RedriveAllowPolicy,
		policy: qs.Policy, kmsMasterKeyID: qs.KMSMasterKeyID, createdAt: qs.CreatedAt,
		lastModifiedAt: qs.LastModifiedAt, deduplicationIndex: qs.DeduplicationIndex,
		dlqConfig: qs.DLQConfig,
	}

	if qd.deduplicationIndex == nil {
		qd.deduplicationIndex = make(map[string]time.Time)
	}

	qd.seqCounter.Store(qs.SeqCounter)

	if len(qs.Messages) > 0 {
		qd.messages = make([]*sqsMessage, 0, len(qs.Messages))
		for _, ms := range qs.Messages {
			qd.messages = append(qd.messages, &sqsMessage{
				ID: ms.ID, Body: ms.Body, GroupID: ms.GroupID, DeduplicationID: ms.DeduplicationID,
				Attributes: ms.Attributes, MessageAttributes: ms.MessageAttributes,
				SystemAttributes: ms.SystemAttributes, SenderID: ms.SenderID,
				SequenceNumber: ms.SequenceNumber, ReceiptHandle: ms.ReceiptHandle,
				VisibleAt: ms.VisibleAt, SentAt: ms.SentAt, FirstReceivedAt: ms.FirstReceivedAt,
				ReceiveCount: ms.ReceiveCount, sourceQueueURL: ms.SourceQueueURL,
			})
		}
	}

	return qd
}
