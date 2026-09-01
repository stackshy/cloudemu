// Package locks implements the Azure Management Locks ARM REST API
// (Microsoft.Authorization/locks) as a server.Handler. Real
// azure-sdk-for-go armlocks ManagementLocksClient requests pointed at this
// server can CRUD management locks end-to-end, exactly as they hit
// management.azure.com.
//
// Coverage (api-version 2016-09-01 / 2020-05-01):
//
//	PUT    /{scope}/providers/Microsoft.Authorization/locks/{lockName}  — CreateOrUpdate
//	GET    /{scope}/providers/Microsoft.Authorization/locks/{lockName}  — Get
//	DELETE /{scope}/providers/Microsoft.Authorization/locks/{lockName}  — Delete
//	GET    /{scope}/providers/Microsoft.Authorization/locks             — List at scope
//
// {scope} can be a subscription (/subscriptions/{sub}), a resource group
// (/subscriptions/{sub}/resourceGroups/{rg}) or an individual resource
// (.../providers/{ns}/{type}/{name}); the handler treats it as an opaque
// string, so the At{Subscription,ResourceGroup,Resource}Level and ByScope SDK
// variants all route through the same code path.
//
// SCOPE BOUNDARY: this handler implements the management-plane CRUD and a
// faithful round-trip of level/notes plus discoverability of the locks
// themselves. It does NOT enforce the locks — a CanNotDelete or ReadOnly lock
// does not (yet) block deletes or writes on the locked resource. Enforcement is
// a cross-cutting change spanning every Azure delete/write path and is tracked
// as follow-up work.
//
// Locks are a pure management-plane concept with no per-provider driver
// counterpart (there is no AWS/GCP analog), so the handler owns its own
// in-memory store rather than delegating to a services/*/driver interface.
package locks

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	// providerSegment is the lower-case marker searched for in request URLs.
	// SDK URL templates vary in case, so matching always lower-cases the path.
	providerSegment = "/providers/microsoft.authorization/locks"

	// providerSegmentCanonical is embedded in returned resource IDs so SDK
	// consumers see Microsoft's canonical capitalization.
	providerSegmentCanonical = "/providers/Microsoft.Authorization/locks/"

	// armType is the ARM resource type reported on every lock.
	armType = "Microsoft.Authorization/locks"
)

// Handler serves Microsoft.Authorization/locks ARM requests from an in-memory
// store keyed by (scope, lockName).
type Handler struct {
	store *store
}

// New returns a locks handler with an empty in-memory store.
func New() *Handler {
	return &Handler{store: newStore()}
}

// Matches claims any path carrying the /providers/Microsoft.Authorization/locks
// segment (case-insensitive), whether a collection URL or a named lock. It does
// not claim sibling Microsoft.Authorization resources such as roleAssignments or
// roleDefinitions — those keep their own handler.
func (*Handler) Matches(r *http.Request) bool {
	_, _, ok := parseLockPath(r.URL.Path)

	return ok
}

// ServeHTTP routes by whether the URL names a lock (CRUD) or is a collection
// (list), then by HTTP verb.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scope, name, ok := parseLockPath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed management-lock path")
		return
	}

	if name == "" {
		h.list(w, r, scope)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, scope, name)
	case http.MethodGet:
		h.get(w, scope, name)
	case http.MethodDelete:
		h.delete(w, scope, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, scope, name string) {
	var req lockRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	l, created := h.store.put(scope, name, req.Properties.Level, req.Properties.Notes)

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(l))
}

func (h *Handler) get(w http.ResponseWriter, scope, name string) {
	l, ok := h.store.get(scope, name)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "management lock not found: "+name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(l))
}

func (h *Handler) delete(w http.ResponseWriter, scope, name string) {
	// ARM DELETE is idempotent: a removed lock returns 200 OK, a missing one
	// 204 No Content. The armlocks client accepts both.
	if h.store.delete(scope, name) {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, scope string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	stored := h.store.list(scope)

	out := listResponse{Value: make([]lockResponse, 0, len(stored))}
	for i := range stored {
		out.Value = append(out.Value, toResponse(stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// parseLockPath splits a management-lock URL into (scope, lockName).
//
//   - scope is everything before /providers/Microsoft.Authorization/locks,
//     normalized to a path-rooted string with any trailing slash trimmed.
//   - lockName is the segment after the marker, or "" for a collection (list).
//
// Returns ok=false when the path does not carry the locks segment or names a
// sub-resource the API does not model.
func parseLockPath(urlPath string) (scope, name string, ok bool) {
	lower := strings.ToLower(urlPath)

	idx := strings.Index(lower, providerSegment)
	if idx < 0 {
		return "", "", false
	}

	scope = normalizeScope(urlPath[:idx])

	rest := strings.TrimRight(urlPath[idx+len(providerSegment):], "/")
	if rest == "" {
		return scope, "", true
	}

	if rest[0] != '/' {
		// e.g. a hypothetical ".../locksomething" resource type — not ours.
		return "", "", false
	}

	name = rest[1:]
	if name == "" || strings.Contains(name, "/") {
		return "", "", false
	}

	return scope, name, true
}

// normalizeScope canonicalizes the chunk of the URL that precedes the locks
// segment into a single-slash, path-rooted string. Empty segments are dropped,
// so a leading slash is guaranteed and any doubled slash is collapsed.
//
// Collapsing matters for cross-variant addressing: the armlocks *ByScope
// methods build the path as "/{scope}/providers/..." where {scope} already
// carries its own leading slash, producing a doubled leading slash; the
// *AtResourceLevel methods leave an empty {parentResourcePath} segment,
// producing an internal doubled slash. Both must reduce to the same key as the
// caller's logical scope so a lock created via one variant is found, listed and
// deleted via the others.
func normalizeScope(raw string) string {
	segs := strings.Split(raw, "/")
	kept := make([]string, 0, len(segs))

	for _, s := range segs {
		if s != "" {
			kept = append(kept, s)
		}
	}

	if len(kept) == 0 {
		return "/"
	}

	return "/" + strings.Join(kept, "/")
}
