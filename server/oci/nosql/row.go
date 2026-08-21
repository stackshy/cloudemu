package nosql

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveRows routes the row sub-collection. Row writes are synchronous in real
// OCI, so none of them records a work request.
func (h *Handler) serveRows(w http.ResponseWriter, r *http.Request, tableID string) {
	switch r.Method {
	case http.MethodGet:
		h.getRow(w, r, tableID)
	case http.MethodPut:
		h.putRow(w, r, tableID)
	case http.MethodDelete:
		h.deleteRow(w, r, tableID)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) getRow(w http.ResponseWriter, r *http.Request, tableID string) {
	key, ok := decodeKey(w, r)
	if !ok {
		return
	}

	if _, err := h.findTable(r, tableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	row, err := h.extras.GetOCIRow(r.Context(), tableID, key)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, rowBody{Value: row.Value, TimeOfExpiration: row.TimeOfExpiration})
}

// putRow serves UpdateRow, OCI's upsert. Its result model carries no row
// value, so the response body is empty; the expiry a table TTL implies is
// observable through GetRow.
func (h *Handler) putRow(w http.ResponseWriter, r *http.Request, tableID string) {
	var req updateRowRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if len(req.Value) == 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "value is required")
		return
	}

	if !h.rowCompartmentMatches(w, r, tableID, req.CompartmentID) {
		return
	}

	if _, err := h.extras.PutOCIRow(r.Context(), tableID, req.Value, req.Option); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, struct{}{})
}

func (h *Handler) deleteRow(w http.ResponseWriter, r *http.Request, tableID string) {
	key, ok := decodeKey(w, r)
	if !ok {
		return
	}

	if _, err := h.findTable(r, tableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	deleted, err := h.extras.DeleteOCIRow(r.Context(), tableID, key)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, deleteRowResult{IsSuccess: deleted})
}

// rowCompartmentMatches checks a body-supplied compartmentId against the
// table's, the way the query-parameter form is checked on the read paths.
func (h *Handler) rowCompartmentMatches(w http.ResponseWriter, r *http.Request, tableID, compartmentID string) bool {
	table, err := h.extras.GetOCITable(r.Context(), tableID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return false
	}

	if compartmentID != "" && table.CompartmentID != compartmentID {
		ocirest.WriteDriverError(w, r, notFound(tableID))
		return false
	}

	return true
}

// decodeKey reads OCI's repeated key parameter, each entry a "column:value"
// pair. A pair without a colon is refused rather than read as a bare column.
func decodeKey(w http.ResponseWriter, r *http.Request) (map[string]string, bool) {
	raw := r.URL.Query()["key"]
	if len(raw) == 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "at least one key parameter is required")
		return nil, false
	}

	key := make(map[string]string, len(raw))

	for _, pair := range raw {
		column, value, found := strings.Cut(pair, ":")
		if !found || column == "" {
			ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
				"key "+pair+" is not a column:value pair")

			return nil, false
		}

		decoded, err := url.QueryUnescape(value)
		if err != nil {
			ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
				"key "+pair+" is not valid percent-encoding")

			return nil, false
		}

		key[column] = decoded
	}

	return key, true
}
