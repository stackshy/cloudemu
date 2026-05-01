// Package servicebus serves Azure Service Bus ARM control-plane requests
// (Microsoft.ServiceBus/namespaces[/queues]) plus a raw-HTTP data plane for
// send/receive against a CloudEmu messagequeue driver.
//
// Real azure-sdk-for-go armservicebus clients drive the ARM control plane
// (PUT/GET/DELETE namespaces and queues). The data-plane azservicebus SDK
// uses AMQP exclusively and is out of scope; tests that exercise send/receive
// hit the REST data plane (POST /{namespace}/{queue}/messages,
// DELETE /{namespace}/{queue}/messages/head) directly with raw HTTP. This
// matches Microsoft's older "Send/Receive REST" endpoints documented at
// https://learn.microsoft.com/rest/api/servicebus/.
//
// MVP coverage:
//
//	PUT/GET/DELETE  .../namespaces/{ns}                        — namespace lifecycle
//	GET             .../namespaces                             — list (subscription scope)
//	PUT/GET/DELETE  .../namespaces/{ns}/queues/{name}          — queue lifecycle
//	GET             .../namespaces/{ns}/queues                 — list queues in namespace
//	POST            /{namespace}/{queue}/messages              — send (raw HTTP)
//	DELETE          /{namespace}/{queue}/messages/head         — receive+ack (raw HTTP)
//
// Topics, subscriptions, rules, AMQP, sessions, and DLQ wire formats are
// deferred to a follow-up.
package servicebus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName = "Microsoft.ServiceBus"
	resourceType = "namespaces"
	subTypeQueue = "queues"

	maxBodyBytes  = 1 << 20
	dataPlanePath = "/messages"
)

// Handler serves ARM Service Bus + raw-HTTP data-plane requests.
type Handler struct {
	mq mqdriver.MessageQueue
}

// New returns a Service Bus handler backed by mq.
func New(mq mqdriver.MessageQueue) *Handler {
	return &Handler{mq: mq}
}

// Matches accepts ARM Microsoft.ServiceBus/namespaces[...] paths plus
// data-plane URLs ending in /messages or /messages/head.
func (*Handler) Matches(r *http.Request) bool {
	if isDataPlanePath(r.URL.Path) {
		return true
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

// ServeHTTP routes by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isDataPlanePath(r.URL.Path) {
		h.serveDataPlane(w, r)
		return
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// /providers/Microsoft.ServiceBus/namespaces (subscription-level list)
	if rp.ResourceName == "" {
		h.listNamespaces(w, r)
		return
	}

	// /namespaces/{ns}/queues[/{name}]
	if rp.SubResource == subTypeQueue {
		h.serveQueue(w, r, rp)
		return
	}

	if rp.SubResource != "" {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"unsupported sub-resource: "+rp.SubResource)
		return
	}

	h.serveNamespace(w, r, rp)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveNamespace(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createNamespace(w, r, rp)
	case http.MethodGet:
		h.getNamespace(w, r, rp)
	case http.MethodDelete:
		h.deleteNamespace(w, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveQueue(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		// Collection: list queues in the namespace.
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		h.listQueues(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createQueue(w, r, rp)
	case http.MethodGet:
		h.getQueue(w, r, rp)
	case http.MethodDelete:
		h.deleteQueue(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// ---------- Namespace control plane ----------

//nolint:gocritic // rp is a request-scoped value
func (*Handler) createNamespace(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req createNamespaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	location := req.Location
	if location == "" {
		location = "eastus"
	}

	azurearm.WriteJSON(w, http.StatusOK, namespaceResource{
		ID: azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup,
			providerName, resourceType, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + resourceType,
		Location: location,
		Properties: namespaceProperties{
			ProvisioningState:  "Succeeded",
			ServiceBusEndpoint: "https://" + rp.ResourceName + ".servicebus.windows.net:443/",
		},
		SKU: req.SKU,
	})
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getNamespace(w http.ResponseWriter, _ *http.Request, rp azurearm.ResourcePath) {
	// Namespaces are virtual — there's no driver state. Always return Succeeded.
	azurearm.WriteJSON(w, http.StatusOK, namespaceResource{
		ID: azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup,
			providerName, resourceType, rp.ResourceName),
		Name:     rp.ResourceName,
		Type:     providerName + "/" + resourceType,
		Location: "eastus",
		Properties: namespaceProperties{
			ProvisioningState:  "Succeeded",
			ServiceBusEndpoint: "https://" + rp.ResourceName + ".servicebus.windows.net:443/",
		},
	})
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deleteNamespace(w http.ResponseWriter, _ azurearm.ResourcePath) {
	// Virtual namespace: nothing to free.
	w.WriteHeader(http.StatusOK)
}

func (*Handler) listNamespaces(w http.ResponseWriter, _ *http.Request) {
	// We don't track namespaces in the driver; return an empty list.
	azurearm.WriteJSON(w, http.StatusOK, listResponse{Value: []any{}})
}

// ---------- Queue control plane ----------

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createQueue(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req createQueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	cfg := mqdriver.QueueConfig{
		Name: rp.SubResourceName,
		FIFO: req.Properties.RequiresSession || strings.HasSuffix(rp.SubResourceName, ".fifo"),
	}

	info, err := h.mq.CreateQueue(r.Context(), cfg)
	if err != nil && !cerrors.IsAlreadyExists(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	if info == nil {
		// Idempotent PUT: re-read the queue.
		queues, _ := h.mq.ListQueues(r.Context(), "")

		for i := range queues {
			if queues[i].Name == rp.SubResourceName {
				info = &queues[i]
				break
			}
		}
	}

	if info == nil {
		azurearm.WriteError(w, http.StatusInternalServerError, "InternalError", "queue lookup failed")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toQueueResource(rp, info))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getQueue(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	for i := range queues {
		if queues[i].Name == rp.SubResourceName {
			azurearm.WriteJSON(w, http.StatusOK, toQueueResource(rp, &queues[i]))
			return
		}
	}

	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "queue not found")
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listQueues(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listResponse{Value: make([]any, 0, len(queues))}
	for i := range queues {
		out.Value = append(out.Value, toQueueResource(rp, &queues[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteQueue(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var url string

	for i := range queues {
		if queues[i].Name == rp.SubResourceName {
			url = queues[i].URL
			break
		}
	}

	if url == "" {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "queue not found")
		return
	}

	if err := h.mq.DeleteQueue(r.Context(), url); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// ---------- Data plane (raw HTTP send/receive) ----------

func isDataPlanePath(p string) bool {
	if !strings.HasSuffix(p, dataPlanePath) && !strings.HasSuffix(p, dataPlanePath+"/head") {
		return false
	}
	// The path must NOT be an ARM URL — those start with /subscriptions/.
	return !strings.HasPrefix(p, "/subscriptions/")
}

func (h *Handler) serveDataPlane(w http.ResponseWriter, r *http.Request) {
	queueName, peek := parseDataPlanePath(r.URL.Path)
	if queueName == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing queue in data-plane path")
		return
	}

	queue, err := h.findQueueByName(r.Context(), queueName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if peek {
		h.dataPlaneReceive(w, r, queue.URL)
		return
	}

	h.dataPlaneSend(w, r, queue.URL)
}

func (h *Handler) dataPlaneSend(w http.ResponseWriter, r *http.Request, queueURL string) {
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
		QueueURL: queueURL,
		Body:     string(body),
	}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) dataPlaneReceive(w http.ResponseWriter, r *http.Request, queueURL string) {
	if r.Method != http.MethodDelete {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"receive-and-delete requires DELETE")
		return
	}

	msgs, err := h.mq.ReceiveMessages(r.Context(), mqdriver.ReceiveMessageInput{
		QueueURL:    queueURL,
		MaxMessages: 1,
	})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if len(msgs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Service Bus REST returns the message body in the response with the
	// brokered message envelope as headers; we put both in the body for
	// simplicity since the modern SDK doesn't use this path.
	if err := h.mq.DeleteMessage(r.Context(), queueURL, msgs[0].ReceiptHandle); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("BrokerProperties", `{"MessageId":"`+msgs[0].MessageID+`"}`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(msgs[0].Body))
}

// parseDataPlanePath returns the queue name and a flag indicating whether
// the URL targets /messages/head (peek-and-delete) or /messages (send).
func parseDataPlanePath(p string) (queueName string, peek bool) {
	trimmed := strings.Trim(p, "/")

	if strings.HasSuffix(trimmed, "messages/head") {
		peek = true
		trimmed = strings.TrimSuffix(trimmed, "/messages/head")
	} else if strings.HasSuffix(trimmed, "messages") {
		trimmed = strings.TrimSuffix(trimmed, "/messages")
	} else {
		return "", false
	}

	// Trimmed is now either "queue" or "namespace/queue". The driver doesn't
	// model namespaces — the queue is the last segment regardless.
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 {
		return "", peek
	}

	return parts[len(parts)-1], peek
}

func (h *Handler) findQueueByName(ctx context.Context, name string) (*mqdriver.QueueInfo, error) {
	queues, err := h.mq.ListQueues(ctx, "")
	if err != nil {
		return nil, err
	}

	for i := range queues {
		if queues[i].Name == name {
			return &queues[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "queue %s not found", name)
}

//nolint:gocritic // rp is a request-scoped value; copying once per response is fine.
func toQueueResource(rp azurearm.ResourcePath, info *mqdriver.QueueInfo) queueResource {
	return queueResource{
		ID: azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup,
			providerName, resourceType, rp.ResourceName) + "/queues/" + info.Name,
		Name: info.Name,
		Type: providerName + "/" + resourceType + "/" + subTypeQueue,
		Properties: queueProperties{
			Status:       "Active",
			MessageCount: info.ApproxMessageCount,
		},
	}
}
