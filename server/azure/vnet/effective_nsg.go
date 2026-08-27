package vnet

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// listEffectiveNSGs serves InterfacesClient.BeginListEffectiveNetworkSecurityGroups:
// POST .../networkInterfaces/{nic}/effectiveNetworkSecurityGroups. It returns
// one EffectiveNetworkSecurityGroup entry per NSG that actually applies to the
// NIC — its own networkSecurityGroup association and its (primary
// ipConfiguration's) subnet's networkSecurityGroup association — each carrying
// that NSG's custom rules plus the six built-in defaults, matching real ARM's
// merged view of "what traffic this NIC actually sees".
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listEffectiveNSGs(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureNetworkInterfaces,
) {
	nic, err := svc.GetNetworkInterface(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	nicID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNIC, rp.ResourceName)

	out := effectiveNSGListResponse{}

	if nic.NetworkSecurityGroupID != "" {
		if entry, ok := h.effectiveNSGEntry(r.Context(), nic.NetworkSecurityGroupID,
			effectiveNSGAssociation{NetworkInterface: &armIDRef{ID: nicID}}); ok {
			out.Value = append(out.Value, entry)
		}
	}

	if subnetID, nsgID := h.nicSubnetNSG(r.Context(), nic); nsgID != "" {
		if entry, ok := h.effectiveNSGEntry(r.Context(), nsgID,
			effectiveNSGAssociation{Subnet: &armIDRef{ID: subnetID}}); ok {
			out.Value = append(out.Value, entry)
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// nicSubnetNSG resolves the subnet referenced by the NIC's primary (or first)
// ipConfiguration and returns its ARM id together with the NSG ARM id tagged
// on it (armSubnetNSGTag), or ("", "") when the NIC has no subnet reference or
// that subnet has no associated NSG.
func (h *Handler) nicSubnetNSG(ctx context.Context, nic *netdriver.AzureNIC) (subnetID, nsgID string) {
	candidate := primaryIPConfigSubnetID(nic)
	if candidate == "" {
		return "", ""
	}

	sp, ok := azurearm.ParsePath(candidate)
	if !ok || sp.ResourceType != typeVNet || sp.SubResource != subResSubnets {
		return "", ""
	}

	vnet, err := findVNetByName(ctx, h.net, sp.ResourceName)
	if err != nil {
		return "", ""
	}

	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return "", ""
	}

	for i := range subs {
		if subs[i].VPCID == vnet.ID && tagOr(subs[i].Tags, armSubnetTag, "") == sp.SubResourceName {
			return candidate, tagOr(subs[i].Tags, armSubnetNSGTag, "")
		}
	}

	return "", ""
}

// primaryIPConfigSubnetID returns the ARM subnet id of nic's primary
// ipConfiguration, falling back to the first ipConfiguration that carries a
// subnet reference when none is marked primary.
func primaryIPConfigSubnetID(nic *netdriver.AzureNIC) string {
	var candidate string

	for i := range nic.IPConfigs {
		if nic.IPConfigs[i].SubnetID == "" {
			continue
		}

		if nic.IPConfigs[i].Primary || candidate == "" {
			candidate = nic.IPConfigs[i].SubnetID
		}
	}

	return candidate
}

// effectiveNSGEntry builds one EffectiveNetworkSecurityGroup entry for the NSG
// identified by its ARM resource id (nsgARMID), combining its custom rules
// with the six built-in defaults. ok is false when the NSG id doesn't resolve
// (e.g. a stale reference).
func (h *Handler) effectiveNSGEntry(ctx context.Context, nsgARMID string, assoc effectiveNSGAssociation) (effectiveNSG, bool) {
	nsgRP, ok := azurearm.ParsePath(nsgARMID)
	if !ok || nsgRP.ResourceType != typeNSG {
		return effectiveNSG{}, false
	}

	info, err := findNSGByName(ctx, h.net, nsgRP.ResourceName)
	if err != nil {
		return effectiveNSG{}, false
	}

	meta, hasMeta := h.azureMeta()

	var rules []netdriver.AzureNSGRule

	if hasMeta {
		if md, found := meta.GetAzureNSGMetadata(ctx, info.ID); found {
			rules = md.SecurityRules
		}
	}

	return effectiveNSG{
		Association:            assoc,
		EffectiveSecurityRules: toEffectiveRules(nsgARMID, rules),
		NetworkSecurityGroup:   armIDRef{ID: nsgARMID},
	}, true
}

// toEffectiveRules flattens an NSG's custom rules and its six built-in
// defaults into the effectiveSecurityRule shape, name-prefixed the way real
// ARM does ("securityRules/{name}" / "defaultSecurityRules/{name}").
func toEffectiveRules(nsgARMID string, custom []netdriver.AzureNSGRule) []effectiveSecurityRule {
	out := make([]effectiveSecurityRule, 0, len(custom)+len(azureDefaultRules))

	for i := range custom {
		r := custom[i]
		out = append(out, effectiveSecurityRule{
			Name:                     "securityRules/" + r.Name,
			Protocol:                 effectiveProtocol(r.Protocol),
			SourcePortRange:          r.SourcePortRange,
			DestinationPortRange:     r.DestinationPortRange,
			SourceAddressPrefix:      r.SourceAddressPrefix,
			DestinationAddressPrefix: r.DestinationAddressPrefix,
			Access:                   r.Access,
			Priority:                 r.Priority,
			Direction:                r.Direction,
		})
	}

	defaults := defaultSecurityRules(nsgARMID)

	for i := range defaults {
		spec := &defaults[i]
		out = append(out, effectiveSecurityRule{
			Name:                     "defaultSecurityRules/" + spec.Name,
			Protocol:                 effectiveProtocol(spec.Properties.Protocol),
			SourcePortRange:          spec.Properties.SourcePortRange,
			DestinationPortRange:     spec.Properties.DestinationPortRange,
			SourceAddressPrefix:      spec.Properties.SourceAddressPrefix,
			DestinationAddressPrefix: spec.Properties.DestinationAddressPrefix,
			Access:                   spec.Properties.Access,
			Priority:                 spec.Properties.Priority,
			Direction:                spec.Properties.Direction,
		})
	}

	return out
}

// effectiveProtocol maps the wire security-rule protocol ("*", "Tcp", ...) to
// the distinct EffectiveSecurityRuleProtocol vocabulary real ARM uses for this
// endpoint, which spells the wildcard "All" rather than "*".
func effectiveProtocol(p string) string {
	if p == protoAny {
		return "All"
	}

	return p
}
