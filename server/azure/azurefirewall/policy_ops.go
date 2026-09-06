package azurefirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fwdriver "github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

// createOrUpdatePolicy handles PUT .../firewallPolicies/{name}. The whole policy
// arrives in one body and fully REPLACES the stored state (ARM CreateOrUpdate).
// FirewallPolicies.CreateOrUpdate is an LRO; returning the fully-provisioned body
// completes the poller on the first response — 201 on create, 200 on update.
func (h *Handler) createOrUpdatePolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body policyJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	pol := buildPolicy(&body)

	stored, created, err := h.fw.CreateOrUpdateFirewallPolicy(r.Context(), rp.ResourceGroup, rp.ResourceName, pol)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toPolicyJSON(rp, stored))
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fw.GetFirewallPolicy(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPolicyJSON(rp, stored))
}

// updatePolicyTags handles PATCH .../firewallPolicies/{name} —
// FirewallPolicies.UpdateTags. UpdateTags REPLACES the tag collection wholesale;
// every other property is left untouched. A request with tags omitted is a no-op.
func (h *Handler) updatePolicyTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body policyJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fw.GetFirewallPolicy(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := *stored
	if body.Tags != nil {
		replaced.Tags = body.Tags
	}

	updated, _, err := h.fw.CreateOrUpdateFirewallPolicy(r.Context(), rp.ResourceGroup, rp.ResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toPolicyJSON(rp, updated))
}

// deletePolicy removes the policy. FirewallPolicies.Delete is an LRO; a 200 with
// empty body completes the poller.
func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.fw.DeleteFirewallPolicy(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // firewall and policy list are parallel over distinct resource types and driver methods.
func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fw.ListFirewallPolicies(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := policyListResult{Value: make([]policyJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toPolicyJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// --- request → native model ---

// policyModeledKeys are the properties keys handled explicitly and stripped from
// OtherProps.
//
//nolint:gochecknoglobals // fixed, read-only registry of modeled property keys.
var policyModeledKeys = []string{skuKey, threatIntelModeKey, provisioningStateKey}

// buildPolicy maps the ARM request body to the native store model. sku.tier and
// threatIntelMode are extracted from properties; every other property (including
// the deferred dnsSettings, threatIntelWhitelist, intrusionDetection) is
// preserved verbatim in OtherProps.
func buildPolicy(body *policyJSON) fwdriver.FirewallPolicy {
	_, tier := skuFromProps(body.Properties)

	return fwdriver.FirewallPolicy{
		Location:        firstNonEmpty(body.Location, defaultLocation),
		Tags:            body.Tags,
		SKUTier:         tier,
		ThreatIntelMode: resolveThreatIntelMode(body.Properties),
		OtherProps:      stripKeys(body.Properties, policyModeledKeys),
	}
}

// --- native model → response ---

// toPolicyJSON reconstructs the ARM policy body from the stored model, stamping
// the policy id/etag and modeled properties. rp is authoritative for the id.
func toPolicyJSON(rp *azurearm.ResourcePath, pol *fwdriver.FirewallPolicy) policyJSON {
	polID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeFirewallPolicies, rp.ResourceName)

	return policyJSON{
		ID:         polID,
		Name:       rp.ResourceName,
		Type:       policyResourceType,
		Location:   firstNonEmpty(pol.Location, defaultLocation),
		Etag:       azurearm.WeakETag(polID),
		Tags:       pol.Tags,
		Properties: assemblePolicyProps(pol),
	}
}

// assemblePolicyProps builds the response properties: every deferred property
// verbatim, then the modeled sku, threatIntelMode and terminal provisioningState.
func assemblePolicyProps(pol *fwdriver.FirewallPolicy) map[string]any {
	const injected = 3

	props := make(map[string]any, len(pol.OtherProps)+injected)
	for k, v := range pol.OtherProps {
		props[k] = v
	}

	if pol.SKUTier != "" {
		props[skuKey] = &skuJSON{Tier: pol.SKUTier}
	}

	props[threatIntelModeKey] = firstNonEmpty(pol.ThreatIntelMode, threatIntelModeDefault)
	props[provisioningStateKey] = provisioningStateSucceeded

	return props
}
