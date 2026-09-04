// Package lro provides a shared long-running-operation poller for GCP
// location-scoped operations: GET, POST …:cancel, and DELETE on
// /v1/projects/{p}/locations/{l}/operations/{op}.
//
// In real GCP each service exposes its own operations endpoint on its own API
// host (alloydb.googleapis.com, artifactregistry.googleapis.com, …). CloudEmu
// collapses every service onto one HTTP server, so those per-service operation
// paths become indistinguishable by URL alone — whichever handler is registered
// first (alloydb/gke) would greedily answer every location operation request
// and fabricate success for the ones it didn't create, shadowing
// artifactregistry, eventarc, memorystore, etc. This one handler, registered
// ahead of the service handlers, owns all location-scoped operation traffic —
// Get, Cancel, and Delete alike — uniformly.
//
// Every CloudEmu mutation completes synchronously, but a client that polls the
// returned operation name still expects real-GCP behaviors the handler must
// reproduce: the completed operation carries the typed result in its
// `response`, an operation name that was never created is 404 NOT_FOUND (not
// masked as done or fabricated success on cancel/delete), Cancel flips a done
// operation's terminal state to canceled, and Delete removes the completed
// operation's record so a later poll 404s. To do that the handler consults a
// shared Registry that each service populates when it creates an operation. A
// handler built without a Registry falls back to the legacy always-succeed
// response on every verb, so standalone per-service package servers (which
// register their own operations handler) are unaffected.
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
	cancelVerb    = "cancel"

	// canceledCode is the numeric google.rpc.Code value (1) a canceled
	// operation's error carries.
	canceledCode = 1
)

// entry is the recorded state of one operation: the typed response a done
// poll replays, and whether Cancel has since been called on it.
type entry struct {
	response any
	canceled bool
}

// Registry records the operations services create, keyed by the operation
// resource name (e.g. "projects/p/locations/l/operations/op-1"), so the
// shared poller can replay the typed response, 404 unknown names, and honor
// Cancel/Delete against a real operation. It is safe for concurrent use:
// services register operations while polls read and mutate them.
type Registry struct {
	mu  sync.RWMutex
	ops map[string]entry
}

// NewRegistry returns an empty operation registry.
func NewRegistry() *Registry {
	return &Registry{ops: make(map[string]entry)}
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

	r.ops[name] = entry{response: response}
}

// lookup returns the recorded entry for name and whether it was registered.
func (r *Registry) lookup(name string) (e entry, found bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, found = r.ops[name]

	return e, found
}

// cancel marks a recorded operation canceled, matching real GCP's
// best-effort Operations.Cancel: since CloudEmu completes every mutation
// synchronously, the operation is already done, so canceling only flips its
// terminal state for a subsequent Get. Reports whether name was registered.
func (r *Registry) cancel(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	e, ok := r.ops[name]
	if !ok {
		return false
	}

	e.canceled = true
	r.ops[name] = e

	return true
}

// delete removes a recorded operation, matching real GCP's Operations.Delete
// (only valid on a completed operation, which every CloudEmu operation already
// is). Reports whether name was registered.
func (r *Registry) delete(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.ops[name]; !ok {
		return false
	}

	delete(r.ops, name)

	return true
}

// Handler answers GET, POST …:cancel, and DELETE on location-scoped operation
// names.
type Handler struct {
	reg *Registry
}

// New returns the shared location-operations handler. When reg is non-nil the
// handler resolves only operations that were registered (404ing unknown names
// on every verb, and applying Get/Cancel/Delete against the registered
// entry); when reg is nil it answers every request with unconditional success
// (legacy standalone behavior).
func New(reg *Registry) *Handler { return &Handler{reg: reg} }

// Matches claims GET, POST /v1/projects/{p}/locations/{l}/operations/{op}:cancel,
// and DELETE /v1/projects/{p}/locations/{l}/operations/{op} — every verb the
// google.longrunning.Operations service exposes on a location-scoped
// operation. Claiming all three verbs (not just GET) is what keeps the
// operations-minting handlers (artifactregistry, eventarc, memorystore,
// alloydb) from greedily answering a cancel/delete on an operation they never
// created; see the package doc.
func (*Handler) Matches(r *http.Request) bool {
	_, _, op, cancel, ok := parse(r.URL.Path)
	if !ok {
		return false
	}

	switch r.Method {
	case http.MethodGet, http.MethodDelete:
		return op != "" && !cancel
	case http.MethodPost:
		return op != "" && cancel
	default:
		return false
	}
}

// ServeHTTP dispatches Get/Cancel/Delete for a location-scoped operation. An
// operation name that was never registered is 404 NOT_FOUND on every verb,
// matching real GCP instead of fabricating success.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	project, location, op, cancel, ok := parse(r.URL.Path)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed operation path")
		return
	}

	name := "projects/" + project + "/locations/" + location + "/operations/" + op

	if h.reg == nil {
		writeLegacy(w, name, cancel, r.Method)
		return
	}

	switch {
	case cancel:
		h.serveCancel(w, name)
	case r.Method == http.MethodDelete:
		h.serveDelete(w, name)
	default:
		h.serveGet(w, name)
	}
}

// serveGet implements Operations.Get.
func (h *Handler) serveGet(w http.ResponseWriter, name string) {
	e, found := h.reg.lookup(name)
	if !found {
		notFound(w, name)
		return
	}

	writeDone(w, name, e.response, e.canceled)
}

// serveCancel implements Operations.Cancel. Real GCP makes a best-effort
// cancel and returns an empty success body even when the operation has
// already completed; CloudEmu's operations are always already complete, so
// cancel just flips the terminal state a later Get reports.
func (h *Handler) serveCancel(w http.ResponseWriter, name string) {
	if !h.reg.cancel(name) {
		notFound(w, name)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// serveDelete implements Operations.Delete: removes a completed operation's
// record, so a subsequent poll 404s — matching real GCP.
func (h *Handler) serveDelete(w http.ResponseWriter, name string) {
	if !h.reg.delete(name) {
		notFound(w, name)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// notFound writes the NOT_FOUND envelope for an operation name that was never
// registered (or was already deleted).
func notFound(w http.ResponseWriter, name string) {
	gcprest.WriteError(w, http.StatusNotFound, "notFound", "operation "+name+" not found")
}

// writeLegacy answers a nil-registry (standalone) handler: every verb
// succeeds unconditionally, matching CloudEmu's pre-registry behavior.
func writeLegacy(w http.ResponseWriter, name string, cancel bool, method string) {
	if cancel || method == http.MethodDelete {
		gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}

	writeDone(w, name, nil, false)
}

// writeDone writes a completed operation. It returns a superset that satisfies
// both operation schemas served here: google.longrunning.Operation reads `done`
// (artifactregistry, eventarc, memorystore, alloydb) while GKE's
// container.Operation reads `status`.
func writeDone(w http.ResponseWriter, name string, response any, canceled bool) {
	body := map[string]any{
		"name":   name,
		"done":   true,
		"status": "DONE",
	}

	switch {
	case canceled:
		body["error"] = map[string]any{"code": canceledCode, "message": "Operation was canceled"}
	case response != nil:
		body["response"] = response
	}

	gcprest.WriteJSON(w, http.StatusOK, body)
}

// parse splits /v1/projects/{p}/locations/{l}/operations/{op}[:cancel].
func parse(urlPath string) (project, location, op string, cancel, ok bool) {
	if len(urlPath) < len(pathPrefix) || urlPath[:len(pathPrefix)] != pathPrefix {
		return "", "", "", false, false
	}

	parts := strings.Split(urlPath[len(pathPrefix):], "/")
	// [project, locations, {l}, operations, {op}[:cancel]]
	const want = 5
	if len(parts) != want || parts[1] != locationsSeg || parts[3] != operationsSeg {
		return "", "", "", false, false
	}

	last := parts[4]

	if idx := strings.LastIndex(last, ":"); idx >= 0 {
		if last[idx+1:] != cancelVerb {
			return "", "", "", false, false
		}

		return parts[0], parts[2], last[:idx], true, true
	}

	return parts[0], parts[2], last, false, true
}
