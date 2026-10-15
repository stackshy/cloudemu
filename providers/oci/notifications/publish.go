package notifications

import (
	"context"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// Message body encodings ONS accepts on PublishMessage.
const (
	MessageTypeRawText = "RAW_TEXT"
	MessageTypeJSON    = "JSON"
)

// maxMessageBytes is the 64 KB ONS caps a published message body at.
const maxMessageBytes = 64 * 1024

// maxDeliveriesPerSubscription bounds the delivery history kept, and
// snapshotted, per subscription. Older messages are dropped.
const maxDeliveriesPerSubscription = 100

// MessageSpec is a message to publish to a topic.
type MessageSpec struct {
	Title string
	Body  string
	Type  string
}

// Message is a published message as it was delivered.
type Message struct {
	ID        string
	TopicID   string
	Title     string
	Body      string
	Type      string
	Timestamp string
}

// Publish publishes a message to a topic. It is the portable entry point onto
// PublishMessage.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) Publish(ctx context.Context, input driver.PublishInput) (*driver.PublishOutput, error) {
	// ONS carries no per-message attributes, so accepting them would drop
	// them silently.
	if len(input.Attributes) > 0 {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"OCI Notifications does not carry message attributes")
	}

	msg, err := m.PublishMessage(ctx, input.TopicID, MessageSpec{
		Title: input.Subject,
		Body:  input.Message,
		Type:  MessageTypeRawText,
	})
	if err != nil {
		return nil, err
	}

	return &driver.PublishOutput{MessageID: msg.ID}, nil
}

// PublishMessage publishes a message to a topic, delivering it to every ACTIVE
// subscription. A subscription still PENDING receives nothing.
func (m *Mock) PublishMessage(_ context.Context, topicID string, spec MessageSpec) (*Message, error) {
	if spec.Body == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "message body is required")
	}

	if len(spec.Body) > maxMessageBytes {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"message body is %d bytes; ONS caps a message at %d", len(spec.Body), maxMessageBytes)
	}

	msgType, err := normalizeMessageType(spec.Type)
	if err != nil {
		return nil, err
	}

	msg, compartmentID, delivered, err := m.deliver(topicID, spec, msgType)
	if err != nil {
		return nil, err
	}

	// Emitted outside the lock: the monitoring backend is another driver, and
	// holding mu across it would make the two mocks lock-ordered.
	m.emitMetric(compartmentID, topicID, "PublishedMessages", 1)
	m.emitMetric(compartmentID, topicID, "DeliveredMessages", float64(delivered))

	return msg, nil
}

// deliver records a message against every ACTIVE subscription on the topic and
// reports the topic's compartment and how many received it.
func (m *Mock) deliver(
	topicID string, spec MessageSpec, msgType string,
) (msg *Message, compartmentID string, delivered int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, ok := m.topics.Get(topicID)
	if !ok {
		return nil, "", 0, cerrors.Newf(cerrors.NotFound, "topic %q not found", topicID)
	}

	sent := Message{
		ID:        idgen.GenerateID("msg-"),
		TopicID:   topicID,
		Title:     spec.Title,
		Body:      spec.Body,
		Type:      msgType,
		Timestamp: m.now(),
	}

	for _, sub := range m.subs.SortedValues() {
		if sub.TopicID != topicID || sub.LifecycleState != StateActive {
			continue
		}

		existing, _ := m.deliveries.Get(sub.ID)
		m.deliveries.Set(sub.ID, keepRecent(append(existing, sent)))

		delivered++
	}

	return &sent, td.Scope.Compartment, delivered, nil
}

// keepRecent trims a delivery history to its last maxDeliveriesPerSubscription
// messages, copying so the dropped ones do not stay pinned in memory.
func keepRecent(history []Message) []Message {
	if len(history) <= maxDeliveriesPerSubscription {
		return history
	}

	return slices.Clone(history[len(history)-maxDeliveriesPerSubscription:])
}

// Deliveries returns the messages a subscription received. Real ONS pushes to
// the endpoint; the emulator records them here instead.
func (m *Mock) Deliveries(subscriptionID string) []Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stored, ok := m.deliveries.Get(subscriptionID)
	if !ok {
		return nil
	}

	out := make([]Message, len(stored))
	copy(out, stored)

	return out
}

// normalizeMessageType defaults an unset message type to RAW_TEXT and rejects
// an encoding ONS does not define.
func normalizeMessageType(msgType string) (string, error) {
	switch strings.ToUpper(msgType) {
	case "", MessageTypeRawText:
		return MessageTypeRawText, nil
	case MessageTypeJSON:
		return MessageTypeJSON, nil
	}

	return "", cerrors.Newf(cerrors.InvalidArgument,
		"messageType %q is not supported; want %s or %s", msgType, MessageTypeRawText, MessageTypeJSON)
}
