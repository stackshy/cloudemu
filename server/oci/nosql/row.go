package nosql

import (
	"encoding/json"
	"net/http"
	"strings"

	nosqlprovider "github.com/stackshy/cloudemu/v2/providers/oci/nosql"
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

	compartmentID := ocirest.CompartmentID(r)

	if _, err := h.findTable(r, compartmentID, tableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	row, err := h.extras.GetOCIRow(r.Context(), compartmentID, tableID, key)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, rowBody{Value: row.Value, TimeOfExpiration: row.TimeOfExpiration})
}

// putRow serves UpdateRow, OCI's upsert. A write its option refuses is still
// a 200, with a null version.
func (h *Handler) putRow(w http.ResponseWriter, r *http.Request, tableID string) {
	var req updateRowRequest

	// Numbers stay json.Number, so a LONG above 2^53 is not rounded through
	// float64 on the way in.
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()

	if err := dec.Decode(&req); err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "invalid JSON: "+err.Error())
		return
	}

	if len(req.Value) == 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "value is required")
		return
	}

	compartmentID := compartmentOf(r, req.CompartmentID)

	if _, err := h.findTable(r, compartmentID, tableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	opts := nosqlprovider.WriteOptions{Option: req.Option, ExactMatch: req.IsExactMatch}

	// A row TTL replaces the table's unless isTtlUseTableDefault asks for it.
	if req.TTL != nil && !req.IsTTLUseTableDefault {
		opts.TTLDays = req.TTL
	}

	res, err := h.extras.PutOCIRow(r.Context(), compartmentID, tableID, req.Value, opts)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	var out updateRowResult

	if res.Version != "" {
		out.Version = &res.Version
	}

	if req.IsGetReturnRow && res.Existing != nil {
		out.ExistingVersion = res.Existing.Version
		out.ExistingValue = res.Existing.Value
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) deleteRow(w http.ResponseWriter, r *http.Request, tableID string) {
	key, ok := decodeKey(w, r)
	if !ok {
		return
	}

	compartmentID := ocirest.CompartmentID(r)

	if _, err := h.findTable(r, compartmentID, tableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	deleted, err := h.extras.DeleteOCIRow(r.Context(), compartmentID, tableID, key)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, deleteRowResult{IsSuccess: deleted})
}

// decodeKey reads OCI's repeated key parameter, each entry a "column:value"
// pair. A pair without a colon is refused rather than read as a bare column.
// The query is already percent-decoded once, which is all the SDK encodes.
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

		key[column] = value
	}

	return key, true
}
