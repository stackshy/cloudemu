// Package networkconnectivity implements the Google Network Connectivity Center
// control plane (networkconnectivity.googleapis.com/v1) as a server.Handler.
// Real google.golang.org/api/networkconnectivity/v1 clients, gcloud, and the
// Terraform google provider's google_network_connectivity_hub and
// google_network_connectivity_spoke resources hit this handler unchanged.
//
// Coverage (hub + spoke control plane only):
//
//	POST   /v1/…/global/hubs?hubId=              — CreateHub (LRO)
//	GET    /v1/…/global/hubs                      — ListHubs
//	GET    /v1/…/global/hubs/{id}                 — GetHub
//	PATCH  /v1/…/global/hubs/{id}?updateMask=     — PatchHub (LRO)
//	DELETE /v1/…/global/hubs/{id}                 — DeleteHub (LRO)
//	POST   /v1/…/{loc}/spokes?spokeId=            — CreateSpoke (LRO)
//	GET    /v1/…/{loc}/spokes                      — ListSpokes
//	GET    /v1/…/{loc}/spokes/{id}                 — GetSpoke
//	PATCH  /v1/…/{loc}/spokes/{id}?updateMask=     — PatchSpoke (LRO)
//	DELETE /v1/…/{loc}/spokes/{id}                 — DeleteSpoke (LRO)
//	GET    /v1/…/operations/{op}                   — Operations.Get (shared poller)
//
// Hubs are global (location "global"); spokes are regional. Both share the same
// /v1/projects/{p}/locations/{l}/{collection} path shape, so a single route
// parser serves both. Every mutating RPC returns a google.longrunning.Operation
// with done=true and the resulting resource embedded in `response`, so an SDK or
// Terraform LRO wait terminates on the first poll instead of hanging.
//
// Location-scoped operations live under /v1/projects/{p}/locations/{l}/
// operations — the SAME space the shared GCP LRO poller owns. Matches returns
// false for operation paths when a shared registry is wired, letting that poller
// win; a standalone package server (no registry) serves its own polls. The
// hubs/spokes resource-type guard keeps this handler disjoint from every other
// /v1/projects/ handler.
package networkconnectivity

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	nccdriver "github.com/stackshy/cloudemu/v2/services/networkconnectivity/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	hubsColl         = "hubs"
	spokesColl       = "spokes"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, uniqueId, state, createTime, updateTime).
	minComputedFields = 5

	hubTypeURL   = "type.googleapis.com/google.cloud.networkconnectivity.v1.Hub"
	spokeTypeURL = "type.googleapis.com/google.cloud.networkconnectivity.v1.Spoke"
)

// collection describes one Network Connectivity Center resource collection,
// binding its wire identity to the driver methods that back it. The two
// collections share every CRUD code path, differing only in these bound values.
type collection struct {
	seg     string // "hubs" | "spokes"
	idParam string // "hubId" | "spokeId"
	typeURL string

	// validateCreate, if set, rejects a malformed create body before it reaches
	// the driver (a spoke's exactly-one linked_* oneof).
	validateCreate func(map[string]jsonRaw) error

	create func(context.Context, *nccdriver.Config) (*nccdriver.Resource, *nccdriver.Operation, error)
	get    func(context.Context, string, string, string) (*nccdriver.Resource, error)
	list   func(context.Context, string, string) ([]nccdriver.Resource, error)
	patch  func(context.Context, *nccdriver.Config, []string) (*nccdriver.Resource, *nccdriver.Operation, error)
	del    func(context.Context, string, string, string) (*nccdriver.Operation, error)
}

// Handler serves networkconnectivity.googleapis.com v1 requests against a
// NetworkConnectivity driver.
type Handler struct {
	db nccdriver.NetworkConnectivity

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Network Connectivity Center handler backed by db.
func New(db nccdriver.NetworkConnectivity) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// collections binds the two resource collections to h's driver method values,
// keyed by their path segment.
func (h *Handler) collections() map[string]*collection {
	return map[string]*collection{
		hubsColl: {
			seg: hubsColl, idParam: "hubId", typeURL: hubTypeURL,
			create: h.db.CreateHub, get: h.db.GetHub, list: h.db.ListHubs,
			patch: h.db.PatchHub, del: h.db.DeleteHub,
		},
		spokesColl: {
			seg: spokesColl, idParam: "spokeId", typeURL: spokeTypeURL, validateCreate: validateSpokeLink,
			create: h.db.CreateSpoke, get: h.db.GetSpoke, list: h.db.ListSpokes,
			patch: h.db.PatchSpoke, del: h.db.DeleteSpoke,
		},
	}
}

// route holds the parsed components of a Network Connectivity Center v1 path.
type route struct {
	project  string
	location string
	resource string // "hubs" | "spokes" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a Network Connectivity Center v1 path. It
// recognizes only the hubs, spokes, and operations resources under a locations
// scope (hubs use location "global"; spokes a region).
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
	return seg == hubsColl || seg == spokesColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{hubs|spokes|operations}[/…]
// paths. The hubs/spokes guard keeps it disjoint from the other /v1/projects/
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
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Network Connectivity Center path")
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
