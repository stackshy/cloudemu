package databricks

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveOperations handles GET /providers/Microsoft.Databricks/operations.
func (h *Handler) serveOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)

		return
	}

	ops, err := h.dbx.ListOperations(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]armOperation, 0, len(ops))
	for i := range ops {
		out = append(out, toARMOperation(&ops[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, armOperationList{Value: out})
}
