// Package pubsub provides an in-memory mock implementation of GCP Pub/Sub.
package pubsub

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/NitinKumar004/cloudemu/config"
	cerrors "github.com/NitinKumar004/cloudemu/errors"
	"github.com/NitinKumar004/cloudemu/internal/idgen"
	"github.com/NitinKumar004/cloudemu/internal/memstore"
	"github.com/NitinKumar004/cloudemu/messagequeue/driver"
)

// Compile-time check that Mock implements driver.MessageQueue.
var _ driver.MessageQueue = (*Mock)(nil)

// pubsubMessage represents an internal message stored in a topic/subscription.
type pubsubMessage struct {
	ID              string
	Body            string
	GroupID         string
	DeduplicationID string
	Attributes      map[string]string
	ReceiptHandle   string
	VisibleAt       time.Time
}

// queueData holds the internal state of a single Pub/Sub topic+subscription pair.
type queueData struct {
	info     driver.QueueInfo
	messages []*pubsubMessage
	mu       sync.Mutex

	delaySeconds      int
	visibilityTimeout int
	maxMessageSize    int
	messageRetention  int
}

// Mock is an in-memory mock implementation of the GCP Pub/Sub service.
type Mock struct {
	queues *memstore.Store[*queueData]
	opts   *config.Options
}

// New creates a new Pub/Sub mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		queues: memstore.New[*queueData](),
		opts:   opts,
	}
}

// CreateQueue creates a new Pub/Sub topic and subscription pair.
func (m *Mock) CreateQueue(_ context.Context, cfg driver.QueueConfig) (*driver.QueueInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "topic name is required")
	}

	if cfg.FIFO && !strings.HasSuffix(cfg.Name, ".fifo") {
		return nil, cerrors.New(cerrors.InvalidArgument, "FIFO topic name must end with .fifo")
	}

	url := fmt.Sprintf("projects/%s/subscriptions/%s", m.opts.ProjectID, cfg.Name)
	arn := idgen.GCPID(m.opts.ProjectID, "topics", cfg.Name)

	if m.queues.Has(url) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "topic %q already exists", cfg.Name)
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	visibilityTimeout := cfg.VisibilityTimeout
	if visibilityTimeout == 0 {
		visibilityTimeout = 30 // default 30 seconds
	}

	info := driver.QueueInfo{
		URL:                url,
		ARN:                arn,
		Name:               cfg.Name,
		FIFO:               cfg.FIFO,
		ApproxMessageCount: 0,
		Tags:               tags,
	}

	qd := &queueData{
		info:              info,
		messages:          make([]*pubsubMessage, 0),
		delaySeconds:      cfg.DelaySeconds,
		visibilityTimeout: visibilityTimeout,
		maxMessageSize:    cfg.MaxMessageSize,
		messageRetention:  cfg.MessageRetention,
	}

	m.queues.Set(url, qd)

	result := info
	return &result, nil
}

// DeleteQueue deletes a Pub/Sub topic and subscription pair by URL.
func (m *Mock) DeleteQueue(_ context.Context, url string) error {
	if !m.queues.Delete(url) {
		return cerrors.Newf(cerrors.NotFound, "topic %q not found", url)
	}

	return nil
}

// GetQueueInfo retrieves information about a Pub/Sub topic/subscription by URL.
func (m *Mock) GetQueueInfo(_ context.Context, url string) (*driver.QueueInfo, error) {
	qd, ok := m.queues.Get(url)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "topic %q not found", url)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	// Count visible messages for the approximate message count.
	now := m.opts.Clock.Now()
	count := 0
	for _, msg := range qd.messages {
		if !msg.VisibleAt.After(now) {
			count++
		}
	}

	info := qd.info
	info.ApproxMessageCount = count

	return &info, nil
}

// ListQueues returns all topics whose names match the given prefix.
// If prefix is empty, all topics are returned.
func (m *Mock) ListQueues(_ context.Context, prefix string) ([]driver.QueueInfo, error) {
	all := m.queues.All()

	results := make([]driver.QueueInfo, 0, len(all))
	for _, qd := range all {
		if prefix == "" || strings.HasPrefix(qd.info.Name, prefix) {
			results = append(results, qd.info)
		}
	}

	return results, nil
}

// SendMessage publishes a message to the specified Pub/Sub topic.
func (m *Mock) SendMessage(_ context.Context, input driver.SendMessageInput) (*driver.SendMessageOutput, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "topic %q not found", input.QueueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	// Enforce ordering key (FIFO) requirements.
	if qd.info.FIFO {
		if input.GroupID == "" {
			return nil, cerrors.New(cerrors.InvalidArgument, "GroupID (ordering key) is required for FIFO topics")
		}
		if input.DeduplicationID == "" {
			return nil, cerrors.New(cerrors.InvalidArgument, "DeduplicationID is required for FIFO topics")
		}
	}

	msgID := idgen.GenerateID("msg-")

	attrs := make(map[string]string, len(input.Attributes))
	for k, v := range input.Attributes {
		attrs[k] = v
	}

	delaySeconds := input.DelaySeconds
	if delaySeconds == 0 {
		delaySeconds = qd.delaySeconds
	}

	now := m.opts.Clock.Now()
	visibleAt := now.Add(time.Duration(delaySeconds) * time.Second)

	msg := &pubsubMessage{
		ID:              msgID,
		Body:            input.Body,
		GroupID:         input.GroupID,
		DeduplicationID: input.DeduplicationID,
		Attributes:      attrs,
		VisibleAt:       visibleAt,
	}

	qd.messages = append(qd.messages, msg)

	return &driver.SendMessageOutput{
		MessageID: msgID,
	}, nil
}

// ReceiveMessages pulls messages from the specified Pub/Sub subscription.
// Returns messages where VisibleAt <= now, and sets a new VisibleAt based on the ack deadline (visibility timeout).
func (m *Mock) ReceiveMessages(_ context.Context, input driver.ReceiveMessageInput) ([]driver.Message, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "topic %q not found", input.QueueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	maxMessages := input.MaxMessages
	if maxMessages <= 0 {
		maxMessages = 1
	}
	if maxMessages > 10 {
		maxMessages = 10
	}

	visibilityTimeout := input.VisibilityTimeout
	if visibilityTimeout == 0 {
		visibilityTimeout = qd.visibilityTimeout
	}

	now := m.opts.Clock.Now()
	var results []driver.Message

	for _, msg := range qd.messages {
		if len(results) >= maxMessages {
			break
		}

		if msg.VisibleAt.After(now) {
			continue
		}

		// Generate a new receipt handle (ack ID) for this pull.
		receiptHandle := idgen.GenerateID("ack-")
		msg.ReceiptHandle = receiptHandle
		msg.VisibleAt = now.Add(time.Duration(visibilityTimeout) * time.Second)

		attrs := make(map[string]string, len(msg.Attributes))
		for k, v := range msg.Attributes {
			attrs[k] = v
		}

		results = append(results, driver.Message{
			MessageID:     msg.ID,
			ReceiptHandle: receiptHandle,
			Body:          msg.Body,
			Attributes:    attrs,
			GroupID:       msg.GroupID,
		})
	}

	if results == nil {
		results = []driver.Message{}
	}

	return results, nil
}

// DeleteMessage acknowledges and removes a message from the subscription using its ack ID (receipt handle).
func (m *Mock) DeleteMessage(_ context.Context, queueURL, receiptHandle string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "topic %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	for i, msg := range qd.messages {
		if msg.ReceiptHandle == receiptHandle {
			qd.messages = append(qd.messages[:i], qd.messages[i+1:]...)
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "message with ack ID %q not found", receiptHandle)
}

// ChangeVisibility modifies the ack deadline (visibility timeout) of a message in the subscription.
func (m *Mock) ChangeVisibility(_ context.Context, queueURL, receiptHandle string, timeout int) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "topic %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	now := m.opts.Clock.Now()

	for _, msg := range qd.messages {
		if msg.ReceiptHandle == receiptHandle {
			msg.VisibleAt = now.Add(time.Duration(timeout) * time.Second)
			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "message with ack ID %q not found", receiptHandle)
}
