package workflows

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	wdriver "github.com/stackshy/cloudemu/v2/services/workflows/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
)

// createWorkflow handles POST .../workflows?workflowId=. The id is the query
// param, falling back to the trailing segment of the body name. The operation
// completes inline.
func (h *Handler) createWorkflow(w http.ResponseWriter, r *http.Request, rt route) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := r.URL.Query().Get("workflowId")
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "workflowId is required")
		return
	}

	res, op, err := h.db.CreateWorkflow(r.Context(), &wdriver.Config{
		Project: rt.project, Location: rt.location, ID: id, Fields: fields,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, op, res)
}

// getWorkflow handles GET .../workflows/{id}.
func (h *Handler) getWorkflow(w http.ResponseWriter, r *http.Request, rt route) {
	res, err := h.db.GetWorkflow(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeResource(w, res)
}

// listWorkflows handles GET .../workflows, scoped to the request's project+
// location and ordered by resource id.
func (h *Handler) listWorkflows(w http.ResponseWriter, r *http.Request, rt route) {
	all, err := h.db.ListWorkflows(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b wdriver.Resource) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := toResourceJSON(&page.Items[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"workflows":     items,
		"nextPageToken": page.NextPageToken,
	})
}

// patchWorkflow handles PATCH .../workflows/{id}?updateMask=. Only the masked
// top-level fields mutate. The operation completes inline.
func (h *Handler) patchWorkflow(w http.ResponseWriter, r *http.Request, rt route) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	res, op, err := h.db.PatchWorkflow(r.Context(), &wdriver.Config{
		Project: rt.project, Location: rt.location, ID: rt.name, Fields: fields,
	}, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, op, res)
}

// deleteWorkflow handles DELETE .../workflows/{id}. The operation completes
// inline with no response.
func (h *Handler) deleteWorkflow(w http.ResponseWriter, r *http.Request, rt route) {
	op, err := h.db.DeleteWorkflow(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, nil))
}

// serveOperation resolves a (done) long-running operation poll for a standalone
// package server (no shared registry). The operation resource name is the
// request path without the /v1/ version prefix.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/v1/")

	op, err := h.db.GetOperation(r.Context(), name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: op.Name, Done: true})
}

// writeResource renders a driver resource as workflows/v1 wire JSON.
func writeResource(w http.ResponseWriter, res *wdriver.Resource) {
	raw, err := toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// writeResourceOperation writes a completed operation carrying the resource as
// its Any-typed response (create/patch).
func (h *Handler) writeResourceOperation(w http.ResponseWriter, op *wdriver.Operation, res *wdriver.Resource) {
	raw, err := toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, responseAny(raw)))
}

// doneOperation builds a completed google.longrunning.Operation and records it
// with the shared LRO poller (a no-op on a nil registry) so a client polling the
// returned name resolves the same done operation (with its response).
func (h *Handler) doneOperation(name string, resp json.RawMessage) operationJSON {
	if h.ops != nil {
		h.ops.Register(name, resp)
	}

	return operationJSON{Name: name, Done: true, Response: resp}
}

// parseMask splits a comma-separated updateMask query param into field paths.
func parseMask(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// lastSegment returns the trailing path segment of a resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// pageSize reads ?pageSize, clamping to a sane default and ceiling.
func pageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultPageSize
	}

	if n > maxPageSize {
		return maxPageSize
	}

	return n
}
