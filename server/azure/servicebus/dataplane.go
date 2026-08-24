package servicebus

import (
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
)

// isDataPlanePath reports whether p is a raw-HTTP Service Bus data-plane URL
// (contains a /messages segment) rather than an ARM control-plane path.
func isDataPlanePath(p string) bool {
	if strings.HasPrefix(p, "/subscriptions/") {
		return false
	}

	return strings.Contains(p, "/"+segMessages)
}

// dataPlaneTarget is a parsed data-plane URL.
type dataPlaneTarget struct {
	namespace string
	queue     string
	head      bool     // .../messages/head
	lock      []string // [messageId, lockToken] for .../messages/{id}/{token}
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

	tgt := dataPlaneTarget{queue: segs[mi-1]}
	if mi >= lockTokenParts {
		tgt.namespace = segs[mi-lockTokenParts]
	}

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

func (h *Handler) serveDataPlane(w http.ResponseWriter, r *http.Request) {
	tgt, ok := parseDataPlanePath(r.URL.Path)
	if !ok || tgt.queue == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed data-plane path")
		return
	}

	url, ok := h.resolveQueueURL(tgt.namespace, tgt.queue)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "queue not found: "+tgt.queue)
		return
	}

	switch {
	case len(tgt.lock) == lockTokenParts:
		h.serveLockedMessage(w, r, url, tgt.lock[1])
	case tgt.head:
		h.serveHead(w, r, url, tgt)
	default:
		h.dataPlaneSend(w, r, url)
	}
}

// resolveQueueURL finds the driver URL for a namespace/queue pair. When the
// namespace segment is absent, it falls back to the first matching queue name.
func (h *Handler) resolveQueueURL(namespace, queue string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if namespace != "" {
		ns, ok := h.namespaces.Get(namespace)
		if !ok {
			return "", false
		}

		rec, ok := ns.Queues[queue]
		if !ok {
			return "", false
		}

		return rec.DriverURL, true
	}

	for _, ns := range h.namespaces.SortedValues() {
		if rec, ok := ns.Queues[queue]; ok {
			return rec.DriverURL, true
		}
	}

	return "", false
}

func (h *Handler) serveHead(w http.ResponseWriter, r *http.Request, url string, tgt dataPlaneTarget) {
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

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		azurearm.WriteError(w, http.StatusRequestEntityTooLarge, "PayloadTooLarge", err.Error())
		return
	}

	if _, err := h.mq.SendMessage(r.Context(), mqdriver.SendMessageInput{
		QueueURL: url, Body: string(body),
	}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
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

func (h *Handler) dataPlanePeekLock(w http.ResponseWriter, r *http.Request, url string, tgt dataPlaneTarget) {
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
	base := "/" + tgt.namespace + "/" + tgt.queue + "/" + segMessages
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
