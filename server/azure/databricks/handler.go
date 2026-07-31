// Package databricks implements the Azure Databricks (Microsoft.Databricks)
// ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com.
//
// Coverage (Microsoft.Databricks):
//
//	workspaces                                       CRUD, list by RG / subscription (#164)
//	accessConnectors                                 CRUD, update, list by RG / subscription
//	workspaces/{w}/privateEndpointConnections        create, get, list, delete
//	workspaces/{w}/privateLinkResources              get, list
//	workspaces/{w}/virtualNetworkPeerings            createOrUpdate, get, list, delete
//	workspaces/{w}/outboundNetworkDependenciesEndpoints  list
//	/providers/Microsoft.Databricks/operations       list
//
// Mutating ops return 200 OK with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package databricks

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

const (
	providerName = "Microsoft.Databricks"
	resourceType = "workspaces"

	accessConnectorsType = "accessConnectors"

	// Workspace sub-resource collections.
	subPEC      = "privateEndpointConnections"
	subPLR      = "privateLinkResources"
	subPeering  = "virtualNetworkPeerings"
	subOutbound = "outboundNetworkDependenciesEndpoints"

	// operationsPath is the subscription-less provider operations list path,
	// which azurearm.ParsePath does not model (it requires a /subscriptions
	// prefix), so the handler matches it directly.
	operationsPath = "providers/" + providerName + "/operations"
)

// Handler serves Microsoft.Databricks ARM requests against a Databricks driver.
type Handler struct {
	dbx dbxdriver.Databricks
}

// New returns an Azure Databricks handler backed by drv.
func New(drv dbxdriver.Databricks) *Handler {
	return &Handler{dbx: drv}
}

// Matches returns true for the Microsoft.Databricks ARM surface: workspaces (and
// their sub-resources), accessConnectors, and the provider operations list.
func (*Handler) Matches(r *http.Request) bool {
	if isOperationsPath(r.URL.Path) {
		return true
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName &&
		(rp.ResourceType == resourceType || rp.ResourceType == accessConnectorsType)
}

// isOperationsPath reports whether urlPath is the provider operations list path
// (case-insensitive on the provider segment, tolerant of a trailing slash).
func isOperationsPath(urlPath string) bool {
	return strings.EqualFold(strings.Trim(urlPath, "/"), operationsPath)
}

// ServeHTTP routes the request based on resource type, path shape, and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isOperationsPath(r.URL.Path) {
		h.serveOperations(w, r)

		return
	}

	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")

		return
	}

	switch rp.ResourceType {
	case accessConnectorsType:
		h.serveAccessConnectors(w, r, &rp)
	case resourceType:
		h.serveWorkspaces(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "unsupported resource type")
	}
}

// serveWorkspaces routes workspace collection, resource, and sub-resource paths.
func (h *Handler) serveWorkspaces(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// Collection: list by resource group (rg present) or by subscription.
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listWorkspaces(w, r, rp)

		return
	}

	if rp.SubResource != "" {
		h.serveWorkspaceChild(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateWorkspace(w, r, rp)
	case http.MethodGet:
		h.getWorkspace(w, r, rp)
	case http.MethodPatch:
		h.updateWorkspace(w, r, rp)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// serveWorkspaceChild routes a workspace sub-resource collection.
func (h *Handler) serveWorkspaceChild(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch rp.SubResource {
	case subPEC:
		h.servePEC(w, r, rp)
	case subPLR:
		h.servePLR(w, r, rp)
	case subPeering:
		h.servePeering(w, r, rp)
	case subOutbound:
		h.serveOutbound(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "unsupported sub-resource")
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
