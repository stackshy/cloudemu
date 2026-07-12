// Package fcm implements the Firebase Cloud Messaging (fcm.googleapis.com) v1
// REST API as a server.Handler. Real google.golang.org/api/fcm/v1 clients
// pointed at this server can send messages end-to-end against the shared
// notification driver.
//
// # What maps and what does not
//
// FCM v1 is a message-*send* API, not a topic/subscription control plane. Its
// single documented method is:
//
//	POST /v1/projects/{project}/messages:send   →  Notification.Publish
//
// FCM has no CreateTopic / Subscribe / ListTopics equivalents — an app
// publishes to a topic string (e.g. "weather") and devices subscribe to topics
// out-of-band through the client SDKs, not through this REST surface. This
// handler therefore implements ONLY messages:send. It does not fabricate topic
// or subscription CRUD that the real FCM API does not expose.
//
// Because the notification driver's Publish requires the target topic to exist,
// this handler auto-provisions the topic named in the message on first send
// (mirroring FCM, where publishing to a topic name that no devices have
// subscribed to still succeeds). A token- or condition-addressed message with
// no topic is published to a synthetic per-request topic so the round-trip
// still yields a message id.
package fcm

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

const (
	pathPrefix   = "/v1/projects/"
	messagesSend = "messages:send"
	tokenTopic   = "_fcm_token_target"
	minPathParts = 3 // [projects, {p}, messages:send]
)

// route is the parsed components of an FCM v1 path.
type route struct {
	project string
}

// parseRoute recognizes /v1/projects/{project}/messages:send and nothing else.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) != minPathParts || parts[0] != "projects" || parts[2] != messagesSend {
		return route{}, false
	}

	if parts[1] == "" {
		return route{}, false
	}

	return route{project: parts[1]}, true
}

// Handler serves fcm.googleapis.com v1 messages:send requests against a
// notification driver.
type Handler struct {
	notif notifdriver.Notification
}

// New returns an FCM handler backed by n.
func New(n notifdriver.Notification) *Handler {
	return &Handler{notif: n}
}

// Matches claims /v1/projects/{p}/messages:send — disjoint from the other
// /v1/projects/ GCP handlers (Firestore's databases/…, PubSub's
// topics|subscriptions/…, IAM's serviceAccounts|roles/…), which never use the
// messages:send suffix. Registered before Firestore's permissive /v1/projects/
// prefix match.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)
	return ok
}

// ServeHTTP handles the single supported method: POST messages:send.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized FCM path")
		return
	}

	if r.Method != http.MethodPost {
		gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported FCM operation")
		return
	}

	h.sendMessage(w, r, rt)
}
