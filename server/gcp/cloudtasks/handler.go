// Package cloudtasks implements the cloudtasks.googleapis.com v2 REST API as a
// server.Handler. Real google.golang.org/api/cloudtasks/v2 clients — and
// Terraform's google provider (google_cloud_tasks_queue) — pointed at this
// server CRUD queues and drive the pause/resume/purge verbs and the IAM methods
// end-to-end against the Cloud Tasks driver.
//
// Coverage (v2 REST), all SYNCHRONOUS (the Queue, or Empty for delete, or the
// IAM Policy, is returned directly — Cloud Tasks has no long-running
// operations):
//
//	POST   /v2/projects/{p}/locations/{l}/queues                     — Create queue (name in body)
//	GET    /v2/projects/{p}/locations/{l}/queues/{q}                 — Get queue
//	GET    /v2/projects/{p}/locations/{l}/queues                     — List queues (paged)
//	PATCH  /v2/projects/{p}/locations/{l}/queues/{q}?updateMask=…    — Patch queue
//	DELETE /v2/projects/{p}/locations/{l}/queues/{q}                 — Delete queue
//	POST   /v2/projects/{p}/locations/{l}/queues/{q}:pause           — Pause queue  (→ PAUSED)
//	POST   /v2/projects/{p}/locations/{l}/queues/{q}:resume          — Resume queue (→ RUNNING)
//	POST   /v2/projects/{p}/locations/{l}/queues/{q}:purge           — Purge queue  (sets purgeTime)
//	POST   /v2/projects/{p}/locations/{l}/queues/{q}:getIamPolicy    — Get IAM policy
//	POST   /v2/projects/{p}/locations/{l}/queues/{q}:setIamPolicy    — Set IAM policy
//	POST   /v2/projects/{p}/locations/{l}/queues/{q}:testIamPermissions — Test IAM permissions
//
// This is the queue control plane only. Task-level operations and real task
// dispatch/execution are out of scope; a queue's httpTarget config round-trips
// verbatim but no task is ever dispatched.
package cloudtasks

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

const (
	pathPrefix   = "/v2/projects/"
	locationsSeg = "locations"
	queuesSeg    = "queues"

	// minResourceParts counts [projects, {p}, locations, {l}, queues].
	minResourceParts = 5

	verbPause              = "pause"
	verbResume             = "resume"
	verbPurge              = "purge"
	verbGetIamPolicy       = "getIamPolicy"
	verbSetIamPolicy       = "setIamPolicy"
	verbTestIamPermissions = "testIamPermissions"
)

// Handler serves cloudtasks.googleapis.com v2 requests.
type Handler struct {
	queues ctdriver.Queues
}

// New returns a Cloud Tasks handler backed by q.
func New(q ctdriver.Queues) *Handler {
	return &Handler{queues: q}
}

// route holds the parsed components of a Cloud Tasks v2 path.
type route struct {
	project  string
	location string
	queue    string // queue id; empty for the collection
	verb     string // pause|resume|purge|*IamPolicy|testIamPermissions, if any
}

// parent returns the projects/{p}/locations/{l} resource name.
func (rt route) parent() string {
	return "projects/" + rt.project + "/locations/" + rt.location
}

// fullName returns the projects/{p}/locations/{l}/queues/{q} resource name.
func (rt route) fullName() string {
	return rt.parent() + "/queues/" + rt.queue
}

// parseRoute extracts the components of a Cloud Tasks v2 path. The trailing
// queue segment may carry a ":verb" suffix.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v2/"), "/")
	if len(parts) < minResourceParts || parts[0] != "projects" || parts[2] != locationsSeg || parts[4] != queuesSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}
	rest := parts[minResourceParts:]

	switch len(rest) {
	case 0:
		return rt, true
	case 1:
		rt.queue, rt.verb, _ = strings.Cut(rest[0], ":")
		return rt, true
	default:
		return route{}, false
	}
}

// Matches claims /v2/projects/{p}/locations/{l}/queues[/…] paths. The queues
// resource-type guard keeps it disjoint from Cloud Run (jobs|services on the
// same /v2/ prefix) and every other handler, so registration order is
// unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	return ok && rt.project != "" && rt.location != ""
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud Tasks path")
		return
	}

	switch {
	case rt.queue == "":
		h.serveCollection(w, r, rt)
	case rt.verb != "":
		h.serveVerb(w, r, rt)
	default:
		h.serveQueue(w, r, rt)
	}
}

// serveCollection dispatches /queues collection requests.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createQueue(w, r, rt)
	case http.MethodGet:
		h.listQueues(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveQueue dispatches /queues/{id} resource requests.
func (h *Handler) serveQueue(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getQueue(w, r, rt)
	case http.MethodPatch:
		h.patchQueue(w, r, rt)
	case http.MethodDelete:
		h.deleteQueue(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveVerb dispatches the colon custom methods (POST-only).
func (h *Handler) serveVerb(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		writeUnsupported(w)
		return
	}

	switch rt.verb {
	case verbPause:
		h.pauseQueue(w, r, rt)
	case verbResume:
		h.resumeQueue(w, r, rt)
	case verbPurge:
		h.purgeQueue(w, r, rt)
	case verbGetIamPolicy:
		h.getIamPolicy(w, r, rt)
	case verbSetIamPolicy:
		h.setIamPolicy(w, r, rt)
	case verbTestIamPermissions:
		h.testIamPermissions(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Cloud Tasks operation")
}
