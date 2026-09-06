// Package filestore implements the Google Cloud Filestore
// (file.googleapis.com/v1) instance control plane as a server.Handler. Real
// google.golang.org/api/file/v1 clients, gcloud, and the Terraform google
// provider (google_filestore_instance) pointed at this server manage Filestore
// instances end-to-end.
//
// Coverage (v1 REST — instance control plane):
//
//	POST   /v1/projects/{p}/locations/{l}/instances?instanceId={i}  — Create (LRO)
//	GET    /v1/projects/{p}/locations/{l}/instances/{i}             — Get
//	GET    /v1/projects/{p}/locations/{l}/instances                 — List
//	PATCH  /v1/projects/{p}/locations/{l}/instances/{i}?updateMask= — Update (LRO)
//	DELETE /v1/projects/{p}/locations/{l}/instances/{i}             — Delete (LRO)
//
// Every mutating RPC returns a google.longrunning.Operation with done=true; a
// created instance is READY immediately, so an SDK or Terraform LRO wait
// terminates on the first poll. The completed operation is recorded with the
// shared lro.Registry so a client that polls the returned operation name gets
// the typed instance response (empty on delete), and an unknown name 404s.
//
// # Path collision with Memorystore (the make-or-break integration risk)
//
// Filestore and Memorystore for Redis are distinct real APIs on distinct hosts
// (file.googleapis.com vs redis.googleapis.com) that share the EXACT same path
// grammar — /v1/projects/{p}/locations/{l}/instances[/{i}]. CloudEmu collapses
// every GCP service onto one HTTP server, and a client pointed at it via a
// custom endpoint (option.WithEndpoint / *_custom_endpoint) sends the emulator's
// own host in the Host header, not the API host — so the two CANNOT be told
// apart by URL or Host alone.
//
// They are disambiguated by content and ownership, the pattern Spanner uses to
// coexist with Cloud SQL on /v1/projects/{p}/instances. Filestore registers
// BEFORE Memorystore and claims only genuinely-Filestore traffic, letting every
// Memorystore request fall through to it:
//
//   - Create POST: claimed only when the body carries a Filestore shape
//     (fileShares or networks) — a Redis create body (memorySizeGb/redisConfigs,
//     no fileShares/networks) is not claimed.
//   - Item GET/PATCH/DELETE (.../instances/{i}): claimed only when THIS store
//     owns the instance, so Redis instance traffic falls through.
//   - Bare LIST (.../instances): claimed only when this store owns an instance
//     in the addressed (project, location), so a pure-Memorystore project's LIST
//     falls through. A project holding both services' instances in one location
//     is the one documented ambiguity (Filestore wins); every other request is
//     unambiguous.
//
// Operations paths are owned by the shared lro.Handler (registered ahead of
// both), so this handler defers them whenever a shared registry is wired,
// claiming them only in a standalone package server.
//
// Scope: the instance control plane only. Backups, snapshots, multishare and the
// NFS data plane are out of scope for this build (see BUILDOUT_BACKLOG.md).
package filestore

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	pathPrefix       = "/v1/projects/"
	locationsSeg     = "locations"
	instancesSeg     = "instances"
	operationsSeg    = "operations"
	minResourceParts = 4 // [projects, {p}, locations, {l}]
	maxProbeBytes    = 1 << 20

	restCollection = 1 // [instances]           — the collection
	restItem       = 2 // [instances, {name}]   — a named item
)

// Handler serves file.googleapis.com v1 instance requests against its own store.
type Handler struct {
	store *store

	// ops records created operations with the shared poller so a client that
	// polls the returned operation name gets the typed response. Nil in a
	// standalone package server, where this handler serves its own /operations/
	// poll.
	ops *lro.Registry
}

// New returns a Filestore handler. clock stamps createTime; pass a
// config.FakeClock for deterministic tests, or nil for the real clock.
func New(clock config.Clock) *Handler {
	return &Handler{store: newStore(clock)}
}

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response) through the full server's operations host.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) { h.ops = reg }

// route holds the parsed components of a Filestore v1 path.
type route struct {
	project  string
	location string
	resource string // "instances" or "operations"
	name     string // instance id or operation id; empty for the collection
}

// parseRoute extracts the components of a Filestore v1 path. It recognizes only
// the instances and operations resources under a locations scope.
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
	case restCollection:
		return rt, true
	case restItem:
		rt.name = rest[1]
		return rt, true
	default:
		return route{}, false
	}
}

// Matches claims only genuinely-Filestore traffic on the /v1/projects/{p}/
// locations/{l}/instances path it shares with Memorystore; see the package doc
// for the content/ownership disambiguation.
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

	// Item request: claim only when this store owns the instance.
	if rt.name != "" {
		return h.store.owns(instanceName(rt.project, rt.location, rt.name))
	}

	// Collection request.
	switch r.Method {
	case http.MethodPost:
		return bodyLooksLikeFilestore(r)
	case http.MethodGet:
		return h.store.ownsAnyIn(rt.project, rt.location)
	default:
		return false
	}
}

// bodyLooksLikeFilestore reports whether a POST /instances body is a Filestore
// Instance (has fileShares or networks) rather than a Memorystore Redis
// Instance. It reads and restores the body so a fall-through to Memorystore
// still sees the full request.
func bodyLooksLikeFilestore(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxProbeBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	var probe struct {
		FileShares []any `json:"fileShares"`
		Networks   []any `json:"networks"`
	}

	if json.Unmarshal(raw, &probe) != nil {
		return false
	}

	return len(probe.FileShares) > 0 || len(probe.Networks) > 0
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Filestore path")
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
			h.listInstances(w, rt)
		default:
			writeUnsupported(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getInstance(w, rt)
	case http.MethodPatch:
		h.patchInstance(w, r, rt)
	case http.MethodDelete:
		h.deleteInstance(w, rt)
	default:
		writeUnsupported(w)
	}
}

// serveOperation answers a standalone package server's own operation poll: every
// mutation completes inline, so a poll re-reports a completed operation.
func (*Handler) serveOperation(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodGet {
		writeUnsupported(w)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{
		Name: operationName(rt.project, rt.location, rt.name),
		Done: true,
	})
}

func writeUnsupported(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusBadRequest, "badRequest", "unsupported Filestore operation")
}
