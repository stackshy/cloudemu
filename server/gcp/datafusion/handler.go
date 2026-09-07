// Package datafusion implements the Google Cloud Data Fusion
// (datafusion.googleapis.com/v1) instance control plane as a server.Handler.
// Real google.golang.org/api/datafusion/v1 clients, gcloud, and the Terraform
// google provider's google_data_fusion_instance resource (which the STABLE
// hashicorp/google provider serves from the v1 base path) hit this handler
// unchanged.
//
// Coverage (v1 REST — instance control plane):
//
//	POST   /v1/…/instances?instanceId={i}          — Create (LRO)
//	GET    /v1/…/instances/{i}                      — Get
//	GET    /v1/…/instances                          — List
//	PATCH  /v1/…/instances/{i}?updateMask=          — Patch (LRO)
//	DELETE /v1/…/instances/{i}                      — Delete (LRO)
//	POST   /v1/…/instances/{i}:restart             — Restart (LRO)
//	GET    /v1/…/operations/{op}                    — Operations.Get (shared poller)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true and
// the resulting instance embedded in `response` as a typed Any, so an SDK or
// Terraform LRO wait terminates on the first poll. A created instance is ACTIVE
// immediately (the v1 running state; RUNNING in v1beta1).
//
// # Path collision with Memorystore and Filestore
//
// Data Fusion, Memorystore for Redis (redis.googleapis.com), and Filestore
// (file.googleapis.com) are distinct real APIs on distinct hosts that share the
// EXACT same path grammar — /v1/projects/{p}/locations/{l}/instances[/{i}]. A
// custom-endpoint client sends the emulator's own host, so they CANNOT be told
// apart by URL or Host. Following the Filestore/Spanner content+ownership
// pattern, this handler claims only genuinely-Data-Fusion traffic:
//
//   - Create POST: claimed only when the body carries a Data Fusion `type`
//     (BASIC/ENTERPRISE/DEVELOPER) — a Redis (memorySizeGb/tier) or Filestore
//     (fileShares/tier) create body has no top-level `type` and falls through.
//   - The :restart custom verb: claimed unconditionally — it is Data Fusion's
//     alone, so a restart of a missing instance 404s here as Data Fusion.
//   - Item GET/PATCH/DELETE: claimed only when THIS store owns the instance, so
//     Redis/Filestore item traffic falls through.
//   - Bare LIST: claimed only when this store owns an instance in the addressed
//     (project, location).
//
// Operations paths are owned by the shared lro.Handler; this handler yields them
// whenever a shared registry is wired, claiming them only in a standalone
// package server.
//
// Scope: the instance control plane only. The CDAP pipeline/data plane, DNS
// peerings, and IAM policy verbs are out of scope (see BUILDOUT_BACKLOG.md).
package datafusion

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

const (
	pathPrefix       = "/v1/projects/"
	projectsSeg      = "projects"
	locationsSeg     = "locations"
	instancesSeg     = "instances"
	operationsSeg    = "operations"
	restartVerb      = "restart"
	minResourceParts = 4 // [projects, {p}, locations, {l}]

	restCollection = 1 // [instances]         — the collection
	restItem       = 2 // [instances, {name}] — a named item (possibly :verb)

	instanceTypeURL = "type.googleapis.com/google.cloud.datafusion.v1.Instance"
)

// Handler serves datafusion.googleapis.com v1 requests against a DataFusion
// driver.
type Handler struct {
	db dfdriver.DataFusion

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response (and unknown
	// names 404). Nil in a standalone package server, where this handler serves
	// its own /operations/ poll.
	ops *lro.Registry
}

// New returns a Data Fusion handler backed by db.
func New(db dfdriver.DataFusion) *Handler { return &Handler{db: db} }

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a Data Fusion v1 path.
type route struct {
	project  string
	location string
	resource string // "instances" | "operations"
	name     string // instance id or operation id; empty for the collection
	verb     string // custom verb after ':' on an item (e.g. "restart"); empty otherwise
}

// parseRoute extracts the components of a Data Fusion v1 path. It recognizes only
// the instances and operations resources under a locations scope, and the
// :restart custom verb on an instance item.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) < minResourceParts || parts[0] != projectsSeg || parts[2] != locationsSeg {
		return route{}, false
	}

	rest := parts[minResourceParts:]
	if len(rest) == 0 {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3], resource: rest[0]}
	if rt.resource != instancesSeg && rt.resource != operationsSeg {
		return route{}, false
	}

	switch len(rest) {
	case restCollection:
		return rt, true
	case restItem:
		rt.name, rt.verb = splitVerb(rest[1])
		return rt, true
	default:
		return route{}, false
	}
}

// splitVerb splits a "{id}:{verb}" trailing segment into its id and custom verb.
// A segment with no ':' yields an empty verb.
func splitVerb(seg string) (name, verb string) {
	if i := strings.IndexByte(seg, ':'); i >= 0 {
		return seg[:i], seg[i+1:]
	}

	return seg, ""
}

// Matches claims only genuinely-Data-Fusion traffic on the /instances path it
// shares with Memorystore and Filestore; see the package doc for the content/
// ownership/verb disambiguation.
func (h *Handler) Matches(r *http.Request) bool {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	// Operations are owned by the shared lro.Handler in an assembled server;
	// claim them only when standalone (no shared registry).
	if rt.resource == operationsSeg {
		return h.ops == nil
	}

	// The :restart custom verb is Data Fusion's alone — claim it unconditionally
	// so a restart of a missing instance 404s here rather than falling through.
	if rt.verb == restartVerb {
		return true
	}

	// Item request: claim only when this store owns the instance.
	if rt.name != "" {
		return h.db.Owns(rt.project, rt.location, rt.name)
	}

	// Collection request.
	switch r.Method {
	case http.MethodPost:
		return bodyLooksLikeDataFusion(r)
	case http.MethodGet:
		return h.db.OwnsAnyIn(rt.project, rt.location)
	default:
		return false
	}
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Data Fusion path")
		return
	}

	switch rt.resource {
	case operationsSeg:
		h.serveOperation(w, r, &rt)
	case instancesSeg:
		h.serveInstances(w, r, &rt)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported resource: "+rt.resource)
	}
}

// serveInstances dispatches instance collection and item requests.
func (h *Handler) serveInstances(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.name == "" {
		switch r.Method {
		case http.MethodPost:
			h.createInstance(w, r, rt)
		case http.MethodGet:
			h.listInstances(w, r, rt)
		default:
			writeMethodNotAllowed(w)
		}

		return
	}

	if rt.verb != "" {
		h.serveVerb(w, r, rt)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, r, rt)
	case http.MethodPatch:
		h.patchInstance(w, r, rt)
	case http.MethodDelete:
		h.deleteInstance(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveVerb dispatches an instance custom verb (:restart).
func (h *Handler) serveVerb(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.verb != restartVerb {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported verb: "+rt.verb)
		return
	}

	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	h.restartInstance(w, r, rt)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
