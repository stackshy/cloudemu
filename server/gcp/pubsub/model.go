package pubsub

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const defaultAckDeadlineSeconds = 10

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

	ackCounter atomic.Uint64
}

// New returns a Pub/Sub handler backed by mq.
func New(mq mqdriver.MessageQueue) *Handler {
	return &Handler{
		mq:        mq,
		topics:    make(map[string]*topicState),
		subs:      make(map[string]*subState),
		snapshots: make(map[string]*snapState),
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

// appendMessage records a published message on a topic and returns its id.
func (h *Handler) appendMessage(topicName string, msg *storedMessage) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	ts := h.topicLog(topicName)
	msg.id = fmt.Sprintf("%d", h.ackCounter.Add(1))
	ts.messages = append(ts.messages, *msg)

	return msg.id
}

// newSub registers a subscription that starts fresh: all messages already on
// the topic are treated as consumed, so it only receives future publishes
// (matching real Pub/Sub, where a new subscription has no backlog).
func (h *Handler) newSub(name, topicShort string, cfg *subscription) {
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

	leased := make(map[int]bool, len(sub.outstanding))
	for _, l := range sub.outstanding {
		leased[l.msgIdx] = true
	}

	ackDeadline := sub.cfg.AckDeadlineSeconds
	if ackDeadline <= 0 {
		ackDeadline = defaultAckDeadlineSeconds
	}

	out := make([]receivedMessage, 0, maxMessages)

	for idx := range ts.messages {
		if len(out) >= maxMessages {
			break
		}

		if sub.acked[idx] || leased[idx] {
			continue
		}

		ackID := fmt.Sprintf("ack-%d", h.ackCounter.Add(1))
		sub.outstanding[ackID] = &lease{msgIdx: idx, deadline: now.Add(time.Duration(ackDeadline) * time.Second)}
		sub.deliveryAttempts[idx]++

		attempt := 0
		if len(sub.cfg.DeadLetterPolicy) > 0 {
			attempt = sub.deliveryAttempts[idx]
		}

		out = append(out, buildReceived(ackID, &ts.messages[idx], attempt))
	}

	return out
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
