package gcs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	// payloadFormatJSONAPIV1 attaches the object resource JSON as the message
	// data; payloadFormatNone sends attributes only.
	payloadFormatJSONAPIV1 = "JSON_API_V1"

	// GCS object-change event types published to a notification config's topic.
	eventTypeObjectFinalize = "OBJECT_FINALIZE"
	eventTypeObjectDelete   = "OBJECT_DELETE"

	// pubsubTopicPrefix is the leading form the storage SDK uses for a
	// notification's topic: //pubsub.googleapis.com/projects/{p}/topics/{t}.
	pubsubTopicPrefix = "//pubsub.googleapis.com/"
)

// TopicPublisher emits an object-change event to a Pub/Sub topic. The Pub/Sub
// handler implements it; the GCS handler calls it best-effort so a slow or
// missing target never fails an object operation. It mirrors the same fan-out
// (push subscriptions + event-triggered Cloud Functions) the REST publish uses.
type TopicPublisher interface {
	PublishToTopic(ctx context.Context, project, topic string, data []byte, attributes map[string]string)
}

// SetPublisher wires the Pub/Sub backend so object changes on a bucket with a
// matching notification config are delivered to the configured topic. Nil (the
// default) leaves object-change notifications a no-op.
func (h *Handler) SetPublisher(p TopicPublisher) {
	h.publisher = p
}

// notificationResource is the storage#notification JSON shape. The snake_case
// field names mirror the storage v1 API / Go storage SDK exactly.
type notificationResource struct {
	Kind             string            `json:"kind"`
	ID               string            `json:"id,omitempty"`
	Topic            string            `json:"topic,omitempty"`
	PayloadFormat    string            `json:"payload_format,omitempty"`
	EventTypes       []string          `json:"event_types,omitempty"`
	CustomAttributes map[string]string `json:"custom_attributes,omitempty"`
	ObjectNamePrefix string            `json:"object_name_prefix,omitempty"`
	Etag             string            `json:"etag,omitempty"`
	SelfLink         string            `json:"selfLink,omitempty"`
}

// notificationsListResponse is the storage#notifications list shape.
type notificationsListResponse struct {
	Kind  string                 `json:"kind"`
	Items []notificationResource `json:"items"`
}

// notificationCollection serves /b/{bucket}/notificationConfigs — POST inserts
// a config, GET lists them.
func (h *Handler) notificationCollection(w http.ResponseWriter, r *http.Request, bucket string) {
	if h.ext == nil {
		writeError(w, http.StatusNotImplemented, "notImplemented", "notifications not supported")
		return
	}

	if !h.bucketExists(r, bucket) {
		writeError(w, http.StatusNotFound, "notFound", "bucket "+bucket+" not found")
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.createNotification(w, r, bucket)
	case http.MethodGet:
		h.listNotifications(w, r, bucket)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// notificationResourceOp serves /b/{bucket}/notificationConfigs/{id} — GET
// fetches a config, DELETE removes it.
func (h *Handler) notificationResourceOp(w http.ResponseWriter, r *http.Request, bucket, id string) {
	if h.ext == nil {
		writeError(w, http.StatusNotImplemented, "notImplemented", "notifications not supported")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getNotification(w, r, bucket, id)
	case http.MethodDelete:
		h.deleteNotification(w, r, bucket, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createNotification(w http.ResponseWriter, r *http.Request, bucket string) {
	var body notificationResource
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Topic == "" {
		writeError(w, http.StatusBadRequest, "invalid", "topic required")
		return
	}

	payloadFormat := body.PayloadFormat
	if payloadFormat == "" {
		payloadFormat = payloadFormatJSONAPIV1
	}

	cfg, err := h.ext.CreateNotificationConfig(r.Context(), bucket, &storagedriver.GCSNotificationConfig{
		Topic:            body.Topic,
		PayloadFormat:    payloadFormat,
		EventTypes:       body.EventTypes,
		CustomAttributes: body.CustomAttributes,
		ObjectNamePrefix: body.ObjectNamePrefix,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, notificationView(r, bucket, &cfg))
}

func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request, bucket string) {
	cfgs, err := h.ext.ListNotificationConfigs(r.Context(), bucket)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := notificationsListResponse{Kind: "storage#notifications"}
	for i := range cfgs {
		out.Items = append(out.Items, notificationView(r, bucket, &cfgs[i]))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getNotification(w http.ResponseWriter, r *http.Request, bucket, id string) {
	cfg, err := h.ext.GetNotificationConfig(r.Context(), bucket, id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, notificationView(r, bucket, &cfg))
}

func (h *Handler) deleteNotification(w http.ResponseWriter, r *http.Request, bucket, id string) {
	if err := h.ext.DeleteNotificationConfig(r.Context(), bucket, id); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// notificationView renders a stored config as the storage#notification wire
// shape, filling in the kind and selfLink.
func notificationView(r *http.Request, bucket string, cfg *storagedriver.GCSNotificationConfig) notificationResource {
	return notificationResource{
		Kind:             "storage#notification",
		ID:               cfg.ID,
		Topic:            cfg.Topic,
		PayloadFormat:    cfg.PayloadFormat,
		EventTypes:       cfg.EventTypes,
		CustomAttributes: cfg.CustomAttributes,
		ObjectNamePrefix: cfg.ObjectNamePrefix,
		Etag:             cfg.Etag,
		SelfLink:         selfLink(r, "/storage/v1/b/"+bucket+"/notificationConfigs/"+cfg.ID),
	}
}

// emitObjectEvent publishes an object-change event to every bucket notification
// config whose event-type filter and object-name prefix match. Best-effort: a
// nil publisher, no configs, or a publish failure never affects the object op.
func (h *Handler) emitObjectEvent(r *http.Request, bucket string, res *objectResource, eventType string) {
	if h.publisher == nil || h.ext == nil {
		return
	}

	cfgs, err := h.ext.ListNotificationConfigs(r.Context(), bucket)
	if err != nil || len(cfgs) == 0 {
		return
	}

	for i := range cfgs {
		h.emitToConfig(r.Context(), bucket, res, eventType, &cfgs[i])
	}
}

func (h *Handler) emitToConfig(
	ctx context.Context, bucket string, res *objectResource, eventType string, cfg *storagedriver.GCSNotificationConfig,
) {
	if !eventTypeMatches(cfg.EventTypes, eventType) {
		return
	}

	if cfg.ObjectNamePrefix != "" && !strings.HasPrefix(res.Name, cfg.ObjectNamePrefix) {
		return
	}

	project, topic := parsePubsubTopic(cfg.Topic)
	if topic == "" {
		return
	}

	attrs := map[string]string{
		"eventType":          eventType,
		"objectId":           res.Name,
		"bucketId":           bucket,
		"objectGeneration":   res.Generation,
		"payloadFormat":      cfg.PayloadFormat,
		"notificationConfig": "projects/_/buckets/" + bucket + "/notificationConfigs/" + cfg.ID,
		"eventTime":          time.Now().UTC().Format(time.RFC3339),
	}

	for k, v := range cfg.CustomAttributes {
		attrs[k] = v
	}

	var data []byte
	if cfg.PayloadFormat == payloadFormatJSONAPIV1 {
		data, _ = json.Marshal(res)
	}

	h.publisher.PublishToTopic(ctx, project, topic, data, attrs)
}

// eventTypeMatches reports whether want is configured. An empty filter means
// all event types fire (real GCS default).
func eventTypeMatches(configured []string, want string) bool {
	if len(configured) == 0 {
		return true
	}

	for _, e := range configured {
		if e == want {
			return true
		}
	}

	return false
}

// parsePubsubTopic extracts the project and topic short name from a
// notification topic in the //pubsub.googleapis.com/projects/{p}/topics/{t}
// form (also tolerating a bare projects/{p}/topics/{t}). Returns empty strings
// when the topic is not a recognizable Pub/Sub topic reference.
func parsePubsubTopic(topic string) (project, name string) {
	t := strings.TrimPrefix(topic, pubsubTopicPrefix)
	t = strings.TrimPrefix(t, "/")

	parts := strings.Split(t, "/")
	if len(parts) != 4 || parts[0] != "projects" || parts[2] != "topics" {
		return "", ""
	}

	return parts[1], parts[3]
}
