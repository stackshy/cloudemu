// Package pubsub implements the GCP Pub/Sub v1 REST API as a server.Handler.
// Real google.golang.org/api/pubsub/v1 and cloud.google.com/go/pubsub clients
// configured with a custom endpoint hit this handler the same way they hit
// pubsub.googleapis.com.
//
// Coverage:
//
//	topics       create/get/list/delete/publish, getIamPolicy/setIamPolicy/testIamPermissions,
//	             topics.subscriptions.list, topics.snapshots.list
//	subscriptions create/get/list/delete, pull/acknowledge/modifyAckDeadline/
//	             modifyPushConfig/seek, getIamPolicy/setIamPolicy/testIamPermissions
//	snapshots    create/get/list/delete
//
// Topic existence and labels are backed by the portable messagequeue driver;
// Pub/Sub-native delivery (independent fan-out per subscription, publish-time
// stamping, ordering keys, snapshots, seek, IAM) is modeled in this handler
// because that behavior does not map onto the SQS-style driver.
package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const (
	pathPrefix = "/v1/projects/"

	resTopics        = "topics"
	resSubscriptions = "subscriptions"
	resSnapshots     = "snapshots"

	verbGetIamPolicy       = "getIamPolicy"
	verbSetIamPolicy       = "setIamPolicy"
	verbTestIamPermissions = "testIamPermissions"

	reasonNotFound         = "NOT_FOUND"
	reasonInvalidArgument  = "INVALID_ARGUMENT"
	reasonMethodNotAllowed = "METHOD_NOT_ALLOWED"
	reasonAlreadyExists    = "ALREADY_EXISTS"
	reasonInternal         = "INTERNAL"

	contentTypeJSON = "application/json"
	maxBodyBytes    = 5 << 20

	partsTypeOnly = 2 // /v1/projects/{p}/{type}
	partsResource = 3 // /v1/projects/{p}/{type}/{name}
	partsNested   = 4 // /v1/projects/{p}/{type}/{name}/{subtype}
)

// Matches accepts /v1/projects/{p}/{topics|subscriptions|snapshots}[...].
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pathPrefix) {
		return false
	}

	parts := splitPath(r.URL.Path)
	if len(parts) < partsTypeOnly {
		return false
	}

	t, _ := splitColon(parts[1])

	return t == resTopics || t == resSubscriptions || t == resSnapshots
}

// ServeHTTP routes by URL shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < partsTypeOnly {
		writeError(w, http.StatusNotFound, reasonNotFound, "unsupported path")
		return
	}

	project := parts[0]
	bareType, typeAction := splitColon(parts[1])

	if len(parts) == partsTypeOnly {
		if typeAction != "" {
			writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
			return
		}

		h.serveCollection(w, r, project, bareType)

		return
	}

	name, action := splitColon(parts[2])

	// 4-segment nested collections: topics/{t}/subscriptions|snapshots.
	if len(parts) >= partsNested {
		sub, _ := splitColon(parts[3])
		h.serveNested(w, r, project, bareType, name, sub)

		return
	}

	switch bareType {
	case resTopics:
		h.serveTopic(w, r, project, name, action)
	case resSubscriptions:
		h.serveSubscription(w, r, project, name, action)
	case resSnapshots:
		h.serveSnapshot(w, r, project, name, action)
	default:
		writeError(w, http.StatusNotFound, reasonNotFound, "unknown resource type: "+bareType)
	}
}

func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, project, resType string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	switch resType {
	case resTopics:
		h.listTopics(w, r, project)
	case resSubscriptions:
		h.listSubscriptions(w, r, project)
	case resSnapshots:
		h.listSnapshots(w, r, project)
	default:
		writeError(w, http.StatusNotFound, reasonNotFound, "unknown collection type")
	}
}

// serveNested handles topics/{t}/subscriptions and topics/{t}/snapshots list.
func (h *Handler) serveNested(w http.ResponseWriter, r *http.Request, project, bareType, name, sub string) {
	if bareType != resTopics || r.Method != http.MethodGet {
		writeError(w, http.StatusNotFound, reasonNotFound, "unsupported nested path")
		return
	}

	switch sub {
	case resSubscriptions:
		h.listTopicSubscriptions(w, r, project, name)
	case resSnapshots:
		h.listTopicSnapshots(w, project, name)
	default:
		writeError(w, http.StatusNotFound, reasonNotFound, "unsupported nested collection: "+sub)
	}
}

// ---------- Topics ----------

func (h *Handler) serveTopic(w http.ResponseWriter, r *http.Request, project, name, action string) {
	switch action {
	case "publish":
		h.publish(w, r, name)
		return
	case verbGetIamPolicy, verbSetIamPolicy, verbTestIamPermissions:
		h.serveIam(w, r, resTopics, name, action)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createTopic(w, r, project, name)
	case http.MethodGet:
		h.getTopic(w, r, project, name)
	case http.MethodDelete:
		h.deleteTopic(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request, project, name string) {
	var body topic
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "invalid JSON: "+err.Error())
		return
	}

	info, err := h.mq.CreateQueue(r.Context(), mqdriver.QueueConfig{Name: name, Tags: body.Labels})
	if err != nil {
		writeErr(w, err)
		return
	}

	h.mu.Lock()
	h.topicLog(name)
	h.mu.Unlock()

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

func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request, name string) {
	q, err := h.findQueueByName(r, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	if err := h.mq.DeleteQueue(r.Context(), q.URL); err != nil {
		writeErr(w, err)
		return
	}

	h.mu.Lock()
	delete(h.topics, name)
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request, project string) {
	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		writeErr(w, err)
		return
	}

	items := make([]topic, 0, len(queues))
	for i := range queues {
		items = append(items, topic{Name: topicName(project, queues[i].Name), Labels: queues[i].Tags})
	}

	page, err := pagination.PaginateSorted(items, func(a, b topic) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "invalid pageToken")
		return
	}

	writeJSON(w, http.StatusOK, listTopicsResponse{Topics: page.Items, NextPageToken: page.NextPageToken})
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	var req publishRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if _, err := h.findQueueByName(r, name); err != nil {
		writeErr(w, err)
		return
	}

	publishTime := time.Now().UTC()
	out := publishResponse{MessageIDs: make([]string, 0, len(req.Messages))}

	for i := range req.Messages {
		id := h.appendMessage(name, &storedMessage{
			body:        decodeData(req.Messages[i].Data),
			attributes:  req.Messages[i].Attributes,
			orderingKey: req.Messages[i].OrderingKey,
			publishTime: publishTime,
		})
		out.MessageIDs = append(out.MessageIDs, id)
	}

	writeJSON(w, http.StatusOK, out)
}

// ---------- helpers ----------

func splitPath(p string) []string {
	rest := strings.TrimPrefix(p, pathPrefix)
	if rest == "" {
		return nil
	}

	return strings.Split(rest, "/")
}

// splitColon separates a "name:action" segment into its parts.
func splitColon(s string) (name, action string) {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}

	return s, ""
}

func topicName(project, name string) string {
	return "projects/" + project + "/topics/" + name
}

func subscriptionName(project, name string) string {
	return "projects/" + project + "/subscriptions/" + name
}

func snapshotName(project, name string) string {
	return "projects/" + project + "/snapshots/" + name
}

// shortName returns the trailing segment of a resource path.
func shortName(full string) string {
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

func pageSize(r *http.Request) int {
	if v := r.URL.Query().Get("pageSize"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}

	return 0
}

func encodeData(raw string) string {
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// decodeData tolerates unencoded payloads — some test clients send raw JSON.
func decodeData(data string) string {
	if b, err := base64.StdEncoding.DecodeString(data); err == nil {
		return string(b)
	}

	return data
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "invalid JSON: "+err.Error())
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
		writeError(w, http.StatusNotFound, reasonNotFound, err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, reasonAlreadyExists, err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, reasonInternal, err.Error())
	}
}
