package nosql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveQuery routes /20190828/query and reports the statement endpoints
// CloudEmu does not serve.
func (h *Handler) serveQuery(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.ID {
	case "":
	case queryPrepare, querySummarize:
		unemulated(w, r, "query/"+rt.ID,
			"CloudEmu has no prepared-statement handle to hand back or bind variables to")

		return
	default:
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown query sub-resource "+rt.ID)
		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	h.runQuery(w, r)
}

// runQuery executes one statement. OCI's REST API has no multi-delete
// operation, so DELETE FROM over this endpoint is how several rows go at once.
func (h *Handler) runQuery(w http.ResponseWriter, r *http.Request) {
	var req queryRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.Statement == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "statement is required")
		return
	}

	items, err := h.extras.QueryOCI(r.Context(), req.CompartmentID, req.Statement, req.Limit)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if items == nil {
		items = []map[string]any{}
	}

	ocirest.WriteJSON(w, r, http.StatusOK, queryResultCollection{Items: paginate(w, r, items)})
}
