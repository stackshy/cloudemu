// Package sqs provides an in-memory mock implementation of AWS Simple Queue Service.
package sqs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/messagequeue/driver"
)

// Compile-time check that Mock implements driver.MessageQueue.
var _ driver.MessageQueue = (*Mock)(nil)

const (
	defaultVisibilityTimeout = 30
	maxReceiveMessages       = 10
	deduplicationWindow      = 5 * time.Minute
)

// sqsMessage represents an internal message stored in a queue.
type sqsMessage struct {
	ID              string
	Body            string
	GroupID         string
	DeduplicationID string
	Attributes      map[string]string
	ReceiptHandle   string
	VisibleAt       time.Time
	SentAt          time.Time
	ReceiveCount    int
}

// queueData holds the internal state of a single SQS queue.
type queueData struct {
	info     driver.QueueInfo
	messages []*sqsMessage
	mu       sync.Mutex

	delaySeconds       int
	visibilityTimeout  int
	maxMessageSize     int
	messageRetention   int
	deduplicationIndex map[string]time.Time
	dlqConfig          *driver.DeadLetterConfig
}

// LambdaTrigger is a function that gets called when a message is sent to a queue.
type LambdaTrigger func(queueURL string, message driver.Message)

// Mock is an in-memory mock implementation of the AWS SQS service.
type Mock struct {
	queues   *memstore.Store[*queueData]
	opts     *config.Options
	mu       sync.RWMutex
	triggers map[string]LambdaTrigger // queueURL -> trigger
}

// New creates a new SQS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		queues:   memstore.New[*queueData](),
		opts:     opts,
		triggers: make(map[string]LambdaTrigger),
	}
}

// SetTrigger registers a Lambda trigger for a queue. When a message is sent to the
// queue, the trigger function is called automatically.
func (m *Mock) SetTrigger(queueURL string, fn LambdaTrigger) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.triggers[queueURL] = fn
}

// RemoveTrigger removes a Lambda trigger from a queue.
func (m *Mock) RemoveTrigger(queueURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.triggers, queueURL)
}

// CreateQueue creates a new SQS queue.
func (m *Mock) CreateQueue(_ context.Context, cfg driver.QueueConfig) (*driver.QueueInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "queue name is required")
	}

	if cfg.FIFO && !strings.HasSuffix(cfg.Name, ".fifo") {
		return nil, errors.New(errors.InvalidArgument, "FIFO queue name must end with .fifo")
	}

	url := fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", m.opts.Region, m.opts.AccountID, cfg.Name)
	arn := idgen.AWSARN("sqs", m.opts.Region, m.opts.AccountID, cfg.Name)

	if m.queues.Has(url) {
		return nil, errors.Newf(errors.AlreadyExists, "queue %q already exists", cfg.Name)
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	visibilityTimeout := cfg.VisibilityTimeout
	if visibilityTimeout == 0 {
		visibilityTimeout = defaultVisibilityTimeout
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
		info:               info,
		messages:           make([]*sqsMessage, 0),
		delaySeconds:       cfg.DelaySeconds,
		visibilityTimeout:  visibilityTimeout,
		maxMessageSize:     cfg.MaxMessageSize,
		messageRetention:   cfg.MessageRetention,
		deduplicationIndex: make(map[string]time.Time),
		dlqConfig:          cfg.DeadLetterQueue,
	}

	m.queues.Set(url, qd)

	result := info

	return &result, nil
}

// DeleteQueue deletes an SQS queue by URL.
func (m *Mock) DeleteQueue(_ context.Context, url string) error {
	if !m.queues.Delete(url) {
		return errors.Newf(errors.NotFound, "queue %q not found", url)
	}

	return nil
}

// GetQueueInfo retrieves information about an SQS queue by URL.
func (m *Mock) GetQueueInfo(_ context.Context, url string) (*driver.QueueInfo, error) {
	qd, ok := m.queues.Get(url)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", url)
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

// ListQueues returns all queues whose names match the given prefix.
// If prefix is empty, all queues are returned.
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

// SendMessage sends a message to the specified SQS queue.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) SendMessage(_ context.Context, input driver.SendMessageInput) (*driver.SendMessageOutput, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", input.QueueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	if err := validateFIFORequirements(qd, &input); err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now()

	// FIFO deduplication: check if same DeduplicationID was sent within 5-min window.
	if existingID, found := findDuplicate(qd, &input, now); found {
		return &driver.SendMessageOutput{MessageID: existingID}, nil
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

	visibleAt := now.Add(time.Duration(delaySeconds) * time.Second)

	msg := &sqsMessage{
		ID:              msgID,
		Body:            input.Body,
		GroupID:         input.GroupID,
		DeduplicationID: input.DeduplicationID,
		Attributes:      attrs,
		VisibleAt:       visibleAt,
		SentAt:          now,
	}

	qd.messages = append(qd.messages, msg)

	if qd.info.FIFO && input.DeduplicationID != "" {
		qd.deduplicationIndex[input.DeduplicationID] = now
	}

	// Fire Lambda trigger if registered.
	m.mu.RLock()
	trigger := m.triggers[input.QueueURL]
	m.mu.RUnlock()

	if trigger != nil {
		triggerMsg := driver.Message{
			MessageID:  msgID,
			Body:       input.Body,
			Attributes: attrs,
			GroupID:    input.GroupID,
		}

		trigger(input.QueueURL, triggerMsg)
	}

	return &driver.SendMessageOutput{
		MessageID: msgID,
	}, nil
}

func validateFIFORequirements(qd *queueData, input *driver.SendMessageInput) error {
	if !qd.info.FIFO {
		return nil
	}

	if input.GroupID == "" {
		return errors.New(errors.InvalidArgument, "GroupID is required for FIFO queues")
	}

	if input.DeduplicationID == "" {
		return errors.New(errors.InvalidArgument, "DeduplicationID is required for FIFO queues")
	}

	return nil
}

func findDuplicate(qd *queueData, input *driver.SendMessageInput, now time.Time) (string, bool) {
	if !qd.info.FIFO || input.DeduplicationID == "" {
		return "", false
	}

	sentAt, ok := qd.deduplicationIndex[input.DeduplicationID]
	if !ok {
		return "", false
	}

	if now.Sub(sentAt) >= deduplicationWindow {
		return "", false
	}

	for _, existing := range qd.messages {
		if existing.DeduplicationID == input.DeduplicationID {
			return existing.ID, true
		}
	}

	return "", false
}

// ReceiveMessages receives messages from the specified SQS queue.
// Returns messages where VisibleAt <= now, and sets a new VisibleAt based on the visibility timeout.
func (m *Mock) ReceiveMessages(_ context.Context, input driver.ReceiveMessageInput) ([]driver.Message, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", input.QueueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	maxMessages := clampMaxMessages(input.MaxMessages)

	visibilityTimeout := input.VisibilityTimeout
	if visibilityTimeout == 0 {
		visibilityTimeout = qd.visibilityTimeout
	}

	now := m.opts.Clock.Now()
	results, toRemove := m.collectVisibleMessages(qd, maxMessages, visibilityTimeout, now)

	// Remove DLQ-moved messages in reverse order.
	for i := len(toRemove) - 1; i >= 0; i-- {
		idx := toRemove[i]
		qd.messages = append(qd.messages[:idx], qd.messages[idx+1:]...)
	}

	if results == nil {
		results = []driver.Message{}
	}

	return results, nil
}

func clampMaxMessages(maxMessages int) int {
	if maxMessages <= 0 {
		return 1
	}

	if maxMessages > maxReceiveMessages {
		return maxReceiveMessages
	}

	return maxMessages
}

func (m *Mock) collectVisibleMessages(
	qd *queueData, maxMessages, visibilityTimeout int, now time.Time,
) (messages []driver.Message, dlqIndices []int) {
	var results []driver.Message

	var toRemove []int

	for i, msg := range qd.messages {
		if len(results) >= maxMessages {
			break
		}

		if msg.VisibleAt.After(now) {
			continue
		}

		msg.ReceiveCount++

		// Check if message exceeded max receive count - move to DLQ.
		if qd.dlqConfig != nil && qd.dlqConfig.MaxReceiveCount > 0 && msg.ReceiveCount > qd.dlqConfig.MaxReceiveCount {
			m.moveToDLQ(qd.dlqConfig.TargetQueueURL, msg)

			toRemove = append(toRemove, i)

			continue
		}

		results = append(results, buildReceivedMessage(msg, visibilityTimeout, now))
	}

	return results, toRemove
}

func buildReceivedMessage(msg *sqsMessage, visibilityTimeout int, now time.Time) driver.Message {
	// Generate a new receipt handle for this receive.
	receiptHandle := idgen.GenerateID("receipt-")
	msg.ReceiptHandle = receiptHandle
	msg.VisibleAt = now.Add(time.Duration(visibilityTimeout) * time.Second)

	attrs := make(map[string]string, len(msg.Attributes))
	for k, v := range msg.Attributes {
		attrs[k] = v
	}

	return driver.Message{
		MessageID:     msg.ID,
		ReceiptHandle: receiptHandle,
		Body:          msg.Body,
		Attributes:    attrs,
		GroupID:       msg.GroupID,
	}
}

// moveToDLQ moves a message to the dead-letter queue.
func (m *Mock) moveToDLQ(dlqURL string, msg *sqsMessage) {
	dlq, ok := m.queues.Get(dlqURL)
	if !ok {
		return
	}

	dlq.mu.Lock()
	defer dlq.mu.Unlock()

	dlqMsg := &sqsMessage{
		ID:         msg.ID,
		Body:       msg.Body,
		GroupID:    msg.GroupID,
		Attributes: msg.Attributes,
		VisibleAt:  m.opts.Clock.Now(),
		SentAt:     m.opts.Clock.Now(),
	}

	dlq.messages = append(dlq.messages, dlqMsg)
}

// DeleteMessage deletes a message from the specified queue using its receipt handle.
func (m *Mock) DeleteMessage(_ context.Context, queueURL, receiptHandle string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	for i, msg := range qd.messages {
		if msg.ReceiptHandle == receiptHandle {
			qd.messages = append(qd.messages[:i], qd.messages[i+1:]...)
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "message with receipt handle %q not found", receiptHandle)
}

// ChangeVisibility changes the visibility timeout of a message in the specified queue.
func (m *Mock) ChangeVisibility(_ context.Context, queueURL, receiptHandle string, timeout int) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queueURL)
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

	return errors.Newf(errors.NotFound, "message with receipt handle %q not found", receiptHandle)
}
