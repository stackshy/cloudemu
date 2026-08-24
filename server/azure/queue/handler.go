// Package queue implements the Azure Queue Storage REST+XML wire protocol as a
// server.Handler. Real azure-sdk-for-go azqueue clients configured with a
// custom service URL hit this handler the same way they hit
// {account}.queue.core.windows.net.
//
// It maps the Azure Queue REST surface onto the shared messagequeue driver:
//
//	PUT    /{queue}                              — create queue
//	DELETE /{queue}                              — delete queue
//	GET    /?comp=list                           — list queues
//	POST   /{queue}/messages                     — enqueue message
//	GET    /{queue}/messages                     — dequeue messages
//	DELETE /{queue}/messages/{messageid}?popreceipt=… — delete message
//
// Message bodies are XML <QueueMessage><MessageText>… envelopes; the SDK
// base64-encodes application payloads into MessageText, so this handler treats
// MessageText as an opaque string and round-trips it verbatim.
//
// Less-used surfaces (metadata, ACLs, message update/peek/clear, service
// properties) are not yet wired and return 501.
package queue

import (
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const (
	contentTypeXML = "application/xml"

	// xmsVersion is the Queue Storage service version we report.
	xmsVersion = "2018-03-28"

	compList     = "list"
	compMetadata = "metadata"

	// maxUpdateVisibilityTimeout is the Azure ceiling for a message's visibility
	// timeout (7 days, in seconds).
	maxUpdateVisibilityTimeout = 7 * 24 * 60 * 60

	// maxEnqueueBodyBytes caps a single enqueued message body. Azure allows up
	// to 64 KiB; we use a generous 1 MiB cap.
	maxEnqueueBodyBytes = 1 << 20
)

// Handler serves Azure Queue Storage REST requests against a messagequeue
// driver.
type Handler struct {
	mq mqdriver.MessageQueue
}

// New returns a Queue handler backed by mq.
func New(mq mqdriver.MessageQueue) *Handler {
	return &Handler{mq: mq}
}

// Matches returns true for requests that look like Azure Queue Storage calls.
// These are non-ARM data-plane URLs; the detection signals are disjoint from
// the Blob fallback:
//
//   - /{queue}/messages and /{queue}/messages/{id} — the "/messages" segment
//     is the queue data-plane marker. NOTE: this is not strictly disjoint from
//     Blob — a blob literally named "messages" (or under a "messages/" prefix)
//     has the same shape. Azure separates them only by the {account}.queue vs
//     {account}.blob hostname, invisible behind a shared endpoint. When both
//     handlers are wired, the Queue handler owns this shape; a blob named
//     "messages" is a known collision.
//   - PUT|DELETE /{queue} with no restype=container query — Blob container ops
//     always carry restype=container, so a bare PUT/DELETE on a single path
//     segment is a queue create/delete. Disjoint from Blob container ops.
//   - GET /?comp=list (list queues) — this shape is byte-for-byte identical to
//     Blob's list-containers; Azure disambiguates only by hostname. When both
//     handlers are registered, the Queue handler (registered first) owns it.
//
// The two shared-hostname shapes above are inherent to serving Queue and Blob
// on one endpoint (real Azure uses distinct hostnames); documented, not fixable
// without host-based routing.
//
// Registered before the permissive Blob fallback so these shapes win.
func (*Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/subscriptions/") {
		return false
	}

	queue, sub, msgID := parseQueuePath(r.URL.Path)
	q := r.URL.Query()

	// /{queue}/messages[/{id}] — the unambiguous queue message surface.
	if sub == "messages" {
		_ = msgID

		return true
	}

	// GET /?comp=list — list queues (see Matches doc: shares Blob's shape).
	if queue == "" {
		return r.Method == http.MethodGet && q.Get("comp") == compList && q.Get("restype") == ""
	}

	// Bare /{queue} create/delete: PUT or DELETE with no container/blob query
	// markers. Blob container ops carry restype=container.
	if sub == "" {
		switch r.Method {
		case http.MethodPut, http.MethodDelete:
			return q.Get("restype") == ""
		case http.MethodGet, http.MethodHead:
			// GET|HEAD /{queue}?comp=metadata — queue properties. Blob container
			// metadata carries restype=container, so this is unambiguous.
			return q.Get("comp") == compMetadata && q.Get("restype") == ""
		}
	}

	return false
}

// ServeHTTP routes on the parsed path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Ms-Version", xmsVersion)

	queue, sub, msgID := parseQueuePath(r.URL.Path)
	q := r.URL.Query()

	switch {
	case queue == "" && r.Method == http.MethodGet && q.Get("comp") == compList:
		h.listQueues(w, r)
	case queue == "":
		writeError(w, http.StatusNotImplemented, "NotImplemented", "operation not supported on root")
	case sub == "messages" && msgID == "":
		h.messagesOp(w, r, queue)
	case sub == "messages":
		h.messageIDOp(w, r, queue, msgID)
	case sub == "":
		h.queueOp(w, r, queue)
	default:
		writeError(w, http.StatusBadRequest, "InvalidUri", "unrecognized queue path")
	}
}

// parseQueuePath splits "/queue/messages/id" into ("queue", "messages", "id").
// For "/queue" it returns ("queue", "", ""); for "/queue/messages" it returns
// ("queue", "messages", "").
func parseQueuePath(path string) (queue, sub, msgID string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", "", ""
	}

	const maxParts = 3
	parts := strings.SplitN(path, "/", maxParts)
	queue = parts[0]

	if len(parts) > 1 {
		sub = parts[1]
	}

	if len(parts) > 2 {
		msgID = parts[2]
	}

	return queue, sub, msgID
}

func (h *Handler) queueOp(w http.ResponseWriter, r *http.Request, queue string) {
	if r.URL.Query().Get("comp") == compMetadata {
		h.metadataOp(w, r, queue)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createQueue(w, r, queue)
	case http.MethodDelete:
		h.deleteQueue(w, r, queue)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// metadataOp handles GET|PUT /{queue}?comp=metadata (queue properties + metadata).
func (h *Handler) metadataOp(w http.ResponseWriter, r *http.Request, queue string) {
	svc, ok := h.mq.(mqdriver.AzureQueueStorage)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "queue metadata is not supported by this queue driver")
		return
	}

	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		getQueueMetadata(w, r, svc, url)
	case http.MethodPut:
		setQueueMetadata(w, r, svc, url)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func getQueueMetadata(w http.ResponseWriter, r *http.Request, svc mqdriver.AzureQueueStorage, url string) {
	meta, err := svc.GetQueueMetadata(r.Context(), url)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("x-ms-approximate-messages-count", strconv.Itoa(meta.ApproximateMessageCount))

	for k, v := range meta.Metadata {
		w.Header().Set("x-ms-meta-"+k, v)
	}

	w.WriteHeader(http.StatusOK)
}

func setQueueMetadata(w http.ResponseWriter, r *http.Request, svc mqdriver.AzureQueueStorage, url string) {
	if err := svc.SetQueueMetadata(r.Context(), url, metadataFromHeaders(r.Header)); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createQueue(w http.ResponseWriter, r *http.Request, queue string) {
	_, err := h.mq.CreateQueue(r.Context(), mqdriver.QueueConfig{Name: queue})
	if err != nil {
		// Azure returns 204 No Content when the queue already exists with the
		// same metadata (idempotent create).
		if cerrors.IsAlreadyExists(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		writeErr(w, err)

		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) deleteQueue(w http.ResponseWriter, r *http.Request, queue string) {
	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.mq.DeleteQueue(r.Context(), url); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listQueues(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")

	queues, err := h.mq.ListQueues(r.Context(), prefix)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listQueuesResult{Prefix: prefix}
	for _, qi := range queues {
		out.Queues.Queues = append(out.Queues.Queues, queueXML{Name: qi.Name})
	}

	writeXML(w, http.StatusOK, out)
}

// messagesOp handles POST (enqueue) and GET (dequeue) on /{queue}/messages.
func (h *Handler) messagesOp(w http.ResponseWriter, r *http.Request, queue string) {
	switch r.Method {
	case http.MethodPost:
		h.enqueue(w, r, queue)
	case http.MethodGet:
		if r.URL.Query().Get("peekonly") == "true" {
			h.peek(w, r, queue)
			return
		}

		h.dequeue(w, r, queue)
	case http.MethodDelete:
		// DELETE /{queue}/messages with no id = clear queue.
		h.clearQueue(w, r, queue)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) enqueue(w http.ResponseWriter, r *http.Request, queue string) {
	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnqueueBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "could not read body")
		return
	}

	var req enqueueRequest
	if err := xml.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidXmlDocument", "malformed request body")
		return
	}

	visTimeout := atoiDefault(r.URL.Query().Get("visibilitytimeout"), 0)

	out, err := h.mq.SendMessage(r.Context(), mqdriver.SendMessageInput{
		QueueURL:     url,
		Body:         req.MessageText,
		DelaySeconds: visTimeout,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	now := time.Now().UTC()
	resp := messagesList{Messages: []messageXML{{
		MessageID:       out.MessageID,
		InsertionTime:   now.Format(time.RFC1123),
		ExpirationTime:  now.Add(7 * 24 * time.Hour).Format(time.RFC1123),
		PopReceipt:      out.MessageID,
		TimeNextVisible: now.Add(time.Duration(visTimeout) * time.Second).Format(time.RFC1123),
	}}}

	writeXML(w, http.StatusCreated, resp)
}

func (h *Handler) dequeue(w http.ResponseWriter, r *http.Request, queue string) {
	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	q := r.URL.Query()
	maxMsgs := atoiDefault(q.Get("numofmessages"), 1)
	visTimeout := atoiDefault(q.Get("visibilitytimeout"), 0)

	msgs, err := h.dequeueMessages(r, url, maxMsgs, visTimeout)
	if err != nil {
		writeErr(w, err)
		return
	}

	now := time.Now().UTC()
	out := messagesList{}

	for _, m := range msgs {
		out.Messages = append(out.Messages, messageXML{
			MessageID:       m.MessageID,
			InsertionTime:   now.Format(time.RFC1123),
			ExpirationTime:  now.Add(7 * 24 * time.Hour).Format(time.RFC1123),
			PopReceipt:      m.ReceiptHandle,
			TimeNextVisible: now.Add(time.Duration(visTimeout) * time.Second).Format(time.RFC1123),
			DequeueCount:    int64(m.ReceiveCount),
			MessageText:     m.Body,
		})
	}

	writeXML(w, http.StatusOK, out)
}

// dequeueMessages retrieves messages using the Azure Queue Storage surface when
// the driver exposes it (respecting the 32-message max), falling back to the
// cross-cloud receive path otherwise.
func (h *Handler) dequeueMessages(r *http.Request, url string, maxMsgs, visTimeout int) ([]mqdriver.Message, error) {
	if svc, ok := h.mq.(mqdriver.AzureQueueStorage); ok {
		return svc.DequeueMessages(r.Context(), url, maxMsgs, visTimeout)
	}

	return h.mq.ReceiveMessages(r.Context(), mqdriver.ReceiveMessageInput{
		QueueURL:          url,
		MaxMessages:       maxMsgs,
		VisibilityTimeout: visTimeout,
	})
}

// peek handles GET /{queue}/messages?peekonly=true — a non-destructive read
// that leaves message visibility unchanged and issues no pop receipts.
func (h *Handler) peek(w http.ResponseWriter, r *http.Request, queue string) {
	svc, ok := h.mq.(mqdriver.AzureQueueStorage)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "peek is not supported by this queue driver")
		return
	}

	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	maxMsgs := atoiDefault(r.URL.Query().Get("numofmessages"), 1)

	msgs, err := svc.PeekMessages(r.Context(), url, maxMsgs)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := peekMessagesList{}
	for _, m := range msgs {
		out.Messages = append(out.Messages, peekMessageXML{
			MessageID:      m.MessageID,
			InsertionTime:  m.InsertedAt.UTC().Format(time.RFC1123),
			ExpirationTime: m.ExpiresAt.UTC().Format(time.RFC1123),
			DequeueCount:   int64(m.ReceiveCount),
			MessageText:    m.Body,
		})
	}

	writeXML(w, http.StatusOK, out)
}

func (h *Handler) clearQueue(w http.ResponseWriter, r *http.Request, queue string) {
	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.mq.PurgeQueue(r.Context(), url); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// messageIDOp handles DELETE and PUT on /{queue}/messages/{messageid}.
func (h *Handler) messageIDOp(w http.ResponseWriter, r *http.Request, queue, msgID string) {
	switch r.Method {
	case http.MethodDelete:
		h.deleteMessage(w, r, queue)
	case http.MethodPut:
		h.updateMessage(w, r, queue, msgID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// deleteMessage handles DELETE /{queue}/messages/{messageid}?popreceipt=….
func (h *Handler) deleteMessage(w http.ResponseWriter, r *http.Request, queue string) {
	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	popReceipt := r.URL.Query().Get("popreceipt")
	if popReceipt == "" {
		writeError(w, http.StatusBadRequest, "InvalidQueryParameterValue", "popreceipt is required")
		return
	}

	if err := h.mq.DeleteMessage(r.Context(), url, popReceipt); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// updateMessage handles PUT /{queue}/messages/{id}?popreceipt=…&visibilitytimeout=….
// It renews visibility and optionally replaces the message body, returning a new
// pop receipt in x-ms-popreceipt (Azure Update Message).
func (h *Handler) updateMessage(w http.ResponseWriter, r *http.Request, queue, msgID string) {
	svc, ok := h.mq.(mqdriver.AzureQueueStorage)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "update message is not supported by this queue driver")
		return
	}

	url, err := h.resolveQueueURL(r, queue)
	if err != nil {
		writeErr(w, err)
		return
	}

	q := r.URL.Query()

	popReceipt := q.Get("popreceipt")
	if popReceipt == "" {
		writeError(w, http.StatusBadRequest, "InvalidQueryParameterValue", "popreceipt is required")
		return
	}

	visTimeout, ok := parseVisibilityTimeout(w, q.Get("visibilitytimeout"))
	if !ok {
		return
	}

	body, err := readMessageText(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidXmlDocument", "malformed request body")
		return
	}

	res, err := svc.UpdateMessage(r.Context(), url, msgID, popReceipt, visTimeout, body)
	if err != nil {
		// The queue was already resolved, so a NotFound here means the message
		// or its pop receipt did not match.
		if cerrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "MessageNotFound", err.Error())
			return
		}

		writeErr(w, err)

		return
	}

	w.Header().Set("x-ms-popreceipt", res.PopReceipt)
	w.Header().Set("x-ms-time-next-visible", res.TimeNextVisible.UTC().Format(time.RFC1123))
	w.WriteHeader(http.StatusNoContent)
}

// resolveQueueURL maps an Azure queue name to the driver's internal queue URL
// via ListQueues. The messagequeue driver keys queues by an opaque URL rather
// than by name, so we look the name up.
func (h *Handler) resolveQueueURL(r *http.Request, queue string) (string, error) {
	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		return "", err
	}

	for _, qi := range queues {
		if qi.Name == queue {
			return qi.URL, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "queue %q not found", queue)
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}

	if n, err := strconv.Atoi(s); err == nil {
		return n
	}

	return def
}

// parseVisibilityTimeout validates the required visibilitytimeout parameter for
// Update Message: a non-negative integer no larger than 7 days. It writes a 400
// and returns ok=false on a bad value.
func parseVisibilityTimeout(w http.ResponseWriter, raw string) (int, bool) {
	if raw == "" {
		writeError(w, http.StatusBadRequest, "InvalidQueryParameterValue", "visibilitytimeout is required")
		return 0, false
	}

	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 || v > maxUpdateVisibilityTimeout {
		writeError(w, http.StatusBadRequest, "OutOfRangeQueryParameterValue", "visibilitytimeout is out of range")
		return 0, false
	}

	return v, true
}

// readMessageText reads an optional <QueueMessage><MessageText> body, returning
// nil when the body is empty (visibility-only update).
func readMessageText(w http.ResponseWriter, r *http.Request) (*string, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnqueueBodyBytes))
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}

	var req enqueueRequest
	if err := xml.Unmarshal(data, &req); err != nil {
		return nil, err
	}

	return &req.MessageText, nil
}

// metadataFromHeaders collects x-ms-meta-{name} request headers into a map.
func metadataFromHeaders(h http.Header) map[string]string {
	const prefix = "X-Ms-Meta-"

	out := make(map[string]string)

	for k, vals := range h {
		if len(vals) == 0 || !strings.HasPrefix(k, prefix) {
			continue
		}

		out[strings.TrimPrefix(k, prefix)] = vals[0]
	}

	return out
}

func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeXML)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", contentTypeXML)
	w.Header().Set("X-Ms-Error-Code", code)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(errorXML{Code: code, Message: msg})
}

// writeErr maps CloudEmu canonical errors to Azure Queue HTTP errors.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "QueueNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "QueueAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
