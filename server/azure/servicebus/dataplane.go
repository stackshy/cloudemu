package servicebus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const (
	segMessages = "messages"
	// lockTokenParts is the [messageId, lockToken] tail of a locked-message URL.
	lockTokenParts = 2
	// subPathTail is the segment count of ".../subscriptions/{name}/messages",
	// counted from the message segment back to the "subscriptions" keyword.
	subPathTail = 2
	// dlqSuffix is the reserved sub-queue segment addressing an entity's
	// dead-letter queue (received from <entity>/$DeadLetterQueue/messages).
	dlqSuffix = "$DeadLetterQueue"
)

// isDLQSegment reports whether seg is the dead-letter sub-queue address suffix,
// matched case-insensitively ($DeadLetterQueue / $deadletterqueue).
func isDLQSegment(seg string) bool { return strings.EqualFold(seg, dlqSuffix) }

// isDataPlanePath reports whether p is a raw-HTTP Service Bus data-plane URL
// (contains a /messages segment) rather than an ARM control-plane path.
func isDataPlanePath(p string) bool {
	if strings.HasPrefix(p, "/subscriptions/") {
		return false
	}

	return strings.Contains(p, "/"+segMessages)
}

// dataPlaneTarget is a parsed data-plane URL. entity is a queue name, or a
// topic name when sub is non-empty (topic-subscription addressing).
type dataPlaneTarget struct {
	namespace string
	entity    string
	sub       string   // subscription name; "" for a queue or a topic send
	dlq       bool     // .../$DeadLetterQueue/messages addressing
	head      bool     // .../messages/head
	lock      []string // [messageId, lockToken] for .../messages/{id}/{token}
}

// messagesBase returns the "/messages" URL prefix for building the Location
// header of a peek-locked message, honoring topic-subscription and dead-letter
// sub-queue addressing.
func (t *dataPlaneTarget) messagesBase() string {
	base := "/" + t.namespace + "/" + t.entity
	if t.sub != "" {
		base += "/" + segSubs + "/" + t.sub
	}

	if t.dlq {
		base += "/" + dlqSuffix
	}

	return base + "/" + segMessages
}

func parseDataPlanePath(p string) (dataPlaneTarget, bool) {
	segs := strings.Split(strings.Trim(p, "/"), "/")

	mi := -1

	for i, s := range segs {
		if s == segMessages {
			mi = i
			break
		}
	}

	if mi < 1 {
		return dataPlaneTarget{}, false
	}

	tgt := parseEntityScope(segs, mi)

	switch after := segs[mi+1:]; {
	case len(after) == 0:
	case len(after) == 1 && after[0] == "head":
		tgt.head = true
	case len(after) == lockTokenParts:
		tgt.lock = after
	default:
		return dataPlaneTarget{}, false
	}

	return tgt, true
}

// parseEntityScope resolves namespace/entity/subscription from the segments
// that precede the /messages segment at index mi. A ".../{topic}/subscriptions/
// {sub}/messages" shape addresses a topic subscription; anything else is a flat
// queue (or a topic send). A trailing "$DeadLetterQueue" segment before
// /messages is stripped into the dlq flag, leaving the entity address behind it.
func parseEntityScope(segs []string, mi int) dataPlaneTarget {
	// end is the virtual /messages index once a $DeadLetterQueue suffix, if any,
	// is peeled off the entity address.
	end := mi

	dlq := mi >= 1 && isDLQSegment(segs[mi-1])
	if dlq {
		end = mi - 1
	}

	// A bare "$DeadLetterQueue/messages" leaves no entity segment; report it as
	// empty so serveDataPlane rejects the path.
	if end < 1 {
		return dataPlaneTarget{dlq: dlq}
	}

	var tgt dataPlaneTarget

	switch {
	case end > subPathTail && strings.EqualFold(segs[end-subPathTail], segSubs):
		tgt = dataPlaneTarget{
			namespace: segs[end-subPathTail-2],
			entity:    segs[end-subPathTail-1],
			sub:       segs[end-1],
		}
	default:
		tgt = dataPlaneTarget{entity: segs[end-1]}
		if end >= lockTokenParts {
			tgt.namespace = segs[end-lockTokenParts]
		}
	}

	tgt.dlq = dlq

	return tgt
}

func (h *Handler) serveDataPlane(w http.ResponseWriter, r *http.Request) {
	tgt, ok := parseDataPlanePath(r.URL.Path)
	if !ok || tgt.entity == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed data-plane path")
		return
	}

	if tgt.sub != "" {
		h.serveSubscriptionData(w, r, &tgt)
		return
	}

	h.serveEntityData(w, r, &tgt)
}

// serveEntityData routes a flat "/{entity}/messages" request. The entity is a
// queue when it exists as one; otherwise a topic (send fans out to every
// subscription). Receiving directly from a topic is not permitted.
func (h *Handler) serveEntityData(w http.ResponseWriter, r *http.Request, tgt *dataPlaneTarget) {
	if cfg, ok := h.resolveQueue(tgt.namespace, tgt.entity); ok {
		h.routeQueueOps(w, r, &cfg, tgt)
		return
	}

	targets, ok := h.resolveTopicSubURLs(tgt.namespace, tgt.entity)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "entity not found: "+tgt.entity)
		return
	}

	if tgt.head || len(tgt.lock) == lockTokenParts {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidOperation",
			"cannot receive directly from a topic; address a topic subscription")

		return
	}

	h.dataPlaneTopicSend(w, r, targets)
}

// serveSubscriptionData routes a "/{topic}/subscriptions/{sub}/messages"
// request. Only receive/peek-lock/complete/abandon/renew-lock (and the same on
// the $DeadLetterQueue sub-queue) are valid; sending is done against the parent
// topic.
func (h *Handler) serveSubscriptionData(w http.ResponseWriter, r *http.Request, tgt *dataPlaneTarget) {
	cfg, ok := h.resolveSubscription(tgt.namespace, tgt.entity, tgt.sub)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found: "+tgt.sub)
		return
	}

	if tgt.dlq {
		h.serveDeadLetter(w, r, cfg.dlqURL, cfg.lockSecs, tgt)
		return
	}

	switch {
	case len(tgt.lock) == lockTokenParts:
		h.serveLockedMessage(w, r, cfg.url, tgt.lock[1], cfg.lockSecs)
	case tgt.head:
		h.serveHead(w, r, cfg.url, tgt)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidOperation",
			"cannot send to a subscription; send to the parent topic")
	}
}

// routeQueueOps dispatches the queue send/receive/complete/abandon/renew-lock
// verbs, routing $DeadLetterQueue-addressed requests to the DLQ store.
func (h *Handler) routeQueueOps(w http.ResponseWriter, r *http.Request, cfg *queueDataPlane, tgt *dataPlaneTarget) {
	if tgt.dlq {
		h.serveDeadLetter(w, r, cfg.dlqURL, cfg.lockSecs, tgt)
		return
	}

	switch {
	case len(tgt.lock) == lockTokenParts:
		h.serveLockedMessage(w, r, cfg.url, tgt.lock[1], cfg.lockSecs)
	case tgt.head:
		h.serveHead(w, r, cfg.url, tgt)
	default:
		h.dataPlaneSend(w, r, cfg.url, cfg.ttlSecs)
	}
}

// serveDeadLetter handles receive/complete/abandon/renew-lock against an
// entity's dead-letter sub-queue. Sending to the DLQ is rejected.
func (h *Handler) serveDeadLetter(w http.ResponseWriter, r *http.Request, dlqURL string, lockSecs int, tgt *dataPlaneTarget) {
	if dlqURL == "" {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "dead-letter queue not found")
		return
	}

	switch {
	case len(tgt.lock) == lockTokenParts:
		h.serveLockedMessage(w, r, dlqURL, tgt.lock[1], lockSecs)
	case tgt.head:
		h.serveHead(w, r, dlqURL, tgt)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidOperation",
			"cannot send to the dead-letter sub-queue")
	}
}

// dataNamespaces returns the namespace(s) to search for a data-plane entity.
// With an explicit namespace it returns just that one (case-insensitively) when
// present; without one, it returns every namespace so a bare entity name still
// resolves. Callers must hold h.mu.
func (h *Handler) dataNamespaces(namespace string) []*namespaceState {
	if namespace != "" {
		if ns, ok := h.namespaces.Get(nsKey(namespace)); ok {
			return []*namespaceState{ns}
		}

		return nil
	}

	return h.namespaces.SortedValues()
}

// queueDataPlane is a queue's resolved data-plane configuration: its backing
// store URL, its paired dead-letter store URL, the default message TTL applied
// to sends, and the peek-lock duration used for locks/renew-lock.
type queueDataPlane struct {
	url      string
	dlqURL   string
	ttlSecs  int
	lockSecs int
}

// resolveQueue resolves a namespace/queue pair's data-plane configuration.
func (h *Handler) resolveQueue(namespace, queue string) (queueDataPlane, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ns := range h.dataNamespaces(namespace) {
		if rec, ok := ns.Queues[queue]; ok {
			return queueDataPlane{
				url:      rec.DriverURL,
				dlqURL:   rec.DLQURL,
				ttlSecs:  ttlSecondsFromISO(rec.Props.DefaultMessageTimeToLive),
				lockSecs: lockDurationSeconds(rec.Props.LockDuration),
			}, true
		}
	}

	return queueDataPlane{}, false
}

// subDataPlane is a subscription's resolved data-plane configuration.
type subDataPlane struct {
	url      string
	dlqURL   string
	lockSecs int
}

// resolveSubscription resolves a topic subscription's data-plane configuration.
func (h *Handler) resolveSubscription(namespace, topic, sub string) (subDataPlane, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ns := range h.dataNamespaces(namespace) {
		t, ok := ns.Topics[topic]
		if !ok {
			continue
		}

		s, ok := t.Subs[sub]
		if !ok {
			return subDataPlane{}, false
		}

		return subDataPlane{
			url:      s.DriverURL,
			dlqURL:   s.DLQURL,
			lockSecs: lockDurationSeconds(s.Props.LockDuration),
		}, true
	}

	return subDataPlane{}, false
}

// topicSubTarget is one subscription fan-out target: its backing queue URL, its
// default message TTL, plus a snapshot of its rules' filter properties,
// evaluated against each published message to decide whether that subscription
// receives it.
type topicSubTarget struct {
	url     string
	ttlSecs int
	rules   []ruleProperties
}

// resolveTopicSubURLs returns the fan-out targets of every subscription on a
// topic, and whether the topic exists.
func (h *Handler) resolveTopicSubURLs(namespace, topic string) ([]topicSubTarget, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ns := range h.dataNamespaces(namespace) {
		t, ok := ns.Topics[topic]
		if !ok {
			continue
		}

		targets := make([]topicSubTarget, 0, len(t.Subs))

		for _, n := range sortedKeys(t.Subs) {
			sub := t.Subs[n]

			rules := make([]ruleProperties, 0, len(sub.Rules))
			for _, rn := range sortedKeys(sub.Rules) {
				rules = append(rules, sub.Rules[rn].Props)
			}

			targets = append(targets, topicSubTarget{
				url:     sub.DriverURL,
				ttlSecs: ttlSecondsFromISO(sub.Props.DefaultMessageTimeToLive),
				rules:   rules,
			})
		}

		return targets, true
	}

	return nil, false
}

func (h *Handler) serveHead(w http.ResponseWriter, r *http.Request, url string, tgt *dataPlaneTarget) {
	switch r.Method {
	case http.MethodDelete:
		h.dataPlaneReceiveDelete(w, r, url)
	case http.MethodPost:
		h.dataPlanePeekLock(w, r, url, tgt)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"messages/head accepts DELETE (receive-and-delete) or POST (peek-lock)")
	}
}

// serveLockedMessage handles complete (DELETE), abandon (PUT) and renew-lock
// (POST) on a locked message addressed by .../messages/{messageId}/{lockToken}.
// Renew re-arms the visibility window by the entity's configured lock duration.
func (h *Handler) serveLockedMessage(w http.ResponseWriter, r *http.Request, url, lockToken string, lockSecs int) {
	switch r.Method {
	case http.MethodDelete:
		if err := h.mq.DeleteMessage(r.Context(), url, lockToken); err != nil {
			writeLockError(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	case http.MethodPut:
		if err := h.mq.ChangeVisibility(r.Context(), url, lockToken, 0); err != nil {
			writeLockError(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	case http.MethodPost:
		if err := h.mq.ChangeVisibility(r.Context(), url, lockToken, lockSecs); err != nil {
			writeLockError(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"locked message accepts DELETE (complete), PUT (abandon) or POST (renew-lock)")
	}
}

func (h *Handler) dataPlaneSend(w http.ResponseWriter, r *http.Request, url string, ttlSecs int) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "send requires POST")
		return
	}

	body, ok := readSendBody(w, r)
	if !ok {
		return
	}

	bp := parseWireBrokerProps(r)

	if _, err := h.mq.SendMessage(r.Context(), bp.sendInput(url, body, ttlSecs)); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// dataPlaneTopicSend fans a published message out to every subscription whose
// rules it matches (each subscription's filter-only rules are OR'd, mirroring
// real Service Bus). A topic with no subscriptions, or with none whose rules
// match, still returns 201 (the message is accepted and dropped), matching
// Azure. Each matching subscription persists the sender's system properties and
// applies its own default message TTL. Both the well-known system properties
// and the sender's custom application-property headers are evaluated in
// filters.
func (h *Handler) dataPlaneTopicSend(w http.ResponseWriter, r *http.Request, targets []topicSubTarget) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "send requires POST")
		return
	}

	bp := parseWireBrokerProps(r)
	filter := bp.filterProps(customHeaderProps(r))

	body, ok := readSendBody(w, r)
	if !ok {
		return
	}

	for _, tgt := range targets {
		if !rulesMatch(tgt.rules, &filter) {
			continue
		}

		if _, err := h.mq.SendMessage(r.Context(), bp.sendInput(tgt.url, body, tgt.ttlSecs)); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
}

// wireBrokerProps is the sender-supplied BrokerProperties header of the Service
// Bus REST "Send Message" API: brokered-message system properties plus the
// TimeToLive and ScheduledEnqueueTimeUtc that control expiry and scheduling.
// https://learn.microsoft.com/rest/api/servicebus/send-message-to-queue
type wireBrokerProps struct {
	MessageID               string  `json:"MessageId,omitempty"`
	CorrelationID           string  `json:"CorrelationId,omitempty"`
	SessionID               string  `json:"SessionId,omitempty"`
	Label                   string  `json:"Label,omitempty"`
	ReplyTo                 string  `json:"ReplyTo,omitempty"`
	To                      string  `json:"To,omitempty"`
	ReplyToSessionID        string  `json:"ReplyToSessionId,omitempty"`
	ContentType             string  `json:"ContentType,omitempty"`
	TimeToLive              float64 `json:"TimeToLive,omitempty"`
	ScheduledEnqueueTimeUtc string  `json:"ScheduledEnqueueTimeUtc,omitempty"`
}

// parseWireBrokerProps reads the sender-supplied BrokerProperties header off a
// data-plane send request. A missing or malformed header yields the zero value.
func parseWireBrokerProps(r *http.Request) wireBrokerProps {
	var bp wireBrokerProps

	if h := r.Header.Get("BrokerProperties"); h != "" {
		_ = json.Unmarshal([]byte(h), &bp)
	}

	return bp
}

// filterProps projects the well-known system properties, plus the supplied
// custom application properties, a subscription rule filter evaluates against.
func (b *wireBrokerProps) filterProps(custom map[string]string) messageProps {
	return messageProps{
		MessageID:        b.MessageID,
		CorrelationID:    b.CorrelationID,
		Label:            b.Label,
		To:               b.To,
		ReplyTo:          b.ReplyTo,
		SessionID:        b.SessionID,
		ReplyToSessionID: b.ReplyToSessionID,
		ContentType:      b.ContentType,
		Custom:           custom,
	}
}

// customHeaderProps extracts the sender's custom application properties from a
// data-plane send request. Per the Service Bus REST protocol, user properties
// travel as individual HTTP headers alongside the system-property
// BrokerProperties header; reserved protocol headers and the literal value
// "null" are dropped. https://learn.microsoft.com/rest/api/servicebus/send-message-to-queue
func customHeaderProps(r *http.Request) map[string]string {
	out := map[string]string{}

	for name, vals := range r.Header {
		if len(vals) == 0 || isReservedHeader(name) {
			continue
		}

		v := vals[0]
		if v == "" || strings.EqualFold(v, "null") {
			continue
		}

		out[name] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// isReservedHeader reports whether a header name is a Service Bus protocol
// header or a standard HTTP header that must not be surfaced as a custom
// application property. Matched case-insensitively.
func isReservedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "content-type", "content-length", "content-encoding",
		"content-md5", "brokerproperties", "x-ms-retrypolicy", "host", "user-agent",
		"accept", "accept-encoding", "accept-charset", "accept-language", "connection",
		"date", "expect", "transfer-encoding", "cookie", "cache-control", "pragma",
		"referer", "origin", "range", "te", "trailer", "upgrade", "via", "warning",
		"keep-alive", "server", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto":
		return true
	default:
		return false
	}
}

// systemProperties returns the non-empty brokered-message system properties to
// persist and echo back to the receiver, or nil when none were supplied.
func (b *wireBrokerProps) systemProperties() map[string]string {
	m := map[string]string{}

	for k, v := range map[string]string{
		"MessageId":        b.MessageID,
		"CorrelationId":    b.CorrelationID,
		"SessionId":        b.SessionID,
		"Label":            b.Label,
		"ReplyTo":          b.ReplyTo,
		"To":               b.To,
		"ReplyToSessionId": b.ReplyToSessionID,
		"ContentType":      b.ContentType,
	} {
		if v != "" {
			m[k] = v
		}
	}

	if len(m) == 0 {
		return nil
	}

	return m
}

// sendInput builds the driver send input for a message: it persists the
// sender's system properties, resolves the effective TTL (a per-message
// TimeToLive overrides the entity default), and converts ScheduledEnqueueTimeUtc
// into a delivery delay.
func (b *wireBrokerProps) sendInput(url, body string, defaultTTLSecs int) mqdriver.SendMessageInput {
	in := mqdriver.SendMessageInput{
		QueueURL:         url,
		Body:             body,
		SystemProperties: b.systemProperties(),
	}

	ttl := defaultTTLSecs
	if b.TimeToLive > 0 {
		ttl = int(b.TimeToLive)
	}

	if ttl > 0 {
		in.MessageTTLSeconds = &ttl
	}

	if b.ScheduledEnqueueTimeUtc != "" {
		if t, err := time.Parse(time.RFC3339, b.ScheduledEnqueueTimeUtc); err == nil {
			if delay := int(time.Until(t).Seconds()); delay > 0 {
				in.DelaySeconds = delay
			}
		}
	}

	return in
}

// readSendBody reads a size-capped request body, writing an error on failure.
func readSendBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		azurearm.WriteError(w, http.StatusRequestEntityTooLarge, "PayloadTooLarge", err.Error())
		return "", false
	}

	return string(body), true
}

func (h *Handler) dataPlaneReceiveDelete(w http.ResponseWriter, r *http.Request, url string) {
	msgs, err := h.mq.ReceiveMessages(r.Context(), mqdriver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if len(msgs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.mq.DeleteMessage(r.Context(), url, msgs[0].ReceiptHandle); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeMessage(w, &msgs[0], http.StatusOK)
}

func (h *Handler) dataPlanePeekLock(w http.ResponseWriter, r *http.Request, url string, tgt *dataPlaneTarget) {
	msgs, err := h.mq.ReceiveMessages(r.Context(), mqdriver.ReceiveMessageInput{QueueURL: url, MaxMessages: 1})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if len(msgs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	msg := msgs[0]
	base := tgt.messagesBase()
	w.Header().Set("Location", base+"/"+msg.MessageID+"/"+msg.ReceiptHandle)
	w.Header().Set("BrokerProperties", brokerPropertiesHeader(&msg, msg.ReceiptHandle))
	writeMessage(w, &msg, http.StatusCreated)
}

// brokerPropertiesHeader builds the BrokerProperties response header from a
// received message: the sender's preserved system properties, the effective
// MessageId (the sender's when supplied, else the server id), and the LockToken
// for a peek-locked message.
func brokerPropertiesHeader(msg *mqdriver.Message, lockToken string) string {
	props := make(map[string]string, len(msg.SystemProperties)+lockTokenParts)
	for k, v := range msg.SystemProperties {
		props[k] = v
	}

	if props["MessageId"] == "" {
		props["MessageId"] = msg.MessageID
	}

	if lockToken != "" {
		props["LockToken"] = lockToken
	}

	b, _ := json.Marshal(props)

	return string(b)
}

func writeMessage(w http.ResponseWriter, msg *mqdriver.Message, status int) {
	if h := w.Header().Get("BrokerProperties"); h == "" {
		w.Header().Set("BrokerProperties", brokerPropertiesHeader(msg, ""))
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg.Body))
}

func writeLockError(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		// A lock that no longer exists maps to Gone in the Service Bus REST plane.
		azurearm.WriteError(w, http.StatusGone, "MessageLockLost", err.Error())
		return
	}

	azurearm.WriteCErr(w, err)
}

// deleteBackingQueue removes a subscription/queue's message store, ignoring a
// missing store.
func (h *Handler) deleteBackingQueue(url string) {
	if url == "" {
		return
	}

	_ = h.mq.DeleteQueue(context.Background(), url)
}
