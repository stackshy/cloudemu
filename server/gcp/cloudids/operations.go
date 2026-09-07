package cloudids

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	idsdriver "github.com/stackshy/cloudemu/v2/services/cloudids/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
)

// createEndpoint handles POST .../endpoints?endpointId=. The id is the query
// param, falling back to the trailing segment of the body name. severity is a
// required enum, validated before the seed. The seed hook mints the output-only
// state (READY) and the deterministic endpointForwardingRule/endpointIp so a
// later GET is stable (the classic Cloud IDS drift point). The operation
// completes inline.
func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request, rt route) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := r.URL.Query().Get(endpointIDParam)
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", endpointIDParam+" is required")
		return
	}

	if msg, valid := validateSeverity(fields); !valid {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", msg)
		return
	}

	seedEndpoint(fields, rt.project, rt.location, id)

	res, op, err := h.db.CreateEndpoint(r.Context(), &idsdriver.Config{
		Project: rt.project, Location: rt.location, ID: id, Fields: fields,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, op, res)
}

// getEndpoint handles GET .../endpoints/{id}.
func (h *Handler) getEndpoint(w http.ResponseWriter, r *http.Request, rt route) {
	res, err := h.db.GetEndpoint(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeEndpoint(w, res)
}

// listEndpoints handles GET .../endpoints, scoped to the request's project+
// location and ordered by resource name.
func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request, rt route) {
	all, err := h.db.ListEndpoints(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b idsdriver.Resource) bool { return a.ID < b.ID },
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
		"endpoints":     items,
		"nextPageToken": page.NextPageToken,
	})
}

// patchEndpoint handles PATCH .../endpoints/{id}?updateMask=. Only the masked
// top-level fields mutate (in practice threatExceptions and labels — severity and
// network are ForceNew in the Terraform provider). The operation completes
// inline.
func (h *Handler) patchEndpoint(w http.ResponseWriter, r *http.Request, rt route) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	res, op, err := h.db.PatchEndpoint(r.Context(), &idsdriver.Config{
		Project: rt.project, Location: rt.location, ID: rt.name, Fields: fields,
	}, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, op, res)
}

// deleteEndpoint handles DELETE .../endpoints/{id}. The operation completes
// inline with no response.
func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request, rt route) {
	op, err := h.db.DeleteEndpoint(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, nil))
}

// serveOperation resolves a (done) long-running operation poll for a standalone
// package server (no shared registry). The operation resource name is the request
// path without the /v1/ version prefix.
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
