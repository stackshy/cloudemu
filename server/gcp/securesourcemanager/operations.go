package securesourcemanager

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/securesourcemanager/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
)

// createResource handles POST .../{collection}?{idParam}=. The id is the query
// param, falling back to the trailing segment of the body name. A collection's
// validate hook rejects a malformed body (a repository's required `instance`
// reference); its seed hook mints computed values (an instance's state +
// hostConfig, a repository's uid + uris) so a later GET is stable. The operation
// completes inline.
func (h *Handler) createResource(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := clientID(r, col.idParam)
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", col.idParam+" is required")
		return
	}

	if col.validate != nil {
		if err := col.validate(fields); err != nil {
			gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", err.Error())
			return
		}
	}

	if col.seed != nil {
		col.seed(fields, rt.project, rt.location, id)
	}

	res, op, err := col.create(r.Context(), &ssmdriver.Config{
		Project: rt.project, Location: rt.location, ID: id, Fields: fields,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, col, op, res)
}

// getResource handles GET .../{collection}/{id}.
func (*Handler) getResource(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	res, err := col.get(r.Context(), rt.project, rt.location, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeResource(w, col, res)
}

// listResources handles GET .../{collection}, scoped to the request's project+
// location and ordered by resource name.
func (*Handler) listResources(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	all, err := col.list(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b ssmdriver.Resource) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := col.toResourceJSON(&page.Items[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		col.seg:         items,
		"nextPageToken": page.NextPageToken,
	})
}

// patchResource handles PATCH .../{collection}/{id}?updateMask=. Only the masked
// top-level fields mutate. The operation completes inline.
func (h *Handler) patchResource(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))

	res, op, err := col.patch(r.Context(), &ssmdriver.Config{
		Project: rt.project, Location: rt.location, ID: rt.name, Fields: fields,
	}, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, col, op, res)
}

// deleteResource handles DELETE .../{collection}/{id}. The operation completes
// inline with no response.
func (h *Handler) deleteResource(w http.ResponseWriter, r *http.Request, rt route, col *collection) {
	op, err := col.del(r.Context(), rt.project, rt.location, rt.name)
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

// writeResource renders a driver resource as securesourcemanager/v1 wire JSON.
func writeResource(w http.ResponseWriter, col *collection, res *ssmdriver.Resource) {
	raw, err := col.toResourceJSON(res)
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
func (h *Handler) writeResourceOperation(w http.ResponseWriter, col *collection, op *ssmdriver.Operation, res *ssmdriver.Resource) {
	raw, err := col.toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, col.responseAny(raw)))
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

// clientID reads the client-supplied resource id from the create query param.
// GCP APIs accept the id key in either camelCase (instanceId/repositoryId, what
// a GAPIC client sends) or snake_case (instance_id/repository_id, what the
// Terraform google provider sends), so both are honored.
func clientID(r *http.Request, camelParam string) string {
	if v := r.URL.Query().Get(camelParam); v != "" {
		return v
	}

	return r.URL.Query().Get(camelToSnake(camelParam))
}

// camelToSnake lowercases each interior uppercase letter and prefixes it with an
// underscore (instanceId -> instance_id).
func camelToSnake(s string) string {
	var b strings.Builder

	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}

			b.WriteRune(c - 'A' + 'a')

			continue
		}

		b.WriteRune(c)
	}

	return b.String()
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
