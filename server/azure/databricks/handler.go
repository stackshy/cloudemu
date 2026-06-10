// Package databricks implements the Azure Databricks (Microsoft.Databricks)
// ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com.
//
// MVP coverage (Microsoft.Databricks/workspaces):
//
//	PUT    .../resourceGroups/{rg}/providers/Microsoft.Databricks/workspaces/{w} — Create or update
//	GET    .../resourceGroups/{rg}/providers/Microsoft.Databricks/workspaces/{w} — Get
//	PATCH  .../resourceGroups/{rg}/providers/Microsoft.Databricks/workspaces/{w} — Update tags
//	DELETE .../resourceGroups/{rg}/providers/Microsoft.Databricks/workspaces/{w} — Delete
//	GET    .../resourceGroups/{rg}/providers/Microsoft.Databricks/workspaces     — List by resource group
//	GET    /subscriptions/{sub}/providers/Microsoft.Databricks/workspaces        — List by subscription
//
// Mutating ops return 200 OK with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package databricks

import (
	"net/http"

	dbxdriver "github.com/stackshy/cloudemu/databricks/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName = "Microsoft.Databricks"
	resourceType = "workspaces"
)

// Handler serves Microsoft.Databricks ARM requests against a Databricks driver.
type Handler struct {
	dbx dbxdriver.Databricks
}

// New returns an Azure Databricks handler backed by drv.
func New(drv dbxdriver.Databricks) *Handler {
	return &Handler{dbx: drv}
}

// Matches returns true for ARM Microsoft.Databricks/workspaces paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == resourceType
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")

		return
	}

	// Collection: list by resource group (rg present) or by subscription.
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listWorkspaces(w, r, &rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateWorkspace(w, r, &rp)
	case http.MethodGet:
		h.getWorkspace(w, r, &rp)
	case http.MethodPatch:
		h.updateWorkspace(w, r, &rp)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
