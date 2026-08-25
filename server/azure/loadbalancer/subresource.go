package loadbalancer

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// subResourceKind identifies which nested collection an ARM sub-resource path
// segment addresses. Real ARM exposes each kind with a different operation
// surface (see kindOps below); routing must dispatch on the kind before any
// whole-LB handler ever sees the request, or a standalone child PUT/GET/DELETE
// gets misparsed as a whole-LB request scoped to the load balancer's own name.
type subResourceKind int

const (
	kindUnknown subResourceKind = iota
	kindFrontendIPConfigurations
	kindBackendAddressPools
	kindLoadBalancingRules
	kindProbes
	kindInboundNatRules
	kindOutboundRules
)

// parseSubResourceKind maps the ARM URL segment to the kind it addresses.
// inboundNatPools is deliberately absent: real ARM has no standalone
// inboundNatPools operation group at all (Get/List/CreateOrUpdate/Delete) —
// it is reflected only as a nested array inside the whole load balancer body.
func parseSubResourceKind(segment string) subResourceKind {
	switch segment {
	case subResourceFrontendIPConfigurations:
		return kindFrontendIPConfigurations
	case subResourceBackendAddressPools:
		return kindBackendAddressPools
	case subResourceLoadBalancingRules:
		return kindLoadBalancingRules
	case subResourceProbes:
		return kindProbes
	case subResourceInboundNatRules:
		return kindInboundNatRules
	case subResourceOutboundRules:
		return kindOutboundRules
	default:
		return kindUnknown
	}
}

// standaloneCRUD reports whether kind has a real standalone
// CreateOrUpdate/Delete ARM operation group (backendAddressPools and
// inboundNatRules), as opposed to a kind that is Get/List-only in real ARM
// (probes, loadBalancingRules, outboundRules, frontendIPConfigurations) and
// can only be mutated through the whole-LB CreateOrUpdate.
func standaloneCRUD(kind subResourceKind) bool {
	return kind == kindBackendAddressPools || kind == kindInboundNatRules
}

// serveSubResource routes a request addressing one load-balancer sub-resource
// collection or child. Registered before any whole-LB handler runs.
func (h *Handler) serveSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	kind := parseSubResourceKind(rp.SubResource)
	if kind == kindUnknown {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"unknown load balancer sub-resource "+rp.SubResource)

		return
	}

	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listSubResource(w, r, rp, kind)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getSubResource(w, r, rp, kind)
	case http.MethodPut:
		h.putSubResource(w, r, rp, kind)
	case http.MethodDelete:
		h.deleteSubResource(w, r, rp, kind)
	default:
		writeMethodNotAllowed(w)
	}
}

// listSubResource handles GET .../loadBalancers/{name}/{kind} — every child of
// kind on the load balancer.
func (h *Handler) listSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind subResourceKind) {
	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "load balancer not found")
		return
	}

	lb, err := az.GetAzureLoadBalancer(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	lbID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeLBs, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, subResourceListResult{Value: listChildren(lbID, lb, kind)})
}

// getSubResource handles GET .../loadBalancers/{name}/{kind}/{childName} — the
// one addressed child, not the parent load balancer.
func (h *Handler) getSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind subResourceKind) {
	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "load balancer not found")
		return
	}

	lb, err := az.GetAzureLoadBalancer(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	lbID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeLBs, rp.ResourceName)

	child, found := findChild(lbID, lb, kind, rp.SubResourceName)
	if !found {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			rp.SubResource+" "+rp.SubResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, child)
}

// putSubResource handles PUT .../loadBalancers/{name}/{kind}/{childName} — the
// real standalone create/update ARM exposes only for backendAddressPools and
// inboundNatRules. It mutates only the addressed child, leaving every sibling
// untouched; every other kind has no standalone PUT in real ARM (400/405
// there, matching the SDK surface — see standaloneCRUD).
func (h *Handler) putSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind subResourceKind) {
	if !standaloneCRUD(kind) {
		writeMethodNotAllowed(w)
		return
	}

	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"load balancers are not supported by this driver")

		return
	}

	lbID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeLBs, rp.ResourceName)

	switch kind {
	case kindBackendAddressPools:
		putBackendPool(w, r, rp, az, lbID)
	case kindInboundNatRules:
		putNatRule(w, r, rp, az, lbID)
	case kindUnknown, kindFrontendIPConfigurations, kindLoadBalancingRules, kindProbes, kindOutboundRules:
		writeMethodNotAllowed(w)
	}
}

func putBackendPool(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, az lbdriver.AzureLoadBalancers, lbID string,
) {
	var body backendPoolJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	lb, err := az.UpsertAzureLBBackendPool(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	child, found := findChild(lbID, lb, kindBackendAddressPools, rp.SubResourceName)
	if !found {
		azurearm.WriteError(w, http.StatusInternalServerError, "InternalError", "backend address pool not found after create")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, child)
}

func putNatRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, az lbdriver.AzureLoadBalancers, lbID string,
) {
	var body inboundNatRuleJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	// The URL name is authoritative, matching ARM's PUT-by-name semantics; a
	// body name (if the caller sent one) is ignored.
	rule := buildNatRules([]inboundNatRuleJSON{body})[0]
	rule.Name = rp.SubResourceName

	lb, err := az.UpsertAzureLBNatRule(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, rule)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	child, found := findChild(lbID, lb, kindInboundNatRules, rp.SubResourceName)
	if !found {
		azurearm.WriteError(w, http.StatusInternalServerError, "InternalError", "inbound NAT rule not found after create")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, child)
}

// deleteSubResource handles DELETE .../loadBalancers/{name}/{kind}/{childName}
// — the real standalone delete ARM exposes only for backendAddressPools and
// inboundNatRules. It removes only the addressed child, leaving every sibling
// (and the parent load balancer itself) untouched.
func (h *Handler) deleteSubResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, kind subResourceKind) {
	if !standaloneCRUD(kind) {
		writeMethodNotAllowed(w)
		return
	}

	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "load balancer not found")
		return
	}

	var err error

	switch kind {
	case kindBackendAddressPools:
		err = az.DeleteAzureLBBackendPool(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	case kindInboundNatRules:
		err = az.DeleteAzureLBNatRule(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	case kindUnknown, kindFrontendIPConfigurations, kindLoadBalancingRules, kindProbes, kindOutboundRules:
		writeMethodNotAllowed(w)
		return
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// listChildren builds the full JSON slice for kind from lb, for the
// collection-list response envelope.
func listChildren(lbID string, lb *lbdriver.AzureLoadBalancer, kind subResourceKind) any {
	switch kind {
	case kindFrontendIPConfigurations:
		return frontendsJSON(lbID, lb.Frontends)
	case kindBackendAddressPools:
		return poolsJSON(lbID, lb.BackendPools)
	case kindLoadBalancingRules:
		return rulesJSON(lbID, lb.Rules)
	case kindProbes:
		return probesJSON(lbID, lb.Probes)
	case kindInboundNatRules:
		return natRulesJSON(lbID, lb.NatRules)
	case kindOutboundRules:
		return outboundRulesJSON(lbID, lb.OutboundRules)
	case kindUnknown:
		return []struct{}{}
	}

	return []struct{}{}
}

// findChild locates the single named child of kind on lb and returns its JSON
// representation. found is false when no child of that name exists.
func findChild(lbID string, lb *lbdriver.AzureLoadBalancer, kind subResourceKind, name string) (any, bool) {
	switch kind {
	case kindFrontendIPConfigurations:
		return findFrontend(lbID, lb.Frontends, name)
	case kindBackendAddressPools:
		return findPool(lbID, lb.BackendPools, name)
	case kindLoadBalancingRules:
		return findRule(lbID, lb.Rules, name)
	case kindProbes:
		return findProbe(lbID, lb.Probes, name)
	case kindInboundNatRules:
		return findNatRule(lbID, lb.NatRules, name)
	case kindOutboundRules:
		return findOutboundRule(lbID, lb.OutboundRules, name)
	case kindUnknown:
		return nil, false
	}

	return nil, false
}

func findFrontend(lbID string, in []lbdriver.AzureLBFrontend, name string) (any, bool) {
	for i := range in {
		if in[i].Name == name {
			return frontendsJSON(lbID, []lbdriver.AzureLBFrontend{in[i]})[0], true
		}
	}

	return nil, false
}

func findPool(lbID string, in []string, name string) (any, bool) {
	for _, p := range in {
		if p == name {
			return poolsJSON(lbID, []string{p})[0], true
		}
	}

	return nil, false
}

func findRule(lbID string, in []lbdriver.AzureLBRule, name string) (any, bool) {
	for i := range in {
		if in[i].Name == name {
			return rulesJSON(lbID, []lbdriver.AzureLBRule{in[i]})[0], true
		}
	}

	return nil, false
}

func findProbe(lbID string, in []lbdriver.AzureLBProbe, name string) (any, bool) {
	for i := range in {
		if in[i].Name == name {
			return probesJSON(lbID, []lbdriver.AzureLBProbe{in[i]})[0], true
		}
	}

	return nil, false
}

func findNatRule(lbID string, in []lbdriver.AzureLBNatRule, name string) (any, bool) {
	for i := range in {
		if in[i].Name == name {
			return natRulesJSON(lbID, []lbdriver.AzureLBNatRule{in[i]})[0], true
		}
	}

	return nil, false
}

func findOutboundRule(lbID string, in []lbdriver.AzureLBOutboundRule, name string) (any, bool) {
	for i := range in {
		if in[i].Name == name {
			return outboundRulesJSON(lbID, []lbdriver.AzureLBOutboundRule{in[i]})[0], true
		}
	}

	return nil, false
}
