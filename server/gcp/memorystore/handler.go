// Package memorystore implements the GCP Memorystore for Redis
// (redis.googleapis.com) v1 REST API as a server.Handler. Real
// google.golang.org/api/redis/v1 clients pointed at this server manage Redis
// instances end-to-end against the shared cache driver's cluster control plane.
//
// Coverage (v1 REST):
//
//	POST   /v1/projects/{p}/locations/{l}/instances?instanceId={i}  — Create (LRO)
//	GET    /v1/projects/{p}/locations/{l}/instances/{i}             — Get
//	GET    /v1/projects/{p}/locations/{l}/instances                 — List
//	DELETE /v1/projects/{p}/locations/{l}/instances/{i}             — Delete (LRO)
//	GET    /v1/projects/{p}/locations/{l}/operations/{op}           — Operations.Get
//
// Mutating ops return a google.longrunning.Operation with done=true so SDK
// pollers terminate on the first response. The operation's `response` carries
// the Instance (Create) or an empty object (Delete).
//
// Matches claims /v1/projects/{p}/locations/{l}/{instances|operations}/... — a
// distinct sub-path within the /v1/projects/ family used by Firestore, IAM,
// Secret Manager, etc. Its {instances|operations} guard is disjoint from those
// (Cloud Functions uses functions/, GKE uses clusters/, …), so registration
// order relative to them is unconstrained. Registered before the permissive
// Firestore / GCS fallbacks so its paths aren't swallowed.
//
// Only the instance control plane is mapped — the real Memorystore SDK manages
// instances, not the Redis data plane. The driver's data-plane methods
// (Set/Get/Incr/…) have no cloud-SDK surface and are out of scope.
package memorystore

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	locationsSeg     = "locations"
	instancesSeg     = "instances"
	operationsSeg    = "operations"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
)

// Handler serves redis.googleapis.com v1 requests against a cache driver.
type Handler struct {
	cache cachedriver.Cache
}

// New returns a Memorystore handler backed by c.
func New(c cachedriver.Cache) *Handler {
	return &Handler{cache: c}
}

// route holds the parsed components of a Memorystore v1 path.
type route struct {
	project  string
	location string
	resource string // "instances" or "operations"
	name     string // instance id or operation id; empty for the collection
}

// parseRoute extracts the components of a Memorystore v1 path. It recognizes
// only the instances and operations resources under a locations scope.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != "projects" || parts[2] != locationsSeg {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}

	rest := parts[minResourceParts:]
	if len(rest) == 0 {
		return route{}, false
	}

	rt.resource = rest[0]
	if rt.resource != instancesSeg && rt.resource != operationsSeg {
		return route{}, false
	}

	switch len(rest) {
	case 1:
		return rt, true
	case 2:
		rt.name = rest[1]
		return rt, true
	default:
		return route{}, false
	}
}

// Matches claims /v1/projects/{p}/locations/{l}/{instances|operations}[/...]
// paths. The {instances|operations} guard keeps it disjoint from the other
// /v1/projects/ handlers (functions, clusters, secrets, databases, …).
func (*Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)
	return ok
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Memorystore path")
		return
	}

	switch rt.resource {
	case operationsSeg:
		h.serveOperation(w, r, rt)
	case instancesSeg:
		h.serveInstances(w, r, rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported resource: "+rt.resource)
	}
}

func (h *Handler) serveInstances(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.name == "" {
		switch r.Method {
		case http.MethodPost:
			h.createInstance(w, r, rt)
		case http.MethodGet:
			h.listInstances(w, r, rt)
		default:
			writeUnsupported(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, r, rt)
	case http.MethodDelete:
		h.deleteInstance(w, r, rt)
	default:
		writeUnsupported(w)
	}
}

// serveOperation handles Operations.Get. Because mutations complete inline (the
// mutating handlers return a done=true operation), a subsequent poll GET simply
// re-reports a completed operation. The operation state is not persisted, so we
// synthesize a terminal operation for the requested id.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodGet {
		writeUnsupported(w)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt.project, rt.location, rt.name, nil))
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Memorystore operation")
}
