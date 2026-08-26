// Package loadbalancer implements the Azure Load Balancer
// (Microsoft.Network/loadBalancers) ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork
// LoadBalancersClient clients configured with a custom endpoint hit this
// handler the same way they hit management.azure.com, driving the shared
// loadbalancer driver.
//
// Azure Load Balancer shares the Microsoft.Network ARM provider with the VNet
// handler (server/azure/network) and the DNS handler (server/azure/dns), but on
// a disjoint resource type — this handler claims loadBalancers while the network
// handler claims virtualNetworks / networkSecurityGroups / locations and the DNS
// handler claims dnsZones — so registration order between them is unconstrained.
// All must register before the permissive BlobStorage fallback.
//
// Driver-abstraction mapping (Azure → loadbalancer driver):
//
//	loadBalancers/{name}                       → LoadBalancer (LBInfo.Name = {name})
//	.../backendAddressPools/{pool}             → TargetGroup  (one per pool)
//	.../loadBalancingRules/{rule}              → Listener     (FrontendPort→Port,
//	                                             Protocol, BackendAddressPool→TargetGroupARN)
//	.../frontendIPConfigurations/{fe}          → reflected back on Get/List only
//
// Azure addresses the load balancer by its user-assigned name in the URL, while
// the driver keys load balancers on a generated id. The handler resolves the
// SDK-facing name to the driver LB via a DescribeLoadBalancers scan (LBInfo.Name
// is preserved verbatim by the driver), and scopes backend pools / rules to
// their parent load balancer via internal tags so multiple load balancers stay
// isolated.
//
// Coverage:
//
//	PUT    .../loadBalancers/{name}            — LoadBalancers.BeginCreateOrUpdate (LRO, sync-200)
//	GET    .../loadBalancers/{name}            — LoadBalancers.Get
//	DELETE .../loadBalancers/{name}            — LoadBalancers.BeginDelete (LRO, sync-200)
//	GET    .../resourceGroups/{rg}/…/loadBalancers    — LoadBalancers.List (RG scope)
//	GET    .../subscriptions/{s}/…/loadBalancers      — LoadBalancers.ListAll (sub scope)
//
// Sub-resource requests (a URL with a segment past the load balancer name)
// route through serveSubResource (subresource.go) to true per-child CRUD
// instead of ever reaching the whole-LB handlers above, matching the real ARM
// operation surface per child kind:
//
//	GET                     .../backendAddressPools[/{name}]      — Get/List
//	PUT/DELETE              .../backendAddressPools/{name}        — BeginCreateOrUpdate/BeginDelete (LRO, sync-200)
//	GET                     .../inboundNatRules[/{name}]          — Get/List
//	PUT/DELETE              .../inboundNatRules/{name}            — BeginCreateOrUpdate/BeginDelete (LRO, sync-200)
//	GET                     .../probes[/{name}]                   — Get/List only (405 on PUT/DELETE)
//	GET                     .../loadBalancingRules[/{name}]       — Get/List only (405 on PUT/DELETE)
//	GET                     .../outboundRules[/{name}]            — Get/List only (405 on PUT/DELETE)
//	GET                     .../frontendIPConfigurations[/{name}] — Get/List only (405 on PUT/DELETE)
//
// inboundNatPools has no standalone ARM operation group at all — it is
// reflected only as a nested array inside the whole load balancer body, so a
// request addressing it standalone 404s.
package loadbalancer

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Handler serves Microsoft.Network/loadBalancers ARM requests against a
// loadbalancer driver.
type Handler struct {
	lb lbdriver.LoadBalancer
	// nics resolves NIC ipConfigurations so a backend address pool can project
	// its read-only backendIPConfigurations from the NIC side of the
	// association. Optional: nil when the networking driver is absent, in which
	// case pools report no members. Wired via SetNICResolver.
	nics netdriver.AzureNetworkInterfaces
}

// New returns an Azure Load Balancer handler backed by lb.
func New(lb lbdriver.LoadBalancer) *Handler {
	return &Handler{lb: lb}
}

// SetNICResolver wires the Azure network-interface surface so a backend
// address pool can project the ipConfigurations that reference it into its
// read-only backendIPConfigurations. Without it, pools resolve with no members.
func (h *Handler) SetNICResolver(nics netdriver.AzureNetworkInterfaces) {
	h.nics = nics
}

// poolMembers builds a lookup from a backend address pool's ARM id (lower-cased,
// since ARM ids are case-insensitive) to the ipConfiguration ARM ids that have
// joined it, scanning every NIC's stored loadBalancerBackendAddressPools. It
// returns nil when no NIC resolver is wired, so pools simply carry no members.
func (h *Handler) poolMembers(ctx context.Context, subscription string) map[string][]string {
	if h.nics == nil {
		return nil
	}

	nics, err := h.nics.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return nil
	}

	members := make(map[string][]string)

	for i := range nics {
		nic := &nics[i]

		for j := range nic.IPConfigs {
			cfg := &nic.IPConfigs[j]
			ipCfgID := nicIPConfigID(subscription, nic.ResourceGroup, nic.Name, cfg.Name)

			for _, poolID := range cfg.LBBackendPoolIDs {
				key := strings.ToLower(poolID)
				members[key] = append(members[key], ipCfgID)
			}
		}
	}

	return members
}

// nicIPConfigID builds the nested ARM resource id of a NIC ipConfiguration,
// used as a backend address pool's backendIPConfigurations reference.
func nicIPConfigID(subscription, resourceGroup, nicName, configName string) string {
	return "/subscriptions/" + subscription +
		"/resourceGroups/" + resourceGroup +
		"/providers/" + providerName + "/networkInterfaces/" +
		nicName + "/ipConfigurations/" + configName
}

// isLBsType reports whether the ARM resource type is loadBalancers,
// case-insensitively (the subscription-scoped list may use lowercase).
func isLBsType(resourceType string) bool {
	return strings.EqualFold(resourceType, typeLBs)
}

// Matches claims ARM URLs targeting Microsoft.Network/loadBalancers. Disjoint
// from the network handler (virtualNetworks / networkSecurityGroups /
// locations) and the dns handler (dnsZones) on the same provider, so
// registration order between them is unconstrained. Registered before the
// BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && isLBsType(rp.ResourceType)
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Collection list: no load balancer name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listLoadBalancers(w, r, &rp)

		return
	}

	// A sub-resource segment (backendAddressPools, probes, loadBalancingRules,
	// inboundNatRules, inboundNatPools, outboundRules, frontendIPConfigurations)
	// addresses one child of the load balancer, not the load balancer itself —
	// route it to per-child CRUD before any whole-LB handler ever sees the
	// request. Falling through here would let a standalone child PUT/GET/DELETE
	// be misparsed as a whole-LB request scoped to rp.ResourceName, silently
	// wiping or deleting every other child.
	if rp.SubResource != "" {
		h.serveSubResource(w, r, &rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateLoadBalancer(w, r, &rp)
	case http.MethodGet:
		h.getLoadBalancer(w, r, &rp)
	case http.MethodPatch:
		h.updateLoadBalancerTags(w, r, &rp)
	case http.MethodDelete:
		h.deleteLoadBalancer(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
