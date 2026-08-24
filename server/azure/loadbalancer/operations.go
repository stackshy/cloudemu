package loadbalancer

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// azureLB returns the native Azure load-balancer store if the driver implements
// it (the Azure provider does).
func (h *Handler) azureLB() (lbdriver.AzureLoadBalancers, bool) {
	az, ok := h.lb.(lbdriver.AzureLoadBalancers)

	return az, ok
}

// createOrUpdateLoadBalancer handles PUT .../loadBalancers/{name}. The whole
// nested load balancer arrives in one body and fully REPLACES the stored state,
// so any frontend / pool / rule / probe omitted from the body is removed —
// matching ARM's CreateOrUpdate semantics (no stale-child accumulation).
//
// LoadBalancers.CreateOrUpdate is an LRO in the SDK; returning 200 with the
// fully-provisioned body (ProvisioningState=Succeeded) completes the poller on
// the first response.
func (h *Handler) createOrUpdateLoadBalancer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body loadBalancerJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"load balancers are not supported by this driver")

		return
	}

	stored, err := az.CreateOrUpdateAzureLoadBalancer(r.Context(), rp.ResourceGroup, rp.ResourceName, buildAzureLB(&body))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// Keep a minimal cross-cloud LB record so resource discovery still sees the
	// load balancer; its rich shape lives in the native store.
	h.syncGenericLB(r.Context(), rp.ResourceName, &body)

	azurearm.WriteJSON(w, http.StatusOK, toLBJSON(rp, stored))
}

func (h *Handler) getLoadBalancer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "load balancer not found")
		return
	}

	stored, err := az.GetAzureLoadBalancer(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toLBJSON(rp, stored))
}

// deleteLoadBalancer removes the load balancer from both the native store and
// the cross-cloud record. LoadBalancers.Delete is an LRO; a 200 with empty body
// completes the poller.
func (h *Handler) deleteLoadBalancer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "load balancer not found")
		return
	}

	if derr := az.DeleteAzureLoadBalancer(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	if lb, ferr := h.findGenericLB(r.Context(), rp.ResourceName); ferr == nil {
		_ = h.lb.DeleteLoadBalancer(r.Context(), lb.ARN)
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listLoadBalancers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	az, ok := h.azureLB()
	if !ok {
		azurearm.WriteJSON(w, http.StatusOK, lbListResult{Value: []loadBalancerJSON{}})
		return
	}

	stored, err := az.ListAzureLoadBalancers(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := lbListResult{Value: make([]loadBalancerJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toLBJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// --- request → native model ---

// buildAzureLB maps the ARM request body to the native store model, preserving
// every frontend/pool/rule/probe name and each rule's independent ports and
// child references verbatim.
func buildAzureLB(body *loadBalancerJSON) lbdriver.AzureLoadBalancer {
	lb := lbdriver.AzureLoadBalancer{
		Location: firstNonEmpty(body.Location, defaultLBLocation),
		SKUName:  defaultSKUName,
		SKUTier:  defaultSKUTier,
		Tags:     stripInternalTags(body.Tags),
	}

	if body.SKU != nil {
		lb.SKUName = firstNonEmpty(body.SKU.Name, defaultSKUName)
		lb.SKUTier = firstNonEmpty(body.SKU.Tier, defaultSKUTier)
	}

	if body.Properties == nil {
		return lb
	}

	lb.Frontends = buildFrontends(body.Properties.FrontendIPConfigurations)
	lb.BackendPools = buildPools(body.Properties.BackendAddressPools)
	lb.Probes = buildProbes(body.Properties.Probes)
	lb.Rules = buildRules(body.Properties.LoadBalancingRules)

	return lb
}

func buildFrontends(in []frontendIPJSON) []lbdriver.AzureLBFrontend {
	out := make([]lbdriver.AzureLBFrontend, 0, len(in))

	for i := range in {
		fe := lbdriver.AzureLBFrontend{Name: in[i].Name}

		if p := in[i].Properties; p != nil {
			fe.PrivateIPAddress = p.PrivateIPAddress
			fe.AllocationMethod = p.PrivateIPAllocationMethod

			if p.Subnet != nil {
				fe.SubnetID = p.Subnet.ID
			}

			if p.PublicIPAddress != nil {
				fe.PublicIPID = p.PublicIPAddress.ID
			}
		}

		out = append(out, fe)
	}

	return out
}

func buildPools(in []backendPoolJSON) []string {
	out := make([]string, 0, len(in))

	for i := range in {
		if in[i].Name != "" {
			out = append(out, in[i].Name)
		}
	}

	return out
}

func buildProbes(in []probeJSON) []lbdriver.AzureLBProbe {
	out := make([]lbdriver.AzureLBProbe, 0, len(in))

	for i := range in {
		pr := lbdriver.AzureLBProbe{Name: in[i].Name}

		if p := in[i].Properties; p != nil {
			pr.Protocol = p.Protocol
			pr.Port = int(p.Port)
			pr.RequestPath = p.RequestPath
			pr.IntervalInSeconds = int(p.IntervalInSeconds)
			pr.NumberOfProbes = int(p.NumberOfProbes)
		}

		out = append(out, pr)
	}

	return out
}

func buildRules(in []loadBalancingRuleJSON) []lbdriver.AzureLBRule {
	out := make([]lbdriver.AzureLBRule, 0, len(in))

	for i := range in {
		rule := lbdriver.AzureLBRule{Name: in[i].Name}

		if p := in[i].Properties; p != nil {
			rule.Protocol = firstNonEmpty(p.Protocol, protocolTCP)
			rule.FrontendPort = int(p.FrontendPort)
			rule.BackendPort = int(p.BackendPort)

			if rule.BackendPort == 0 {
				rule.BackendPort = rule.FrontendPort
			}

			rule.FrontendName = lastPathSegment(refID(p.FrontendIPConfiguration))
			rule.BackendPoolName = lastPathSegment(refID(p.BackendAddressPool))
			rule.ProbeName = lastPathSegment(refID(p.Probe))
			rule.IdleTimeoutMin = int(p.IdleTimeoutInMinutes)
			rule.LoadDistribution = p.LoadDistribution

			if p.EnableFloatingIP != nil {
				rule.EnableFloatingIP = *p.EnableFloatingIP
			}
		}

		out = append(out, rule)
	}

	return out
}

// --- native model → response ---

// toLBJSON reconstructs the nested ARM load balancer body from the stored
// native model, stamping ids, etags and terminal provisioning states.
func toLBJSON(rp *azurearm.ResourcePath, lb *lbdriver.AzureLoadBalancer) loadBalancerJSON {
	lbID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeLBs, rp.ResourceName)

	props := &loadBalancerProps{
		ProvisioningState:        provisioningStateSucceeded,
		FrontendIPConfigurations: frontendsJSON(lbID, lb.Frontends),
		BackendAddressPools:      poolsJSON(lbID, lb.BackendPools),
		Probes:                   probesJSON(lbID, lb.Probes),
		LoadBalancingRules:       rulesJSON(lbID, lb.Rules),
	}

	return loadBalancerJSON{
		ID:         lbID,
		Name:       rp.ResourceName,
		Type:       lbResourceType,
		Location:   firstNonEmpty(lb.Location, defaultLBLocation),
		Etag:       weakETag(lbID),
		SKU:        &lbSKU{Name: firstNonEmpty(lb.SKUName, defaultSKUName), Tier: firstNonEmpty(lb.SKUTier, defaultSKUTier)},
		Tags:       lb.Tags,
		Properties: props,
	}
}

func frontendsJSON(lbID string, in []lbdriver.AzureLBFrontend) []frontendIPJSON {
	out := make([]frontendIPJSON, 0, len(in))

	for i := range in {
		fe := in[i]
		id := lbID + "/frontendIPConfigurations/" + fe.Name
		p := &frontendIPProps{
			PrivateIPAddress:          fe.PrivateIPAddress,
			PrivateIPAllocationMethod: fe.AllocationMethod,
			ProvisioningState:         provisioningStateSucceeded,
		}

		if fe.SubnetID != "" {
			p.Subnet = &subResource{ID: fe.SubnetID}
		}

		if fe.PublicIPID != "" {
			p.PublicIPAddress = &subResource{ID: fe.PublicIPID}
		}

		out = append(out, frontendIPJSON{
			ID: id, Name: fe.Name, Type: feResourceType, Etag: weakETag(id), Properties: p,
		})
	}

	return out
}

func poolsJSON(lbID string, names []string) []backendPoolJSON {
	out := make([]backendPoolJSON, 0, len(names))

	for _, name := range names {
		id := lbID + "/backendAddressPools/" + name
		out = append(out, backendPoolJSON{
			ID: id, Name: name, Type: poolResourceType, Etag: weakETag(id),
			Properties: &backendPoolProps{ProvisioningState: provisioningStateSucceeded},
		})
	}

	return out
}

func probesJSON(lbID string, in []lbdriver.AzureLBProbe) []probeJSON {
	out := make([]probeJSON, 0, len(in))

	for i := range in {
		pr := in[i]
		id := lbID + "/probes/" + pr.Name
		out = append(out, probeJSON{
			ID: id, Name: pr.Name, Type: probeResourceType, Etag: weakETag(id),
			Properties: &probeProps{
				Protocol:          pr.Protocol,
				Port:              i32(pr.Port),
				RequestPath:       pr.RequestPath,
				IntervalInSeconds: i32(pr.IntervalInSeconds),
				NumberOfProbes:    i32(pr.NumberOfProbes),
				ProvisioningState: provisioningStateSucceeded,
			},
		})
	}

	return out
}

func rulesJSON(lbID string, in []lbdriver.AzureLBRule) []loadBalancingRuleJSON {
	out := make([]loadBalancingRuleJSON, 0, len(in))

	for i := range in {
		rule := in[i]
		id := lbID + "/loadBalancingRules/" + rule.Name
		p := &loadBalancingRuleProps{
			Protocol:             firstNonEmpty(rule.Protocol, protocolTCP),
			FrontendPort:         i32(rule.FrontendPort),
			BackendPort:          i32(rule.BackendPort),
			IdleTimeoutInMinutes: i32(rule.IdleTimeoutMin),
			LoadDistribution:     rule.LoadDistribution,
			EnableFloatingIP:     &rule.EnableFloatingIP,
			ProvisioningState:    provisioningStateSucceeded,
		}

		if rule.FrontendName != "" {
			p.FrontendIPConfiguration = &subResource{ID: lbID + "/frontendIPConfigurations/" + rule.FrontendName}
		}

		if rule.BackendPoolName != "" {
			p.BackendAddressPool = &subResource{ID: lbID + "/backendAddressPools/" + rule.BackendPoolName}
		}

		if rule.ProbeName != "" {
			p.Probe = &subResource{ID: lbID + "/probes/" + rule.ProbeName}
		}

		out = append(out, loadBalancingRuleJSON{
			ID: id, Name: rule.Name, Type: ruleResourceType, Etag: weakETag(id), Properties: p,
		})
	}

	return out
}

// --- cross-cloud (discovery) sync ---

// syncGenericLB ensures a minimal cross-cloud LB record exists for name so
// resource discovery can still enumerate the load balancer.
func (h *Handler) syncGenericLB(ctx context.Context, name string, body *loadBalancerJSON) {
	if _, err := h.findGenericLB(ctx, name); err == nil {
		return
	}

	_, _ = h.lb.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name:   name,
		Type:   "network",
		Scheme: schemeFromBody(body),
		Tags:   stripInternalTags(body.Tags),
	})
}

// findGenericLB resolves the cross-cloud LB record by its user-assigned name.
func (h *Handler) findGenericLB(ctx context.Context, name string) (*lbdriver.LBInfo, error) {
	lbs, err := h.lb.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return nil, err
	}

	for i := range lbs {
		if strings.EqualFold(lbs[i].Name, name) {
			return &lbs[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "load balancer %q not found", name)
}

// --- helpers ---

// weakETag wraps a stable etag in the weak-validator form (W/"...") ARM uses
// for load-balancer resources.
func weakETag(id string) string {
	return azurearm.WeakETag(id)
}

// schemeFromBody infers the cross-cloud scheme from the request body. An Azure
// load balancer is internal unless a public frontend is present.
func schemeFromBody(body *loadBalancerJSON) string {
	if body.Properties != nil {
		for _, fe := range body.Properties.FrontendIPConfigurations {
			if fe.Properties != nil && fe.Properties.PublicIPAddress != nil {
				return "internet-facing"
			}
		}
	}

	return "internal"
}

// stripInternalTags removes cloudemu-internal bookkeeping tags before echoing
// tags back to the caller.
func stripInternalTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if strings.HasPrefix(k, "cloudemu:") {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// refID returns the id of a sub-resource reference, or "" if nil.
func refID(ref *subResource) string {
	if ref == nil {
		return ""
	}

	return ref.ID
}

// lastPathSegment returns the trailing segment of an ARM resource id.
func lastPathSegment(id string) string {
	id = strings.TrimRight(id, "/")
	if idx := strings.LastIndexByte(id, '/'); idx >= 0 {
		return id[idx+1:]
	}

	return id
}

// firstNonEmpty returns a if non-empty, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// i32 narrows a small, non-negative load-balancer config value (port, interval,
// probe count, timeout) to the int32 the ARM wire types use.
//
//nolint:gosec // G115: these are small, non-negative port/interval/count/timeout inputs
func i32(n int) int32 {
	return int32(n)
}
