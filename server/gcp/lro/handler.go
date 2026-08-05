// Package lro provides a shared long-running-operation poller for GCP
// location-scoped operations: GET /v1/projects/{p}/locations/{l}/operations/{op}.
//
// In real GCP each service exposes its own operations endpoint on its own API
// host (alloydb.googleapis.com, artifactregistry.googleapis.com, …). CloudEmu
// collapses every service onto one HTTP server, so those per-service operation
// paths become indistinguishable by URL alone — whichever handler is registered
// first (alloydb/gke) would greedily answer every location operation poll and
// 404 the ones it didn't create, shadowing artifactregistry, eventarc,
// memorystore, etc.
//
// Every CloudEmu mutation completes synchronously (the create/delete response
// already carries done:true with the result inlined), so an operation poll only
// needs to report completion. This one handler, registered ahead of the
// service handlers, answers all location-scoped operation polls uniformly with
// a done operation — the single "operations host" the collapsed server needs.
package lro

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	pathPrefix    = "/v1/projects/"
	locationsSeg  = "locations"
	operationsSeg = "operations"
)

// Handler answers GET on location-scoped operation names.
type Handler struct{}

// New returns the shared location-operations handler.
func New() *Handler { return &Handler{} }

// Matches claims GET /v1/projects/{p}/locations/{l}/operations/{op}.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	_, _, op, ok := parse(r.URL.Path)

	return ok && op != ""
}

// ServeHTTP returns a completed operation echoing the polled name.
func (*Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	project, location, op, ok := parse(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed operation path")
		return
	}

	// Return a superset that satisfies both operation schemas served here:
	// google.longrunning.Operation reads `done` (artifactregistry, eventarc,
	// memorystore, alloydb), while GKE's container.Operation reads `status`.
	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"name":   "projects/" + project + "/locations/" + location + "/operations/" + op,
		"done":   true,
		"status": "DONE",
	})
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
