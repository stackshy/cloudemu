package metastore

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	msdriver "github.com/stackshy/cloudemu/v2/services/metastore/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
)

// createService handles POST .../services?serviceId=. The id is the query param,
// falling back to the trailing segment of the body name. The seed hook mints the
// output-only fields (endpointUri, state, stateMessage, artifactGcsUri, uid) and
// fills the top-level server defaults (port, databaseType, releaseChannel, tier)
// so a later GET is stable — the classic Dataproc Metastore drift point. The
// operation completes inline.
func (h *Handler) createService(w http.ResponseWriter, r *http.Request, rt route) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := r.URL.Query().Get(serviceIDParam)
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", serviceIDParam+" is required")
		return
	}

	seedService(fields, resourceName(rt.project, rt.location, id))

	res, op, err := h.db.CreateService(r.Context(), &msdriver.Config{
		Project: rt.project, Location: rt.location, ID: id, Fields: fields,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, op, res)
}

// getService handles GET .../services/{id}.
func (h *Handler) getService(w http.ResponseWriter, r *http.Request, rt route) {
	res, err := h.db.GetService(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeService(w, res)
}

// listServices handles GET .../services, scoped to the request's project+
// location and ordered by resource name.
func (h *Handler) listServices(w http.ResponseWriter, r *http.Request, rt route) {
	all, err := h.db.ListServices(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b msdriver.Resource) bool { return a.ID < b.ID },
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
		"services":      items,
		"nextPageToken": page.NextPageToken,
	})
}

// patchService handles PATCH .../services/{id}?updateMask=. Only the masked
// top-level fields mutate. The operation completes inline.
func (h *Handler) patchService(w http.ResponseWriter, r *http.Request, rt route) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	res, op, err := h.db.PatchService(r.Context(), &msdriver.Config{
		Project: rt.project, Location: rt.location, ID: rt.name, Fields: fields,
	}, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, op, res)
}

// deleteService handles DELETE .../services/{id}. The operation completes inline
// with no response.
func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request, rt route) {
	op, err := h.db.DeleteService(r.Context(), rt.project, rt.location, rt.name)
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
