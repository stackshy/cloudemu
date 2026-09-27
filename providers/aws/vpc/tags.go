package vpc

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// UpdateResourceTags merges tags onto a VPC-family resource that has no
// dedicated Update*Tags method: route tables, internet gateways, NAT
// gateways, network ACLs, DHCP option sets, peering connections, managed
// prefix lists, egress-only internet gateways, security-group rules, Elastic IP
// allocations, VPC endpoints and VPC endpoint services. An unknown or missing id
// is NotFound; the wire layer maps it to the resource-specific code real EC2
// defines for it (InvalidAllocationID.NotFound for eipalloc-,
// InvalidVpcEndpointId.NotFound for vpce-, InvalidVpcEndpointServiceId.NotFound
// for vpce-svc-, InvalidSecurityGroupRuleId.NotFound for sgr-), and to the
// generic InvalidID.NotFound for the rest.
func (m *Mock) UpdateResourceTags(_ context.Context, id string, tags map[string]string) error {
	if !m.mutateResourceTags(id, func(existing map[string]string) map[string]string {
		return mergeTagMap(existing, tags)
	}) {
		return errors.Newf(errors.NotFound, "resource %q not found", id)
	}

	return nil
}

// reservedTagPrefix is the namespace of AWS-generated tags, which EC2 DeleteTags
// never removes.
const reservedTagPrefix = "aws:"

// RemoveResourceTags drops the given tag keys from a VPC-family resource, the
// DeleteTags counterpart to UpdateResourceTags. An empty key list deletes every
// user-defined tag and keeps the AWS-generated "aws:" ones, which is what EC2
// DeleteTags does when the Tag parameter is omitted.
func (m *Mock) RemoveResourceTags(_ context.Context, id string, keys []string) error {
	if !m.mutateResourceTags(id, func(existing map[string]string) map[string]string {
		if len(keys) == 0 {
			return keepReservedTags(existing)
		}

		return removeTagMapKeys(existing, keys)
	}) {
		return errors.Newf(errors.NotFound, "resource %q not found", id)
	}

	return nil
}

// ResourceTags returns a copy of the current tags on any resource the EC2 tag
// API addresses through this provider: VPCs, subnets and security groups plus
// every id UpdateResourceTags accepts. It is NotFound for an unknown id, so the
// EC2 CreateTags/DeleteTags handler can check every resource in a batch
// (existence, the per-resource tag limit, DeleteTags value matching) before it
// writes to any of them. The read goes through the same store/m.mu path as the
// writers, so it never races a concurrent tag write.
func (m *Mock) ResourceTags(_ context.Context, id string) (map[string]string, error) {
	var out map[string]string

	snapshot := func(existing map[string]string) map[string]string {
		out = copyTags(existing)
		return existing
	}

	var found bool

	switch {
	case strings.HasPrefix(id, "vpc-"):
		found = m.vpcs.Update(id, func(v *vpcData) *vpcData { v.Tags = snapshot(v.Tags); return v })
	case strings.HasPrefix(id, "subnet-"):
		found = m.subnets.Update(id, func(s *subnetData) *subnetData { s.Tags = snapshot(s.Tags); return s })
	case strings.HasPrefix(id, "sg-"):
		found = m.securityGroups.Update(id, func(sg *sgData) *sgData { sg.Tags = snapshot(sg.Tags); return sg })
	default:
		found = m.mutateResourceTags(id, snapshot)
	}

	if !found {
		return nil, errors.Newf(errors.NotFound, "resource %q not found", id)
	}

	return out, nil
}

// keepReservedTags returns a fresh map holding only the "aws:" tags of existing.
func keepReservedTags(existing map[string]string) map[string]string {
	out := make(map[string]string)

	for k, v := range existing {
		if strings.HasPrefix(k, reservedTagPrefix) {
			out[k] = v
		}
	}

	return out
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
		return m.mutateAddressingTags(id, transform)
	}
}

// mutateAddressingTags is the mutateResourceTags continuation for Elastic IP
// allocations (eipalloc-), VPC endpoint services (vpce-svc-) and VPC endpoints
// (vpce-). These are the same records AllocateAddress / CreateVpcEndpoint
// TagSpecifications write, so tags added after creation show up on the
// Describe calls. vpce-svc- is checked before vpce- because it shares the prefix.
// m.mu is held because the Describe readers for these records read their fields
// under m.mu.RLock (see the Mock.mu comment).
func (m *Mock) mutateAddressingTags(id string, transform func(map[string]string) map[string]string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case strings.HasPrefix(id, "eipalloc-"):
		return m.eips.Update(id, func(v *eipData) *eipData { v.Tags = transform(v.Tags); return v })
	case strings.HasPrefix(id, "vpce-svc-"):
		return m.endpointServices.Update(id, func(v *driver.EndpointService) *driver.EndpointService {
			v.Tags = transform(v.Tags)
			return v
		})
	case strings.HasPrefix(id, "vpce-"):
		return m.endpoints.Update(id, func(v *driver.VPCEndpoint) *driver.VPCEndpoint { v.Tags = transform(v.Tags); return v })
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
