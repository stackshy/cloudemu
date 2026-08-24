// Package sqs provides an in-memory mock implementation of AWS Simple Queue Service.
package sqs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time check that Mock implements driver.MessageQueue.
var _ driver.MessageQueue = (*Mock)(nil)

const (
	defaultMaxMessageSize   = 262144
	defaultMessageRetention = 345600
	maxReceiveMessages      = 10
	deduplicationWindow     = 5 * time.Minute
)

// sqsMessage represents an internal message stored in a queue.
type sqsMessage struct {
	ID                string
	Body              string
	GroupID           string
	DeduplicationID   string
	Attributes        map[string]string
	MessageAttributes map[string]driver.MessageAttributeValue
	SenderID          string
	SequenceNumber    string
	ReceiptHandle     string
	VisibleAt         time.Time
	SentAt            time.Time
	FirstReceivedAt   time.Time
	ReceiveCount      int
	// sourceQueueURL records the queue a message was redriven from when it lands
	// in a DLQ, so a DLQ redrive (StartMessageMoveTask without a DestinationArn)
	// can return it to its original source.
	sourceQueueURL string
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
	receiveWaitTime    int
	contentBasedDedup  bool
	redrivePolicy      string
	policy             string
	kmsMasterKeyID     string
	createdAt          time.Time
	lastModifiedAt     time.Time
	deduplicationIndex map[string]time.Time
	dlqConfig          *driver.DeadLetterConfig
	seqCounter         atomic.Uint64
}

// LambdaTrigger is a function that gets called when a message is sent to a queue.
type LambdaTrigger func(queueURL string, message driver.Message)

// Mock is an in-memory mock implementation of the AWS SQS service.
type Mock struct {
	queues     *memstore.Store[*queueData]
	moveTasks  *memstore.Store[*moveTask]
	opts       *config.Options
	mu         sync.RWMutex
	triggers   map[string]LambdaTrigger // queueURL -> trigger
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(metricName string, value float64, unit string, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/SQS", MetricName: metricName, Value: value, Unit: unit,
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new SQS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		queues:    memstore.New[*queueData](),
		moveTasks: memstore.New[*moveTask](),
		opts:      opts,
		triggers:  make(map[string]LambdaTrigger),
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

// DeliverExternal enqueues body into the queue identified by ARN. It is used
// for cross-service delivery such as SNS -> SQS and EventBridge -> SQS, where
// the source only knows the target queue's ARN. Returns NotFound if no queue
// matches the ARN.
func (m *Mock) DeliverExternal(ctx context.Context, queueARN, body string) error {
	var url string

	for _, qd := range m.queues.SortedValues() {
		if qd.info.ARN == queueARN {
			url = qd.info.URL
			break
		}
	}

	if url == "" {
		return errors.Newf(errors.NotFound, "no queue found for arn %q", queueARN)
	}

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: body})

	return err
}

// CreateQueue creates a new SQS queue.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateQueue(_ context.Context, cfg driver.QueueConfig) (*driver.QueueInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "queue name is required")
	}

	if cfg.FIFO && !strings.HasSuffix(cfg.Name, ".fifo") {
		return nil, errors.New(errors.InvalidArgument, "FIFO queue name must end with .fifo")
	}

	url := fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", m.opts.Region, m.opts.AccountID, cfg.Name)
	arn := idgen.AWSARN("sqs", m.opts.Region, m.opts.AccountID, cfg.Name)

	// CreateQueue is idempotent: re-creating an existing queue with the exact
	// same attributes returns the existing URL. QueueNameExists is returned only
	// when the incoming attributes differ from the stored ones.
	if existing, ok := m.queues.Get(url); ok {
		if sameQueueConfig(existing, &cfg) {
			result := existing.info
			return &result, nil
		}

		return nil, errors.Newf(errors.AlreadyExists, "queue %q already exists", cfg.Name)
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	maxMessageSize, messageRetention := queueSizeDefaults(&cfg)

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
		messages:           make([]*sqsMessage, 0),
		delaySeconds:       cfg.DelaySeconds,
		visibilityTimeout:  cfg.VisibilityTimeout,
		maxMessageSize:     maxMessageSize,
		messageRetention:   messageRetention,
		receiveWaitTime:    cfg.ReceiveMessageWaitTimeSeconds,
		contentBasedDedup:  cfg.ContentBasedDeduplication,
		createdAt:          now,
		lastModifiedAt:     now,
		deduplicationIndex: make(map[string]time.Time),
		dlqConfig:          cfg.DeadLetterQueue,
	}

	if cfg.RedrivePolicy != "" {
		qd.redrivePolicy = cfg.RedrivePolicy

		if dlq := m.parseRedrivePolicy(cfg.RedrivePolicy); dlq != nil {
			qd.dlqConfig = dlq
		}
	}

	m.queues.Set(url, qd)

	result := info

	return &result, nil
}

// queueSizeDefaults resolves the numeric size attributes that have no valid
// zero value, applying SQS defaults for any left at zero. VisibilityTimeout is
// intentionally excluded: 0 is a valid VisibilityTimeout, so its default (30)
// is applied by the AWS wire handler only when the attribute is absent, letting
// an explicit "0" round-trip unchanged.
func queueSizeDefaults(cfg *driver.QueueConfig) (maxSize, retention int) {
	maxSize = cfg.MaxMessageSize
	if maxSize == 0 {
		maxSize = defaultMaxMessageSize
	}

	retention = cfg.MessageRetention
	if retention == 0 {
		retention = defaultMessageRetention
	}

	return maxSize, retention
}

// sameQueueConfig reports whether an existing queue's stored attributes match
// the incoming CreateQueue config exactly, which is what makes CreateQueue
// idempotent (identical re-create returns the existing URL).
func sameQueueConfig(existing *queueData, cfg *driver.QueueConfig) bool {
	maxSize, retention := queueSizeDefaults(cfg)

	existing.mu.Lock()
	defer existing.mu.Unlock()

	return existing.info.FIFO == cfg.FIFO &&
		existing.visibilityTimeout == cfg.VisibilityTimeout &&
		existing.maxMessageSize == maxSize &&
		existing.messageRetention == retention &&
		existing.delaySeconds == cfg.DelaySeconds &&
		existing.receiveWaitTime == cfg.ReceiveMessageWaitTimeSeconds &&
		existing.contentBasedDedup == cfg.ContentBasedDeduplication &&
		existing.redrivePolicy == cfg.RedrivePolicy
}

// parseRedrivePolicy resolves an SQS RedrivePolicy JSON document into a
// DeadLetterConfig, converting the target DLQ ARN into its queue URL so that
// redrive delivery can locate the queue. Returns nil if the policy is malformed
// or the target queue is unknown.
func (m *Mock) parseRedrivePolicy(raw string) *driver.DeadLetterConfig {
	var doc struct {
		DeadLetterTargetArn string      `json:"deadLetterTargetArn"`
		MaxReceiveCount     json.Number `json:"maxReceiveCount"`
	}

	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil
	}

	if doc.DeadLetterTargetArn == "" {
		return nil
	}

	maxReceive, err := doc.MaxReceiveCount.Int64()
	if err != nil {
		return nil
	}

	url := m.urlForARN(doc.DeadLetterTargetArn)
	if url == "" {
		return nil
	}

	return &driver.DeadLetterConfig{TargetQueueURL: url, MaxReceiveCount: int(maxReceive)}
}

// urlForARN returns the queue URL whose ARN matches, or "" if none.
func (m *Mock) urlForARN(arn string) string {
	for _, qd := range m.queues.SortedValues() {
		if qd.info.ARN == arn {
			return qd.info.URL
		}
	}

	return ""
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

	if qd.maxMessageSize > 0 && len(input.Body) > qd.maxMessageSize {
		return nil, errors.Newf(errors.InvalidArgument,
			"One or more parameters are invalid. Reason: Message must be shorter than %d bytes.", qd.maxMessageSize)
	}

	input.DeduplicationID = effectiveDedupID(qd, &input)

	if err := validateFIFORequirements(qd, &input); err != nil {
		return nil, err
	}

	now := m.opts.Clock.Now()

	// FIFO deduplication: check if same DeduplicationID was sent within 5-min window.
	if existingID, found := findDuplicate(qd, &input, now); found {
		return &driver.SendMessageOutput{MessageID: existingID}, nil
	}

	msg := m.buildStoredMessage(qd, &input, now)
	msgID := msg.ID
	seqNum := msg.SequenceNumber

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
			MessageID:         msgID,
			Body:              input.Body,
			Attributes:        msg.Attributes,
			MessageAttributes: msg.MessageAttributes,
			GroupID:           input.GroupID,
		}

		trigger(input.QueueURL, triggerMsg)
	}

	dims := map[string]string{"QueueName": qd.info.Name}
	m.emitMetric("NumberOfMessagesSent", 1, "Count", dims)
	m.emitMetric("SentMessageSize", float64(len(input.Body)), "Bytes", dims)

	return &driver.SendMessageOutput{
		MessageID:      msgID,
		SequenceNumber: seqNum,
	}, nil
}

// effectiveDedupID returns the deduplication ID to use, deriving it from the
// body when a FIFO queue has ContentBasedDeduplication enabled and the caller
// supplied none.
func effectiveDedupID(qd *queueData, input *driver.SendMessageInput) string {
	if qd.info.FIFO && input.DeduplicationID == "" && qd.contentBasedDedup {
		return contentDeduplicationID(input.Body)
	}

	return input.DeduplicationID
}

// buildStoredMessage constructs the internal message record for a send.
func (m *Mock) buildStoredMessage(qd *queueData, input *driver.SendMessageInput, now time.Time) *sqsMessage {
	attrs := make(map[string]string, len(input.Attributes))
	for k, v := range input.Attributes {
		attrs[k] = v
	}

	delaySeconds := input.DelaySeconds
	if delaySeconds == 0 {
		delaySeconds = qd.delaySeconds
	}

	var seqNum string
	if qd.info.FIFO {
		seqNum = nextSequenceNumber(qd)
	}

	return &sqsMessage{
		ID:                idgen.GenerateID("msg-"),
		Body:              input.Body,
		GroupID:           input.GroupID,
		DeduplicationID:   input.DeduplicationID,
		Attributes:        attrs,
		MessageAttributes: copyMessageAttributes(input.MessageAttributes),
		SenderID:          m.opts.AccountID,
		SequenceNumber:    seqNum,
		VisibleAt:         now.Add(time.Duration(delaySeconds) * time.Second),
		SentAt:            now,
	}
}

// contentDeduplicationID derives a FIFO deduplication ID from the message body
// (SHA-256 hex), matching SQS ContentBasedDeduplication.
func contentDeduplicationID(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// copyMessageAttributes returns a defensive copy of typed message attributes.
func copyMessageAttributes(in map[string]driver.MessageAttributeValue) map[string]driver.MessageAttributeValue {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]driver.MessageAttributeValue, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

const sequenceNumberBase = 10000000000000000000 // 20-digit floor, matching SQS width

// nextSequenceNumber returns a monotonically increasing FIFO sequence number.
func nextSequenceNumber(qd *queueData) string {
	n := qd.seqCounter.Add(1)
	return strconv.FormatUint(sequenceNumberBase+n, 10)
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

	dims := map[string]string{"QueueName": qd.info.Name}
	if len(results) > 0 {
		m.emitMetric("NumberOfMessagesReceived", float64(len(results)), "Count", dims)
	} else {
		m.emitMetric("NumberOfEmptyReceives", 1, "Count", dims)
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
			m.moveToDLQ(qd.dlqConfig.TargetQueueURL, qd.info.URL, msg)

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

	if msg.FirstReceivedAt.IsZero() {
		msg.FirstReceivedAt = now
	}

	attrs := make(map[string]string, len(msg.Attributes))
	for k, v := range msg.Attributes {
		attrs[k] = v
	}

	sysAttrs := map[string]string{
		"SentTimestamp":                    strconv.FormatInt(msg.SentAt.UnixMilli(), 10),
		"ApproximateReceiveCount":          strconv.Itoa(msg.ReceiveCount),
		"ApproximateFirstReceiveTimestamp": strconv.FormatInt(msg.FirstReceivedAt.UnixMilli(), 10),
		"SenderId":                         msg.SenderID,
	}
	if msg.SequenceNumber != "" {
		sysAttrs["SequenceNumber"] = msg.SequenceNumber
	}

	if msg.GroupID != "" {
		sysAttrs["MessageGroupId"] = msg.GroupID
	}

	if msg.DeduplicationID != "" {
		sysAttrs["MessageDeduplicationId"] = msg.DeduplicationID
	}

	return driver.Message{
		MessageID:         msg.ID,
		ReceiptHandle:     receiptHandle,
		Body:              msg.Body,
		Attributes:        attrs,
		MessageAttributes: copyMessageAttributes(msg.MessageAttributes),
		SystemAttributes:  sysAttrs,
		SequenceNumber:    msg.SequenceNumber,
		GroupID:           msg.GroupID,
	}
}

// moveToDLQ moves a message to the dead-letter queue, recording the source
// queue URL so a later DLQ redrive can return it to its origin.
func (m *Mock) moveToDLQ(dlqURL, sourceURL string, msg *sqsMessage) {
	dlq, ok := m.queues.Get(dlqURL)
	if !ok {
		return
	}

	dlq.mu.Lock()
	defer dlq.mu.Unlock()

	dlqMsg := &sqsMessage{
		ID:                msg.ID,
		Body:              msg.Body,
		GroupID:           msg.GroupID,
		Attributes:        msg.Attributes,
		MessageAttributes: msg.MessageAttributes,
		SenderID:          msg.SenderID,
		VisibleAt:         m.opts.Clock.Now(),
		SentAt:            m.opts.Clock.Now(),
		sourceQueueURL:    sourceURL,
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
			m.emitMetric("NumberOfMessagesDeleted", 1, "Count", map[string]string{"QueueName": qd.info.Name})

			return nil
		}
	}

	// DeleteMessage is idempotent: an old/stale (but well-formed) receipt handle
	// against an existing queue succeeds without deleting anything.
	return nil
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

	// The queue exists but no in-flight message carries this handle. AWS reports
	// this as ReceiptHandleIsInvalid, not a missing-queue error; FailedPrecondition
	// is mapped to that wire code by the SQS handler.
	return errors.Newf(errors.FailedPrecondition, "receipt handle %q is invalid", receiptHandle)
}

// SendMessageBatch sends up to 10 messages to the specified SQS queue.
func (m *Mock) SendMessageBatch(
	ctx context.Context, queue string, entries []driver.BatchSendEntry,
) (*driver.BatchSendResult, error) {
	if len(entries) > driver.MaxBatchSize {
		return nil, errors.Newf(
			errors.InvalidArgument, "batch size %d exceeds max %d", len(entries), driver.MaxBatchSize,
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
			ID: entry.ID, MessageID: out.MessageID, SequenceNumber: out.SequenceNumber,
		})
	}

	return result, nil
}

func batchEntryToSendInput(queue string, entry *driver.BatchSendEntry) driver.SendMessageInput {
	return driver.SendMessageInput{
		QueueURL:          queue,
		Body:              entry.Body,
		DelaySeconds:      entry.DelaySeconds,
		GroupID:           entry.GroupID,
		DeduplicationID:   entry.DeduplicationID,
		Attributes:        entry.Attributes,
		MessageAttributes: entry.MessageAttributes,
	}
}

// DeleteMessageBatch deletes up to 10 messages from the specified SQS queue.
func (m *Mock) DeleteMessageBatch(
	ctx context.Context, queue string, entries []driver.BatchDeleteEntry,
) (*driver.BatchDeleteResult, error) {
	if len(entries) > driver.MaxBatchSize {
		return nil, errors.Newf(
			errors.InvalidArgument, "batch size %d exceeds max %d", len(entries), driver.MaxBatchSize,
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
		return nil, errors.Newf(errors.NotFound, "queue %q not found", queue)
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

	dims := map[string]string{"QueueName": qd.info.Name}
	if len(results) > 0 {
		m.emitMetric("NumberOfMessagesReceived", float64(len(results)), "Count", dims)
	} else {
		m.emitMetric("NumberOfEmptyReceives", 1.0, "Count", dims)
	}

	return results, nil
}

// GetQueueAttributes returns detailed attributes of the specified queue.
func (m *Mock) GetQueueAttributes(
	_ context.Context, queue string,
) (*driver.QueueAttributes, error) {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", queue)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	now := m.opts.Clock.Now()
	visible, notVisible := countMessages(qd, now)
	delayed := countDelayedMessages(qd, now)

	return &driver.QueueAttributes{
		DelaySeconds:                  qd.delaySeconds,
		MaximumMessageSize:            qd.maxMessageSize,
		MessageRetentionPeriod:        qd.messageRetention,
		VisibilityTimeout:             qd.visibilityTimeout,
		ApproximateMessageCount:       visible,
		ApproximateNotVisibleCount:    notVisible,
		ApproximateDelayedCount:       delayed,
		CreatedAt:                     qd.createdAt,
		LastModifiedAt:                qd.lastModifiedAt,
		FifoQueue:                     qd.info.FIFO,
		ContentBasedDeduplication:     qd.contentBasedDedup,
		RedrivePolicy:                 qd.redrivePolicy,
		ReceiveMessageWaitTimeSeconds: qd.receiveWaitTime,
		Policy:                        qd.policy,
		KmsMasterKeyID:                qd.kmsMasterKeyID,
	}, nil
}

// countDelayedMessages counts messages still within their delay window.
func countDelayedMessages(qd *queueData, now time.Time) int {
	delayed := 0

	for _, msg := range qd.messages {
		if msg.ReceiveCount == 0 && msg.VisibleAt.After(now) {
			delayed++
		}
	}

	return delayed
}

// SetQueueAttributes updates the attributes of the specified queue.
func (m *Mock) SetQueueAttributes(
	_ context.Context, queue string, attrs map[string]int,
) error {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queue)
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

	if v, ok := attrs["ReceiveMessageWaitTimeSeconds"]; ok {
		qd.receiveWaitTime = v
	}
}

// SetQueueAttributesRaw applies the full set of SQS SetQueueAttributes string
// attributes, including non-numeric ones (RedrivePolicy, ContentBasedDeduplication,
// Policy, KmsMasterKeyId) that the numeric SetQueueAttributes path cannot express.
// It is AWS-specific and used directly by the SQS wire handler.
func (m *Mock) SetQueueAttributesRaw(_ context.Context, queue string, attrs map[string]string) error {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queue)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	applyQueueAttributes(qd, parseNumericAttrs(attrs))

	if v, ok := attrs["RedrivePolicy"]; ok {
		qd.redrivePolicy = v
		qd.dlqConfig = m.parseRedrivePolicy(v)
	}

	if v, ok := attrs["ContentBasedDeduplication"]; ok {
		qd.contentBasedDedup = v == "true"
	}

	if v, ok := attrs["Policy"]; ok {
		qd.policy = v
	}

	if v, ok := attrs["KmsMasterKeyId"]; ok {
		qd.kmsMasterKeyID = v
	}

	qd.lastModifiedAt = m.opts.Clock.Now()

	return nil
}

// parseNumericAttrs extracts the integer-valued queue attributes from a raw
// string attribute map, skipping absent or non-numeric entries.
func parseNumericAttrs(attrs map[string]string) map[string]int {
	keys := []string{
		"DelaySeconds", "VisibilityTimeout", "MaximumMessageSize",
		"MessageRetentionPeriod", "ReceiveMessageWaitTimeSeconds",
	}

	numeric := make(map[string]int, len(keys))

	for _, k := range keys {
		if v, ok := attrs[k]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				numeric[k] = n
			}
		}
	}

	return numeric
}

// ListDeadLetterSourceQueues returns the URLs of queues whose RedrivePolicy
// targets the given DLQ URL.
func (m *Mock) ListDeadLetterSourceQueues(_ context.Context, dlqURL string) ([]string, error) {
	if _, ok := m.queues.Get(dlqURL); !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", dlqURL)
	}

	var sources []string

	for _, qd := range m.queues.SortedValues() {
		qd.mu.Lock()
		matches := qd.dlqConfig != nil && qd.dlqConfig.TargetQueueURL == dlqURL
		qd.mu.Unlock()

		if matches {
			sources = append(sources, qd.info.URL)
		}
	}

	return sources, nil
}

// PurgeQueue removes all messages from the specified queue.
func (m *Mock) PurgeQueue(_ context.Context, queue string) error {
	qd, ok := m.queues.Get(queue)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queue)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	qd.messages = make([]*sqsMessage, 0)
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
