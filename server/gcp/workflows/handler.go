// Package workflows implements the Google Cloud Workflows control plane
// (workflows.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/workflows/v1 clients, gcloud, and the Terraform google
// provider's google_workflows_workflow resource hit this handler unchanged.
//
// Coverage (workflow control plane only):
//
//	POST   /v1/…/workflows?workflowId=      — CreateWorkflow (LRO)
//	GET    /v1/…/workflows                  — ListWorkflows
//	GET    /v1/…/workflows/{id}             — GetWorkflow
//	PATCH  /v1/…/workflows/{id}?updateMask= — PatchWorkflow (LRO)
//	DELETE /v1/…/workflows/{id}             — DeleteWorkflow (LRO)
//	GET    /v1/…/operations/{op}            — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response` as a typed Any, so an SDK or
// Terraform LRO wait terminates on the first poll instead of hanging.
//
// Location-scoped operations: Workflows' operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The workflows resource-type guard keeps this handler
// disjoint from every other /v1/projects/ handler (Composer's environments,
// Cloud Deploy's pipelines, Scheduler's jobs, …).
package workflows

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	wdriver "github.com/stackshy/cloudemu/v2/services/workflows/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	workflowsColl    = "workflows"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	workflowTypeURL = "type.googleapis.com/google.cloud.workflows.v1.Workflow"
)

// Handler serves workflows.googleapis.com v1 requests against a Workflows
// driver.
type Handler struct {
	db wdriver.Workflows

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Cloud Workflows handler backed by db.
func New(db wdriver.Workflows) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a Cloud Workflows v1 path.
type route struct {
	project  string
	location string
	resource string // "workflows" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a Cloud Workflows v1 path. It recognizes
// only the workflows and operations resources under a locations scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rest := parts[minResourceParts:]
	if len(rest) == 0 || len(rest) > itemParts || !knownResource(rest[0]) {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3], resource: rest[0]}
	if len(rest) == itemParts {
		rt.name = rest[1]
	}

	return rt, true
}

// knownResource reports whether seg is a resource collection this handler
// serves.
func knownResource(seg string) bool {
	return seg == workflowsColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{workflows|operations}[/…]
// paths. The workflows guard keeps it disjoint from the other /v1/projects/
// handlers. An operations path is claimed only when this handler has no shared
// LRO registry (a standalone package server); in an assembled server the shared
// poller owns it.
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
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud Workflows path")
		return
	}

	if rt.resource == operationsSeg {
		h.serveOperation(w, r)
		return
	}

	if rt.name == "" {
		h.serveCollection(w, r, rt)
		return
	}

	h.serveItem(w, r, rt)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodPost:
		h.createWorkflow(w, r, rt)
	case http.MethodGet:
		h.listWorkflows(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt route) {
	switch r.Method {
	case http.MethodGet:
		h.getWorkflow(w, r, rt)
	case http.MethodPatch:
		h.patchWorkflow(w, r, rt)
	case http.MethodDelete:
		h.deleteWorkflow(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// resourceName builds the full resource name for a workflow.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + workflowsColl + "/" + id
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
