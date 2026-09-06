// Package servicedirectory implements the Google Cloud Service Directory control
// plane (servicedirectory.googleapis.com) as a server.Handler on both the /v1/
// and /v1beta1/ version prefixes. Real google.golang.org/api/servicedirectory/v1
// clients and gcloud use /v1/; the Terraform google-beta provider's
// google_service_directory_{namespace,service,endpoint} resources exist only in
// google-beta and default to /v1beta1/. Both hit this handler unchanged. The two
// versions differ only in the field name of the service/endpoint string map —
// `annotations` in v1, `metadata` in v1beta1 — which the handler decodes from
// either key and re-emits under the name matching the request's version.
//
// Coverage (registration control plane only, synchronous REST — no LRO):
//
//	POST   /v1/…/namespaces?namespaceId=                       — CreateNamespace
//	GET    /v1/…/namespaces                                    — ListNamespaces
//	GET    /v1/…/namespaces/{ns}                               — GetNamespace
//	PATCH  /v1/…/namespaces/{ns}?updateMask=                   — PatchNamespace
//	DELETE /v1/…/namespaces/{ns}                               — DeleteNamespace
//	POST   /v1/…/namespaces/{ns}/services?serviceId=           — CreateService
//	GET    /v1/…/namespaces/{ns}/services                      — ListServices
//	GET    /v1/…/namespaces/{ns}/services/{svc}                — GetService
//	PATCH  /v1/…/namespaces/{ns}/services/{svc}?updateMask=    — PatchService
//	DELETE /v1/…/namespaces/{ns}/services/{svc}                — DeleteService
//	POST   /v1/…/services/{svc}/endpoints?endpointId=          — CreateEndpoint
//	GET    /v1/…/services/{svc}/endpoints                      — ListEndpoints
//	GET    /v1/…/services/{svc}/endpoints/{ep}                 — GetEndpoint
//	PATCH  /v1/…/services/{svc}/endpoints/{ep}?updateMask=     — PatchEndpoint
//	DELETE /v1/…/services/{svc}/endpoints/{ep}                 — DeleteEndpoint
//
// Every RPC returns the resource (or an empty object for delete) directly with
// no google.longrunning.Operation wrapper. Deleting a parent cascades to its
// descendants in the driver. The namespaces resource-segment guard keeps this
// handler disjoint from every other /v1/projects/ handler.
package servicedirectory

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	sddriver "github.com/stackshy/cloudemu/v2/services/servicedirectory/driver"
)

const (
	// apiV1 and apiV1Beta1 are the two API versions this handler serves. The
	// google_service_directory_* Terraform resources exist only in the
	// google-beta provider, whose default base path is
	// servicedirectory.googleapis.com/v1beta1/; real
	// google.golang.org/api/servicedirectory/v1 clients and gcloud use /v1/.
	apiV1      = "v1"
	apiV1Beta1 = "v1beta1"

	projectsSeg   = "projects"
	locationsSeg  = "locations"
	namespacesSeg = "namespaces"
	servicesSeg   = "services"
	endpointsSeg  = "endpoints"

	minParts = 5 // [projects, {p}, locations, {loc}, namespaces]
)

// levelKind identifies which of the three nested resources a path addresses.
type levelKind int

const (
	levelNamespace levelKind = iota
	levelService
	levelEndpoint
)

// Handler serves servicedirectory.googleapis.com v1 requests against a
// ServiceDirectory driver.
type Handler struct {
	db sddriver.ServiceDirectory
}

// New returns a Service Directory handler backed by db.
func New(db sddriver.ServiceDirectory) *Handler { return &Handler{db: db} }

// route holds the parsed components of a Service Directory path. version is the
// API version the request arrived on ("v1" or "v1beta1"), which selects the
// wire field name for the service/endpoint string map. ns/svc/ep hold the
// addressed ids; name is the id at the deepest level, empty for a collection
// request.
type route struct {
	version  string
	project  string
	location string
	ns       string
	svc      string
	ep       string
	level    levelKind
	name     string
}

// parseRoute extracts the components of a Service Directory path. It accepts the
// namespaces → services → endpoints hierarchy under a locations scope on either
// the /v1/ or /v1beta1/ version prefix.
func parseRoute(urlPath string) (route, bool) {
	version, rest, ok := splitVersion(urlPath)
	if !ok {
		return route{}, false
	}

	parts := strings.Split(rest, "/")
	if len(parts) < minParts || parts[0] != projectsSeg || parts[2] != locationsSeg || parts[4] != namespacesSeg {
		return route{}, false
	}

	rt := route{version: version, project: parts[1], location: parts[3], level: levelNamespace}
	if !parseHierarchy(&rt, parts[5:]) {
		return route{}, false
	}

	return rt, true
}

// splitVersion strips a leading /v1/ or /v1beta1/ version segment from a
// projects-scoped path, returning the version and the remainder. It reports
// false for any other prefix. The two version prefixes are disjoint, so match
// order is irrelevant.
func splitVersion(urlPath string) (version, rest string, ok bool) {
	for _, v := range [...]string{apiV1, apiV1Beta1} {
		prefix := "/" + v + "/" + projectsSeg + "/"
		if strings.HasPrefix(urlPath, prefix) {
			return v, strings.TrimPrefix(urlPath, "/"+v+"/"), true
		}
	}

	return "", "", false
}

// parseHierarchy folds the segments after "namespaces" into rt by consuming
// them left to right: [ns] optionally followed by ("services", svc) optionally
// followed by ("endpoints", ep). name tracks the id at the deepest level and is
// cleared when a collection keyword is the last segment. It reports whether the
// path is a valid Service Directory hierarchy; any leftover or mis-keyworded
// segment fails.
func parseHierarchy(rt *route, rest []string) bool {
	if len(rest) == 0 {
		return true // namespaces collection
	}

	rt.ns, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // namespace item
	}

	if rest[0] != servicesSeg {
		return false
	}

	rt.level, rt.name, rest = levelService, "", rest[1:]
	if len(rest) == 0 {
		return true // services collection
	}

	rt.svc, rt.name, rest = rest[0], rest[0], rest[1:]
	if len(rest) == 0 {
		return true // service item
	}

	return parseEndpointTail(rt, rest)
}

// parseEndpointTail consumes the trailing ("endpoints"[, ep]) of a path whose
// namespace and service segments are already parsed into rt.
func parseEndpointTail(rt *route, rest []string) bool {
	if rest[0] != endpointsSeg {
		return false
	}

	rt.level, rt.name, rest = levelEndpoint, "", rest[1:]
	if len(rest) == 0 {
		return true // endpoints collection
	}

	rt.ep, rt.name, rest = rest[0], rest[0], rest[1:]

	return len(rest) == 0 // endpoint item, else trailing junk
}

// Matches claims the Service Directory namespaces/services/endpoints hierarchy.
// The namespaces resource-segment guard keeps it disjoint from every other
// /v1/projects/ handler.
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)

	return ok
}

// rpcSet bundles the five collection/item handlers for one resource level so a
// single dispatch can route by method without repeating the switch per level.
type rpcSet struct {
	create func(http.ResponseWriter, *http.Request, *route)
	list   func(http.ResponseWriter, *http.Request, *route)
	get    func(http.ResponseWriter, *http.Request, *route)
	patch  func(http.ResponseWriter, *http.Request, *route)
	del    func(http.ResponseWriter, *http.Request, *route)
}

// ServeHTTP routes on the parsed path level and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Service Directory path")
		return
	}

	switch rt.level {
	case levelNamespace:
		dispatch(w, r, &rt, rpcSet{h.createNamespace, h.listNamespaces, h.getNamespace, h.patchNamespace, h.deleteNamespace})
	case levelService:
		dispatch(w, r, &rt, rpcSet{h.createService, h.listServices, h.getService, h.patchService, h.deleteService})
	case levelEndpoint:
		dispatch(w, r, &rt, rpcSet{h.createEndpoint, h.listEndpoints, h.getEndpoint, h.patchEndpoint, h.deleteEndpoint})
	}
}

// dispatch selects a handler from s by whether the path addresses a collection
// (no id) or an item, then by the request method.
func dispatch(w http.ResponseWriter, r *http.Request, rt *route, s rpcSet) {
	if rt.name == "" {
		dispatchCollection(w, r, rt, s)
		return
	}

	dispatchItem(w, r, rt, s)
}

// dispatchCollection routes a collection request (POST create, GET list).
func dispatchCollection(w http.ResponseWriter, r *http.Request, rt *route, s rpcSet) {
	switch r.Method {
	case http.MethodPost:
		s.create(w, r, rt)
	case http.MethodGet:
		s.list(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// dispatchItem routes an item request (GET get, PATCH patch, DELETE delete).
func dispatchItem(w http.ResponseWriter, r *http.Request, rt *route, s rpcSet) {
	switch r.Method {
	case http.MethodGet:
		s.get(w, r, rt)
	case http.MethodPatch:
		s.patch(w, r, rt)
	case http.MethodDelete:
		s.del(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
