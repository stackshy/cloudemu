// Package clouddeploy implements the Google Cloud Deploy control plane
// (clouddeploy.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/clouddeploy/v1 clients, gcloud, and the Terraform
// google provider's google_clouddeploy_delivery_pipeline and
// google_clouddeploy_target resources hit this handler unchanged.
//
// Coverage (delivery pipeline + target control plane only):
//
//	POST   /v1/…/deliveryPipelines?deliveryPipelineId=  — CreatePipeline (LRO)
//	GET    /v1/…/deliveryPipelines                      — ListPipelines
//	GET    /v1/…/deliveryPipelines/{id}                 — GetPipeline
//	PATCH  /v1/…/deliveryPipelines/{id}?updateMask=     — PatchPipeline (LRO)
//	DELETE /v1/…/deliveryPipelines/{id}                 — DeletePipeline (LRO)
//	POST   /v1/…/targets?targetId=                      — CreateTarget (LRO)
//	GET    /v1/…/targets                                — ListTargets
//	GET    /v1/…/targets/{id}                           — GetTarget
//	PATCH  /v1/…/targets/{id}?updateMask=               — PatchTarget (LRO)
//	DELETE /v1/…/targets/{id}                           — DeleteTarget (LRO)
//	GET    /v1/…/operations/{op}                        — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response`, so an SDK or Terraform LRO wait
// terminates on the first poll instead of hanging.
//
// Location-scoped operations: Cloud Deploy's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The deliveryPipelines/targets resource-type guard keeps
// this handler disjoint from every other /v1/projects/ handler (Composer's
// environments, Memorystore/Filestore's instances, GKE's clusters, Scheduler's
// jobs, …).
package clouddeploy

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	cdriver "github.com/stackshy/cloudemu/v2/services/clouddeploy/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	pipelinesColl    = "deliveryPipelines"
	targetsColl      = "targets"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, uid, createTime, updateTime, etag + one).
	minComputedFields = 6

	pipelineTypeURL = "type.googleapis.com/google.cloud.deploy.v1.DeliveryPipeline"
	targetTypeURL   = "type.googleapis.com/google.cloud.deploy.v1.Target"
)

// collection describes one Cloud Deploy resource collection, binding its wire
// identity to the driver methods that back it. The two collections share every
// CRUD code path, differing only in these bound values. The create/get/list/
// patch/del fields are the driver method values, whose signatures the two
// resource collections share verbatim.
type collection struct {
	seg     string // "deliveryPipelines" | "targets"
	idParam string // "deliveryPipelineId" | "targetId"
	typeURL string
	oneof   bool // enforce the deployment-target oneof on create (targets only)

	create func(context.Context, *cdriver.Config) (*cdriver.Resource, *cdriver.Operation, error)
	get    func(context.Context, string, string, string) (*cdriver.Resource, error)
	list   func(context.Context, string, string) ([]cdriver.Resource, error)
	patch  func(context.Context, *cdriver.Config, []string) (*cdriver.Resource, *cdriver.Operation, error)
	del    func(context.Context, string, string, string) (*cdriver.Operation, error)
}

// Handler serves clouddeploy.googleapis.com v1 requests against a Cloud Deploy
// driver.
type Handler struct {
	db cdriver.CloudDeploy

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Cloud Deploy handler backed by db.
func New(db cdriver.CloudDeploy) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// collections binds the two resource collections to h's driver method values,
// keyed by their path segment.
func (h *Handler) collections() map[string]*collection {
	return map[string]*collection{
		pipelinesColl: {
			seg: pipelinesColl, idParam: "deliveryPipelineId", typeURL: pipelineTypeURL, oneof: false,
			create: h.db.CreatePipeline, get: h.db.GetPipeline, list: h.db.ListPipelines,
			patch: h.db.PatchPipeline, del: h.db.DeletePipeline,
		},
		targetsColl: {
			seg: targetsColl, idParam: "targetId", typeURL: targetTypeURL, oneof: true,
			create: h.db.CreateTarget, get: h.db.GetTarget, list: h.db.ListTargets,
			patch: h.db.PatchTarget, del: h.db.DeleteTarget,
		},
	}
}

// route holds the parsed components of a Cloud Deploy v1 path.
type route struct {
	project  string
	location string
	resource string // "deliveryPipelines" | "targets" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a Cloud Deploy v1 path. It recognizes
// only the deliveryPipelines, targets, and operations resources under a
// locations scope.
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
	return seg == pipelinesColl || seg == targetsColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{deliveryPipelines|targets|
// operations}[/…] paths. The deliveryPipelines/targets guard keeps it disjoint
// from the other /v1/projects/ handlers. An operations path is claimed only when
// this handler has no shared LRO registry (a standalone package server); in an
// assembled server the shared poller owns it.
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
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud Deploy path")
		return
	}

	if rt.resource == operationsSeg {
		h.serveOperation(w, r)
		return
	}

	col := h.collections()[rt.resource]
	if col == nil {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported resource: "+rt.resource)
		return
	}

	if rt.name == "" {
		h.serveCollection(w, r, rt, col)
		return
	}

	h.serveItem(w, r, rt, col)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	switch r.Method {
	case http.MethodPost:
		h.createResource(w, r, rt, col)
	case http.MethodGet:
		h.listResources(w, r, rt, col)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	switch r.Method {
	case http.MethodGet:
		h.getResource(w, r, rt, col)
	case http.MethodPatch:
		h.patchResource(w, r, rt, col)
	case http.MethodDelete:
		h.deleteResource(w, r, rt, col)
	default:
		writeMethodNotAllowed(w)
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
