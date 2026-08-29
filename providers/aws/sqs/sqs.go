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
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time check that Mock implements driver.MessageQueue.
var _ driver.MessageQueue = (*Mock)(nil)

const (
	defaultVisibilityTimeout = 30
	defaultMaxMessageSize    = 262144
	defaultMessageRetention  = 345600
	maxReceiveMessages       = 10
	deduplicationWindow      = 5 * time.Minute
	maxReceiveWaitSeconds    = 20
	longPollInterval         = 50 * time.Millisecond
)

// sqsMessage represents an internal message stored in a queue.
type sqsMessage struct {
	ID                string
	Body              string
	GroupID           string
	DeduplicationID   string
	Attributes        map[string]string
	MessageAttributes map[string]driver.MessageAttributeValue
	SystemAttributes  map[string]string
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
	redriveAllowPolicy string
	policy             string
	kmsMasterKeyID     string
	createdAt          time.Time
	lastModifiedAt     time.Time
	deduplicationIndex map[string]time.Time
	dlqConfig          *driver.DeadLetterConfig
	seqCounter         atomic.Uint64
}

// EventSourceInvoker delivers an SQS event-source-mapping batch to whatever
// Lambda ESM(s) target the queue identified by eventSourceARN. delivered
// reports whether any enabled mapping actually targeted the queue at all —
// with none configured, SendMessage must leave the message queued rather than
// treat "nothing happened" as a processed batch. err reports a targeted
// mapping's handler failure, so the caller can decide whether to delete the
// message (success) or leave it for redrive (failure). The Lambda mock
// satisfies it, enabling real SQS -> Lambda invocation (mirroring the
// DynamoDB Streams -> Lambda StreamEventInvoker wiring).
type EventSourceInvoker interface {
	DeliverEventSourceBatch(ctx context.Context, eventSourceARN string, payload []byte) (delivered bool, err error)
}

// Mock is an in-memory mock implementation of the AWS SQS service.
type Mock struct {
	queues     *memstore.Store[*queueData]
	moveTasks  *memstore.Store[*moveTask]
	opts       *config.Options
	mu         sync.RWMutex
	esmInvoker EventSourceInvoker
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
	}
}

// SetEventSourceInvoker wires the Lambda backend so a message sent to a queue
// invokes the queue's Lambda event-source-mapping target(s).
func (m *Mock) SetEventSourceInvoker(i EventSourceInvoker) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.esmInvoker = i
}

// DeliverExternal enqueues body into the queue identified by ARN. It is used
// for cross-service delivery such as SNS -> SQS and EventBridge -> SQS, where
// the source only knows the target queue's ARN. Returns NotFound if no queue
// matches the ARN.
func (m *Mock) DeliverExternal(ctx context.Context, queueARN, body string) error {
	return m.DeliverExternalFIFO(ctx, queueARN, body, "", "")
}

// DeliverExternalFIFO is DeliverExternal carrying the FIFO MessageGroupId /
// MessageDeduplicationId. A FIFO queue rejects a send without a MessageGroupId,
// so an SNS FIFO topic fanning out to a FIFO SQS queue must pass the group id
// through; groupID/dedupID are empty for standard delivery.
func (m *Mock) DeliverExternalFIFO(ctx context.Context, queueARN, body, groupID, dedupID string) error {
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

	_, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL:        url,
		Body:            body,
		GroupID:         groupID,
		DeduplicationID: dedupID,
	})

	return err
}

// CreateQueue creates a new SQS queue.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateQueue(ctx context.Context, cfg driver.QueueConfig) (*driver.QueueInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "queue name is required")
	}

	if cfg.FIFO && !strings.HasSuffix(cfg.Name, ".fifo") {
		return nil, errors.New(errors.InvalidArgument, "FIFO queue name must end with .fifo")
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)
	url := fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", region, m.opts.AccountID, cfg.Name)
	arn := idgen.AWSARN("sqs", region, m.opts.AccountID, cfg.Name)

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
	visibilityTimeout := resolveVisibilityTimeout(&cfg)

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
		visibilityTimeout:  visibilityTimeout,
		maxMessageSize:     maxMessageSize,
		messageRetention:   messageRetention,
		receiveWaitTime:    cfg.ReceiveMessageWaitTimeSeconds,
		contentBasedDedup:  cfg.ContentBasedDeduplication,
		createdAt:          now,
		lastModifiedAt:     now,
		deduplicationIndex: make(map[string]time.Time),
		dlqConfig:          cfg.DeadLetterQueue,
		redriveAllowPolicy: cfg.RedriveAllowPolicy,
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

// resolveVisibilityTimeout applies the SQS default of 30 only when the timeout
// was left unset. An explicit 0 (VisibilityTimeoutSet, which the wire handler
// derives from attribute presence) round-trips unchanged; the typed Go API,
// which cannot express an explicit 0 through a plain int, treats 0 as unset.
func resolveVisibilityTimeout(cfg *driver.QueueConfig) int {
	if cfg.VisibilityTimeout == 0 && !cfg.VisibilityTimeoutSet {
		return defaultVisibilityTimeout
	}

	return cfg.VisibilityTimeout
}

// queueSizeDefaults resolves the numeric size attributes that have no valid
// zero value, applying SQS defaults for any left at zero.
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
		existing.visibilityTimeout == resolveVisibilityTimeout(cfg) &&
		existing.maxMessageSize == maxSize &&
		existing.messageRetention == retention &&
		existing.delaySeconds == cfg.DelaySeconds &&
		existing.receiveWaitTime == cfg.ReceiveMessageWaitTimeSeconds &&
		existing.contentBasedDedup == cfg.ContentBasedDeduplication &&
		existing.redrivePolicy == cfg.RedrivePolicy &&
		existing.redriveAllowPolicy == cfg.RedriveAllowPolicy
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

// arnRegion returns the region field of an SQS ARN
// (arn:aws:sqs:<region>:<account>:<name>), or fallback when the ARN is
// malformed. The stored queue ARN is the source of truth for a queue's region,
// so the ESM event region is derived from it rather than the configured default.
func arnRegion(arn, fallback string) string {
	const regionField, minFields = 3, 6

	parts := strings.Split(arn, ":")
	if len(parts) < minFields || parts[regionField] == "" {
		return fallback
	}

	return parts[regionField]
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
func (m *Mock) SendMessage(ctx context.Context, input driver.SendMessageInput) (*driver.SendMessageOutput, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", input.QueueURL)
	}

	qd.mu.Lock()

	if qd.maxMessageSize > 0 && len(input.Body) > qd.maxMessageSize {
		qd.mu.Unlock()
		return nil, errors.Newf(errors.InvalidArgument,
			"One or more parameters are invalid. Reason: Message must be shorter than %d bytes.", qd.maxMessageSize)
	}

	input.DeduplicationID = effectiveDedupID(qd, &input)

	if err := validateFIFORequirements(qd, &input); err != nil {
		qd.mu.Unlock()
		return nil, err
	}

	now := m.opts.Clock.Now()

	// FIFO deduplication: check if same DeduplicationID was sent within 5-min window.
	if existingID, found := findDuplicate(qd, &input, now); found {
		qd.mu.Unlock()
		return &driver.SendMessageOutput{MessageID: existingID}, nil
	}

	msg := m.buildStoredMessage(qd, &input, now)
	msgID := msg.ID
	seqNum := msg.SequenceNumber

	qd.messages = append(qd.messages, msg)

	if qd.info.FIFO && input.DeduplicationID != "" {
		qd.deduplicationIndex[input.DeduplicationID] = now
	}

	dims := map[string]string{"QueueName": qd.info.Name}
	qd.mu.Unlock()

	m.emitMetric("NumberOfMessagesSent", 1, "Count", dims)
	m.emitMetric("SentMessageSize", float64(len(input.Body)), "Bytes", dims)

	// Deliver to any Lambda event-source-mapping targeting this queue. Runs
	// outside qd.mu so a handler that calls back into SQS (SendMessage,
	// ReceiveMessage, DeleteMessage against this or another queue) never
	// deadlocks on it.
	m.deliverToESM(ctx, qd)

	return &driver.SendMessageOutput{
		MessageID:      msgID,
		SequenceNumber: seqNum,
	}, nil
}

// deliverToESM sweeps the queue for messages whose delay has elapsed and
// delivers each to the Lambda event-source-mapping(s) targeting this queue,
// mirroring real SQS ESM polling collapsed into the SendMessage call since the
// emulator has no background poller. Only messages that are visible now
// (VisibleAt <= now) are swept: a message still inside its DelaySeconds window
// is not yet visible to a poller, so it is skipped and picked up by the next
// SendMessage on this queue after its delay elapses — the same visibility gate
// the ReceiveMessages poll path applies (see collectVisibleMessages). The
// visible set is snapshotted once so the sweep is bounded regardless of a
// per-message outcome. A no-op when no invoker is wired.
//
// callerCtx is SendMessage's own context, used only to read the re-entrant
// delivery depth (internal/recursionguard): a mapped handler commonly
// re-sends to the very queue that triggered it, re-entering here through the
// same synchronous call chain (SendMessage -> deliver -> Invoke -> handler ->
// SendMessage -> ...). Delivery itself always runs on a fresh background
// context, decoupled from callerCtx's cancellation/deadline (delivery must
// still complete once the SendMessage call has already returned), carrying
// forward only the depth so the chain stays bounded.
func (m *Mock) deliverToESM(callerCtx context.Context, qd *queueData) {
	if m.esmInvoker == nil {
		return
	}

	ctx := recursionguard.WithDepth(context.Background(), recursionguard.Depth(callerCtx))

	qd.mu.Lock()
	now := m.opts.Clock.Now()
	pending := make([]*sqsMessage, 0, len(qd.messages))

	for _, msg := range qd.messages {
		if !msg.VisibleAt.After(now) {
			pending = append(pending, msg)
		}
	}

	qd.mu.Unlock()

	for _, msg := range pending {
		m.deliverMessageToESM(ctx, qd, msg)
	}
}

// deliverMessageToESM delivers a single already-visible message to the Lambda
// event-source-mapping(s) targeting this queue. Each pass builds a trial
// "received" view of the message (a fresh ReceiptHandle, ReceiveCount+1)
// without touching the stored message yet, and only commits that receive once
// DeliverEventSourceBatch reports delivered=true — i.e. a mapping actually
// exists for this queue's ARN. A queue with no ESM configured at all must
// therefore leave the message completely untouched, rather than have "nothing
// happened" misread as a processed batch.
//
// Once a mapping has genuinely consumed the message: on success it is
// deleted, matching "When your function successfully processes a batch,
// Lambda deletes its messages from the queue"
// (https://docs.aws.amazon.com/lambda/latest/dg/with-sqs.html). On failure it
// is left in the queue (invisible until the visibility timeout, as in real
// SQS) unless a RedrivePolicy is configured, in which case delivery retries
// in a synchronous loop until it either succeeds or exceedsMaxReceive is
// reached, at which point the same DLQ-redrive threshold ReceiveMessages
// honors (see collectVisibleMessages) moves the message to the dead-letter
// queue. ctx already carries the re-entrant delivery depth.
func (m *Mock) deliverMessageToESM(ctx context.Context, qd *queueData, msg *sqsMessage) {
	for {
		qd.mu.Lock()

		if !containsMessage(qd, msg) {
			qd.mu.Unlock()
			return
		}

		trialCount := msg.ReceiveCount + 1

		// A receive that would cross MaxReceiveCount is redirected to the DLQ
		// without ever invoking Lambda for it, mirroring collectVisibleMessages
		// (the receive that crosses the threshold never reaches the consumer
		// there either). This can only fire once at least one earlier attempt
		// in this same call has already been delivered: ReceiveCount starts at
		// 0 for a freshly sent message, so its first attempt (trialCount=1)
		// can never exceed a MaxReceiveCount >= 1.
		if qd.dlqConfig != nil && qd.dlqConfig.MaxReceiveCount > 0 && trialCount > qd.dlqConfig.MaxReceiveCount {
			msg.ReceiveCount = trialCount
			removeQueuedMessage(qd, msg)
			dlqURL, sourceURL := qd.dlqConfig.TargetQueueURL, qd.info.URL
			qd.mu.Unlock()
			m.moveToDLQ(dlqURL, sourceURL, msg)

			return
		}

		trial := *msg
		trial.ReceiveCount = trialCount
		received := buildReceivedMessage(&trial, qd.visibilityTimeout, m.opts.Clock.Now())
		arn := qd.info.ARN
		region := arnRegion(arn, m.opts.Region)
		qd.mu.Unlock()

		payload := driver.BuildLambdaSQSEvent(arn, region, []driver.Message{received})

		delivered, deliverErr := m.esmInvoker.DeliverEventSourceBatch(ctx, arn, payload)
		if !delivered {
			// No event-source-mapping targets this queue: the send is unaffected.
			return
		}

		if m.commitESMReceive(qd, msg, &trial, deliverErr) {
			return
		}
	}
}

// commitESMReceive applies a trial receive (built by deliverToESM once a
// mapping is known to have actually consumed the message) onto the real
// stored message, then resolves the outcome: delete on success, DLQ-redrive
// once exceedsMaxReceive, leave in place with no DLQ configured, or signal
// the caller to retry. It reports whether deliverToESM's loop should stop.
func (m *Mock) commitESMReceive(qd *queueData, msg, trial *sqsMessage, deliverErr error) (done bool) {
	qd.mu.Lock()

	if !containsMessage(qd, msg) {
		qd.mu.Unlock()
		return true
	}

	msg.ReceiveCount = trial.ReceiveCount
	msg.ReceiptHandle = trial.ReceiptHandle
	msg.VisibleAt = trial.VisibleAt
	msg.FirstReceivedAt = trial.FirstReceivedAt

	if deliverErr == nil {
		removeQueuedMessage(qd, msg)
		qd.mu.Unlock()
		m.emitMetric("NumberOfMessagesDeleted", 1, "Count", map[string]string{"QueueName": qd.info.Name})

		return true
	}

	// deliverToESM's upfront check already redirects a receive that would
	// cross the threshold before ever invoking Lambda for it; this only fires
	// if a concurrent SetQueueAttributesRaw shrank MaxReceiveCount between
	// that check and this commit.
	if exceedsMaxReceive(qd, msg) {
		removeQueuedMessage(qd, msg)
		dlqURL, sourceURL := qd.dlqConfig.TargetQueueURL, qd.info.URL
		qd.mu.Unlock()
		m.moveToDLQ(dlqURL, sourceURL, msg)

		return true
	}

	hasDLQ := qd.dlqConfig != nil
	qd.mu.Unlock()

	// No DLQ configured: one delivery attempt only. The message stays in the
	// queue, invisible until the visibility timeout just set expires, exactly
	// as an unconfigured real SQS/Lambda ESM leaves a failed batch for the
	// next poll. A DLQ that hasn't hit its threshold yet retries immediately.
	return !hasDLQ
}

// containsMessage reports whether the given message pointer is still queued.
// Caller must hold qd.mu.
func containsMessage(qd *queueData, target *sqsMessage) bool {
	for _, msg := range qd.messages {
		if msg == target {
			return true
		}
	}

	return false
}

// removeQueuedMessage removes the given message pointer from the queue's
// backlog, if still present. Caller must hold qd.mu.
func removeQueuedMessage(qd *queueData, target *sqsMessage) {
	for i, msg := range qd.messages {
		if msg == target {
			qd.messages = append(qd.messages[:i], qd.messages[i+1:]...)
			return
		}
	}
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
		SystemAttributes:  systemAttributeStrings(input.SystemAttributes),
		SenderID:          m.opts.AccountID,
		SequenceNumber:    seqNum,
		VisibleAt:         now.Add(time.Duration(delaySeconds) * time.Second),
		SentAt:            now,
	}
}

// systemAttributeStrings flattens caller-supplied SQS message system attributes
// (e.g. AWSTraceHeader) into a name->string map for storage. Only the string
// value is retained; system attributes are always String-typed in SQS.
func systemAttributeStrings(in map[string]driver.MessageAttributeValue) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v.StringValue
	}

	return out
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
// When WaitTimeSeconds (or the queue's ReceiveMessageWaitTimeSeconds default) is > 0, it long-polls:
// it retries on a short interval until a message is available, the deadline elapses, or ctx is canceled.
func (m *Mock) ReceiveMessages(ctx context.Context, input driver.ReceiveMessageInput) ([]driver.Message, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "queue %q not found", input.QueueURL)
	}

	deadline := time.Now().Add(resolveWaitDuration(qd, input.WaitTimeSeconds))

	results, err := m.pollForMessages(ctx, qd, input, deadline)
	if err != nil {
		return nil, err
	}

	m.emitReceiveMetrics(qd, len(results))

	return results, nil
}

// pollForMessages performs the bounded long-poll loop. It never holds qd.mu across
// the wait: each attempt acquires the lock via receiveOnce, releases it, then sleeps.
func (m *Mock) pollForMessages(
	ctx context.Context, qd *queueData, input driver.ReceiveMessageInput, deadline time.Time,
) ([]driver.Message, error) {
	for {
		results := m.receiveOnce(qd, input)
		if len(results) > 0 || !time.Now().Before(deadline) {
			return results, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(longPollInterval):
		}
	}
}

// receiveOnce performs a single, immediate receive attempt under the queue lock.
func (m *Mock) receiveOnce(qd *queueData, input driver.ReceiveMessageInput) []driver.Message {
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
		return []driver.Message{}
	}

	return results
}

// emitReceiveMetrics records the CloudWatch receive counters for a single receive call.
func (m *Mock) emitReceiveMetrics(qd *queueData, count int) {
	dims := map[string]string{"QueueName": qd.info.Name}
	if count > 0 {
		m.emitMetric("NumberOfMessagesReceived", float64(count), "Count", dims)
	} else {
		m.emitMetric("NumberOfEmptyReceives", 1, "Count", dims)
	}
}

// resolveWaitDuration derives the long-poll window: the request's WaitTimeSeconds,
// falling back to the queue's ReceiveMessageWaitTimeSeconds default when unset,
// capped at the SQS maximum of 20 seconds.
func resolveWaitDuration(qd *queueData, requested int) time.Duration {
	qd.mu.Lock()
	queueDefault := qd.receiveWaitTime
	qd.mu.Unlock()

	seconds := requested
	if seconds <= 0 {
		seconds = queueDefault
	}

	if seconds > maxReceiveWaitSeconds {
		seconds = maxReceiveWaitSeconds
	}

	if seconds < 0 {
		seconds = 0
	}

	return time.Duration(seconds) * time.Second
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

	// FIFO: any group that already has an in-flight message (received in a prior
	// call, visibility not yet expired) is skipped entirely this call, so a
	// group's messages are delivered strictly in order across consumers. Within
	// a single call a non-blocked group still yields its consecutive messages
	// (up to MaxNumberOfMessages), matching real SQS FIFO batch receives.
	blocked := fifoInFlightGroups(qd, now)

	for i, msg := range qd.messages {
		if len(results) >= maxMessages {
			break
		}

		if blocked[msg.GroupID] {
			continue
		}

		if msg.VisibleAt.After(now) {
			continue
		}

		msg.ReceiveCount++

		// Check if message exceeded max receive count - move to DLQ.
		if exceedsMaxReceive(qd, msg) {
			m.moveToDLQ(qd.dlqConfig.TargetQueueURL, qd.info.URL, msg)

			toRemove = append(toRemove, i)

			continue
		}

		results = append(results, buildReceivedMessage(msg, visibilityTimeout, now))
	}

	return results, toRemove
}

// fifoInFlightGroups returns the set of FIFO message groups that have at least
// one in-flight message (VisibleAt in the future) as of now, computed before the
// receive marks anything. Such groups are skipped for this call so their next
// message is not delivered until the in-flight one is deleted or its visibility
// expires. Returns an empty (never nil) set for non-FIFO queues.
func fifoInFlightGroups(qd *queueData, now time.Time) map[string]bool {
	blocked := make(map[string]bool)
	if !qd.info.FIFO {
		return blocked
	}

	for _, msg := range qd.messages {
		if msg.GroupID != "" && msg.VisibleAt.After(now) {
			blocked[msg.GroupID] = true
		}
	}

	return blocked
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

	// Caller-supplied system attributes (e.g. AWSTraceHeader) round-trip verbatim.
	for k, v := range msg.SystemAttributes {
		sysAttrs[k] = v
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

// exceedsMaxReceive reports whether msg's receive count has now exceeded the
// queue's configured DLQ MaxReceiveCount. Shared by ReceiveMessages (see
// collectVisibleMessages) and Lambda event-source-mapping delivery (see
// deliverToESM) so both polling paths honor the same redrive threshold.
func exceedsMaxReceive(qd *queueData, msg *sqsMessage) bool {
	return qd.dlqConfig != nil && qd.dlqConfig.MaxReceiveCount > 0 && msg.ReceiveCount > qd.dlqConfig.MaxReceiveCount
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

	if !isWellFormedReceiptHandle(receiptHandle) {
		// AWS rejects a syntactically-invalid receipt handle with
		// ReceiptHandleIsInvalid (FailedPrecondition maps to that wire code),
		// distinct from the idempotent no-op for a stale but well-formed handle.
		return errors.Newf(errors.FailedPrecondition, "receipt handle %q is invalid", receiptHandle)
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

// isWellFormedReceiptHandle reports whether a receipt handle is syntactically
// valid: non-empty and built only from the characters a real SQS receipt handle
// (a base64url token) and this emulator's own handles use. A stale handle from a
// prior receive still passes, so DeleteMessage stays idempotent for it; a
// syntactically-invalid handle (e.g. "!!!not-a-handle!!!") is rejected.
func isWellFormedReceiptHandle(handle string) bool {
	if handle == "" {
		return false
	}

	for _, r := range handle {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+', r == '/', r == '=', r == '-', r == '_':
		default:
			return false
		}
	}

	return true
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
		SystemAttributes:  entry.SystemAttributes,
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
		RedriveAllowPolicy:            qd.redriveAllowPolicy,
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

	if v, ok := attrs["RedriveAllowPolicy"]; ok {
		qd.redriveAllowPolicy = v
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
