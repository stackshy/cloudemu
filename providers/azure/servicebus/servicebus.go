// Package servicebus provides an in-memory mock implementation of Azure Service Bus.
package servicebus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time check that Mock implements driver.MessageQueue.
var _ driver.MessageQueue = (*Mock)(nil)

const (
	defaultVisibilityTimeout = 30
	maxReceiveMessages       = 10
	deduplicationWindow      = 5 * time.Minute
	// defaultDupDetectionWindow is Service Bus' default duplicate-detection
	// history window when RequiresDuplicateDetection is set without a window.
	defaultDupDetectionWindow = 10 * time.Minute
)

// sbMessage represents an internal message stored in a queue.
type sbMessage struct {
	ID              string
	Body            string
	GroupID         string
	DeduplicationID string
	Attributes      map[string]string
	// SystemProps carries Azure Service Bus brokered-message system properties
	// (MessageId, CorrelationId, Label, SessionId, ...) preserved from the send.
	SystemProps map[string]string
	// SessionID is the message's Service Bus SessionId, promoted from
	// SystemProps["SessionId"] at enqueue so session receive can filter on it in
	// FIFO order. Empty on a non-session entity.
	SessionID     string
	ReceiptHandle string
	VisibleAt     time.Time
	SentAt        time.Time
	ReceiveCount  int
	// ExpiresAt is the message's absolute expiration time. The zero value
	// means the message never expires — the default for Service Bus queues
	// (which never set SendMessageInput.MessageTTLSeconds) and for Azure
	// Queue Storage messages sent with messagettl=-1. See isMessageExpired.
	ExpiresAt time.Time
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
	// requiresDupDetection enables Azure Service Bus MessageId-based duplicate
	// detection; dedupByMessageID tracks the last-seen time per MessageId and
	// dupDetectionWindow is the look-back window.
	requiresDupDetection bool
	dupDetectionWindow   time.Duration
	dedupByMessageID     map[string]time.Time
	dlqConfig            *driver.DeadLetterConfig
	// deadLetterOnExpiration routes an expired message to the dead-letter queue
	// instead of dropping it (Service Bus deadLetteringOnMessageExpiration).
	deadLetterOnExpiration bool
	metadata               map[string]string
	// requiresSession enables Service Bus sessions on the entity: every message
	// must carry a SessionId, and messages are consumed per session. sessions
	// tracks each session's lock and state, keyed by SessionId; it is allocated
	// only for a session entity.
	requiresSession bool
	sessions        map[string]*sessionState
}

// sessionState tracks a single Service Bus session's lock ownership and its
// opaque state blob. Fields are exported so it snapshots directly like
// sbMessage. A zero LockOwner means the session is unlocked.
type sessionState struct {
	LockOwner   string    // opaque receiver/lock id holding the session; "" = unlocked
	LockedUntil time.Time // session-lock expiry
	State       string    // opaque session-state blob (get/set session state)
}

// FunctionTrigger is a function that gets called when a message is sent to a queue.
type FunctionTrigger func(queueURL string, message driver.Message)

// FunctionTriggerSink delivers a message enqueued to a queue to any Azure
// Function bound to that queue by a trigger binding (queueTrigger for Queue
// Storage, serviceBusTrigger for Service Bus). The Azure Functions provider
// implements it. It is wired per Mock instance via SetFunctionTriggerSink so the
// distinct Queue Storage and Service Bus instances each dispatch their own
// binding type, mirroring how Event Grid delivery calls InvokeExternal.
type FunctionTriggerSink interface {
	DeliverFunctionTrigger(ctx context.Context, bindingType, queueName string, body []byte)
}

// Mock is an in-memory mock implementation of the Azure Service Bus service.
type Mock struct {
	queues     *memstore.Store[*queueData]
	opts       *config.Options
	mu         sync.RWMutex
	triggers   map[string]FunctionTrigger // queueURL -> trigger
	monitoring mondriver.Monitoring
	// funcSink and triggerBinding wire automatic Azure Function trigger delivery:
	// on each enqueue, funcSink is asked to invoke any function whose function.json
	// declares a triggerBinding-typed binding for the queue. Both are guarded by mu.
	funcSink       FunctionTriggerSink
	triggerBinding string
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

// SetFunctionTriggerSink wires the Azure Functions provider as the destination
// for this queue surface's automatic trigger deliveries. bindingType is the
// function.json trigger type this surface fires — "queueTrigger" for Queue
// Storage, "serviceBusTrigger" for Service Bus — so only functions bound with
// the matching trigger are invoked. A nil sink disables trigger delivery (the
// default). This is the cross-service seam, analogous to Event Grid's
// SetFunctionInvoker.
func (m *Mock) SetFunctionTriggerSink(sink FunctionTriggerSink, bindingType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.funcSink = sink
	m.triggerBinding = bindingType
}

// DeliverExternal enqueues body into the queue or topic whose name matches the
// given name. It is used for cross-service delivery such as Event Grid ->
// ServiceBusQueue/ServiceBusTopic, where the source only knows the destination's
// ARM leaf name. A topic is modeled as a queue in this mock, so both resolve the
// same way. Returns NotFound if no queue matches, which callers may ignore for a
// best-effort sink.
func (m *Mock) DeliverExternal(ctx context.Context, name, body string) error {
	var url string

	for _, qd := range m.queues.SortedValues() {
		if qd.info.Name == name {
			url = qd.info.URL
			break
		}
	}

	if url == "" {
		return cerrors.Newf(cerrors.NotFound, "no queue found for name %q", name)
	}

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: body})

	return err
}

// CreateQueue creates a new Service Bus queue.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
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

	metadata := make(map[string]string, len(cfg.Metadata))
	for k, v := range cfg.Metadata {
		metadata[k] = v
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

	dupWindow := cfg.DuplicateDetectionWindow
	if dupWindow <= 0 {
		dupWindow = defaultDupDetectionWindow
	}

	qd := &queueData{
		info:                   info,
		messages:               make([]*sbMessage, 0),
		delaySeconds:           cfg.DelaySeconds,
		visibilityTimeout:      visibilityTimeout,
		maxMessageSize:         cfg.MaxMessageSize,
		messageRetention:       cfg.MessageRetention,
		createdAt:              now,
		lastModifiedAt:         now,
		deduplicationIndex:     make(map[string]time.Time),
		requiresDupDetection:   cfg.RequiresDuplicateDetection,
		dupDetectionWindow:     dupWindow,
		dedupByMessageID:       make(map[string]time.Time),
		dlqConfig:              cfg.DeadLetterQueue,
		deadLetterOnExpiration: cfg.DeadLetterOnExpiration,
		metadata:               metadata,
		requiresSession:        cfg.RequiresSession,
	}

	if cfg.RequiresSession {
		qd.sessions = make(map[string]*sessionState)
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
func (m *Mock) SendMessage(ctx context.Context, input driver.SendMessageInput) (*driver.SendMessageOutput, error) {
	qd, ok := m.queues.Get(input.QueueURL)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", input.QueueURL)
	}

	res, err := m.enqueueLocked(qd, &input)
	if err != nil {
		return nil, err
	}

	// A duplicate send is silently accepted but not re-enqueued, so it fires no
	// trigger and emits no incoming-message metric.
	if res.msg == nil {
		return &driver.SendMessageOutput{MessageID: res.dupID}, nil
	}

	// Triggers run AFTER the queue lock is released: a function invoked by a
	// trigger may re-enqueue to this same queue, which would deadlock on the
	// non-reentrant qd.mu if fired while it is still held. The recursion guard
	// threaded through ctx bounds any self-referential enqueue→invoke→enqueue
	// chain (see dispatchFunctionTrigger / lambda.InvokeExternal).
	m.fireTrigger(input.QueueURL, res.msg)
	m.dispatchFunctionTrigger(ctx, res.queueName, res.msg)

	m.emitMetric(res.queueName, map[string]float64{
		"IncomingMessages": 1, "Size": float64(len(input.Body)),
	})

	return &driver.SendMessageOutput{
		MessageID:  res.msg.ID,
		ExpiresAt:  res.msg.ExpiresAt,
		PopReceipt: res.msg.ReceiptHandle,
	}, nil
}

// enqueueResult carries what SendMessage needs after the queue lock is released:
// the stored message and the queue's name for trigger dispatch. A nil msg with a
// non-empty dupID means the send was a silently-accepted duplicate that was not
// re-enqueued (so it fires no trigger).
type enqueueResult struct {
	msg       *sbMessage
	queueName string
	dupID     string
}

// enqueueLocked validates and appends one message under the queue lock.
func (m *Mock) enqueueLocked(qd *queueData, input *driver.SendMessageInput) (enqueueResult, error) {
	qd.mu.Lock()
	defer qd.mu.Unlock()

	if err := validateFIFORequirements(qd, input); err != nil {
		return enqueueResult{}, err
	}

	// A session-enabled entity requires every message to carry a SessionId (real
	// Azure rejects a session-less send with InvalidOperation → 400).
	sessionID := input.SystemProperties["SessionId"]
	if qd.requiresSession && sessionID == "" {
		return enqueueResult{}, cerrors.New(cerrors.InvalidArgument, "SessionId is required for a session-enabled entity")
	}

	now := m.opts.Clock.Now()

	// A repeat DeduplicationID (FIFO) or MessageId (Service Bus dup-detection)
	// inside the window is silently accepted but not re-enqueued.
	if existingID, found := findSendDuplicate(qd, input, now); found {
		return enqueueResult{queueName: qd.info.Name, dupID: existingID}, nil
	}

	msg := buildSendMessage(input, sessionID, now, qd.delaySeconds)
	qd.messages = append(qd.messages, msg)
	recordSendDedup(qd, input, now)

	return enqueueResult{msg: msg, queueName: qd.info.Name}, nil
}

// dispatchFunctionTrigger forwards a just-enqueued message to the Azure Functions
// provider so any function bound to this queue fires. It is a no-op when no sink
// is wired (the default, and every non-Azure-Functions deployment). Called after
// the queue lock is released; see SendMessage.
func (m *Mock) dispatchFunctionTrigger(ctx context.Context, queueName string, msg *sbMessage) {
	m.mu.RLock()
	sink := m.funcSink
	bindingType := m.triggerBinding
	m.mu.RUnlock()

	if sink == nil {
		return
	}

	sink.DeliverFunctionTrigger(ctx, bindingType, queueName, []byte(msg.Body))
}

// findSendDuplicate reports an already-accepted message id when input is a FIFO
// (DeduplicationID) or Service Bus (MessageId) duplicate within the dedup window.
func findSendDuplicate(qd *queueData, input *driver.SendMessageInput, now time.Time) (string, bool) {
	if id, found := findDuplicate(qd, input, now); found {
		return id, true
	}

	return findMessageIDDuplicate(qd, input, now)
}

// buildSendMessage constructs the stored message from a send, copying the caller
// maps and resolving the effective visibility/expiry. TimeToLive is measured
// from the message's active time (visibleAt), so a scheduled message with a TTL
// shorter than its delay still survives until at least its scheduled enqueue.
func buildSendMessage(input *driver.SendMessageInput, sessionID string, now time.Time, queueDelay int) *sbMessage {
	attrs := make(map[string]string, len(input.Attributes))
	for k, v := range input.Attributes {
		attrs[k] = v
	}

	sysProps := make(map[string]string, len(input.SystemProperties))
	for k, v := range input.SystemProperties {
		sysProps[k] = v
	}

	delaySeconds := input.DelaySeconds
	if delaySeconds == 0 {
		delaySeconds = queueDelay
	}

	visibleAt := now.Add(time.Duration(delaySeconds) * time.Second)

	return &sbMessage{
		ID:              idgen.GenerateID("sb-msg-"),
		Body:            input.Body,
		GroupID:         input.GroupID,
		DeduplicationID: input.DeduplicationID,
		Attributes:      attrs,
		SystemProps:     sysProps,
		SessionID:       sessionID,
		// A pop receipt minted at enqueue lets the message be deleted/updated with
		// the Put Message-returned receipt before its first dequeue.
		ReceiptHandle: idgen.GenerateID("sb-lock-"),
		VisibleAt:     visibleAt,
		SentAt:        now,
		ExpiresAt:     computeExpiry(visibleAt, input.MessageTTLSeconds),
	}
}

// recordSendDedup updates the FIFO and Service Bus duplicate-detection indexes
// for a just-enqueued message.
func recordSendDedup(qd *queueData, input *driver.SendMessageInput, now time.Time) {
	if qd.info.FIFO && input.DeduplicationID != "" {
		qd.deduplicationIndex[input.DeduplicationID] = now
	}

	if qd.requiresDupDetection {
		if mid := messageIDOf(input); mid != "" {
			qd.dedupByMessageID[mid] = now
		}
	}
}

// fireTrigger invokes any registered Function trigger for the queue.
func (m *Mock) fireTrigger(queueURL string, msg *sbMessage) {
	m.mu.RLock()
	trigger := m.triggers[queueURL]
	m.mu.RUnlock()

	if trigger == nil {
		return
	}

	trigger(queueURL, driver.Message{
		MessageID:  msg.ID,
		Body:       msg.Body,
		Attributes: msg.Attributes,
		GroupID:    msg.GroupID,
	})
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

// messageIDOf returns the Azure Service Bus MessageId a send carried, or "" when
// none was set (an empty MessageId is never deduplicated, matching real SB).
func messageIDOf(input *driver.SendMessageInput) string {
	if input.SystemProperties == nil {
		return ""
	}

	return input.SystemProperties["MessageId"]
}

// findMessageIDDuplicate reports whether a send repeats a MessageId already seen
// within the duplicate-detection window on a RequiresDuplicateDetection queue,
// returning the driver id of the surviving message when it is still enqueued.
func findMessageIDDuplicate(qd *queueData, input *driver.SendMessageInput, now time.Time) (string, bool) {
	if !qd.requiresDupDetection {
		return "", false
	}

	mid := messageIDOf(input)
	if mid == "" {
		return "", false
	}

	seenAt, ok := qd.dedupByMessageID[mid]
	if !ok || now.Sub(seenAt) >= qd.dupDetectionWindow {
		return "", false
	}

	for _, existing := range qd.messages {
		if existing.SystemProps["MessageId"] == mid {
			return existing.ID, true
		}
	}

	// The original was already consumed but the MessageId is still within the
	// detection window, so the duplicate is still dropped.
	return mid, true
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
	results, toRemove := m.collectVisibleMessages(qd, maxMsgs, visibilityTimeout, now, plainReceiveAccept(qd))

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

// plainReceiveAccept is the predicate for a plain (non-session) receive. On a
// session entity it accepts nothing — Service Bus sessions are consumed only via
// the session receiver, so a plain REST receive against a session queue returns
// empty, matching real Azure (where session receive is not available over REST).
// On a non-session entity it returns nil, accepting every message unchanged.
func plainReceiveAccept(qd *queueData) func(*sbMessage) bool {
	if !qd.requiresSession {
		return nil
	}

	return func(*sbMessage) bool { return false }
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

// collectVisibleMessages gathers up to maxMessages visible messages that pass
// the accept predicate (nil accepts every message), reaping expired ones and
// dead-lettering exhausted ones. The predicate lets the plain and session
// receive paths share one collector: the plain path skips session messages on a
// session entity, the session path selects a single SessionId.
func (m *Mock) collectVisibleMessages(
	qd *queueData, maxMessages, visibilityTimeout int, now time.Time, accept func(*sbMessage) bool,
) (messages []driver.Message, dlqIndices []int) {
	var results []driver.Message

	var toRemove []int

	for i, msg := range qd.messages {
		if len(results) >= maxMessages {
			break
		}

		if accept != nil && !accept(msg) {
			continue
		}

		if m.reapExpired(qd, msg, now) {
			toRemove = append(toRemove, i)
			continue
		}

		if msg.VisibleAt.After(now) {
			continue
		}

		msg.ReceiveCount++

		if exceeded, moved := m.deadLetterExhausted(qd, msg); exceeded {
			if moved {
				toRemove = append(toRemove, i)
			}

			continue
		}

		results = append(results, buildReceivedMessage(msg, visibilityTimeout, now))
	}

	return results, toRemove
}

// reapExpired handles an expired message (Azure Queue Storage's per-message
// messagettl / Service Bus defaultMessageTimeToLive), mirroring the Cosmos DB
// mock's on-read TTL check: it routes the message to the DLQ when
// dead-lettering-on-expiration is enabled and reports whether it was reaped so
// the caller drops it.
func (m *Mock) reapExpired(qd *queueData, msg *sbMessage, now time.Time) bool {
	if !isMessageExpired(msg, now) {
		return false
	}

	if qd.deadLetterOnExpiration && qd.dlqConfig != nil {
		m.moveToDLQ(qd.dlqConfig.TargetQueueURL, msg)
	}

	return true
}

// deadLetterExhausted reports whether msg has exhausted its delivery attempts,
// and whether it was successfully moved to the DLQ. Only when moved may the
// caller drop it from the main queue -- a missing DLQ store must not silently
// lose the message.
func (m *Mock) deadLetterExhausted(qd *queueData, msg *sbMessage) (exceeded, moved bool) {
	if qd.dlqConfig == nil || qd.dlqConfig.MaxReceiveCount <= 0 || msg.ReceiveCount <= qd.dlqConfig.MaxReceiveCount {
		return false, false
	}

	return true, m.moveToDLQ(qd.dlqConfig.TargetQueueURL, msg)
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

	sysProps := make(map[string]string, len(msg.SystemProps))
	for k, v := range msg.SystemProps {
		sysProps[k] = v
	}

	return driver.Message{
		MessageID:        msg.ID,
		ReceiptHandle:    receiptHandle,
		Body:             msg.Body,
		Attributes:       attrs,
		SystemProperties: sysProps,
		GroupID:          msg.GroupID,
		ReceiveCount:     msg.ReceiveCount,
		ExpiresAt:        msg.ExpiresAt,
		InsertedAt:       msg.SentAt,
	}
}

// moveToDLQ copies a message into the dead-letter queue, reporting whether the
// move succeeded. A missing DLQ store yields false so the caller can keep the
// message on the main queue rather than dropping (and losing) it. Dead-lettered
// messages do not carry the source TTL (ExpiresAt stays zero), matching Service
// Bus, where the DLQ has its own retention.
func (m *Mock) moveToDLQ(dlqURL string, msg *sbMessage) bool {
	dlq, ok := m.queues.Get(dlqURL)
	if !ok {
		return false
	}

	now := m.opts.Clock.Now()

	dlq.mu.Lock()
	defer dlq.mu.Unlock()

	dlq.messages = append(dlq.messages, &sbMessage{
		ID:          msg.ID,
		Body:        msg.Body,
		GroupID:     msg.GroupID,
		Attributes:  msg.Attributes,
		SystemProps: msg.SystemProps,
		VisibleAt:   now,
		SentAt:      now,
	})

	return true
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
	results, toRemove := m.collectVisibleMessages(qd, maxMsgs, visTimeout, now, plainReceiveAccept(qd))

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

	// MaxDeliveryCount reconfigures the dead-letter threshold (Service Bus
	// maxDeliveryCount): lowering it dead-letters already-enqueued messages at the
	// new threshold on their next delivery attempt.
	if v, ok := attrs["MaxDeliveryCount"]; ok && qd.dlqConfig != nil {
		qd.dlqConfig.MaxReceiveCount = v
	}

	// DeadLetterOnExpiration toggles Service Bus'
	// deadLetteringOnMessageExpiration (encoded 0/1 over the int-only map).
	if v, ok := attrs["DeadLetterOnExpiration"]; ok {
		qd.deadLetterOnExpiration = v != 0
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
