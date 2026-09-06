// Package scheduler implements the cloudscheduler.googleapis.com v1 REST API as
// a server.Handler. Real google.golang.org/api/cloudscheduler/v1 clients — and
// Terraform's google provider (google_cloud_scheduler_job) — pointed at this
// server CRUD jobs and drive the pause/resume/run verbs end-to-end against the
// shared scheduler driver.
//
// Coverage (v1 REST), all SYNCHRONOUS (the Job, or Empty for delete, is
// returned directly — Cloud Scheduler has no long-running operations):
//
//	POST   /v1/projects/{p}/locations/{l}/jobs?jobId={id}   — Create job
//	GET    /v1/projects/{p}/locations/{l}/jobs/{job}         — Get job
//	GET    /v1/projects/{p}/locations/{l}/jobs               — List jobs (paged)
//	PATCH  /v1/projects/{p}/locations/{l}/jobs/{job}?updateMask=… — Patch job
//	DELETE /v1/projects/{p}/locations/{l}/jobs/{job}         — Delete job
//	POST   /v1/projects/{p}/locations/{l}/jobs/{job}:pause   — Pause job
//	POST   /v1/projects/{p}/locations/{l}/jobs/{job}:resume  — Resume job
//	POST   /v1/projects/{p}/locations/{l}/jobs/{job}:run     — Run job
//
// This is the control plane only. A job's schedule and target (HTTP, Pub/Sub, or
// App Engine, including OAuth/OIDC token config) round-trip verbatim, but firing
// a job — HTTP delivery, Pub/Sub publish, App Engine routing, token minting — is
// out of scope and never happens.
package scheduler

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	scheddriver "github.com/stackshy/cloudemu/v2/services/scheduler/driver"
)

const (
	pathPrefix   = "/v1/projects/"
	locationsSeg = "locations"
	jobsSeg      = "jobs"

	// minResourceParts counts [projects, {p}, locations, {l}, jobs].
	minResourceParts = 5

	verbPause  = "pause"
	verbResume = "resume"
	verbRun    = "run"
)

// Handler serves cloudscheduler.googleapis.com v1 requests.
type Handler struct {
	jobs scheddriver.Scheduler
}

// New returns a Cloud Scheduler handler backed by s.
func New(s scheddriver.Scheduler) *Handler {
	return &Handler{jobs: s}
}

// route holds the parsed components of a Cloud Scheduler v1 path.
type route struct {
	project  string
	location string
	job      string // job id; empty for the collection
	verb     string // "pause" | "resume" | "run", if any
}

// parent returns the projects/{p}/locations/{l} resource name.
func (rt route) parent() string {
	return "projects/" + rt.project + "/locations/" + rt.location
}

// fullName returns the projects/{p}/locations/{l}/jobs/{j} resource name.
func (rt route) fullName() string {
	return rt.parent() + "/jobs/" + rt.job
}

// parseRoute extracts the components of a Cloud Scheduler v1 path. The trailing
// job segment may carry a ":verb" (pause|resume|run) suffix.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != "projects" || parts[2] != locationsSeg || parts[4] != jobsSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}
	rest := parts[minResourceParts:]

	switch len(rest) {
	case 0:
		return rt, true
	case 1:
		rt.job, rt.verb, _ = strings.Cut(rest[0], ":")
		return rt, true
	default:
		return route{}, false
	}
}

// Matches claims /v1/projects/{p}/locations/{l}/jobs[/…] paths. The jobs
// resource-type guard keeps it disjoint from Memorystore (instances|operations),
// Eventarc (triggers), GKE (clusters), and the rest of the /v1/projects/ family,
// so registration order among them is unconstrained. Cloud Run's jobs live under
// the /v2/ prefix, so there is no collision there either.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	return ok && rt.project != "" && rt.location != ""
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud Scheduler path")
		return
	}

	switch {
	case rt.job == "":
		h.serveCollection(w, r, rt)
	case rt.verb != "":
		h.serveVerb(w, r, rt)
	default:
		h.serveJob(w, r, rt)
	}
}

// serveCollection dispatches /jobs collection requests.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createJob(w, r, rt)
	case http.MethodGet:
		h.listJobs(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveJob dispatches /jobs/{id} resource requests.
func (h *Handler) serveJob(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getJob(w, r, rt)
	case http.MethodPatch:
		h.patchJob(w, r, rt)
	case http.MethodDelete:
		h.deleteJob(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveVerb dispatches the :pause/:resume/:run custom methods (POST-only).
func (h *Handler) serveVerb(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		writeUnsupported(w)
		return
	}

	switch rt.verb {
	case verbPause:
		h.pauseJob(w, r, rt)
	case verbResume:
		h.resumeJob(w, r, rt)
	case verbRun:
		h.runJob(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Cloud Scheduler operation")
}
