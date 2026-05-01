// Package pubsub implements the GCP Pub/Sub v1 REST API as a server.Handler.
// Real google.golang.org/api/pubsub/v1 clients configured with a custom
// endpoint hit this handler the same way they hit pubsub.googleapis.com.
//
// MVP coverage:
//
//	PUT    /v1/projects/{p}/topics/{name}                  — Create
//	GET    /v1/projects/{p}/topics/{name}                  — Get
//	GET    /v1/projects/{p}/topics                         — List
//	DELETE /v1/projects/{p}/topics/{name}                  — Delete
//	POST   /v1/projects/{p}/topics/{name}:publish          — Publish
//	PUT    /v1/projects/{p}/subscriptions/{name}           — Create
//	GET    /v1/projects/{p}/subscriptions/{name}           — Get
//	GET    /v1/projects/{p}/subscriptions                  — List
//	DELETE /v1/projects/{p}/subscriptions/{name}           — Delete
//	POST   /v1/projects/{p}/subscriptions/{name}:pull      — Pull
//	POST   /v1/projects/{p}/subscriptions/{name}:acknowledge — Ack
//
// The portable messagequeue driver pairs a topic and subscription under a
// single queue keyed by name. SDK-compat reflects this: a subscription's
// "topic" must point at a topic with the same trailing name. Cross-name
// subscriptions (sub "billing-events" linked to topic "events") are not
// modeled in the MVP.
package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
)

const (
	pathPrefix = "/v1/projects/"

	resTopics        = "topics"
	resSubscriptions = "subscriptions"

	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	// Path-segment counts used to dispatch by URL shape.
	partsTypeOnly = 2 // /v1/projects/{p}/{type}
	partsResource = 3 // /v1/projects/{p}/{type}/{name}
)

// Handler serves Pub/Sub v1 REST requests against a messagequeue driver.
type Handler struct {
	mq mqdriver.MessageQueue
}

// New returns a Pub/Sub handler backed by mq.
func New(mq mqdriver.MessageQueue) *Handler {
	return &Handler{mq: mq}
}

// Matches accepts /v1/projects/{p}/topics[...] and /v1/projects/{p}/subscriptions[...].
// The resource-type guard prevents this handler from claiming Cloud Functions
// (locations/functions) or Firestore (databases) URLs that share the same
// /v1/projects/ prefix.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := splitPath(r.URL.Path)
	if len(parts) < partsTypeOnly {
		return false
	}

	// parts[1] is "topics" or "subscriptions" (possibly with :action suffix).
	t := parts[1]
	if i := strings.Index(t, ":"); i >= 0 {
		t = t[:i]
	}

	return t == resTopics || t == resSubscriptions
}

// ServeHTTP routes by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < partsTypeOnly {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported path")
		return
	}

	project := parts[0]
	resType := parts[1]

	// Strip ":action" from the type for the bare-collection match.
	bareType, action := resType, ""
	if i := strings.Index(resType, ":"); i >= 0 {
		bareType = resType[:i]
		action = resType[i+1:]
	}

	if len(parts) == partsTypeOnly && action == "" {
		// /v1/projects/{p}/{topics|subscriptions}
		h.serveCollection(w, r, project, bareType)
		return
	}

	// Resource-level: /v1/projects/{p}/{type}/{name}[:action]
	if len(parts) < partsResource {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "missing resource name")
		return
	}

	name := parts[2]
	if i := strings.Index(name, ":"); i >= 0 {
		action = name[i+1:]
		name = name[:i]
	}

	switch bareType {
	case resTopics:
		h.serveTopic(w, r, project, name, action)
	case resSubscriptions:
		h.serveSubscription(w, r, project, name, action)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown resource type: "+bareType)
	}
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, project, resType string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		writeErr(w, err)
		return
	}

	switch resType {
	case resTopics:
		out := listTopicsResponse{Topics: make([]topic, 0, len(queues))}
		for i := range queues {
			out.Topics = append(out.Topics, topic{
				Name: topicName(project, queues[i].Name),
			})
		}

		writeJSON(w, http.StatusOK, out)
	case resSubscriptions:
		out := listSubscriptionsResponse{Subscriptions: make([]subscription, 0, len(queues))}
		for i := range queues {
			out.Subscriptions = append(out.Subscriptions, subscription{
				Name:  subscriptionName(project, queues[i].Name),
				Topic: topicName(project, queues[i].Name),
			})
		}

		writeJSON(w, http.StatusOK, out)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown collection type")
	}
}

// ---------- Topics ----------

func (h *Handler) serveTopic(w http.ResponseWriter, r *http.Request, project, name, action string) {
	if action == "publish" {
		h.publish(w, r, project, name)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createTopic(w, r, project, name)
	case http.MethodGet:
		h.getTopic(w, r, project, name)
	case http.MethodDelete:
		h.deleteTopic(w, r, project, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request, project, name string) {
	var body topic
	_ = decodeJSON(w, r, &body) // topic body is mostly empty for create; tolerate it

	info, err := h.mq.CreateQueue(r.Context(), mqdriver.QueueConfig{Name: name, Tags: body.Labels})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, topic{
		Name:   topicName(project, info.Name),
		Labels: info.Tags,
	})
}

func (h *Handler) getTopic(w http.ResponseWriter, r *http.Request, project, name string) {
	q, err := h.findQueueByName(r, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, topic{
		Name:   topicName(project, q.Name),
		Labels: q.Tags,
	})
}

func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request, _, name string) {
	q, err := h.findQueueByName(r, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.mq.DeleteQueue(r.Context(), q.URL); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request, _, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	var req publishRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	q, err := h.findQueueByName(r, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := publishResponse{MessageIDs: make([]string, 0, len(req.Messages))}

	for i := range req.Messages {
		body, decErr := base64.StdEncoding.DecodeString(req.Messages[i].Data)
		if decErr != nil {
			// Tolerate unencoded payloads — some test clients send raw JSON.
			body = []byte(req.Messages[i].Data)
		}

		send, sendErr := h.mq.SendMessage(r.Context(), mqdriver.SendMessageInput{
			QueueURL:   q.URL,
			Body:       string(body),
			GroupID:    req.Messages[i].OrderingKey,
			Attributes: req.Messages[i].Attributes,
		})
		if sendErr != nil {
			writeErr(w, sendErr)
			return
		}

		out.MessageIDs = append(out.MessageIDs, send.MessageID)
	}

	writeJSON(w, http.StatusOK, out)
}

// ---------- Subscriptions ----------

func (h *Handler) serveSubscription(w http.ResponseWriter, r *http.Request, project, name, action string) {
	switch action {
	case "pull":
		h.pull(w, r, name)
		return
	case "acknowledge":
		h.acknowledge(w, r, name)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createSubscription(w, r, project, name)
	case http.MethodGet:
		h.getSubscription(w, r, project, name)
	case http.MethodDelete:
		h.deleteSubscription(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request, project, name string) {
	var body subscription
	if !decodeJSON(w, r, &body) {
		return
	}

	// The driver pairs topic+subscription under a single queue; require the
	// subscription name to match a known queue (which represents the topic).
	if _, err := h.findQueueByName(r, name); err != nil {
		// Auto-create from the topic field if present and matches the sub name.
		if subToTopicName(body.Topic) != name {
			writeErr(w, err)
			return
		}

		if _, cerr := h.mq.CreateQueue(r.Context(), mqdriver.QueueConfig{Name: name}); cerr != nil &&
			!cerrors.IsAlreadyExists(cerr) {
			writeErr(w, cerr)
			return
		}
	}

	resp := subscription{
		Name:               subscriptionName(project, name),
		Topic:              topicName(project, name),
		AckDeadlineSeconds: body.AckDeadlineSeconds,
		Labels:             body.Labels,
	}
	if resp.AckDeadlineSeconds == 0 {
		resp.AckDeadlineSeconds = 10
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getSubscription(w http.ResponseWriter, r *http.Request, project, name string) {
	q, err := h.findQueueByName(r, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, subscription{
		Name:               subscriptionName(project, q.Name),
		Topic:              topicName(project, q.Name),
		AckDeadlineSeconds: 10,
	})
}

func (*Handler) deleteSubscription(w http.ResponseWriter, _ *http.Request, _ string) {
	// In the driver, deleting the subscription would orphan the topic. Treat
	// it as a no-op: real Pub/Sub has no operation that's both safe and useful
	// here without modeling subscriptions separately.
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) pull(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	var req pullRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	q, err := h.findQueueByName(r, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	if req.MaxMessages == 0 {
		req.MaxMessages = 1
	}

	msgs, err := h.mq.ReceiveMessages(r.Context(), mqdriver.ReceiveMessageInput{
		QueueURL:    q.URL,
		MaxMessages: req.MaxMessages,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := pullResponse{ReceivedMessages: make([]receivedMessage, 0, len(msgs))}
	for i := range msgs {
		out.ReceivedMessages = append(out.ReceivedMessages, receivedMessage{
			AckID: msgs[i].ReceiptHandle,
			Message: pubsubMessage{
				MessageID:  msgs[i].MessageID,
				Data:       base64.StdEncoding.EncodeToString([]byte(msgs[i].Body)),
				Attributes: msgs[i].Attributes,
			},
		})
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) acknowledge(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	var req acknowledgeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	q, err := h.findQueueByName(r, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	for _, ack := range req.AckIDs {
		if delErr := h.mq.DeleteMessage(r.Context(), q.URL, ack); delErr != nil {
			writeErr(w, delErr)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// ---------- helpers ----------

func splitPath(p string) []string {
	rest := strings.TrimPrefix(p, pathPrefix)
	if rest == "" {
		return nil
	}

	return strings.Split(rest, "/")
}

func topicName(project, name string) string {
	return fmt.Sprintf("projects/%s/topics/%s", project, name)
}

func subscriptionName(project, name string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", project, name)
}

// subToTopicName extracts the trailing topic name from "projects/p/topics/foo".
func subToTopicName(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}

	return full
}

func (h *Handler) findQueueByName(r *http.Request, name string) (*mqdriver.QueueInfo, error) {
	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		return nil, err
	}

	for i := range queues {
		if queues[i].Name == name {
			return &queues[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "%s not found", name)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
			"status":  reason,
		},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
