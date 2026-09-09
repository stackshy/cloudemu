package privateca

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	pcadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
)

// cfgFrom builds a driver Config for a create/patch from a parsed route.
func cfgFrom(rt *route, id string, fields map[string]json.RawMessage) *pcadriver.Config {
	return &pcadriver.Config{Project: rt.project, Location: rt.location, CaPool: rt.pool, ID: id, Fields: fields}
}

// idFromQuery reads the create id query param, accepting both the camelCase and
// snake_case spellings (the Terraform provider sends snake_case).
func idFromQuery(r *http.Request, m meta) string {
	if v := r.URL.Query().Get(m.idParam); v != "" {
		return v
	}

	return r.URL.Query().Get(m.idParamAlt)
}

// createResource handles POST .../{collection}?{idParam}=. A certificate issues
// synchronously (returning the resource); a caPool, certificateAuthority, or
// certificateTemplate returns a completed LRO carrying the resource.
func (h *Handler) createResource(w http.ResponseWriter, r *http.Request, rt *route) {
	m := metas()[rt.coll]

	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := idFromQuery(r, m)
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", m.idParam+" is required")
		return
	}

	cfg := cfgFrom(rt, id, fields)

	if rt.coll == certificatesColl {
		res, err := h.db.CreateCertificate(r.Context(), cfg)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return
		}

		writeResource(w, m, res)

		return
	}

	res, op, err := h.lroCreate(r.Context(), rt.coll, cfg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, m, op, res)
}

// lroCreate dispatches a create that returns a completed LRO.
func (h *Handler) lroCreate(ctx context.Context, coll string, cfg *pcadriver.Config) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	switch coll {
	case caPoolsColl:
		return h.db.CreateCaPool(ctx, cfg)
	case authoritiesColl:
		return h.db.CreateCertificateAuthority(ctx, cfg)
	default:
		return h.db.CreateCertificateTemplate(ctx, cfg)
	}
}

// getResource handles GET .../{collection}/{id}.
func (h *Handler) getResource(w http.ResponseWriter, r *http.Request, rt *route) {
	res, err := h.driverGet(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeResource(w, metas()[rt.coll], res)
}

// driverGet dispatches a get to the collection's driver method.
func (h *Handler) driverGet(ctx context.Context, rt *route) (*pcadriver.Resource, error) {
	switch rt.coll {
	case caPoolsColl:
		return h.db.GetCaPool(ctx, rt.project, rt.location, rt.name)
	case authoritiesColl:
		return h.db.GetCertificateAuthority(ctx, rt.project, rt.location, rt.pool, rt.name)
	case templatesColl:
		return h.db.GetCertificateTemplate(ctx, rt.project, rt.location, rt.name)
	default:
		return h.db.GetCertificate(ctx, rt.project, rt.location, rt.pool, rt.name)
	}
}

// listResources handles GET .../{collection}, scoped to the request's project+
// location(+caPool) and ordered by resource id.
func (h *Handler) listResources(w http.ResponseWriter, r *http.Request, rt *route) {
	m := metas()[rt.coll]

	all, err := h.driverList(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b pcadriver.Resource) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := m.toResourceJSON(&page.Items[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		m.coll:          items,
		"nextPageToken": page.NextPageToken,
	})
}

// driverList dispatches a list to the collection's driver method.
func (h *Handler) driverList(ctx context.Context, rt *route) ([]pcadriver.Resource, error) {
	switch rt.coll {
	case caPoolsColl:
		return h.db.ListCaPools(ctx, rt.project, rt.location)
	case authoritiesColl:
		return h.db.ListCertificateAuthorities(ctx, rt.project, rt.location, rt.pool)
	case templatesColl:
		return h.db.ListCertificateTemplates(ctx, rt.project, rt.location)
	default:
		return h.db.ListCertificates(ctx, rt.project, rt.location, rt.pool)
	}
}

// patchResource handles PATCH .../{collection}/{id}?updateMask=. A certificate
// patches synchronously; the others return a completed LRO.
func (h *Handler) patchResource(w http.ResponseWriter, r *http.Request, rt *route) {
	m := metas()[rt.coll]

	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	mask := parseMask(r.URL.Query().Get("updateMask"))
	cfg := cfgFrom(rt, rt.name, fields)

	if rt.coll == certificatesColl {
		res, err := h.db.PatchCertificate(r.Context(), cfg, mask)
		if err != nil {
			gcprest.WriteCErr(w, err)
			return
		}

		writeResource(w, m, res)

		return
	}

	res, op, err := h.lroPatch(r.Context(), rt.coll, cfg, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeResourceOperation(w, m, op, res)
}

// lroPatch dispatches a patch that returns a completed LRO.
func (h *Handler) lroPatch(ctx context.Context, coll string, cfg *pcadriver.Config, mask []string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	switch coll {
	case caPoolsColl:
		return h.db.PatchCaPool(ctx, cfg, mask)
	case authoritiesColl:
		return h.db.PatchCertificateAuthority(ctx, cfg, mask)
	default:
		return h.db.PatchCertificateTemplate(ctx, cfg, mask)
	}
}

// deleteResource handles DELETE .../{collection}/{id}. Certificates cannot be
// deleted in the real API (they expire or are revoked), so a delete there is
// rejected. The others return a completed LRO with no response.
func (h *Handler) deleteResource(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.coll == certificatesColl {
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed",
			"certificates cannot be deleted; revoke them instead")
		return
	}

	op, err := h.driverDelete(r.Context(), rt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, nil))
}

// driverDelete dispatches a delete to the collection's driver method.
func (h *Handler) driverDelete(ctx context.Context, rt *route) (*pcadriver.Operation, error) {
	switch rt.coll {
	case caPoolsColl:
		return h.db.DeleteCaPool(ctx, rt.project, rt.location, rt.name)
	case authoritiesColl:
		return h.db.DeleteCertificateAuthority(ctx, rt.project, rt.location, rt.pool, rt.name)
	default:
		return h.db.DeleteCertificateTemplate(ctx, rt.project, rt.location, rt.name)
	}
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

// writeResource renders a driver resource as privateca/v1 wire JSON.
func writeResource(w http.ResponseWriter, m meta, res *pcadriver.Resource) {
	raw, err := m.toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, raw)
}

// writeResourceOperation writes a completed operation carrying the resource as its
// Any-typed response (create/patch/verbs).
func (h *Handler) writeResourceOperation(w http.ResponseWriter, m meta, op *pcadriver.Operation, res *pcadriver.Resource) {
	raw, err := m.toResourceJSON(res)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(op.Name, m.responseAny(raw)))
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
