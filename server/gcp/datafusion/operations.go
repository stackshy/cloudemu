package datafusion

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

const (
	instanceIDParam = "instanceId"
	defaultPageSize = 500
	maxPageSize     = 500
)

// createInstance handles POST .../instances?instanceId=. The id is the query
// param (accepting both the camelCase instanceId and the snake_case instance_id
// the Terraform google provider may send), falling back to the trailing segment
// of the body name. The operation completes inline.
func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := createID(r, bodyName)
	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", instanceIDParam+" is required")
		return
	}

	res, op, err := h.db.CreateInstance(r.Context(), &dfdriver.Config{
		Project: rt.project, Location: rt.location, ID: id, Fields: fields,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeInstanceOperation(w, op, res)
}

// createID resolves the instance id for a create: the camelCase id query param,
// then the snake_case variant, then the trailing segment of the body name.
func createID(r *http.Request, bodyName string) string {
	if id := r.URL.Query().Get(instanceIDParam); id != "" {
		return id
	}

	if id := r.URL.Query().Get("instance_id"); id != "" {
		return id
	}

	return lastSegment(bodyName)
}

// getInstance handles GET .../instances/{id}.
func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	res, err := h.db.GetInstance(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeInstance(w, res)
}

// listInstances handles GET .../instances, scoped to the request's project+
// location and ordered by instance id.
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListInstances(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b dfdriver.Resource) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := toInstanceJSON(&page.Items[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"instances":     items,
		"nextPageToken": page.NextPageToken,
	})
}

// patchInstance handles PATCH .../instances/{id}?updateMask=. Only the masked
// top-level fields mutate. The operation completes inline.
func (h *Handler) patchInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	res, op, err := h.db.PatchInstance(r.Context(), &dfdriver.Config{
		Project: rt.project, Location: rt.location, ID: rt.name, Fields: fields,
	}, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeInstanceOperation(w, op, res)
}

// deleteInstance handles DELETE .../instances/{id}. The operation completes
// inline with no response.
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	op, err := h.db.DeleteInstance(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, nil))
}

// restartInstance handles POST .../instances/{id}:restart. It runs the state
// machine (ACTIVE -> RESTARTING -> ACTIVE), rejecting a non-ACTIVE instance and
// 404ing a missing one. The operation completes inline with the instance
// embedded in its response.
func (h *Handler) restartInstance(w http.ResponseWriter, r *http.Request, rt *route) {
	res, op, err := h.db.RestartInstance(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeInstanceOperation(w, op, res)
}

// serveOperation resolves a (done) long-running operation poll for a standalone
// package server (no shared registry). The operation resource name is the
// request path without the /v1/ version prefix.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request, _ *route) {
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

// writeInstance renders a driver resource as datafusion/v1 wire JSON.
func writeInstance(w http.ResponseWriter, res *dfdriver.Resource) {
	raw, err := toInstanceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// writeInstanceOperation writes a completed operation carrying the instance as
// its Any-typed response (create/patch/restart).
func (h *Handler) writeInstanceOperation(w http.ResponseWriter, op *dfdriver.Operation, res *dfdriver.Resource) {
	raw, err := toInstanceJSON(res)
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
