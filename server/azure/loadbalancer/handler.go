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
package loadbalancer

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Handler serves Microsoft.Network/loadBalancers ARM requests against a
// loadbalancer driver.
type Handler struct {
	lb lbdriver.LoadBalancer
}

// New returns an Azure Load Balancer handler backed by lb.
func New(lb lbdriver.LoadBalancer) *Handler {
	return &Handler{lb: lb}
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

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateLoadBalancer(w, r, &rp)
	case http.MethodGet:
		h.getLoadBalancer(w, r, &rp)
	case http.MethodDelete:
		h.deleteLoadBalancer(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
