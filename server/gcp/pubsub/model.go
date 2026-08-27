package pubsub

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const (
	defaultAckDeadlineSeconds = 10
	// defaultMaxDeliveryAttempts is Pub/Sub's default when a deadLetterPolicy
	// sets a dead-letter topic but omits maxDeliveryAttempts.
	defaultMaxDeliveryAttempts = 5
	// deletedTopicName is what real Pub/Sub reports as a subscription's topic
	// after the topic it was attached to is deleted.
	deletedTopicName = "_deleted-topic_"
)

// storedMessage is one message in a topic's append-only log. Every subscription
// on the topic reads from this shared log by index; per-subscription ack state
// (not the message bytes) is what makes delivery independent — real Pub/Sub
// fan-out, where each subscription gets its own copy of every message.
type storedMessage struct {
	id          string
	body        string // raw (un-encoded) payload
	attributes  map[string]string
	orderingKey string
	publishTime time.Time
}

// topicState holds a topic's message log and IAM policy. Topic existence and
// labels remain owned by the messagequeue driver (the provider layer); this is
// the Pub/Sub-native state the SQS-style driver cannot express.
type topicState struct {
	messages []storedMessage
	iam      *iamPolicy

	// labels overrides the messagequeue driver's tags once a topic is created or
	// patched through this handler, so topics.patch on labels is reflected on
	// subsequent Get/List. labelsSet distinguishes an explicit empty map from
	// "no override, fall back to the driver's tags".
	labels    map[string]string
	labelsSet bool

	// Extended topic config the SQS-style driver cannot express, persisted here so
	// create/patch round-trip on subsequent Get/List.
	msgRetentionDuration string
	schemaSettings       json.RawMessage
	kmsKeyName           string
	messageStoragePolicy json.RawMessage
	satisfiesPzs         bool
}

// lease is an outstanding (delivered, not-yet-acked) message on a subscription.
type lease struct {
	msgIdx   int
	deadline time.Time
}

// subState is a subscription's full native state: its config (round-tripped
// verbatim) plus independent delivery cursor over its topic's message log.
type subState struct {
	cfg        subscription
	topic      string // topic short-name whose log backs this subscription
	createTime time.Time

	acked            map[int]bool      // message indices already acknowledged
	outstanding      map[string]*lease // ackID -> leased message
	deliveryAttempts map[int]int       // per-index delivery count (dead-letter surface)
	iam              *iamPolicy

	// filter is the parsed (immutable) subscription filter. nil means no filter
	// was set, so every message is deliverable.
	filter filterExpr
}

// snapState captures a subscription's ack cursor for later seek/replay.
type snapState struct {
	topic      string
	acked      map[int]bool
	labels     map[string]string
	createTime time.Time
	expireTime time.Time
}

// Handler serves Pub/Sub v1 REST requests. Topic existence + labels are backed
// by the messagequeue driver (mq); Pub/Sub-native state (per-topic message log,
// per-subscription delivery cursors, snapshots, IAM) lives here because those
// semantics do not map onto the shared SQS-style driver.
type Handler struct {
	mq mqdriver.MessageQueue

	mu        sync.RWMutex
	topics    map[string]*topicState
	subs      map[string]*subState
	snapshots map[string]*snapState

	// pushDeliverer POSTs the push envelope to a push subscription's endpoint on
	// publish; defaulted to a real-HTTP deliverer so push works in production.
	// functionInvoker invokes any Cloud Function whose eventTrigger targets the
	// topic; nil until the server wires the Cloud Functions handler in.
	pushDeliverer   PushDeliverer
	functionInvoker FunctionInvoker

	ackCounter atomic.Uint64
}

// New returns a Pub/Sub handler backed by mq.
func New(mq mqdriver.MessageQueue) *Handler {
	return &Handler{
		mq:            mq,
		topics:        make(map[string]*topicState),
		subs:          make(map[string]*subState),
		snapshots:     make(map[string]*snapState),
		pushDeliverer: newHTTPPushDeliverer(),
	}
}

// topicLog returns the message log for a topic, creating it on first use. The
// caller holds h.mu.
func (h *Handler) topicLog(name string) *topicState {
	ts, ok := h.topics[name]
	if !ok {
		ts = &topicState{}
		h.topics[name] = ts
	}

	return ts
}

// appendMessageLocked records a published message on a topic and returns its id.
// The caller holds h.mu (publish and dead-letter routing from within deliver).
func (h *Handler) appendMessageLocked(topicName string, msg *storedMessage) string {
	ts := h.topicLog(topicName)
	msg.id = fmt.Sprintf("%d", h.ackCounter.Add(1))
	ts.messages = append(ts.messages, *msg)

	return msg.id
}

// newSub registers a subscription that starts fresh: all messages already on
// the topic are treated as consumed, so it only receives future publishes
// (matching real Pub/Sub, where a new subscription has no backlog).
func (h *Handler) newSub(name, topicShort string, cfg *subscription, filter filterExpr) {
	acked := make(map[int]bool)

	if ts, ok := h.topics[topicShort]; ok {
		for i := range ts.messages {
			acked[i] = true
		}
	}

	h.subs[name] = &subState{
		cfg:              *cfg,
		topic:            topicShort,
		createTime:       time.Now().UTC(),
		acked:            acked,
		outstanding:      make(map[string]*lease),
		deliveryAttempts: make(map[int]int),
		filter:           filter,
	}
}

// deliver selects up to maxMessages deliverable messages for a subscription,
// leasing each for ackDeadline. Expired leases are swept first so nacked or
// deadline-lapsed messages redeliver. The caller holds h.mu.
func (h *Handler) deliver(sub *subState, maxMessages int) []receivedMessage {
	ts, ok := h.topics[sub.topic]
	if !ok {
		return nil
	}

	now := time.Now().UTC()
	sweepExpired(sub, now)

	leased := leasedIndices(sub)
	ackDeadline := effectiveAckDeadline(sub)

	// maxMessages is caller-controlled; do not use it as an allocation hint (a
	// huge value would be an unbounded allocation). The append-driven slice grows
	// only as real messages are delivered, and the loop below stops at
	// maxMessages, so the result stays bounded by the actual message count.
	var out []receivedMessage

	for idx := range ts.messages {
		if len(out) >= maxMessages {
			break
		}

		if sub.acked[idx] || leased[idx] {
			continue
		}

		if msg, ok := h.tryDeliver(sub, ts, idx, now, ackDeadline); ok {
			out = append(out, msg)
		}
	}

	return out
}

// tryDeliver decides one message's fate for a subscription: filtered-out and
// dead-lettered messages are auto-acked and not returned; otherwise the message
// is leased and returned. The caller holds h.mu.
func (h *Handler) tryDeliver(
	sub *subState, ts *topicState, idx int, now time.Time, ackDeadline int,
) (receivedMessage, bool) {
	// A message the filter rejects is never delivered and never backlogged:
	// real Pub/Sub auto-acknowledges it for this subscription.
	if sub.filter != nil && !sub.filter.eval(ts.messages[idx].attributes) {
		sub.acked[idx] = true
		return receivedMessage{}, false
	}

	sub.deliveryAttempts[idx]++
	attempts := sub.deliveryAttempts[idx]

	// Once delivery attempts exceed the dead-letter policy's limit, forward the
	// message to the dead-letter topic and stop redelivering it here.
	if h.routeToDeadLetter(sub, idx, attempts) {
		sub.acked[idx] = true
		return receivedMessage{}, false
	}

	ackID := fmt.Sprintf("ack-%d", h.ackCounter.Add(1))
	sub.outstanding[ackID] = &lease{msgIdx: idx, deadline: now.Add(time.Duration(ackDeadline) * time.Second)}

	attempt := 0
	if len(sub.cfg.DeadLetterPolicy) > 0 {
		attempt = attempts
	}

	return buildReceived(ackID, &ts.messages[idx], attempt), true
}

func leasedIndices(sub *subState) map[int]bool {
	leased := make(map[int]bool, len(sub.outstanding))
	for _, l := range sub.outstanding {
		leased[l.msgIdx] = true
	}

	return leased
}

func effectiveAckDeadline(sub *subState) int {
	if sub.cfg.AckDeadlineSeconds <= 0 {
		return defaultAckDeadlineSeconds
	}

	return sub.cfg.AckDeadlineSeconds
}

// routeToDeadLetter forwards message idx to the subscription's dead-letter topic
// when its delivery attempts have exceeded the configured limit, returning true
// when the message was dead-lettered (and must not be delivered on the source).
// The caller holds h.mu.
func (h *Handler) routeToDeadLetter(sub *subState, idx, attempts int) bool {
	dlqTopic, maxAttempts, ok := parseDeadLetter(sub.cfg.DeadLetterPolicy)
	if !ok || attempts <= maxAttempts {
		return false
	}

	src, ok := h.topics[sub.topic]
	if !ok || idx >= len(src.messages) {
		return false
	}

	msg := src.messages[idx]
	h.appendMessageLocked(dlqTopic, &storedMessage{
		body:        msg.body,
		attributes:  msg.attributes,
		orderingKey: msg.orderingKey,
		publishTime: time.Now().UTC(),
	})

	return true
}

// parseDeadLetter extracts the dead-letter topic short-name and effective
// max-delivery-attempts from a subscription's deadLetterPolicy raw JSON.
func parseDeadLetter(raw json.RawMessage) (topicShort string, maxAttempts int, ok bool) {
	if len(raw) == 0 {
		return "", 0, false
	}

	var p deadLetterPolicyJSON
	if err := json.Unmarshal(raw, &p); err != nil || p.DeadLetterTopic == "" {
		return "", 0, false
	}

	maxAttempts = p.MaxDeliveryAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxDeliveryAttempts
	}

	return shortName(p.DeadLetterTopic), maxAttempts, true
}

// sweepExpired drops leases whose deadline has passed, making those messages
// deliverable again. The caller holds h.mu.
func sweepExpired(sub *subState, now time.Time) {
	for id, l := range sub.outstanding {
		if now.After(l.deadline) {
			delete(sub.outstanding, id)
		}
	}
}

func buildReceived(ackID string, msg *storedMessage, deliveryAttempt int) receivedMessage {
	return receivedMessage{
		AckID:           ackID,
		DeliveryAttempt: deliveryAttempt,
		Message: pubsubMessage{
			MessageID:   msg.id,
			Data:        encodeData(msg.body),
			Attributes:  msg.attributes,
			OrderingKey: msg.orderingKey,
			PublishTime: msg.publishTime.Format(time.RFC3339Nano),
		},
	}
}
