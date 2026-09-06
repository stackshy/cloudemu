// Package composer implements the Google Cloud Composer environment control
// plane (composer.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/composer/v1 clients, gcloud, and the Terraform google
// provider's google_composer_environment resource hit this handler unchanged.
//
// Coverage (environment control plane only):
//
//	POST   /v1/projects/{p}/locations/{l}/environments        — CreateEnvironment (LRO)
//	GET    /v1/projects/{p}/locations/{l}/environments        — ListEnvironments
//	GET    /v1/projects/{p}/locations/{l}/environments/{e}    — GetEnvironment
//	PATCH  /v1/projects/{p}/locations/{l}/environments/{e}    — UpdateEnvironment (LRO, updateMask query param)
//	DELETE /v1/projects/{p}/locations/{l}/environments/{e}    — DeleteEnvironment (LRO)
//	GET    /v1/projects/{p}/locations/{l}/operations/{op}     — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting environment embedded in `response`, and a created environment is
// RUNNING immediately with every computed output field populated, so an SDK or
// Terraform LRO wait terminates on the first poll instead of hanging.
//
// Location-scoped operations: Composer's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The environments resource-type guard keeps this handler
// disjoint from every other /v1/projects/ handler (Memorystore/Filestore's
// instances, GKE's clusters, Cloud Functions' functions, …).
package composer

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	cdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	environmentsSeg  = "environments"
	operationsSeg    = "operations"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]
)

// Handler serves composer.googleapis.com v1 requests against a composer driver.
type Handler struct {
	db cdriver.Composer

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Composer handler backed by db.
func New(db cdriver.Composer) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a Composer v1 path.
type route struct {
	project  string
	location string
	resource string // "environments" or "operations"
	name     string // environment id or operation id; empty for the collection
}

// parseRoute extracts the components of a Composer v1 path. It recognizes only
// the environments and operations resources under a locations scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}

	rest := parts[minResourceParts:]
	if len(rest) == 0 || len(rest) > itemParts {
		return route{}, false
	}

	rt.resource = rest[0]
	if rt.resource != environmentsSeg && rt.resource != operationsSeg {
		return route{}, false
	}

	if len(rest) == itemParts {
		rt.name = rest[1]
	}

	return rt, true
}

// Matches claims /v1/projects/{p}/locations/{l}/{environments|operations}[/…]
// paths. The environments guard keeps it disjoint from the other /v1/projects/
// handlers (functions, clusters, instances, secrets, …). An operations path is
// claimed only when this handler has no shared LRO registry (a standalone
// package server); in an assembled server the shared poller owns it.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	if rt.resource == operationsSeg && h.ops != nil {
		return false
	}

	return true
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Composer path")
		return
	}

	switch rt.resource {
	case operationsSeg:
		h.serveOperation(w, r, rt)
	case environmentsSeg:
		h.serveEnvironments(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported resource: "+rt.resource)
	}
}

func (h *Handler) serveEnvironments(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.name == "" {
		switch r.Method {
		case http.MethodPost:
			h.createEnvironment(w, r, rt)
		case http.MethodGet:
			h.listEnvironments(w, r, rt)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getEnvironment(w, r, rt)
	case http.MethodPatch:
		h.patchEnvironment(w, r, rt)
	case http.MethodDelete:
		h.deleteEnvironment(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveOperation resolves a (done) long-running operation poll for a standalone
// package server (no shared registry). The operation resource name is the
// request path without the /v1/ version prefix.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request, _ route) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/v1/")

	op, err := h.db.GetOperation(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(op.Name, h.operationResponse(r, op)))
}

// environmentResourceName builds the full environment resource name.
func environmentResourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/environments/" + id
}

// formatTime renders t as RFC3339Nano; a zero time renders as the empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
