package loadbalancer

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Internal tags scope a driver target group to its Azure parent load balancer
// and preserve the SDK-facing backend-pool name. They are stripped from the
// tags echoed back to callers.
const (
	tagLBName   = "cloudemu:azureLBName"
	tagPoolName = "cloudemu:azurePoolName"
)

// createOrUpdateLoadBalancer handles PUT .../loadBalancers/{name}. The whole
// nested load balancer arrives in one body; we reconcile the driver's LB,
// backend pools (target groups) and load-balancing rules (listeners) to match.
//
// LoadBalancers.CreateOrUpdate is an LRO in the SDK; returning 200 with the
// fully-provisioned body (ProvisioningState=Succeeded) completes the poller on
// the first response.
func (h *Handler) createOrUpdateLoadBalancer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body loadBalancerJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	lb, err := h.upsertLB(r.Context(), rp.ResourceName, &body)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	pools, err := h.reconcilePools(r.Context(), lb, &body)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.reconcileRules(r.Context(), lb, pools, &body); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out, err := h.buildLBJSON(r.Context(), rp, lb)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// upsertLB creates the load balancer if absent, otherwise returns the existing
// one (CreateOrUpdate is idempotent on the top-level resource).
func (h *Handler) upsertLB(ctx context.Context, name string, body *loadBalancerJSON) (*lbdriver.LBInfo, error) {
	if lb, err := h.findLBByName(ctx, name); err == nil {
		return lb, nil
	}

	return h.lb.CreateLoadBalancer(ctx, lbdriver.LBConfig{
		Name:   name,
		Type:   "network",
		Scheme: schemeFromBody(body),
		Tags:   stripInternalTags(body.Tags),
	})
}

// reconcilePools ensures every backend address pool in the body exists as a
// driver target group tagged to this load balancer, and returns them keyed by
// Azure pool name.
func (h *Handler) reconcilePools(ctx context.Context, lb *lbdriver.LBInfo,
	body *loadBalancerJSON,
) (map[string]*lbdriver.TargetGroupInfo, error) {
	out := make(map[string]*lbdriver.TargetGroupInfo)

	if body.Properties == nil {
		return out, nil
	}

	existing, err := h.poolsForLB(ctx, lb.Name)
	if err != nil {
		return nil, err
	}

	for i := range body.Properties.BackendAddressPools {
		pool := body.Properties.BackendAddressPools[i]
		if pool.Name == "" {
			continue
		}

		if tg, ok := existing[pool.Name]; ok {
			out[pool.Name] = tg
			continue
		}

		tg, cErr := h.lb.CreateTargetGroup(ctx, lbdriver.TargetGroupConfig{
			Name: lb.Name + "-" + pool.Name,
			Tags: map[string]string{tagLBName: lb.Name, tagPoolName: pool.Name},
		})
		if cErr != nil {
			return nil, cErr
		}

		out[pool.Name] = tg
	}

	return out, nil
}

// reconcileRules ensures every load-balancing rule in the body exists as a
// driver listener on this load balancer, resolving the rule's backend pool
// reference to the matching target group ARN.
func (h *Handler) reconcileRules(ctx context.Context, lb *lbdriver.LBInfo,
	pools map[string]*lbdriver.TargetGroupInfo, body *loadBalancerJSON,
) error {
	if body.Properties == nil {
		return nil
	}

	existing, err := h.lb.DescribeListeners(ctx, lb.ARN)
	if err != nil {
		return err
	}

	haveByPort := make(map[int]struct{}, len(existing))
	for i := range existing {
		haveByPort[existing[i].Port] = struct{}{}
	}

	for i := range body.Properties.LoadBalancingRules {
		rule := body.Properties.LoadBalancingRules[i]
		if rule.Properties == nil {
			continue
		}

		port := int(rule.Properties.FrontendPort)
		if _, ok := haveByPort[port]; ok {
			continue
		}

		var tgARN string
		if rule.Properties.BackendAddressPool != nil {
			if tg, ok := pools[poolNameFromID(rule.Properties.BackendAddressPool.ID)]; ok {
				tgARN = tg.ARN
			}
		}

		if _, err := h.lb.CreateListener(ctx, lbdriver.ListenerConfig{
			LBARN:          lb.ARN,
			Protocol:       protocolOrDefault(rule.Properties.Protocol),
			Port:           port,
			TargetGroupARN: tgARN,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (h *Handler) getLoadBalancer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	lb, err := h.findLBByName(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out, err := h.buildLBJSON(r.Context(), rp, lb)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// deleteLoadBalancer removes the load balancer and its backend pools. The
// driver drops the load balancer's listeners as part of DeleteLoadBalancer.
// LoadBalancers.Delete is an LRO; a 200 with empty body completes the poller.
func (h *Handler) deleteLoadBalancer(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	lb, err := h.findLBByName(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	pools, err := h.poolsForLB(r.Context(), lb.Name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	for _, tg := range pools {
		if derr := h.lb.DeleteTargetGroup(r.Context(), tg.ARN); derr != nil && !cerrors.IsNotFound(derr) {
			azurearm.WriteCErr(w, derr)
			return
		}
	}

	if derr := h.lb.DeleteLoadBalancer(r.Context(), lb.ARN); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listLoadBalancers(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	lbs, err := h.lb.DescribeLoadBalancers(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := lbListResult{Value: make([]loadBalancerJSON, 0, len(lbs))}

	for i := range lbs {
		scope := *rp
		scope.ResourceName = lbs[i].Name

		item, berr := h.buildLBJSON(r.Context(), &scope, &lbs[i])
		if berr != nil {
			azurearm.WriteCErr(w, berr)
			return
		}

		out.Value = append(out.Value, item)
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// --- helpers ---

// findLBByName resolves the SDK-facing load balancer name to its driver record.
func (h *Handler) findLBByName(ctx context.Context, name string) (*lbdriver.LBInfo, error) {
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

// poolsForLB returns the target groups tagged to load balancer lbName, keyed by
// their Azure pool name.
func (h *Handler) poolsForLB(ctx context.Context, lbName string) (map[string]*lbdriver.TargetGroupInfo, error) {
	tgs, err := h.lb.DescribeTargetGroups(ctx, nil)
	if err != nil {
		return nil, err
	}

	out := make(map[string]*lbdriver.TargetGroupInfo)

	for i := range tgs {
		if tgs[i].Tags[tagLBName] != lbName {
			continue
		}

		out[tgs[i].Tags[tagPoolName]] = &tgs[i]
	}

	return out, nil
}

// buildLBJSON reconstructs the nested ARM load balancer body from driver state.
func (h *Handler) buildLBJSON(ctx context.Context, rp *azurearm.ResourcePath,
	lb *lbdriver.LBInfo,
) (loadBalancerJSON, error) {
	pools, err := h.poolsForLB(ctx, lb.Name)
	if err != nil {
		return loadBalancerJSON{}, err
	}

	listeners, err := h.lb.DescribeListeners(ctx, lb.ARN)
	if err != nil {
		return loadBalancerJSON{}, err
	}

	lbID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeLBs, lb.Name)

	props := &loadBalancerProps{
		ProvisioningState: provisioningStateSucceeded,
		FrontendIPConfigurations: []frontendIPJSON{{
			ID:   lbID + "/frontendIPConfigurations/default",
			Name: "default",
			Type: feResourceType,
			Properties: &frontendIPProps{
				ProvisioningState: provisioningStateSucceeded,
			},
		}},
	}

	// Backend pools, keyed by ARN so rules can reference them.
	poolIDByARN := make(map[string]string, len(pools))

	for name, tg := range pools {
		poolID := lbID + "/backendAddressPools/" + name
		poolIDByARN[tg.ARN] = poolID
		props.BackendAddressPools = append(props.BackendAddressPools, backendPoolJSON{
			ID:         poolID,
			Name:       name,
			Type:       poolResourceType,
			Properties: &backendPoolProps{ProvisioningState: provisioningStateSucceeded},
		})
	}

	for i := range listeners {
		li := listeners[i]
		ruleName := "rule-" + portString(li.Port)
		ruleProps := &loadBalancingRuleProps{
			Protocol:          li.Protocol,
			FrontendPort:      int32(li.Port),
			BackendPort:       int32(li.Port),
			ProvisioningState: provisioningStateSucceeded,
			FrontendIPConfiguration: &subResource{
				ID: lbID + "/frontendIPConfigurations/default",
			},
		}

		if poolID, ok := poolIDByARN[li.TargetGroupARN]; ok {
			ruleProps.BackendAddressPool = &subResource{ID: poolID}
		}

		props.LoadBalancingRules = append(props.LoadBalancingRules, loadBalancingRuleJSON{
			ID:         lbID + "/loadBalancingRules/" + ruleName,
			Name:       ruleName,
			Type:       ruleResourceType,
			Properties: ruleProps,
		})
	}

	return loadBalancerJSON{
		ID:         lbID,
		Name:       lb.Name,
		Type:       lbResourceType,
		Location:   defaultLBLocation,
		SKU:        &lbSKU{Name: "Standard", Tier: "Regional"},
		Tags:       stripInternalTags(lb.Tags),
		Properties: props,
	}, nil
}

// schemeFromBody infers the driver scheme from the request body. Azure load
// balancers are internal unless a public frontend is present; we default to
// internal, which is the common case for the Standard SKU.
func schemeFromBody(body *loadBalancerJSON) string {
	if body.Properties != nil {
		for _, fe := range body.Properties.FrontendIPConfigurations {
			if fe.Properties != nil && fe.Properties.PrivateIPAddress == "" {
				return "internet-facing"
			}
		}
	}

	return "internal"
}

// protocolOrDefault normalizes the ARM transport protocol to the driver's
// stored value, defaulting to TCP.
func protocolOrDefault(p string) string {
	if p == "" {
		return "Tcp"
	}

	return p
}

// poolNameFromID extracts the trailing backend-pool name from an ARM
// backendAddressPools sub-resource ID.
func poolNameFromID(id string) string {
	const marker = "/backendAddressPools/"

	idx := strings.LastIndex(id, marker)
	if idx < 0 {
		return id
	}

	return id[idx+len(marker):]
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

func portString(p int) string {
	return strconv.Itoa(p)
}
