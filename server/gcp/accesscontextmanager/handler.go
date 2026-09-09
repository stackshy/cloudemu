// Package accesscontextmanager implements the Google Access Context Manager
// control plane (accesscontextmanager.googleapis.com/v1) as a server.Handler.
// Real google.golang.org/api/accesscontextmanager/v1 clients, gcloud, and the
// Terraform google provider's google_access_context_manager_access_policy,
// google_access_context_manager_access_level, and
// google_access_context_manager_service_perimeter resources hit this handler
// unchanged.
//
// Coverage (VPC Service Controls control plane):
//
//	POST   /v1/accessPolicies                                    — CreatePolicy (LRO)
//	GET    /v1/accessPolicies?parent=organizations/{org}         — ListPolicies
//	GET    /v1/accessPolicies/{p}                                — GetPolicy
//	PATCH  /v1/accessPolicies/{p}?updateMask=                    — PatchPolicy (LRO)
//	DELETE /v1/accessPolicies/{p}                                — DeletePolicy (LRO)
//	POST   /v1/accessPolicies/{p}/accessLevels?accessLevelId=    — CreateAccessLevel (LRO)
//	…                                                            — Get/List/Patch/Delete
//	POST   /v1/accessPolicies/{p}/servicePerimeters?servicePerimeterId= — CreateServicePerimeter (LRO)
//	…                                                            — Get/List/Patch/Delete
//	GET    /v1/operations/{id}                                   — Operations.Get
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response` as an Any, so an SDK or
// Terraform LRO wait terminates on the first poll.
//
// Organization-scoped routing: unlike every /v1/projects/ handler, Access
// Context Manager's resources are rooted at /v1/accessPolicies and its
// operations at the service root /v1/operations/{id} — NOT under
// projects/locations. The shared GCP LRO poller owns only /v1/projects/.../
// operations, so it never sees these root operations. Cloud Functions gen1
// also mints root /v1/operations/{id} names, so this handler claims a root
// operation path only when it minted that operation (HasOperation), yielding
// every other root operation to its sibling; it must be registered ahead of
// Cloud Functions for that first-look to hold.
package accesscontextmanager

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	acmdriver "github.com/stackshy/cloudemu/v2/services/accesscontextmanager/driver"
)

const (
	v1Prefix              = "/v1/"
	accessPoliciesSeg     = "accessPolicies"
	accessLevelsColl      = "accessLevels"
	servicePerimetersColl = "servicePerimeters"
	operationsSeg         = "operations"

	// minComputedFields is the extra map capacity reserved for injected computed
	// output fields (name, etag, createTime, updateTime).
	minComputedFields = 4
)

// childColl binds one nested resource collection (access levels or service
// perimeters) to the driver methods that back it. The two collections share
// every CRUD code path, differing only in these bound values.
type childColl struct {
	seg     string // "accessLevels" | "servicePerimeters"
	idParam string // "accessLevelId" | "servicePerimeterId"
	typeURL string
	kind    string // "accessLevel" | "servicePerimeter"
	emitTS  bool   // whether createTime/updateTime are surfaced on the wire

	create func(context.Context, *acmdriver.ChildConfig) (*acmdriver.Child, *acmdriver.Operation, error)
	get    func(context.Context, string, string) (*acmdriver.Child, error)
	list   func(context.Context, string) ([]acmdriver.Child, error)
	patch  func(context.Context, *acmdriver.ChildConfig, []string) (*acmdriver.Child, *acmdriver.Operation, error)
	del    func(context.Context, string, string) (*acmdriver.Operation, error)
}

// Handler serves accesscontextmanager.googleapis.com v1 requests against an
// AccessContextManager driver.
type Handler struct {
	db acmdriver.AccessContextManager

	// ops records created operations with the shared poller so a project-scoped
	// sibling poll is never affected. Access Context Manager operations are root
	// scoped and served by this handler itself; the registry is wired only for
	// API parity with sibling handlers.
	ops *lro.Registry
}

// New returns an Access Context Manager handler backed by db.
func New(db acmdriver.AccessContextManager) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller for API parity with sibling
// GCP handlers. Access Context Manager operations are organization-scoped
// (/v1/operations/{id}) and resolved by this handler directly, so the registry
// is not consulted for them.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// childColls binds the two nested collections to h's driver method values, keyed
// by their path segment.
func (h *Handler) childColls() map[string]*childColl {
	return map[string]*childColl{
		accessLevelsColl: {
			seg: accessLevelsColl, idParam: "accessLevelId", typeURL: accessLevelTypeURL,
			kind: "accessLevel", emitTS: false,
			create: h.db.CreateAccessLevel, get: h.db.GetAccessLevel, list: h.db.ListAccessLevels,
			patch: h.db.PatchAccessLevel, del: h.db.DeleteAccessLevel,
		},
		servicePerimetersColl: {
			seg: servicePerimetersColl, idParam: "servicePerimeterId", typeURL: servicePerimeterTypeURL,
			kind: "servicePerimeter", emitTS: true,
			create: h.db.CreateServicePerimeter, get: h.db.GetServicePerimeter, list: h.db.ListServicePerimeters,
			patch: h.db.PatchServicePerimeter, del: h.db.DeleteServicePerimeter,
		},
	}
}

// route holds the parsed components of an Access Context Manager v1 path.
type route struct {
	resource  string // accessPolicies | accessLevels | servicePerimeters | operations
	policyNum string // parent policy number, for a nested collection/item
	id        string // policy number, child id, or full operation name; "" for a collection
}

// parseRoute extracts the components of an Access Context Manager v1 path. It
// recognizes the accessPolicies grammar (and its accessLevels/servicePerimeters
// children) and the root operations grammar.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, v1Prefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, v1Prefix), "/")
	if len(parts) == 0 || parts[0] == "" {
		return route{}, false
	}

	if parts[0] == operationsSeg {
		if len(parts) < itemParts {
			return route{}, false
		}

		return route{resource: operationsSeg, id: strings.Join(parts, "/")}, true
	}

	if parts[0] != accessPoliciesSeg {
		return route{}, false
	}

	return parsePolicyRoute(parts)
}

const (
	itemParts        = 2 // [operations, {id}] or [accessPolicies, {p}]
	childCollParts   = 3 // [accessPolicies, {p}, {coll}]
	childItemParts   = 4 // [accessPolicies, {p}, {coll}, {id}]
	policyNumberIdx  = 1
	childCollSegIdx  = 2
	childItemNameIdx = 3
)

// parsePolicyRoute resolves an accessPolicies-rooted path into a route.
func parsePolicyRoute(parts []string) (route, bool) {
	switch len(parts) {
	case 1:
		return route{resource: accessPoliciesSeg}, true
	case itemParts:
		return route{resource: accessPoliciesSeg, id: parts[policyNumberIdx]}, true
	case childCollParts:
		if !isChildColl(parts[childCollSegIdx]) {
			return route{}, false
		}

		return route{resource: parts[childCollSegIdx], policyNum: parts[policyNumberIdx]}, true
	case childItemParts:
		if !isChildColl(parts[childCollSegIdx]) {
			return route{}, false
		}

		return route{
			resource: parts[childCollSegIdx], policyNum: parts[policyNumberIdx], id: parts[childItemNameIdx],
		}, true
	default:
		return route{}, false
	}
}

// isChildColl reports whether seg names a nested collection this handler serves.
func isChildColl(seg string) bool {
	return seg == accessLevelsColl || seg == servicePerimetersColl
}

// Matches claims /v1/accessPolicies[/…] paths (always this handler's) and a
// root /v1/operations/{id} path only when this backend minted that operation,
// so Cloud Functions gen1's root operations fall through to it.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	if rt.resource == operationsSeg {
		return h.db.HasOperation(r.Context(), rt.id)
	}

	return true
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Access Context Manager path")
		return
	}

	switch rt.resource {
	case operationsSeg:
		h.serveOperation(w, r, rt)
	case accessPoliciesSeg:
		h.servePolicy(w, r, rt)
	default:
		h.serveChild(w, r, rt, h.childColls()[rt.resource])
	}
}

// servePolicy dispatches access-policy requests.
func (h *Handler) servePolicy(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.id == "" {
		switch r.Method {
		case http.MethodPost:
			h.createPolicy(w, r)
		case http.MethodGet:
			h.listPolicies(w, r)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getPolicy(w, r, rt)
	case http.MethodPatch:
		h.patchPolicy(w, r, rt)
	case http.MethodDelete:
		h.deletePolicy(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveChild dispatches access-level and service-perimeter requests.
func (h *Handler) serveChild(w http.ResponseWriter, r *http.Request, rt route, col *childColl) {
	if col == nil {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported resource: "+rt.resource)
		return
	}

	if rt.id == "" {
		switch r.Method {
		case http.MethodPost:
			h.createChild(w, r, rt, col)
		case http.MethodGet:
			h.listChildren(w, r, rt, col)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getChild(w, r, rt, col)
	case http.MethodPatch:
		h.patchChild(w, r, rt, col)
	case http.MethodDelete:
		h.deleteChild(w, r, rt, col)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
