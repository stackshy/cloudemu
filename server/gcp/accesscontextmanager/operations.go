package accesscontextmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	acmdriver "github.com/stackshy/cloudemu/v2/services/accesscontextmanager/driver"
)

const (
	defaultPageSize = 500
	maxPageSize     = 500
	childNameParts  = 4 // [accessPolicies, {num}, {coll}, {id}]
)

// createPolicy handles POST /v1/accessPolicies. The operation completes inline.
func (h *Handler) createPolicy(w http.ResponseWriter, r *http.Request) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	p, op, err := h.db.CreatePolicy(r.Context(), &acmdriver.PolicyConfig{Fields: fields})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	raw, err := policyToJSON(p)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	h.writeDoneOp(w, op.Name, responseAny(policyTypeURL, raw))
}

// getPolicy handles GET /v1/accessPolicies/{p}.
func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	p, err := h.db.GetPolicy(r.Context(), rt.id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	raw, err := policyToJSON(p)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	writeRaw(w, raw)
}

// listPolicies handles GET /v1/accessPolicies?parent=organizations/{org}.
func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request) {
	all, err := h.db.ListPolicies(r.Context(), r.URL.Query().Get("parent"))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b acmdriver.Policy) bool { return a.Number < b.Number },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := policyToJSON(&page.Items[i])
		if mErr != nil {
			gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
			return
		}

		items = append(items, raw)
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{
		"accessPolicies": items,
		"nextPageToken":  page.NextPageToken,
	})
}

// patchPolicy handles PATCH /v1/accessPolicies/{p}?updateMask=.
func (h *Handler) patchPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	p, op, err := h.db.PatchPolicy(r.Context(), rt.id, &acmdriver.PolicyConfig{Fields: fields},
		parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	raw, err := policyToJSON(p)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	h.writeDoneOp(w, op.Name, responseAny(policyTypeURL, raw))
}

// deletePolicy handles DELETE /v1/accessPolicies/{p}.
func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request, rt route) {
	op, err := h.db.DeletePolicy(r.Context(), rt.id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeDoneOp(w, op.Name, nil)
}

// createChild handles POST .../{collection}?{idParam}=.
func (h *Handler) createChild(w http.ResponseWriter, r *http.Request, rt route, col *childColl) {
	fields, bodyName, ok := decodeBody(w, r)
	if !ok {
		return
	}

	id := r.URL.Query().Get(col.idParam)
	if id == "" {
		id = lastSegment(bodyName)
	}

	if id == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", col.idParam+" is required")
		return
	}

	ch, op, err := col.create(r.Context(), &acmdriver.ChildConfig{
		PolicyNumber: rt.policyNum, ID: id, Fields: fields,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeChildOperation(w, col, op, ch)
}

// getChild handles GET .../{collection}/{id}.
func (*Handler) getChild(w http.ResponseWriter, r *http.Request, rt route, col *childColl) {
	ch, err := col.get(r.Context(), rt.policyNum, rt.id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	raw, err := col.toChildJSON(ch)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	writeRaw(w, raw)
}

// listChildren handles GET .../{collection}, scoped to the parent policy.
func (*Handler) listChildren(w http.ResponseWriter, r *http.Request, rt route, col *childColl) {
	all, err := col.list(r.Context(), rt.policyNum)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	page, err := pagination.PaginateSorted(all,
		func(a, b acmdriver.Child) bool { return a.ID < b.ID },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	items := make([]json.RawMessage, 0, len(page.Items))

	for i := range page.Items {
		raw, mErr := col.toChildJSON(&page.Items[i])
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

// patchChild handles PATCH .../{collection}/{id}?updateMask=.
func (h *Handler) patchChild(w http.ResponseWriter, r *http.Request, rt route, col *childColl) {
	fields, _, ok := decodeBody(w, r)
	if !ok {
		return
	}

	ch, op, err := col.patch(r.Context(), &acmdriver.ChildConfig{
		PolicyNumber: rt.policyNum, ID: rt.id, Fields: fields,
	}, parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeChildOperation(w, col, op, ch)
}

// deleteChild handles DELETE .../{collection}/{id}.
func (h *Handler) deleteChild(w http.ResponseWriter, r *http.Request, rt route, col *childColl) {
	op, err := col.del(r.Context(), rt.policyNum, rt.id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeDoneOp(w, op.Name, nil)
}

// serveOperation resolves a (done) root operation poll, re-rendering the target
// resource as the operation's typed response so a create/patch poll carries the
// same bytes the mutating call returned.
func (h *Handler) serveOperation(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	op, err := h.db.GetOperation(r.Context(), rt.id)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var resp json.RawMessage
	if op.Type != "delete" {
		resp = h.renderOpResponse(r.Context(), op)
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: op.Name, Done: true, Response: resp})
}

// renderOpResponse re-renders the resource an operation acted on as its Any-typed
// response, reading from the store so the bytes match the mutating call.
func (h *Handler) renderOpResponse(ctx context.Context, op *acmdriver.Operation) json.RawMessage {
	if op.Kind == "policy" {
		p, err := h.db.GetPolicy(ctx, strings.TrimPrefix(op.TargetName, "accessPolicies/"))
		if err != nil {
			return nil
		}

		raw, err := policyToJSON(p)
		if err != nil {
			return nil
		}

		return responseAny(policyTypeURL, raw)
	}

	col := h.childColls()[collForKind(op.Kind)]
	if col == nil {
		return nil
	}

	parts := strings.Split(op.TargetName, "/")
	if len(parts) != childNameParts {
		return nil
	}

	ch, err := col.get(ctx, parts[1], parts[childNameParts-1])
	if err != nil {
		return nil
	}

	raw, err := col.toChildJSON(ch)
	if err != nil {
		return nil
	}

	return responseAny(col.typeURL, raw)
}

// writeChildOperation writes a completed operation carrying the child resource
// as its Any-typed response (create/patch).
func (h *Handler) writeChildOperation(w http.ResponseWriter, col *childColl, op *acmdriver.Operation, ch *acmdriver.Child) {
	raw, err := col.toChildJSON(ch)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	h.writeDoneOp(w, op.Name, responseAny(col.typeURL, raw))
}

// writeDoneOp writes a completed google.longrunning.Operation and records it with
// the shared LRO poller (a no-op on a nil registry) for parity with siblings.
func (h *Handler) writeDoneOp(w http.ResponseWriter, name string, resp json.RawMessage) {
	if h.ops != nil {
		h.ops.Register(name, resp)
	}

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: name, Done: true, Response: resp})
}

// writeRaw writes a pre-rendered resource JSON body with a 200 status.
func writeRaw(w http.ResponseWriter, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
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
