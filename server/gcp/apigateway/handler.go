// Package apigateway implements the Google API Gateway control plane
// (apigateway.googleapis.com) as a server.Handler on BOTH the /v1beta/ and /v1/
// version prefixes.
//
// API Gateway's Terraform resources — google_api_gateway_api,
// google_api_gateway_api_config, google_api_gateway_gateway — exist ONLY in the
// terraform-provider-google-beta provider, whose default base path is
// apigateway.googleapis.com/v1beta/. A real google.golang.org/api/apigateway/v1
// client and gcloud use /v1/. Both hit this handler unchanged; the version is
// carried through so a completed operation's response @type reports the matching
// proto package (google.cloud.apigateway.v1 vs …v1beta).
//
// Coverage (api + api-config + gateway control plane):
//
//	POST   /{v}/…/locations/global/apis?apiId=                 — CreateApi (LRO)
//	GET    /{v}/…/locations/global/apis                        — ListApis
//	GET    /{v}/…/locations/global/apis/{a}                    — GetApi
//	PATCH  /{v}/…/locations/global/apis/{a}?updateMask=        — PatchApi (LRO)
//	DELETE /{v}/…/locations/global/apis/{a}                    — DeleteApi (LRO, cascades to configs)
//	POST   /{v}/…/apis/{a}/configs?apiConfigId=                — CreateApiConfig (LRO)
//	…                                                          — Get/List/Patch/Delete
//	POST   /{v}/…/locations/{r}/gateways?gatewayId=            — CreateGateway (LRO)
//	…                                                          — Get/List/Patch/Delete
//	GET    /{v}/…/locations/{l}/operations/{op}                — Operations.Get
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response`, so an SDK or Terraform LRO wait
// terminates on the first poll instead of hanging.
//
// Location-scoped operations: the shared GCP LRO poller owns only the /v1/
// operations space. The google-beta provider polls operations at its /v1beta/
// base path, which the shared poller does NOT match — so this handler always
// serves its own /v1beta/ operation polls, and yields the /v1/ ones to the
// shared poller when one is wired (a standalone package server serves its own).
// The apis/gateways/operations resource-type guard keeps this handler disjoint
// from every other /v1/projects/ handler.
package apigateway

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	agdriver "github.com/stackshy/cloudemu/v2/services/apigatewaygcp/driver"
)

const (
	apiV1     = "v1"
	apiV1Beta = "v1beta"

	projectsSeg   = "projects"
	locationsSeg  = "locations"
	apisSeg       = "apis"
	configsSeg    = "configs"
	gatewaysSeg   = "gateways"
	operationsSeg = "operations"

	minParts = 4 // [projects, {p}, locations, {loc}]
)

// levelKind identifies which resource a path addresses.
type levelKind int

const (
	levelAPI levelKind = iota
	levelConfig
	levelGateway
	levelOperation
)

// Handler serves apigateway.googleapis.com requests against an APIGateway driver.
type Handler struct {
	db agdriver.APIGateway

	// ops records created operations with the shared poller so a /v1/ SDK poll
	// resolves the typed response (and unknown names 404). Nil in a standalone
	// package server, where this handler serves its own /v1/ operation poll.
	ops *lro.Registry
}

// New returns an API Gateway handler backed by db.
func New(db agdriver.APIGateway) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's /v1/ operations
// host. The /v1beta/ operation space is always served by this handler.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of an API Gateway path.
type route struct {
	version  string
	project  string
	location string
	api      string // parent api id for a config path
	level    levelKind
	name     string // deepest-level id; empty for a collection request
}

// parseRoute extracts the components of an API Gateway path on either the
// /v1beta/ or /v1/ version prefix.
func parseRoute(urlPath string) (route, bool) {
	version, rest, ok := splitVersion(urlPath)
	if !ok {
		return route{}, false
	}

	parts := strings.Split(rest, "/")
	if len(parts) < minParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rt := route{version: version, project: parts[1], location: parts[3]}
	if !parseTail(&rt, parts[minParts:]) {
		return route{}, false
	}

	return rt, true
}

// splitVersion strips a leading /v1beta/ or /v1/ version segment from a
// projects-scoped path, returning the version and the remainder. The two
// prefixes are disjoint, so match order is irrelevant.
func splitVersion(urlPath string) (version, rest string, ok bool) {
	for _, v := range [...]string{apiV1Beta, apiV1} {
		prefix := "/" + v + "/" + projectsSeg + "/"
		if strings.HasPrefix(urlPath, prefix) {
			return v, strings.TrimPrefix(urlPath, "/"+v+"/"), true
		}
	}

	return "", "", false
}

// parseTail folds the segments after the locations scope into rt: an apis
// hierarchy (apis[/{a}[/configs[/{c}]]]), a gateways collection/item, or an
// operations item. It reports whether the path is a valid API Gateway shape.
func parseTail(rt *route, rest []string) bool {
	if len(rest) == 0 {
		return false
	}

	switch rest[0] {
	case apisSeg:
		return parseAPITail(rt, rest[1:])
	case gatewaysSeg:
		rt.level = levelGateway

		return parseFlatTail(rt, rest[1:])
	case operationsSeg:
		rt.level = levelOperation

		return parseFlatTail(rt, rest[1:])
	default:
		return false
	}
}

// parseAPITail consumes the segments after "apis": optional {api}, then an
// optional ("configs"[, {config}]) nesting.
func parseAPITail(rt *route, rest []string) bool {
	rt.level = levelAPI

	if len(rest) == 0 {
		return true // apis collection
	}

	rt.api, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // api item
	}

	if rest[0] != configsSeg {
		return false
	}

	rt.level, rt.name, rest = levelConfig, "", rest[1:]
	if len(rest) == 0 {
		return true // configs collection
	}

	rt.name, rest = rest[0], rest[1:]

	return len(rest) == 0 // config item, else trailing junk
}

// parseFlatTail consumes an optional single id segment for a flat collection
// (gateways, operations).
func parseFlatTail(rt *route, rest []string) bool {
	if len(rest) == 0 {
		return true // collection
	}

	rt.name, rest = rest[0], rest[1:]

	return len(rest) == 0 // item, else trailing junk
}

// Matches claims the apis/gateways/operations shapes on both version prefixes.
// An operations path on /v1/ is yielded to the shared LRO poller when one is
// wired; a /v1beta/ operations path is always claimed here, because the shared
// poller only serves the /v1/ operations space.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	if rt.level == levelOperation && rt.version == apiV1 && h.ops != nil {
		return false
	}

	return true
}

// ServeHTTP routes on the parsed path level and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized API Gateway path")
		return
	}

	if rt.level == levelOperation {
		h.serveOperation(w, r, &rt)
		return
	}

	col := h.collections()[rt.level]
	if rt.name == "" {
		h.serveCollection(w, r, &rt, col)
		return
	}

	h.serveItem(w, r, &rt, col)
}

// serveCollection dispatches collection-level requests (create, list).
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt *route, col *collection) {
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
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, rt *route, col *collection) {
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

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
