// Package gkehub implements the GKE Hub / Fleet control plane
// (gkehub.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/gkehub/v1 clients, gcloud, and the Terraform google
// provider's google_gke_hub_membership, google_gke_hub_feature, and
// google_gke_hub_fleet resources — all GA on the stable hashicorp/google
// provider at the /v1/ base path — hit this handler unchanged.
//
// Coverage (membership + feature + fleet control plane only):
//
//	POST   /v1/…/memberships?membershipId=  — CreateMembership (LRO)
//	GET    /v1/…/memberships                — ListMemberships
//	GET    /v1/…/memberships/{id}           — GetMembership
//	PATCH  /v1/…/memberships/{id}?updateMask= — PatchMembership (LRO)
//	DELETE /v1/…/memberships/{id}           — DeleteMembership (LRO)
//	POST   /v1/…/features?featureId=        — CreateFeature (LRO)
//	GET    /v1/…/features                   — ListFeatures
//	GET    /v1/…/features/{id}              — GetFeature
//	PATCH  /v1/…/features/{id}?updateMask=  — PatchFeature (LRO)
//	DELETE /v1/…/features/{id}              — DeleteFeature (LRO)
//	POST   /v1/…/fleets                     — CreateFleet (LRO, singleton "default")
//	GET    /v1/…/fleets                     — ListFleets
//	GET    /v1/…/fleets/{id}               — GetFleet
//	PATCH  /v1/…/fleets/{id}?updateMask=    — PatchFleet (LRO)
//	DELETE /v1/…/fleets/{id}               — DeleteFleet (LRO)
//	GET    /v1/…/operations/{op}            — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response`, so an SDK or Terraform LRO wait
// terminates on the first poll instead of hanging.
//
// Location-scoped operations: GKE Hub's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The memberships/features/fleets resource-type guard
// keeps this handler disjoint from every other /v1/projects/ handler.
package gkehub

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	gdriver "github.com/stackshy/cloudemu/v2/services/gkehub/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	membershipsColl  = "memberships"
	featuresColl     = "features"
	fleetsColl       = "fleets"
	defaultFleetID   = "default"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, createTime, updateTime + the per-collection
	// state/uid block).
	minComputedFields = 5

	membershipTypeURL = "type.googleapis.com/google.cloud.gkehub.v1.Membership"
	featureTypeURL    = "type.googleapis.com/google.cloud.gkehub.v1.Feature"
	fleetTypeURL      = "type.googleapis.com/google.cloud.gkehub.v1.Fleet"
)

// collection describes one GKE Hub resource collection, binding its wire
// identity to the driver methods that back it. The three collections share every
// CRUD code path, differing only in these bound values. The create/get/list/
// patch/del fields are the driver method values, whose signatures the three
// resource collections share verbatim.
type collection struct {
	seg       string   // "memberships" | "features" | "fleets"
	listKey   string   // JSON array key of the List response ("resources" | "fleets")
	idParams  []string // accepted create id query params (camel + snake); nil for the singleton fleet
	defaultID string   // id used when no idParam is supplied ("default" for the singleton fleet)
	typeURL   string
	seed      func(m map[string]json.RawMessage, uid string) // inject the per-collection computed fields

	create func(context.Context, *gdriver.Config) (*gdriver.Resource, *gdriver.Operation, error)
	get    func(context.Context, string, string, string) (*gdriver.Resource, error)
	list   func(context.Context, string, string) ([]gdriver.Resource, error)
	patch  func(context.Context, *gdriver.Config, []string) (*gdriver.Resource, *gdriver.Operation, error)
	del    func(context.Context, string, string, string) (*gdriver.Operation, error)
}

// Handler serves gkehub.googleapis.com v1 requests against a GKE Hub driver.
type Handler struct {
	db gdriver.GKEHub

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a GKE Hub handler backed by db.
func New(db gdriver.GKEHub) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// collections binds the three resource collections to h's driver method values,
// keyed by their path segment.
func (h *Handler) collections() map[string]*collection {
	return map[string]*collection{
		membershipsColl: {
			seg: membershipsColl, listKey: "resources", idParams: []string{"membershipId", "membership_id"},
			typeURL: membershipTypeURL, seed: seedMembership,
			create: h.db.CreateMembership, get: h.db.GetMembership, list: h.db.ListMemberships,
			patch: h.db.PatchMembership, del: h.db.DeleteMembership,
		},
		featuresColl: {
			seg: featuresColl, listKey: "resources", idParams: []string{"featureId", "feature_id"},
			typeURL: featureTypeURL, seed: seedFeature,
			create: h.db.CreateFeature, get: h.db.GetFeature, list: h.db.ListFeatures,
			patch: h.db.PatchFeature, del: h.db.DeleteFeature,
		},
		fleetsColl: {
			seg: fleetsColl, listKey: fleetsColl, idParams: nil, defaultID: defaultFleetID,
			typeURL: fleetTypeURL, seed: seedFleet,
			create: h.db.CreateFleet, get: h.db.GetFleet, list: h.db.ListFleets,
			patch: h.db.PatchFleet, del: h.db.DeleteFleet,
		},
	}
}

// route holds the parsed components of a GKE Hub v1 path.
type route struct {
	project  string
	location string
	resource string // "memberships" | "features" | "fleets" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a GKE Hub v1 path. It recognizes only
// the memberships, features, fleets, and operations resources under a locations
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
	return seg == membershipsColl || seg == featuresColl || seg == fleetsColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{memberships|features|fleets|
// operations}[/…] paths. The memberships/features/fleets guard keeps it disjoint
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
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized GKE Hub path")
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
