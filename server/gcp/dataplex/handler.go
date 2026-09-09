// Package dataplex implements the Google Cloud Dataplex control plane
// (dataplex.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/dataplex/v1 clients, gcloud, and the Terraform google
// provider's google_dataplex_lake, google_dataplex_zone, and
// google_dataplex_asset resources (all GA in the stable hashicorp/google
// provider, which drives them at the default /v1/ base path) hit this handler
// unchanged.
//
// Coverage (the lake → zone → asset hierarchy, LRO-wrapped):
//
//	POST   /v1/…/lakes?lakeId=                                   — CreateLake (LRO)
//	GET    /v1/…/lakes                                           — ListLakes
//	GET    /v1/…/lakes/{lake}                                    — GetLake
//	PATCH  /v1/…/lakes/{lake}?updateMask=                        — PatchLake (LRO)
//	DELETE /v1/…/lakes/{lake}                                    — DeleteLake (LRO, cascades)
//	POST   /v1/…/lakes/{lake}/zones?zoneId=                      — CreateZone (LRO)
//	GET    /v1/…/lakes/{lake}/zones                              — ListZones
//	GET    /v1/…/lakes/{lake}/zones/{zone}                       — GetZone
//	PATCH  /v1/…/lakes/{lake}/zones/{zone}?updateMask=           — PatchZone (LRO)
//	DELETE /v1/…/lakes/{lake}/zones/{zone}                       — DeleteZone (LRO, cascades)
//	POST   /v1/…/zones/{zone}/assets?assetId=                    — CreateAsset (LRO)
//	GET    /v1/…/zones/{zone}/assets                             — ListAssets
//	GET    /v1/…/zones/{zone}/assets/{asset}                     — GetAsset
//	PATCH  /v1/…/zones/{zone}/assets/{asset}?updateMask=         — PatchAsset (LRO)
//	DELETE /v1/…/zones/{zone}/assets/{asset}                     — DeleteAsset (LRO)
//	GET    /v1/…/operations/{op}                                 — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response` as a typed Any, so an SDK or
// Terraform LRO wait terminates on the first poll instead of hanging. A zone
// create validates its parent lake exists; an asset create validates its parent
// lake and zone (404 otherwise). Deleting a lake cascades to its zones and their
// assets; deleting a zone cascades to its assets.
//
// Location-scoped operations: Dataplex's operations live under
// /v1/projects/{p}/locations/{l}/operations — the same space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server serves its own
// polls. The lakes resource-segment guard keeps this handler disjoint from every
// other /v1/projects/ handler (Composer's environments, Cloud Deploy's pipelines,
// Datastream's streams, Certificate Manager's certificates, Metastore's services,
// …).
package dataplex

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
)

const (
	pathPrefix    = "/v1/projects/"
	projectsSeg   = "projects"
	locationsSeg  = "locations"
	operationsSeg = "operations"
	lakesSeg      = "lakes"
	zonesSeg      = "zones"
	assetsSeg     = "assets"

	minParts = 5 // [projects, {p}, locations, {loc}, lakes|operations]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, uid, state, createTime, updateTime, status).
	minComputedFields = 8

	lakeTypeURL  = "type.googleapis.com/google.cloud.dataplex.v1.Lake"
	zoneTypeURL  = "type.googleapis.com/google.cloud.dataplex.v1.Zone"
	assetTypeURL = "type.googleapis.com/google.cloud.dataplex.v1.Asset"
)

// levelKind identifies which of the three nested resources (or the operations
// space) a path addresses.
type levelKind int

const (
	levelLake levelKind = iota
	levelZone
	levelAsset
	levelOperation
)

// level binds one resource level's wire identity to the driver methods and
// computed-field hooks that back it. The three levels share every CRUD code path,
// differing only in these bound values.
type level struct {
	seg     string // "lakes" | "zones" | "assets"
	idParam string // "lakeId" | "zoneId" | "assetId"
	typeURL string

	// validate rejects a create/patch body that violates a level invariant (a
	// zone type/location_type enum, an asset resource_spec.type enum). Nil where
	// none applies.
	validate func(fields map[string]json.RawMessage) error

	// injectComputed writes the level's output-only fields (uid, state, the status
	// blocks, a lake's service_account) into a rendered resource so every read is
	// stable.
	injectComputed func(rt *route, r *dpdriver.Resource, m map[string]json.RawMessage)

	create func(context.Context, *dpdriver.Config) (*dpdriver.Resource, *dpdriver.Operation, error)
	get    func(context.Context, *route) (*dpdriver.Resource, error)
	list   func(context.Context, *route) ([]dpdriver.Resource, error)
	patch  func(context.Context, *dpdriver.Config, []string) (*dpdriver.Resource, *dpdriver.Operation, error)
	del    func(context.Context, *route) (*dpdriver.Operation, error)
}

// Handler serves dataplex.googleapis.com v1 requests against a Dataplex driver.
type Handler struct {
	db dpdriver.Dataplex

	// ops records created operations with the shared poller so a client that polls
	// the returned operation name gets the typed response (and unknown names 404).
	// Nil in a standalone package server, where this handler serves its own
	// /operations/ poll.
	ops *lro.Registry
}

// New returns a Dataplex handler backed by db.
func New(db dpdriver.Dataplex) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a Dataplex v1 path. lake/zone/asset hold
// the addressed ids; name is the id at the deepest level, empty for a collection
// request. For an operations path, name holds the operation id.
type route struct {
	project  string
	location string
	lake     string
	zone     string
	asset    string
	level    levelKind
	name     string
}

// parseRoute extracts the components of a Dataplex v1 path. It accepts the
// lakes → zones → assets hierarchy and the operations space under a locations
// scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}
	rest := parts[4:]

	switch rest[0] {
	case operationsSeg:
		return parseOperations(&rt, rest)
	case lakesSeg:
		if parseHierarchy(&rt, rest[1:]) {
			return rt, true
		}
	}

	return route{}, false
}

// itemTail is the length of a [collection, id] path tail.
const itemTail = 2

// parseOperations folds an operations path (operations[/{op}]) into rt.
func parseOperations(rt *route, rest []string) (route, bool) {
	rt.level = levelOperation

	switch len(rest) {
	case 1:
		return *rt, true // operations collection (unused, but well-formed)
	case itemTail:
		rt.name = rest[1]
		return *rt, true
	default:
		return route{}, false
	}
}

// parseHierarchy folds the segments after "lakes" into rt by consuming them left
// to right: [lake] optionally followed by ("zones", zone) optionally followed by
// ("assets", asset). name tracks the id at the deepest level and is cleared when
// a collection keyword is the last segment. It reports whether the path is a valid
// Dataplex hierarchy; any leftover or mis-keyworded segment fails.
func parseHierarchy(rt *route, rest []string) bool {
	rt.level = levelLake

	if len(rest) == 0 {
		return true // lakes collection
	}

	rt.lake, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // lake item
	}

	if rest[0] != zonesSeg {
		return false
	}

	rt.level, rt.name, rest = levelZone, "", rest[1:]
	if len(rest) == 0 {
		return true // zones collection
	}

	rt.zone, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // zone item
	}

	return parseAssetTail(rt, rest)
}

// parseAssetTail consumes the trailing ("assets"[, asset]) of a path whose lake
// and zone segments are already parsed into rt.
func parseAssetTail(rt *route, rest []string) bool {
	if rest[0] != assetsSeg {
		return false
	}

	rt.level, rt.name, rest = levelAsset, "", rest[1:]
	if len(rest) == 0 {
		return true // assets collection
	}

	rt.asset, rt.name, rest = rest[0], rest[0], rest[1:]

	return len(rest) == 0 // asset item, else trailing junk
}

// Matches claims the Dataplex lakes/zones/assets hierarchy and operations space.
// The lakes resource-segment guard keeps it disjoint from every other
// /v1/projects/ handler. An operations path is claimed only when this handler has
// no shared LRO registry (a standalone package server); in an assembled server
// the shared poller owns it.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	if rt.level == levelOperation && h.ops != nil {
		return false
	}

	return true
}

// ServeHTTP routes on the parsed path level and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Dataplex path")
		return
	}

	if rt.level == levelOperation {
		h.serveOperation(w, r)
		return
	}

	lvl := h.levels()[rt.level]
	if rt.name == "" {
		h.serveCollection(w, r, &rt, lvl)
		return
	}

	h.serveItem(w, r, &rt, lvl)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt *route, lvl *level) {
	switch r.Method {
	case http.MethodPost:
		h.createResource(w, r, rt, lvl)
	case http.MethodGet:
		h.listResources(w, r, rt, lvl)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveItem dispatches item-level requests (get, patch, delete).
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt *route, lvl *level) {
	switch r.Method {
	case http.MethodGet:
		h.getResource(w, r, rt, lvl)
	case http.MethodPatch:
		h.patchResource(w, r, rt, lvl)
	case http.MethodDelete:
		h.deleteResource(w, r, rt, lvl)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
