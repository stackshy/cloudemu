package synapse

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// The SQL-pool, Spark-pool and integration-runtime handlers share the same
// read-lock / workspace-lookup / child-lookup scaffolding for GET, LIST and
// DELETE. These generics carry that scaffolding once, parameterized by the
// child map a workspace exposes and the projection to its wire shape, so each
// resource file only supplies its own map accessor and response projector.

// childGet writes the projected response for a named child of a workspace, or a
// parent/child not-found error.
func childGet[V any](
	h *Handler, w http.ResponseWriter, rp *azurearm.ResourcePath,
	children func(*workspaceState) map[string]V,
	notFound func(http.ResponseWriter, string),
	project func(*workspaceState, V) any,
) {
	h.mu.RLock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.RUnlock()
		writeParentNotFound(w, rp.ResourceName)

		return
	}

	v, ok := children(ws)[strings.ToLower(rp.SubResourceName)]
	if !ok {
		h.mu.RUnlock()
		notFound(w, rp.SubResourceName)

		return
	}

	resource := project(ws, v)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

// childList writes the {value:[...]} listing of a workspace's children, sorted
// by name, or a parent not-found error.
func childList[V any](
	h *Handler, w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	children func(*workspaceState) map[string]V,
	project func(*workspaceState, V) any,
) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.RUnlock()
		writeParentNotFound(w, rp.ResourceName)

		return
	}

	m := children(ws)
	out := listEnvelope{Value: make([]any, 0, len(m))}

	for _, name := range sortedNames(m) {
		out.Value = append(out.Value, project(ws, m[name]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// childDelete removes a named child from a workspace and writes the idempotent
// delete status (200 when it existed, 204 when it was already absent).
func childDelete[V any](
	h *Handler, w http.ResponseWriter, rp *azurearm.ResourcePath,
	children func(*workspaceState) map[string]V,
) {
	h.mu.Lock()

	ws, ok := h.getWorkspace(rp)
	if !ok {
		h.mu.Unlock()
		deleteStatus(w, false)

		return
	}

	m := children(ws)
	k := strings.ToLower(rp.SubResourceName)
	_, existed := m[k]
	delete(m, k)
	h.mu.Unlock()

	deleteStatus(w, existed)
}
