// Package lro provides a shared long-running-operation poller for GCP
// location-scoped operations: GET /v1/projects/{p}/locations/{l}/operations/{op}.
//
// In real GCP each service exposes its own operations endpoint on its own API
// host (alloydb.googleapis.com, artifactregistry.googleapis.com, …). CloudEmu
// collapses every service onto one HTTP server, so those per-service operation
// paths become indistinguishable by URL alone — whichever handler is registered
// first (alloydb/gke) would greedily answer every location operation poll and
// 404 the ones it didn't create, shadowing artifactregistry, eventarc,
// memorystore, etc. This one handler, registered ahead of the service handlers,
// owns all location-scoped operation polls uniformly.
//
// Every CloudEmu mutation completes synchronously, but a client that polls the
// returned operation name still expects two real-GCP behaviors the handler
// must reproduce: the completed operation carries the typed result in its
// `response`, and an operation name that was never created is 404 NOT_FOUND (not
// masked as "done"). To do that the handler consults a shared Registry that each
// service populates when it creates an operation. A handler built without a
// Registry falls back to the legacy always-done response, so standalone
// per-service package servers (which register their own operations handler) are
// unaffected.
package lro

import (
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	pathPrefix    = "/v1/projects/"
	locationsSeg  = "locations"
	operationsSeg = "operations"
)

// Registry records the completed operations services create, keyed by the
// operation resource name (e.g. "projects/p/locations/l/operations/op-1"), so
// the shared poller can replay the typed response and 404 unknown names. It is
// safe for concurrent use: services register operations while polls read them.
type Registry struct {
	mu  sync.RWMutex
	ops map[string]any
}

// NewRegistry returns an empty operation registry.
func NewRegistry() *Registry {
	return &Registry{ops: make(map[string]any)}
}

// Register records a completed operation under its resource name together with
// the typed response a poller reads off the done operation (nil for operations
// whose result is empty, e.g. deletes). A nil registry is a no-op, so a service
// wired without the shared poller (a standalone package server) is unaffected.
func (r *Registry) Register(name string, response any) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.ops[name] = response
}

// lookup returns the recorded response for name and whether it was registered.
func (r *Registry) lookup(name string) (response any, found bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	response, found = r.ops[name]

	return response, found
}

// Handler answers GET on location-scoped operation names.
type Handler struct {
	reg *Registry
}

// New returns the shared location-operations handler. When reg is non-nil the
// handler resolves only operations that were registered (404ing unknown names
// and echoing the registered response); when reg is nil it answers every poll
// with a bare done operation (legacy standalone behavior).
func New(reg *Registry) *Handler { return &Handler{reg: reg} }

// Matches claims GET /v1/projects/{p}/locations/{l}/operations/{op}.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	_, _, op, ok := parse(r.URL.Path)

	return ok && op != ""
}

// ServeHTTP returns the polled operation as a completed google.longrunning
// Operation, carrying the registered typed response. An operation name that was
// never registered is 404 NOT_FOUND, matching real GCP.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	project, location, op, ok := parse(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed operation path")
		return
	}

	name := "projects/" + project + "/locations/" + location + "/operations/" + op

	if h.reg == nil {
		writeDone(w, name, nil)
		return
	}

	response, found := h.reg.lookup(name)
	if !found {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "operation "+name+" not found")
		return
	}

	writeDone(w, name, response)
}

// writeDone writes a completed operation. It returns a superset that satisfies
// both operation schemas served here: google.longrunning.Operation reads `done`
// (artifactregistry, eventarc, memorystore, alloydb) while GKE's
// container.Operation reads `status`.
func writeDone(w http.ResponseWriter, name string, response any) {
	body := map[string]any{
		"name":   name,
		"done":   true,
		"status": "DONE",
	}

	if response != nil {
		body["response"] = response
	}

	gcprest.WriteJSON(w, http.StatusOK, body)
}

// parse splits /v1/projects/{p}/locations/{l}/operations/{op}.
func parse(urlPath string) (project, location, op string, ok bool) {
	if len(urlPath) < len(pathPrefix) || urlPath[:len(pathPrefix)] != pathPrefix {
		return "", "", "", false
	}

	parts := strings.Split(urlPath[len(pathPrefix):], "/")
	// [project, locations, {l}, operations, {op}]
	const want = 5
	if len(parts) != want || parts[1] != locationsSeg || parts[3] != operationsSeg {
		return "", "", "", false
	}

	return parts[0], parts[2], parts[4], true
}
