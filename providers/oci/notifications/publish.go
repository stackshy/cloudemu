package notifications

import (
	"context"
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

	msgType, err := normalizeMessageType(spec.Type)
	if err != nil {
		return nil, err
	}

	msg, delivered, err := m.deliver(topicID, spec, msgType)
	if err != nil {
		return nil, err
	}

	// Emitted outside the lock: the monitoring backend is another driver, and
	// holding mu across it would make the two mocks lock-ordered.
	dims := map[string]string{"topicId": topicID}
	m.emitMetric("PublishedMessages", 1, dims)
	m.emitMetric("DeliveredMessages", float64(delivered), dims)

	return msg, nil
}

// deliver records a message against every ACTIVE subscription on the topic and
// reports how many received it.
func (m *Mock) deliver(topicID string, spec MessageSpec, msgType string) (*Message, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.topics.Has(topicID) {
		return nil, 0, cerrors.Newf(cerrors.NotFound, "topic %q not found", topicID)
	}

	msg := Message{
		ID:        idgen.GenerateID("msg-"),
		TopicID:   topicID,
		Title:     spec.Title,
		Body:      spec.Body,
		Type:      msgType,
		Timestamp: m.now(),
	}

	delivered := 0

	for _, sub := range m.subs.SortedValues() {
		if sub.TopicID != topicID || sub.LifecycleState != StateActive {
			continue
		}

		existing, _ := m.deliveries.Get(sub.ID)
		m.deliveries.Set(sub.ID, append(existing, msg))

		delivered++
	}

	return &msg, delivered, nil
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
