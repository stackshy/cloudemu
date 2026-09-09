// Package datastream implements the Google Datastream control plane
// (datastream.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/datastream/v1 clients, gcloud, and the Terraform google
// provider's google_datastream_connection_profile and google_datastream_stream
// resources hit this handler unchanged.
//
// Coverage (connection-profile + stream control plane only):
//
//	POST   /v1/…/connectionProfiles?connectionProfileId=  — CreateConnectionProfile (LRO)
//	GET    /v1/…/connectionProfiles                       — ListConnectionProfiles
//	GET    /v1/…/connectionProfiles/{id}                  — GetConnectionProfile
//	PATCH  /v1/…/connectionProfiles/{id}?updateMask=      — PatchConnectionProfile (LRO)
//	DELETE /v1/…/connectionProfiles/{id}                  — DeleteConnectionProfile (LRO)
//	POST   /v1/…/streams?streamId=                        — CreateStream (LRO)
//	GET    /v1/…/streams                                  — ListStreams
//	GET    /v1/…/streams/{id}                             — GetStream
//	PATCH  /v1/…/streams/{id}?updateMask=                 — PatchStream (LRO)
//	DELETE /v1/…/streams/{id}                             — DeleteStream (LRO)
//	GET    /v1/…/operations/{op}                          — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting resource embedded in `response`, so an SDK or Terraform LRO wait
// terminates on the first poll instead of hanging.
//
// Location-scoped operations: Datastream's operations live under
// /v1/projects/{p}/locations/{l}/operations — the SAME space the shared GCP LRO
// poller owns. Matches returns false for operation paths when a shared registry
// is wired, letting that poller win; a standalone package server (no registry)
// serves its own polls. The connectionProfiles/streams resource-type guard keeps
// this handler disjoint from every other /v1/projects/ handler (Composer's
// environments, Cloud Deploy's pipelines/targets, Scheduler's jobs, …).
package datastream

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dsdriver "github.com/stackshy/cloudemu/v2/services/datastream/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	operationsSeg    = "operations"
	profilesColl     = "connectionProfiles"
	streamsColl      = "streams"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	itemParts        = 2 // [resource, {name}]

	// minComputedFields is the extra map capacity reserved for the injected
	// computed output fields (name, createTime, updateTime).
	minComputedFields = 3

	profileTypeURL = "type.googleapis.com/google.cloud.datastream.v1.ConnectionProfile"
	streamTypeURL  = "type.googleapis.com/google.cloud.datastream.v1.Stream"

	// defaultStreamState is the state a stream is minted with when the create
	// body carries none, matching real Datastream (a stream starts NOT_STARTED
	// and is transitioned to RUNNING/PAUSED by a state-masked patch). Terraform's
	// desired_state reconciles clean against it.
	defaultStreamState = `"NOT_STARTED"`
)

// collection describes one Datastream resource collection, binding its wire
// identity to the driver methods that back it. The two collections share every
// CRUD code path, differing only in these bound values.
type collection struct {
	seg          string // "connectionProfiles" | "streams"
	idParam      string // "connectionProfileId" | "streamId"
	typeURL      string
	defaultState string // seeded into a create body's `state` if absent (streams only)

	create func(context.Context, *dsdriver.Config) (*dsdriver.Resource, *dsdriver.Operation, error)
	get    func(context.Context, string, string, string) (*dsdriver.Resource, error)
	list   func(context.Context, string, string) ([]dsdriver.Resource, error)
	patch  func(context.Context, *dsdriver.Config, []string) (*dsdriver.Resource, *dsdriver.Operation, error)
	del    func(context.Context, string, string, string) (*dsdriver.Operation, error)
}

// Handler serves datastream.googleapis.com v1 requests against a Datastream
// driver.
type Handler struct {
	db dsdriver.Datastream

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Datastream handler backed by db.
func New(db dsdriver.Datastream) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// collections binds the two resource collections to h's driver method values,
// keyed by their path segment.
func (h *Handler) collections() map[string]*collection {
	return map[string]*collection{
		profilesColl: {
			seg: profilesColl, idParam: "connectionProfileId", typeURL: profileTypeURL,
			create: h.db.CreateConnectionProfile, get: h.db.GetConnectionProfile, list: h.db.ListConnectionProfiles,
			patch: h.db.PatchConnectionProfile, del: h.db.DeleteConnectionProfile,
		},
		streamsColl: {
			seg: streamsColl, idParam: "streamId", typeURL: streamTypeURL, defaultState: defaultStreamState,
			create: h.db.CreateStream, get: h.db.GetStream, list: h.db.ListStreams,
			patch: h.db.PatchStream, del: h.db.DeleteStream,
		},
	}
}

// route holds the parsed components of a Datastream v1 path.
type route struct {
	project  string
	location string
	resource string // "connectionProfiles" | "streams" | "operations"
	name     string // resource id or operation id; empty for the collection
}

// parseRoute extracts the components of a Datastream v1 path. It recognizes only
// the connectionProfiles, streams, and operations resources under a locations
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
	return seg == profilesColl || seg == streamsColl || seg == operationsSeg
}

// Matches claims /v1/projects/{p}/locations/{l}/{connectionProfiles|streams|
// operations}[/…] paths. The connectionProfiles/streams guard keeps it disjoint
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
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Datastream path")
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
