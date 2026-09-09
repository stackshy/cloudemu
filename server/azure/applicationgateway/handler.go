// Package applicationgateway implements the Azure Application Gateway
// (Microsoft.Network/applicationGateways) ARM REST API as a server.Handler.
// Real github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork
// ApplicationGatewaysClient clients, and the Terraform azurerm provider,
// configured with a custom endpoint hit this handler the same way they hit
// management.azure.com, driving the shared applicationgateway driver.
//
// Application Gateway shares the Microsoft.Network ARM provider with the VNet
// handler (server/azure/vnet), the DNS handler and the Load Balancer handler,
// but on a disjoint resource type — this handler claims applicationGateways
// while those claim virtualNetworks / dnsZones / loadBalancers — so registration
// order between them is unconstrained. All must register before the permissive
// BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../applicationGateways/{name}   — ApplicationGateways.BeginCreateOrUpdate (LRO, sync 201/200)
//	GET    .../applicationGateways/{name}   — ApplicationGateways.Get
//	PATCH  .../applicationGateways/{name}   — ApplicationGateways.UpdateTags
//	DELETE .../applicationGateways/{name}   — ApplicationGateways.BeginDelete (LRO, sync-200)
//	GET    .../resourceGroups/{rg}/…/applicationGateways — ApplicationGateways.List (RG scope)
//	GET    .../subscriptions/{s}/…/applicationGateways   — ApplicationGateways.ListAll (sub scope)
//
// The whole gateway arrives in one PUT body and fully replaces the stored state
// (ARM CreateOrUpdate semantics). SKU (name/tier/capacity), zones and identity
// are modeled explicitly — the server-wide unmodeled-property echo only reaches
// the top-level "properties" object and so cannot preserve them. The seven
// required nested collections plus optional probes/sslCertificates are modeled:
// each item gets an ARM sub-resource id self-link and a provisioningState, while
// its property values (ports, protocols, cross-collection {id} references,
// explicit booleans) round-trip verbatim. Every other top-level property (WAF,
// urlPathMaps, autoscaleConfiguration, ...) is a deferred sub-surface preserved
// verbatim so it is echo-through-safe.
//
// Unlike Load Balancer, Application Gateway has NO standalone child ARM
// operation groups (backendAddressPools/httpListeners/... are not independently
// addressable) — every child is managed only through the whole-gateway PUT. A
// sub-resource path is therefore served read-only (Get/List reflection of the
// inline children, with ids), and 405s on child PUT/DELETE (subresource.go).
package applicationgateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	agdriver "github.com/stackshy/cloudemu/v2/services/applicationgateway/driver"
)

// Handler serves Microsoft.Network/applicationGateways ARM requests against an
// application gateway driver.
type Handler struct {
	gw agdriver.AzureApplicationGateways
}

// New returns an Azure Application Gateway handler backed by gw.
func New(gw agdriver.AzureApplicationGateways) *Handler {
	return &Handler{gw: gw}
}

// isAppGwsType reports whether the ARM resource type is applicationGateways,
// case-insensitively (the subscription-scoped list may use lowercase).
func isAppGwsType(resourceType string) bool {
	return strings.EqualFold(resourceType, typeAppGws)
}

// Matches claims ARM URLs targeting Microsoft.Network/applicationGateways.
// Disjoint from the network / dns / load-balancer handlers on the same provider,
// so registration order between them is unconstrained. Registered before the
// BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && isAppGwsType(rp.ResourceType)
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Collection list: no gateway name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listGateways(w, r, &rp)

		return
	}

	// A sub-resource segment addresses one child collection of the gateway, not
	// the gateway itself — route it to the read-only reflection before any
	// whole-gateway handler sees it, so a child GET is never misparsed as a
	// whole-gateway request scoped to the gateway's own name.
	if rp.SubResource != "" {
		h.serveSubResource(w, r, &rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateGateway(w, r, &rp)
	case http.MethodGet:
		h.getGateway(w, r, &rp)
	case http.MethodPatch:
		h.updateGatewayTags(w, r, &rp)
	case http.MethodDelete:
		h.deleteGateway(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// PurgeResourceGroup deletes every application gateway stored under the given
// resource group, backing the resource-group cascade delete. Gateways are stored
// resource-group-natively (keyed by resourceGroup+name), so
// ListAzureApplicationGateways already scopes to the group. Best-effort: a
// single failure is returned but does not stop the remaining teardown. The
// subscription is unused (the emulator is single-estate).
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	gws, err := h.gw.ListAzureApplicationGateways(ctx, resourceGroup)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range gws {
		if derr := h.gw.DeleteAzureApplicationGateway(ctx, gws[i].ResourceGroup, gws[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
