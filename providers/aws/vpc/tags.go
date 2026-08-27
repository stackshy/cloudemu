package vpc

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// UpdateResourceTags merges tags onto a VPC-family resource that has no
// dedicated Update*Tags method — route tables, internet gateways, NAT
// gateways, network ACLs, DHCP option sets, peering connections, managed
// prefix lists, and egress-only internet gateways. An unknown or missing id
// is NotFound, so the wire layer can map it to the InvalidID.NotFound code
// real EC2 returns for CreateTags on a non-existent resource.
func (m *Mock) UpdateResourceTags(_ context.Context, id string, tags map[string]string) error {
	if !m.mutateResourceTags(id, func(existing map[string]string) map[string]string {
		return mergeTagMap(existing, tags)
	}) {
		return errors.Newf(errors.NotFound, "resource %q not found", id)
	}

	return nil
}

// RemoveResourceTags drops the given tag keys from a VPC-family resource, the
// DeleteTags counterpart to UpdateResourceTags.
func (m *Mock) RemoveResourceTags(_ context.Context, id string, keys []string) error {
	if !m.mutateResourceTags(id, func(existing map[string]string) map[string]string {
		return removeTagMapKeys(existing, keys)
	}) {
		return errors.Newf(errors.NotFound, "resource %q not found", id)
	}

	return nil
}

// mutateResourceTags routes an id to the store that owns it and applies
// transform to that record's tag map inside memstore.Update, so the store lock
// covers the read-modify-write. It reports whether the id matched a known
// prefix and an existing record.
func (m *Mock) mutateResourceTags(id string, transform func(map[string]string) map[string]string) bool {
	switch {
	case strings.HasPrefix(id, "rtb-"):
		return m.routeTables.Update(id, func(v *routeTableData) *routeTableData { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "igw-"):
		return m.igws.Update(id, func(v *igwData) *igwData { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "nat-"):
		return m.natGateways.Update(id, func(v *natGatewayData) *natGatewayData { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "acl-"):
		return m.networkACLs.Update(id, func(v *networkACLData) *networkACLData { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "dopt-"):
		return m.dhcpOptions.Update(id, func(v *driver.DHCPOptions) *driver.DHCPOptions { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "pcx-"):
		return m.peerings.Update(id, func(v *peeringData) *peeringData { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "pl-"):
		return m.prefixLists.Update(id, func(v *driver.PrefixList) *driver.PrefixList { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "eigw-"):
		return m.egressOnlyIGWs.Update(id, func(v *driver.EgressOnlyInternetGateway) *driver.EgressOnlyInternetGateway {
			v.Tags = transform(v.Tags)
			return v
		})
	case strings.HasPrefix(id, "sgr-"):
		return m.mutateRuleTags(id, transform)
	default:
		return false
	}
}

// mutateRuleTags applies transform to the tag map of the ingress/egress rule
// whose RuleID equals id, mutating it by slice index under the owning group's
// store lock (rules live by value inside sgData). It reports whether a rule
// with that id was found.
func (m *Mock) mutateRuleTags(id string, transform func(map[string]string) map[string]string) bool {
	found := false

	for _, groupID := range m.securityGroups.Keys() {
		if m.securityGroups.Update(groupID, func(sg *sgData) *sgData {
			for i := range sg.IngressRules {
				if sg.IngressRules[i].RuleID == id {
					sg.IngressRules[i].Tags = transform(sg.IngressRules[i].Tags)
					found = true
				}
			}

			for i := range sg.EgressRules {
				if sg.EgressRules[i].RuleID == id {
					sg.EgressRules[i].Tags = transform(sg.EgressRules[i].Tags)
					found = true
				}
			}

			return sg
		}) && found {
			return true
		}
	}

	return false
}
