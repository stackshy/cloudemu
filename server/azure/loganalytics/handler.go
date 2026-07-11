// Package loganalytics implements the Azure Log Analytics
// (Microsoft.OperationalInsights/workspaces) ARM REST API as a server.Handler.
// Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com, driving the shared logging driver.
//
// The workspace lifecycle (CreateOrUpdate / Get / List / Delete) maps onto the
// logging driver's log-group lifecycle: a workspace is a log group. The
// retention-in-days and tags carry across; ARM read-only fields
// (provisioningState, customerId, sku) are synthesized. The data-plane
// log-query API (api.loganalytics.io "query" / ingestion) is a separate wire
// surface and is out of scope for this slice — see the package README note in
// the roundtrip test.
//
// Microsoft.OperationalInsights is a distinct ARM provider name from every
// other Azure handler (compute, network, DNS, sql, …), so registration order is
// unconstrained. It must register before the permissive BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../workspaces/{w}                                — Workspaces.BeginCreateOrUpdate (LRO, completes inline)
//	GET    .../workspaces/{w}                                — Workspaces.Get
//	DELETE .../workspaces/{w}                                — Workspaces.BeginDelete (LRO, completes inline)
//	GET    .../providers/Microsoft.OperationalInsights/workspaces — Workspaces.NewListPager (subscription scope)
//	GET    .../resourceGroups/{rg}/…/workspaces             — Workspaces.NewListByResourceGroupPager
package loganalytics

import (
	"net/http"
	"strings"

	logdriver "github.com/stackshy/cloudemu/logging/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName = "Microsoft.OperationalInsights"
	// typeWorkspaces is the ARM resource type. The subscription-scoped list path
	// may serialize it lowercase, so matching is case-insensitive.
	typeWorkspaces = "workspaces"
)

// Handler serves Microsoft.OperationalInsights/workspaces ARM requests against
// a logging driver.
type Handler struct {
	logs logdriver.Logging
}

// New returns an Azure Log Analytics handler backed by l.
func New(l logdriver.Logging) *Handler {
	return &Handler{logs: l}
}

// isWorkspacesType reports whether the ARM resource type is workspaces,
// case-insensitively.
func isWorkspacesType(resourceType string) bool {
	return strings.EqualFold(resourceType, typeWorkspaces)
}

// Matches claims ARM URLs targeting Microsoft.OperationalInsights/workspaces.
// Its provider name is disjoint from every other Azure handler, so registration
// order is unconstrained. Registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && isWorkspacesType(rp.ResourceType)
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Collection list: no workspace name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		h.serveWorkspaceCollection(w, r, &rp)
		return
	}

	h.serveWorkspace(w, r, &rp)
}

func (h *Handler) serveWorkspaceCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listWorkspaces(w, r, rp)
}

func (h *Handler) serveWorkspace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateWorkspace(w, r, rp)
	case http.MethodGet:
		h.getWorkspace(w, r, rp)
	case http.MethodDelete:
		h.deleteWorkspace(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
