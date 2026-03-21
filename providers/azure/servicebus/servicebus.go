// Package servicebus provides an in-memory mock implementation of Azure Service Bus.
package servicebus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
)

// Compile-time check that Mock implements driver.MessageQueue.
var _ driver.MessageQueue = (*Mock)(nil)

const (
	defaultVisibilityTimeout = 30
	maxReceiveMessages       = 10
	deduplicationWindow      = 5 * time.Minute
)

// sbMessage represents an internal message stored in a queue.
type sbMessage struct {
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

// queueData holds the internal state of a single Service Bus queue.
type queueData struct {
	info     driver.QueueInfo
	messages []*sbMessage
	mu       sync.Mutex

	delaySeconds       int
	visibilityTimeout  int
	maxMessageSize     int
	messageRetention   int
	createdAt          time.Time
	lastModifiedAt     time.Time
	deduplicationIndex map[string]time.Time
	dlqConfig          *driver.DeadLetterConfig
}

// FunctionTrigger is a function that gets called when a message is sent to a queue.
type FunctionTrigger func(queueURL string, message driver.Message)

// Mock is an in-memory mock implementation of the Azure Service Bus service.
type Mock struct {
	queues     *memstore.Store[*queueData]
	opts       *config.Options
	mu         sync.RWMutex
	triggers   map[string]FunctionTrigger // queueURL -> trigger
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(queueName string, metrics map[string]float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, 0, len(metrics))

	for name, value := range metrics {
		data = append(data, mondriver.MetricDatum{
			Namespace:  "Microsoft.ServiceBus/namespaces",
			MetricName: name,
			Value:      value,
			Unit:       "None",
			Dimensions: map[string]string{"queueName": queueName},
			Timestamp:  now,
		})
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// New creates a new Service Bus mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		queues:   memstore.New[*queueData](),
		opts:     opts,
		triggers: make(map[string]FunctionTrigger),
	}
}

// SetTrigger registers an Azure Function trigger for a queue.
func (m *Mock) SetTrigger(queueURL string, fn FunctionTrigger) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.triggers[queueURL] = fn
}

// RemoveTrigger removes a Function trigger from a queue.
func (m *Mock) RemoveTrigger(queueURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.triggers, queueURL)
}

// CreateQueue creates a new Service Bus queue.
func (m *Mock) CreateQueue(_ context.Context, cfg driver.QueueConfig) (*driver.QueueInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "queue name is required")
	}

	if cfg.FIFO && !strings.HasSuffix(cfg.Name, ".fifo") {
		return nil, cerrors.New(cerrors.InvalidArgument, "FIFO queue name must end with .fifo")
	}

	url := fmt.Sprintf("https://%s.servicebus.windows.net/%s", m.opts.AccountID, cfg.Name)
	arn := idgen.AzureID(m.opts.AccountID, "cloud-mock", "Microsoft.ServiceBus", "queues", cfg.Name)

	if m.queues.Has(url) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "queue %q already exists", cfg.Name)
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

	now := m.opts.Clock.Now()

	qd := &queueData{
		info:               info,
		messages:           make([]*sbMessage, 0),
		delaySeconds:       cfg.DelaySeconds,
		visibilityTimeout:  visibilityTimeout,
		maxMessageSize:     cfg.MaxMessageSize,
		messageRetention:   cfg.MessageRetention,
		createdAt:          now,
		lastModifiedAt:     now,
		deduplicationIndex: make(map[string]time.Time),
		dlqConfig:          cfg.DeadLetterQueue,
	}

	m.queues.Set(url, qd)

	result := info

	return &result, nil
}

// DeleteQueue deletes a Service Bus queue by URL.
func (m *Mock) DeleteQueue(_ context.Context, url string) error {
	if !m.queues.Delete(url) {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", url)
	}

	return nil
}

// GetQueueInfo retrieves information about a Service Bus queue by URL.
func (m *Mock) GetQueueInfo(_ context.Context, url string) (*driver.QueueInfo, error) {
	qd, ok := m.queues.Get(url)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", url)
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

// SendMessage sends a message to the specified Service Bus queue.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) SendMessage(_ context.Context, input driver.SendMessageInput) (*driver.SendMessageOutput, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", input.QueueURL)
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

	msgID := idgen.GenerateID("sb-msg-")

	attrs := make(map[string]string, len(input.Attributes))
	for k, v := range input.Attributes {
		attrs[k] = v
	}

	delaySeconds := input.DelaySeconds
	if delaySeconds == 0 {
		delaySeconds = qd.delaySeconds
	}

	visibleAt := now.Add(time.Duration(delaySeconds) * time.Second)

	msg := &sbMessage{
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

	// Fire Function trigger if registered.
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

	m.emitMetric(qd.info.Name, map[string]float64{
		"IncomingMessages": 1, "Size": float64(len(input.Body)),
	})

	return &driver.SendMessageOutput{
		MessageID: msgID,
	}, nil
}

func validateFIFORequirements(qd *queueData, input *driver.SendMessageInput) error {
	if !qd.info.FIFO {
		return nil
	}

	if input.GroupID == "" {
		return cerrors.New(cerrors.InvalidArgument, "GroupID is required for FIFO queues")
	}

	if input.DeduplicationID == "" {
		return cerrors.New(cerrors.InvalidArgument, "DeduplicationID is required for FIFO queues")
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

// ReceiveMessages receives messages from the specified Service Bus queue.
// Returns messages where VisibleAt <= now, and sets a new VisibleAt based on the visibility timeout.
func (m *Mock) ReceiveMessages(_ context.Context, input driver.ReceiveMessageInput) ([]driver.Message, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", input.QueueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	maxMsgs := clampMaxMessages(input.MaxMessages)

	visibilityTimeout := input.VisibilityTimeout
	if visibilityTimeout == 0 {
		visibilityTimeout = qd.visibilityTimeout
	}

	now := m.opts.Clock.Now()
	results, toRemove := m.collectVisibleMessages(qd, maxMsgs, visibilityTimeout, now)

	// Remove DLQ-moved messages in reverse order.
	for i := len(toRemove) - 1; i >= 0; i-- {
		idx := toRemove[i]
		qd.messages = append(qd.messages[:idx], qd.messages[idx+1:]...)
	}

	if results == nil {
		results = []driver.Message{}
	}

	remaining := len(qd.messages)
	m.emitMetric(qd.info.Name, map[string]float64{
		"OutgoingMessages": float64(len(results)), "ActiveMessages": float64(remaining),
	})

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

		// Check if message exceeded max receive count -- move to DLQ.
		if qd.dlqConfig != nil && qd.dlqConfig.MaxReceiveCount > 0 && msg.ReceiveCount > qd.dlqConfig.MaxReceiveCount {
			m.moveToDLQ(qd.dlqConfig.TargetQueueURL, msg)

			toRemove = append(toRemove, i)

			continue
		}

		results = append(results, buildReceivedMessage(msg, visibilityTimeout, now))
	}

	return results, toRemove
}

func buildReceivedMessage(msg *sbMessage, visibilityTimeout int, now time.Time) driver.Message {
	// Generate a new receipt handle (lock token) for this receive.
	receiptHandle := idgen.GenerateID("sb-lock-")
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
func (m *Mock) moveToDLQ(dlqURL string, msg *sbMessage) {
	dlq, ok := m.queues.Get(dlqURL)
	if !ok {
		return
	}

	dlq.mu.Lock()
	defer dlq.mu.Unlock()

	dlqMsg := &sbMessage{
		ID:         msg.ID,
		Body:       msg.Body,
		GroupID:    msg.GroupID,
		Attributes: msg.Attributes,
		VisibleAt:  m.opts.Clock.Now(),
		SentAt:     m.opts.Clock.Now(),
	}

	dlq.messages = append(dlq.messages, dlqMsg)
}

// DeleteMessage deletes (completes) a message from the specified queue using its receipt handle (lock token).
func (m *Mock) DeleteMessage(_ context.Context, queueURL, receiptHandle string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	for i, msg := range qd.messages {
		if msg.ReceiptHandle == receiptHandle {
			qd.messages = append(qd.messages[:i], qd.messages[i+1:]...)

			m.emitMetric(qd.info.Name, map[string]float64{"CompletedMessages": 1})

			return nil
		}
	}

	return cerrors.Newf(cerrors.NotFound, "message with receipt handle %q not found", receiptHandle)
}

// ChangeVisibility changes the lock duration (visibility timeout) of a message in the specified queue.
func (m *Mock) ChangeVisibility(_ context.Context, queueURL, receiptHandle string, timeout int) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
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

	return cerrors.Newf(cerrors.NotFound, "message with receipt handle %q not found", receiptHandle)
}

// SendMessageBatch sends up to 10 messages to the specified Service Bus queue.
func (m *Mock) SendMessageBatch(
	ctx context.Context, queue string, entries []driver.BatchSendEntry,
) (*driver.BatchSendResult, error) {
	if len(entries) > driver.MaxBatchSize {
		return nil, cerrors.Newf(
			cerrors.InvalidArgument, "batch size %d exceeds max %d", len(entries), driver.MaxBatchSize,
		)
	}

	result := &driver.BatchSendResult{}

	for _, entry := range entries {
		input := batchEntryToSendInput(queue, &entry)

		out, err := m.SendMessage(ctx, input)
		if err != nil {
			result.Failed = append(result.Failed, driver.BatchSendFailEntry{
				ID: entry.ID, Code: "SendFailure", Message: err.Error(),
			})

			continue
		}

		result.Successful = append(result.Successful, driver.BatchSendResultEntry{
			ID: entry.ID, MessageID: out.MessageID,
		})
	}

	return result, nil
}

func batchEntryToSendInput(queue string, entry *driver.BatchSendEntry) driver.SendMessageInput {
	return driver.SendMessageInput{
		QueueURL:        queue,
		Body:            entry.Body,
		DelaySeconds:    entry.DelaySeconds,
		GroupID:         entry.GroupID,
		DeduplicationID: entry.DeduplicationID,
		Attributes:      entry.Attributes,
	}
}

// DeleteMessageBatch deletes up to 10 messages from the specified Service Bus queue.
func (m *Mock) DeleteMessageBatch(
	ctx context.Context, queue string, entries []driver.BatchDeleteEntry,
) (*driver.BatchDeleteResult, error) {
	if len(entries) > driver.MaxBatchSize {
		return nil, cerrors.Newf(
			cerrors.InvalidArgument, "batch size %d exceeds max %d", len(entries), driver.MaxBatchSize,
		)
	}

	result := &driver.BatchDeleteResult{}

	for _, entry := range entries {
		err := m.DeleteMessage(ctx, queue, entry.ReceiptHandle)
		if err != nil {
			result.Failed = append(result.Failed, driver.BatchSendFailEntry{
				ID: entry.ID, Code: "DeleteFailure", Message: err.Error(),
			})

			continue
		}

		result.Successful = append(result.Successful, entry.ID)
	}

	return result, nil
}

// ReceiveMessagesWithOptions receives messages with configurable options.
func (m *Mock) ReceiveMessagesWithOptions(
	_ context.Context, queue string, opts driver.ReceiveOptions,
) ([]driver.Message, error) {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", queue)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	maxMsgs := clampMaxMessages(opts.MaxMessages)

	visTimeout := opts.VisibilityTimeout
	if visTimeout == 0 {
		visTimeout = qd.visibilityTimeout
	}

	now := m.opts.Clock.Now()
	results, toRemove := m.collectVisibleMessages(qd, maxMsgs, visTimeout, now)

	removeByIndices(qd, toRemove)

	if results == nil {
		results = []driver.Message{}
	}

	remaining := len(qd.messages)
	m.emitMetric(qd.info.Name, map[string]float64{
		"OutgoingMessages": float64(len(results)), "ActiveMessages": float64(remaining),
	})

	return results, nil
}

// GetQueueAttributes returns detailed attributes of the specified queue.
func (m *Mock) GetQueueAttributes(
	_ context.Context, queue string,
) (*driver.QueueAttributes, error) {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", queue)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	now := m.opts.Clock.Now()
	visible, notVisible := countMessages(qd, now)

	return &driver.QueueAttributes{
		DelaySeconds:               qd.delaySeconds,
		MaximumMessageSize:         qd.maxMessageSize,
		MessageRetentionPeriod:     qd.messageRetention,
		VisibilityTimeout:          qd.visibilityTimeout,
		ApproximateMessageCount:    visible,
		ApproximateNotVisibleCount: notVisible,
		CreatedAt:                  qd.createdAt,
		LastModifiedAt:             qd.lastModifiedAt,
		FifoQueue:                  qd.info.FIFO,
	}, nil
}

// SetQueueAttributes updates the attributes of the specified queue.
func (m *Mock) SetQueueAttributes(
	_ context.Context, queue string, attrs map[string]int,
) error {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", queue)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	applyQueueAttributes(qd, attrs)
	qd.lastModifiedAt = m.opts.Clock.Now()

	return nil
}

func applyQueueAttributes(qd *queueData, attrs map[string]int) {
	if v, ok := attrs["DelaySeconds"]; ok {
		qd.delaySeconds = v
	}

	if v, ok := attrs["VisibilityTimeout"]; ok {
		qd.visibilityTimeout = v
	}

	if v, ok := attrs["MaximumMessageSize"]; ok {
		qd.maxMessageSize = v
	}

	if v, ok := attrs["MessageRetentionPeriod"]; ok {
		qd.messageRetention = v
	}
}

// PurgeQueue removes all messages from the specified queue.
func (m *Mock) PurgeQueue(_ context.Context, queue string) error {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", queue)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	qd.messages = make([]*sbMessage, 0)
	qd.lastModifiedAt = m.opts.Clock.Now()

	return nil
}

func countMessages(qd *queueData, now time.Time) (visible, notVisible int) {
	for _, msg := range qd.messages {
		if msg.VisibleAt.After(now) {
			notVisible++
		} else {
			visible++
		}
	}

	return visible, notVisible
}

func removeByIndices(qd *queueData, indices []int) {
	for i := len(indices) - 1; i >= 0; i-- {
		idx := indices[i]
		qd.messages = append(qd.messages[:idx], qd.messages[idx+1:]...)
	}
}
