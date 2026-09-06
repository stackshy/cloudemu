// Package azurefirewall implements the Azure Firewall
// (Microsoft.Network/azureFirewalls) and Azure Firewall Policy
// (Microsoft.Network/firewallPolicies) ARM REST APIs as a server.Handler.
// Real github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork
// AzureFirewallsClient / FirewallPoliciesClient clients, and the Terraform
// azurerm provider, configured with a custom endpoint hit this handler the same
// way they hit management.azure.com, driving the shared azurefirewall driver.
//
// Azure Firewall shares the Microsoft.Network ARM provider with the VNet, DNS,
// Load Balancer and Application Gateway handlers, but claims two disjoint
// resource types (azureFirewalls and firewallPolicies), so registration order
// between them is unconstrained. All must register before the permissive
// BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../azureFirewalls/{name}    — AzureFirewalls.BeginCreateOrUpdate (LRO, sync 201/200)
//	GET    .../azureFirewalls/{name}    — AzureFirewalls.Get
//	PATCH  .../azureFirewalls/{name}    — AzureFirewalls.UpdateTags
//	DELETE .../azureFirewalls/{name}    — AzureFirewalls.BeginDelete (LRO, sync-200)
//	GET    .../{scope}/…/azureFirewalls — AzureFirewalls.List / ListAll
//	(and the identical surface for firewallPolicies)
//
// The whole resource arrives in one PUT body and fully replaces the stored state
// (ARM CreateOrUpdate). The firewall sku (name/tier) and threatIntelMode live
// nested under properties and are modeled explicitly (the server-wide echo only
// reaches the top-level properties object, not its nested values, and must inject
// the threatIntelMode default and the terminal provisioningState); zones is
// top-level; each ipConfiguration round-trips with a stamped ARM id and a
// computed privateIPAddress; firewallPolicy is the association back-reference.
// Every other property is preserved verbatim so deferred sub-surfaces (classic
// rule collections, IDPS, TLS inspection, DNS proxy, rule collection groups) stay
// echo-through-safe.
package firewall

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fwdriver "github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

// Handler serves Microsoft.Network/azureFirewalls and firewallPolicies ARM
// requests against an Azure Firewall driver.
type Handler struct {
	fw fwdriver.AzureFirewalls
}

// New returns an Azure Firewall handler backed by fw.
func New(fw fwdriver.AzureFirewalls) *Handler {
	return &Handler{fw: fw}
}

// isFirewallsType reports whether the ARM resource type is azureFirewalls.
func isFirewallsType(resourceType string) bool {
	return strings.EqualFold(resourceType, typeAzureFirewalls)
}

// isPoliciesType reports whether the ARM resource type is firewallPolicies.
func isPoliciesType(resourceType string) bool {
	return strings.EqualFold(resourceType, typeFirewallPolicies)
}

// Matches claims ARM URLs targeting Microsoft.Network/azureFirewalls or
// firewallPolicies. Disjoint from the network / dns / load-balancer / app-gateway
// handlers on the same provider, so registration order between them is
// unconstrained. Registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && (isFirewallsType(rp.ResourceType) || isPoliciesType(rp.ResourceType))
}

// ServeHTTP routes on resource type, then path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if isFirewallsType(rp.ResourceType) {
		h.serveFirewall(w, r, &rp)
		return
	}

	h.servePolicy(w, r, &rp)
}

// serveFirewall routes an azureFirewalls request.
//
//nolint:dupl // firewall and policy routers are parallel over distinct resource types and driver methods.
func (h *Handler) serveFirewall(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listFirewalls(w, r, rp)

		return
	}

	// A sub-resource segment on a firewall is not part of the core control plane;
	// there is no independently-addressable child (rule collections are echoed in
	// the body). Reject cleanly rather than misparsing it as a firewall request.
	if rp.SubResource != "" {
		writeSubResourceDeferred(w)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateFirewall(w, r, rp)
	case http.MethodGet:
		h.getFirewall(w, r, rp)
	case http.MethodPatch:
		h.updateFirewallTags(w, r, rp)
	case http.MethodDelete:
		h.deleteFirewall(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// servePolicy routes a firewallPolicies request.
//
//nolint:dupl // firewall and policy routers are parallel over distinct resource types and driver methods.
func (h *Handler) servePolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listPolicies(w, r, rp)

		return
	}

	// ruleCollectionGroups and other policy sub-resources are deferred; reject
	// cleanly rather than misparsing them as a policy request.
	if rp.SubResource != "" {
		writeSubResourceDeferred(w)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdatePolicy(w, r, rp)
	case http.MethodGet:
		h.getPolicy(w, r, rp)
	case http.MethodPatch:
		h.updatePolicyTags(w, r, rp)
	case http.MethodDelete:
		h.deletePolicy(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// PurgeResourceGroup deletes every firewall and firewall policy stored under the
// given resource group, backing the resource-group cascade delete. Best-effort:
// a single failure is returned but does not stop the remaining teardown. The
// subscription is unused (the emulator is single-estate).
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	var firstErr error

	fws, err := h.fw.ListAzureFirewalls(ctx, resourceGroup)
	if err != nil {
		firstErr = err
	}

	for i := range fws {
		if derr := h.fw.DeleteAzureFirewall(ctx, fws[i].ResourceGroup, fws[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	pols, err := h.fw.ListFirewallPolicies(ctx, resourceGroup)
	if err != nil && firstErr == nil {
		firstErr = err
	}

	for i := range pols {
		if derr := h.fw.DeleteFirewallPolicy(ctx, pols[i].ResourceGroup, pols[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

// writeSubResourceDeferred rejects an addressable sub-resource path (rule
// collection groups, ...) that the core control plane does not model yet.
func writeSubResourceDeferred(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotFound, "NotFound",
		"azure firewall sub-resource is not supported")
}
