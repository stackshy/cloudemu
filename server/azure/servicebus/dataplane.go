package servicebus

import (
	"context"
	"io"
	"net/http"
	"strings"

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
)

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
	head      bool     // .../messages/head
	lock      []string // [messageId, lockToken] for .../messages/{id}/{token}
}

// messagesBase returns the "/messages" URL prefix for building the Location
// header of a peek-locked message, honoring topic-subscription addressing.
func (t *dataPlaneTarget) messagesBase() string {
	if t.sub != "" {
		return "/" + t.namespace + "/" + t.entity + "/" + segSubs + "/" + t.sub + "/" + segMessages
	}

	return "/" + t.namespace + "/" + t.entity + "/" + segMessages
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
// queue (or a topic send).
func parseEntityScope(segs []string, mi int) dataPlaneTarget {
	if mi > subPathTail && strings.EqualFold(segs[mi-subPathTail], segSubs) {
		return dataPlaneTarget{
			namespace: segs[mi-subPathTail-2],
			entity:    segs[mi-subPathTail-1],
			sub:       segs[mi-1],
		}
	}

	tgt := dataPlaneTarget{entity: segs[mi-1]}
	if mi >= lockTokenParts {
		tgt.namespace = segs[mi-lockTokenParts]
	}

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
	if url, ok := h.resolveQueueURL(tgt.namespace, tgt.entity); ok {
		h.routeQueueOps(w, r, url, tgt)
		return
	}

	subURLs, ok := h.resolveTopicSubURLs(tgt.namespace, tgt.entity)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "entity not found: "+tgt.entity)
		return
	}

	if tgt.head || len(tgt.lock) == lockTokenParts {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidOperation",
			"cannot receive directly from a topic; address a topic subscription")

		return
	}

	h.dataPlaneTopicSend(w, r, subURLs)
}

// serveSubscriptionData routes a "/{topic}/subscriptions/{sub}/messages"
// request. Only receive/peek-lock/complete/abandon are valid; sending is done
// against the parent topic.
func (h *Handler) serveSubscriptionData(w http.ResponseWriter, r *http.Request, tgt *dataPlaneTarget) {
	url, ok := h.resolveSubURL(tgt.namespace, tgt.entity, tgt.sub)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found: "+tgt.sub)
		return
	}

	switch {
	case len(tgt.lock) == lockTokenParts:
		h.serveLockedMessage(w, r, url, tgt.lock[1])
	case tgt.head:
		h.serveHead(w, r, url, tgt)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidOperation",
			"cannot send to a subscription; send to the parent topic")
	}
}

// routeQueueOps dispatches the queue send/receive/complete/abandon verbs.
func (h *Handler) routeQueueOps(w http.ResponseWriter, r *http.Request, url string, tgt *dataPlaneTarget) {
	switch {
	case len(tgt.lock) == lockTokenParts:
		h.serveLockedMessage(w, r, url, tgt.lock[1])
	case tgt.head:
		h.serveHead(w, r, url, tgt)
	default:
		h.dataPlaneSend(w, r, url)
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

// resolveQueueURL finds the driver URL for a namespace/queue pair.
func (h *Handler) resolveQueueURL(namespace, queue string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ns := range h.dataNamespaces(namespace) {
		if rec, ok := ns.Queues[queue]; ok {
			return rec.DriverURL, true
		}
	}

	return "", false
}

// resolveTopicSubURLs returns the backing driver URLs of every subscription on
// a topic (the fan-out targets for a topic publish), and whether the topic
// exists.
func (h *Handler) resolveTopicSubURLs(namespace, topic string) ([]string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ns := range h.dataNamespaces(namespace) {
		t, ok := ns.Topics[topic]
		if !ok {
			continue
		}

		urls := make([]string, 0, len(t.Subs))
		for _, n := range sortedKeys(t.Subs) {
			urls = append(urls, t.Subs[n].DriverURL)
		}

		return urls, true
	}

	return nil, false
}

// resolveSubURL finds the backing driver URL of a topic subscription.
func (h *Handler) resolveSubURL(namespace, topic, sub string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ns := range h.dataNamespaces(namespace) {
		t, ok := ns.Topics[topic]
		if !ok {
			continue
		}

		if s, ok := t.Subs[sub]; ok {
			return s.DriverURL, true
		}

		return "", false
	}

	return "", false
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

// serveLockedMessage handles complete (DELETE) and abandon (PUT) on a locked
// message addressed by .../messages/{messageId}/{lockToken}.
func (h *Handler) serveLockedMessage(w http.ResponseWriter, r *http.Request, url, lockToken string) {
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
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"locked message accepts DELETE (complete) or PUT (abandon)")
	}
}

func (h *Handler) dataPlaneSend(w http.ResponseWriter, r *http.Request, url string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "send requires POST")
		return
	}

	body, ok := readSendBody(w, r)
	if !ok {
		return
	}

	if _, err := h.mq.SendMessage(r.Context(), mqdriver.SendMessageInput{
		QueueURL: url, Body: body,
	}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// dataPlaneTopicSend fans a published message out to every subscription's
// backing queue. A topic with no subscriptions still returns 201 (the message
// is accepted and dropped), matching Azure.
func (h *Handler) dataPlaneTopicSend(w http.ResponseWriter, r *http.Request, subURLs []string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "send requires POST")
		return
	}

	body, ok := readSendBody(w, r)
	if !ok {
		return
	}

	for _, url := range subURLs {
		if _, err := h.mq.SendMessage(r.Context(), mqdriver.SendMessageInput{
			QueueURL: url, Body: body,
		}); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
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
	w.Header().Set("BrokerProperties",
		`{"MessageId":"`+msg.MessageID+`","LockToken":"`+msg.ReceiptHandle+`"}`)
	writeMessage(w, &msg, http.StatusCreated)
}

func writeMessage(w http.ResponseWriter, msg *mqdriver.Message, status int) {
	if h := w.Header().Get("BrokerProperties"); h == "" {
		w.Header().Set("BrokerProperties", `{"MessageId":"`+msg.MessageID+`"}`)
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
