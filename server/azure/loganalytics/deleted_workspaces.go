package loganalytics

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveDeletedWorkspaces answers the subscription and resource-group scoped
// deletedWorkspaces lists. cloudemu has no soft delete: every workspace DELETE
// is permanent, as with force=true, so there is never a recoverable workspace
// and the list is empty. azurerm reads it on every workspace create.
func serveDeletedWorkspaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName != "" {
		azurearm.WriteUnknownType(w, r, rp)
		return
	}

	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, workspaceListResult{Value: []workspaceJSON{}})
}
