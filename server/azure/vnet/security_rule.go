package vnet

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Azure port/prefix wildcard and the service tags the default rules reference.
const (
	anyPrefix         = "*"
	anyPortRange      = "*"
	protoAny          = "*"
	accessAllow       = "Allow"
	accessDeny        = "Deny"
	directionInbound  = "Inbound"
	directionOutbound = "Outbound"
	tagVirtualNetwork = "VirtualNetwork"
	tagAzureLB        = "AzureLoadBalancer"
	tagInternet       = "Internet"

	provisioningSucceeded = "Succeeded"
)

// defaultRuleSpec is the static shape of one built-in default security rule.
type defaultRuleSpec struct {
	name      string
	priority  int
	direction string
	access    string
	src       string
	dst       string
}

// azureDefaultRules are the six rules Azure stamps on every network security
// group. They cannot be created or removed by callers; a Get always echoes
// them under defaultSecurityRules. Priorities and service tags match the
// Microsoft.Network defaults exactly.
//
//nolint:gochecknoglobals // fixed platform constant, read-only lookup table
var azureDefaultRules = [...]defaultRuleSpec{
	{"AllowVnetInBound", 65000, directionInbound, accessAllow, tagVirtualNetwork, tagVirtualNetwork},
	{"AllowAzureLoadBalancerInBound", 65001, directionInbound, accessAllow, tagAzureLB, anyPrefix},
	{"DenyAllInBound", 65500, directionInbound, accessDeny, anyPrefix, anyPrefix},
	{"AllowVnetOutBound", 65000, directionOutbound, accessAllow, tagVirtualNetwork, tagVirtualNetwork},
	{"AllowInternetOutBound", 65001, directionOutbound, accessAllow, anyPrefix, tagInternet},
	{"DenyAllOutBound", 65500, directionOutbound, accessDeny, anyPrefix, anyPrefix},
}

// defaultSecurityRules builds the six built-in default rules for the NSG whose
// ARM id is nsgID.
func defaultSecurityRules(nsgID string) []securityRule {
	out := make([]securityRule, 0, len(azureDefaultRules))

	for i := range azureDefaultRules {
		spec := azureDefaultRules[i]
		out = append(out, securityRule{
			Name: spec.name,
			ID:   nsgID + "/defaultSecurityRules/" + spec.name,
			Properties: securityRuleProps{
				Description:              "",
				Protocol:                 protoAny,
				SourceAddressPrefix:      spec.src,
				DestinationAddressPrefix: spec.dst,
				SourcePortRange:          anyPortRange,
				DestinationPortRange:     anyPortRange,
				Access:                   spec.access,
				Priority:                 spec.priority,
				Direction:                spec.direction,
				ProvisioningState:        provisioningSucceeded,
			},
		})
	}

	return out
}

// toAzureNSGRules maps submitted wire security rules to the driver's stored
// representation. The rule name is preserved; empty ports/prefixes are kept
// verbatim so a Get round-trips exactly what the caller sent.
func toAzureNSGRules(in []securityRule) []netdriver.AzureNSGRule {
	out := make([]netdriver.AzureNSGRule, 0, len(in))

	for i := range in {
		p := in[i].Properties
		out = append(out, netdriver.AzureNSGRule{
			Name:                     in[i].Name,
			Description:              p.Description,
			Protocol:                 p.Protocol,
			SourceAddressPrefix:      p.SourceAddressPrefix,
			DestinationAddressPrefix: p.DestinationAddressPrefix,
			SourcePortRange:          p.SourcePortRange,
			DestinationPortRange:     p.DestinationPortRange,
			Access:                   p.Access,
			Direction:                p.Direction,
			Priority:                 p.Priority,
		})
	}

	return out
}

// fromAzureNSGRules maps stored driver rules back to their wire shape, stamping
// each rule's ARM id and a terminal provisioning state.
func fromAzureNSGRules(nsgID string, in []netdriver.AzureNSGRule) []securityRule {
	out := make([]securityRule, 0, len(in))

	for i := range in {
		r := in[i]
		out = append(out, securityRule{
			Name: r.Name,
			ID:   nsgID + "/securityRules/" + r.Name,
			Properties: securityRuleProps{
				Description:              r.Description,
				Protocol:                 r.Protocol,
				SourceAddressPrefix:      r.SourceAddressPrefix,
				DestinationAddressPrefix: r.DestinationAddressPrefix,
				SourcePortRange:          r.SourcePortRange,
				DestinationPortRange:     r.DestinationPortRange,
				Access:                   r.Access,
				Priority:                 r.Priority,
				Direction:                r.Direction,
				ProvisioningState:        provisioningSucceeded,
			},
		})
	}

	return out
}

// azureMeta returns the Azure metadata surface if the networking driver
// implements it (the Azure provider does; AWS/GCP do not).
func (h *Handler) azureMeta() (netdriver.AzureNetworkMetadata, bool) {
	m, ok := h.net.(netdriver.AzureNetworkMetadata)

	return m, ok
}

// etagOf returns a stable etag for an ARM resource id.
func etagOf(id string) string {
	return azurearm.ETag(id)
}
