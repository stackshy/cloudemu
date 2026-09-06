// Package bastion implements the Azure Bastion (Microsoft.Network/bastionHosts)
// ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork
// BastionHostsClient clients, and the Terraform azurerm provider, configured
// with a custom endpoint hit this handler the same way they hit
// management.azure.com, driving the shared bastion driver.
//
// Azure Bastion shares the Microsoft.Network ARM provider with the VNet, DNS,
// Load Balancer, Application Gateway and Firewall handlers, but claims a disjoint
// resource type (bastionHosts), so registration order relative to them is
// unconstrained. It must register before the permissive BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../bastionHosts/{name}    — BastionHosts.BeginCreateOrUpdate (LRO, sync 201/200)
//	GET    .../bastionHosts/{name}    — BastionHosts.Get
//	PATCH  .../bastionHosts/{name}    — BastionHosts.UpdateTags
//	DELETE .../bastionHosts/{name}    — BastionHosts.BeginDelete (LRO, sync-200)
//	GET    .../{scope}/…/bastionHosts — BastionHosts.List / ListByResourceGroup
//
// The whole host arrives in one PUT body and fully replaces the stored state
// (ARM CreateOrUpdate). sku and zones are top-level; the dnsName is computed once
// and stable; scaleUnits, the six feature toggles and ipConfigurations are
// modeled under properties; every other property is preserved verbatim so
// deferred sub-surfaces stay echo-through-safe. Real bastion session tunneling
// (the data plane) is out of scope.
package bastion

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	bastiondriver "github.com/stackshy/cloudemu/v2/services/bastion/driver"
)

// Handler serves Microsoft.Network/bastionHosts ARM requests against a Bastion
// host driver.
type Handler struct {
	hosts bastiondriver.BastionHosts
}

// New returns an Azure Bastion handler backed by hosts.
func New(hosts bastiondriver.BastionHosts) *Handler {
	return &Handler{hosts: hosts}
}

// Matches claims ARM URLs targeting Microsoft.Network/bastionHosts. Disjoint from
// the network / dns / load-balancer / app-gateway / firewall handlers on the same
// provider, so registration order relative to them is unconstrained. Registered
// before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && strings.EqualFold(rp.ResourceType, typeBastionHosts)
}

// ServeHTTP routes on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listBastionHosts(w, r, &rp)

		return
	}

	// A sub-resource segment is not part of the control plane; ipConfigurations
	// are echoed in the host body and there is no independently-addressable child.
	// Reject cleanly rather than misparsing it as a host request.
	if rp.SubResource != "" {
		writeSubResourceDeferred(w)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateBastionHost(w, r, &rp)
	case http.MethodGet:
		h.getBastionHost(w, r, &rp)
	case http.MethodPatch:
		h.updateBastionHostTags(w, r, &rp)
	case http.MethodDelete:
		h.deleteBastionHost(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// PurgeResourceGroup deletes every bastion host stored under the given resource
// group, backing the resource-group cascade delete. Best-effort: a single failure
// is returned but does not stop the remaining teardown. The subscription is
// unused (the emulator is single-estate).
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	var firstErr error

	hosts, err := h.hosts.ListBastionHosts(ctx, resourceGroup)
	if err != nil {
		firstErr = err
	}

	for i := range hosts {
		if derr := h.hosts.DeleteBastionHost(ctx, hosts[i].ResourceGroup, hosts[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

// writeSubResourceDeferred rejects an addressable sub-resource path that the
// control plane does not model.
func writeSubResourceDeferred(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotFound, "NotFound",
		"bastion host sub-resource is not supported")
}
