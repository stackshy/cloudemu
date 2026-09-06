package firewall

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	fwdriver "github.com/stackshy/cloudemu/v2/services/azurefirewall/driver"
)

// createOrUpdateFirewall handles PUT .../azureFirewalls/{name}. The whole
// firewall arrives in one body and fully REPLACES the stored state, matching
// ARM's CreateOrUpdate semantics. AzureFirewalls.CreateOrUpdate is an LRO;
// returning the fully-provisioned body (provisioningState=Succeeded) completes
// the poller on the first response — 201 on create, 200 on update.
func (h *Handler) createOrUpdateFirewall(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body firewallJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	fw := buildFirewall(&body)

	stored, created, err := h.fw.CreateOrUpdateAzureFirewall(r.Context(), rp.ResourceGroup, rp.ResourceName, fw)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toFirewallJSON(rp, stored))
}

func (h *Handler) getFirewall(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fw.GetAzureFirewall(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toFirewallJSON(rp, stored))
}

// updateFirewallTags handles PATCH .../azureFirewalls/{name} —
// AzureFirewalls.UpdateTags. Real armnetwork UpdateTags REPLACES the tag
// collection wholesale (an omitted existing key is dropped); every other property
// is left untouched. A request with tags entirely omitted (nil map) is a no-op.
func (h *Handler) updateFirewallTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body firewallJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	stored, err := h.fw.GetAzureFirewall(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	replaced := *stored
	if body.Tags != nil {
		replaced.Tags = body.Tags
	}

	updated, _, err := h.fw.CreateOrUpdateAzureFirewall(r.Context(), rp.ResourceGroup, rp.ResourceName, replaced)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toFirewallJSON(rp, updated))
}

// deleteFirewall removes the firewall. AzureFirewalls.Delete is an LRO; a 200
// with empty body completes the poller.
func (h *Handler) deleteFirewall(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.fw.DeleteAzureFirewall(r.Context(), rp.ResourceGroup, rp.ResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // firewall and policy list are parallel over distinct resource types and driver methods.
func (h *Handler) listFirewalls(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.fw.ListAzureFirewalls(r.Context(), rp.ResourceGroup)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := firewallListResult{Value: make([]firewallJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.ResourceGroup = stored[i].ResourceGroup
		scope.ResourceName = stored[i].Name
		out.Value = append(out.Value, toFirewallJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// --- request → native model ---

// buildFirewall maps the ARM request body to the native store model. Zones and
// tags are top-level; sku, threatIntelMode, ipConfigurations and firewallPolicy
// are extracted from the properties object; every other property is preserved
// verbatim in OtherProps.
func buildFirewall(body *firewallJSON) fwdriver.AzureFirewall {
	fw := fwdriver.AzureFirewall{
		Location:         firstNonEmpty(body.Location, defaultLocation),
		Zones:            body.Zones,
		Tags:             body.Tags,
		ThreatIntelMode:  resolveThreatIntelMode(body.Properties),
		FirewallPolicyID: subResourceID(body.Properties, firewallPolicyKey),
		IPConfigurations: buildIPConfigs(body.Properties),
	}

	fw.SKUName, fw.SKUTier = skuFromProps(body.Properties)
	fw.OtherProps = firewallOtherProps(body.Properties)

	return fw
}

// firewallModeledKeys are the properties keys handled explicitly and therefore
// stripped from OtherProps.
//
//nolint:gochecknoglobals // fixed, read-only registry of modeled property keys.
var firewallModeledKeys = []string{skuKey, threatIntelModeKey, ipConfigsKey, firewallPolicyKey, provisioningStateKey}

// firewallOtherProps returns the deferred/echo-through properties (everything but
// the modeled keys).
func firewallOtherProps(props map[string]any) map[string]any {
	return stripKeys(props, firewallModeledKeys)
}

// buildIPConfigs reads the ipConfigurations array into modeled items and computes
// a deterministic privateIPAddress for each item that carries a subnet.
func buildIPConfigs(props map[string]any) []fwdriver.AzureFirewallIPConfig {
	arr, ok := props[ipConfigsKey].([]any)
	if !ok {
		return nil
	}

	out := make([]fwdriver.AzureFirewallIPConfig, 0, len(arr))
	subnetSeen := 0

	for _, el := range arr {
		item, ok := el.(map[string]any)
		if !ok {
			continue
		}

		cfg := fwdriver.AzureFirewallIPConfig{Name: stringField(item, "name")}
		p, _ := item["properties"].(map[string]any)
		cfg.SubnetID = nestedID(p, "subnet")
		cfg.PublicIPAddressID = nestedID(p, "publicIPAddress")

		if cfg.SubnetID != "" {
			cfg.PrivateIPAddress = privateIPPrefix + strconv.Itoa(privateIPFirst+subnetSeen)
			subnetSeen++
		}

		out = append(out, cfg)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// --- native model → response ---

// toFirewallJSON reconstructs the nested ARM firewall body from the stored model,
// stamping the firewall id/etag and the modeled properties. rp is authoritative
// for the id (taken from the URL).
func toFirewallJSON(rp *azurearm.ResourcePath, fw *fwdriver.AzureFirewall) firewallJSON {
	fwID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeAzureFirewalls, rp.ResourceName)

	return firewallJSON{
		ID:         fwID,
		Name:       rp.ResourceName,
		Type:       firewallResourceType,
		Location:   firstNonEmpty(fw.Location, defaultLocation),
		Etag:       azurearm.WeakETag(fwID),
		Zones:      fw.Zones,
		Tags:       fw.Tags,
		Properties: assembleFirewallProps(fwID, fw),
	}
}

// assembleFirewallProps builds the response properties object: every deferred
// property verbatim, then the modeled sku, threatIntelMode, firewallPolicy,
// ipConfigurations and the terminal provisioningState.
func assembleFirewallProps(fwID string, fw *fwdriver.AzureFirewall) map[string]any {
	const injected = 5

	props := make(map[string]any, len(fw.OtherProps)+injected)
	for k, v := range fw.OtherProps {
		props[k] = v
	}

	if sku := firewallSKUJSON(fw); sku != nil {
		props[skuKey] = sku
	}

	props[threatIntelModeKey] = firstNonEmpty(fw.ThreatIntelMode, threatIntelModeDefault)

	if fw.FirewallPolicyID != "" {
		props[firewallPolicyKey] = subResource{ID: fw.FirewallPolicyID}
	}

	if cfgs := ipConfigsJSON(fwID, fw.IPConfigurations); cfgs != nil {
		props[ipConfigsKey] = cfgs
	}

	props[provisioningStateKey] = provisioningStateSucceeded

	return props
}

// firewallSKUJSON projects the modeled sku, or nil when unset.
func firewallSKUJSON(fw *fwdriver.AzureFirewall) *skuJSON {
	if fw.SKUName == "" && fw.SKUTier == "" {
		return nil
	}

	return &skuJSON{Name: fw.SKUName, Tier: fw.SKUTier}
}

// ipConfigsJSON stamps each ipConfiguration's ARM id/etag and injects the
// computed privateIPAddress and terminal provisioningState.
func ipConfigsJSON(fwID string, cfgs []fwdriver.AzureFirewallIPConfig) []ipConfigJSON {
	if len(cfgs) == 0 {
		return nil
	}

	out := make([]ipConfigJSON, 0, len(cfgs))

	for i := range cfgs {
		id := fwID + "/" + ipConfigsKey + "/" + cfgs[i].Name
		out = append(out, ipConfigJSON{
			ID:   id,
			Name: cfgs[i].Name,
			Etag: azurearm.WeakETag(id),
			Properties: &ipConfigPropsJSON{
				Subnet:            optionalSubResource(cfgs[i].SubnetID),
				PublicIPAddress:   optionalSubResource(cfgs[i].PublicIPAddressID),
				PrivateIPAddress:  cfgs[i].PrivateIPAddress,
				ProvisioningState: provisioningStateSucceeded,
			},
		})
	}

	return out
}
