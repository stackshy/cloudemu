// Package gkebackup implements the Google Backup for GKE control plane
// (gkebackup.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/gkebackup/v1 clients, gcloud, and the Terraform google
// provider's google_gke_backup_backup_plan and google_gke_backup_restore_plan
// resources hit this handler unchanged.
//
// Coverage (backup-plan + restore-plan control plane):
//
//	POST   /v1/…/backupPlans?backupPlanId=            — CreateBackupPlan (LRO)
//	GET    /v1/…/backupPlans                          — ListBackupPlans
//	GET    /v1/…/backupPlans/{id}                     — GetBackupPlan
//	PATCH  /v1/…/backupPlans/{id}?updateMask=         — PatchBackupPlan (LRO)
//	DELETE /v1/…/backupPlans/{id}                     — DeleteBackupPlan (LRO)
//	POST   /v1/…/restorePlans?restorePlanId=          — CreateRestorePlan (LRO)
//	…                                                  — Get/List/Patch/Delete (as above)
//	GET    /v1/…/operations/{op}                      — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response`, so an SDK or Terraform LRO wait
// terminates on the first poll instead of hanging.
//
// Location-scoped operations: Backup for GKE's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The backupPlans/restorePlans resource-type guard keeps
// this handler disjoint from every other /v1/projects/ handler (Composer's
// environments, Cloud Deploy's pipelines/targets, Certificate Manager's
// certificates, …).
package gkebackup

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	gkbdriver "github.com/stackshy/cloudemu/v2/services/gkebackup/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	backupPlansColl  = "backupPlans"
	restorePlansColl = "restorePlans"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, uid, etag, state, createTime, updateTime).
	minComputedFields = 6

	backupPlanTypeURL  = "type.googleapis.com/google.cloud.gkebackup.v1.BackupPlan"
	restorePlanTypeURL = "type.googleapis.com/google.cloud.gkebackup.v1.RestorePlan"
)

// collection describes one Backup for GKE resource collection, binding its wire
// identity to the driver methods that back it. The two collections share every
// CRUD code path, differing only in these bound values and a small set of
// computed-field hooks.
type collection struct {
	seg     string // "backupPlans" | "restorePlans"
	idParam string // "backupPlanId" | "restorePlanId"
	typeURL string

	// extraComputed injects collection-specific output-only body values into a
	// rendered resource (a backupPlan's protectedPodCount/protectedNamespaceCount).
	// Nil where none applies.
	extraComputed func(map[string]json.RawMessage)

	create func(context.Context, *gkbdriver.Config) (*gkbdriver.Resource, *gkbdriver.Operation, error)
	get    func(context.Context, string, string, string) (*gkbdriver.Resource, error)
	list   func(context.Context, string, string) ([]gkbdriver.Resource, error)
	patch  func(context.Context, *gkbdriver.Config, []string) (*gkbdriver.Resource, *gkbdriver.Operation, error)
	del    func(context.Context, string, string, string) (*gkbdriver.Operation, error)
}

// Handler serves gkebackup.googleapis.com v1 requests against a GKEBackup driver.
type Handler struct {
	db gkbdriver.GKEBackup

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Backup for GKE handler backed by db.
func New(db gkbdriver.GKEBackup) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// collections binds the two resource collections to h's driver method values,
// keyed by their path segment.
func (h *Handler) collections() map[string]*collection {
	return map[string]*collection{
		backupPlansColl: {
			seg: backupPlansColl, idParam: "backupPlanId", typeURL: backupPlanTypeURL,
			extraComputed: seedBackupPlanCounts,
			create:        h.db.CreateBackupPlan, get: h.db.GetBackupPlan, list: h.db.ListBackupPlans,
			patch: h.db.PatchBackupPlan, del: h.db.DeleteBackupPlan,
		},
		restorePlansColl: {
			seg: restorePlansColl, idParam: "restorePlanId", typeURL: restorePlanTypeURL,
			create: h.db.CreateRestorePlan, get: h.db.GetRestorePlan, list: h.db.ListRestorePlans,
			patch: h.db.PatchRestorePlan, del: h.db.DeleteRestorePlan,
		},
	}
}

// route holds the parsed components of a Backup for GKE v1 path.
type route struct {
	project  string
	location string
	resource string // "backupPlans" | "restorePlans" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a Backup for GKE v1 path. It recognizes
// only the backupPlans, restorePlans, and operations resources under a locations
// scope.
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
	return seg == backupPlansColl || seg == restorePlansColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{backupPlans|restorePlans|
// operations}[/…] paths. The resource-segment guard keeps it disjoint from the
// other /v1/projects/ handlers. An operations path is claimed only when this
// handler has no shared LRO registry (a standalone package server); in an
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
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Backup for GKE path")
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
