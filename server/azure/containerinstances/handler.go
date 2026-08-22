// Package containerinstances implements the Azure Container Instances
// (Microsoft.ContainerInstance/containerGroups) ARM REST API as a
// server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance
// clients configured with a custom endpoint drive the shared ACI driver's
// container-group control plane here the same way they hit
// management.azure.com.
//
// Coverage:
//
//	PUT    .../providers/Microsoft.ContainerInstance/containerGroups/{name}   — ContainerGroups.BeginCreateOrUpdate (LRO, completes inline)
//	GET    .../providers/Microsoft.ContainerInstance/containerGroups/{name}   — ContainerGroups.Get
//	DELETE .../providers/Microsoft.ContainerInstance/containerGroups/{name}   — ContainerGroups.BeginDelete (LRO, completes inline)
//	GET    .../providers/Microsoft.ContainerInstance/containerGroups          — ContainerGroups.ListByResourceGroup / List
//	GET    .../containerGroups/{cg}/containers/{c}/logs                        — Containers.ListLogs
package containerinstances

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
)

const (
	providerName        = "Microsoft.ContainerInstance"
	typeContainerGroups = "containerGroups"

	// subResourceContainers and subActionLogs identify the per-container logs
	// sub-path .../containerGroups/{cg}/containers/{c}/logs.
	subResourceContainers = "containers"
	subActionLogs         = "logs"
)

// Handler serves Microsoft.ContainerInstance/containerGroups ARM requests
// against a ContainerInstances driver.
type Handler struct {
	aci driver.ContainerInstances
}

// New returns an Azure Container Instances handler backed by aci.
func New(aci driver.ContainerInstances) *Handler {
	return &Handler{aci: aci}
}

// Matches claims ARM URLs targeting Microsoft.ContainerInstance/containerGroups.
// The provider name is unique among Azure handlers, so registration order is
// unconstrained; registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == typeContainerGroups
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")

		return
	}

	// Per-container logs: .../containerGroups/{cg}/containers/{c}/logs
	if rp.SubResource == subResourceContainers && rp.SubResourceAction == subActionLogs {
		h.containerLogs(w, r, &rp)

		return
	}

	// Collection list: no group name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listGroups(w, r, &rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateGroup(w, r, &rp)
	case http.MethodGet:
		h.getGroup(w, r, &rp)
	case http.MethodDelete:
		h.deleteGroup(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
