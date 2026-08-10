package vcn

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// ocidPrefixParts is the number of dot-separated segments before an OCID's
// resource type.
const ocidPrefixParts = 2

// SetTags replaces a resource's freeform tags, whatever kind of resource it
// is. The portable driver only exposes tag mutation for VCNs, subnets and
// NSGs, but OCI's Update calls carry tags on every resource.
func (m *Mock) SetTags(id string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := copyTags(tags)

	if m.setCoreTags(id, next) || m.setGatewayTags(id, next) {
		return nil
	}

	return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
}

// setCoreTags replaces the tags of a VCN-owned resource. One switch per
// resource family; a single switch busts the cyclomatic budget.
func (m *Mock) setCoreTags(id string, tags map[string]string) bool {
	switch ocidType(id) {
	case typeVCN:
		return replaceTags(m.vcns, id, tags, func(v *vcnData, t map[string]string) { v.Tags = t })
	case typeSubnet:
		return replaceTags(m.subnets, id, tags, func(s *subnetData, t map[string]string) { s.Tags = t })
	case typeNSG:
		return replaceTags(m.nsgs, id, tags, func(n *nsgData, t map[string]string) { n.Tags = t })
	case typeSecurityList:
		return replaceTags(m.securityLists, id, tags, func(s *securityListData, t map[string]string) { s.Tags = t })
	case typeRouteTable:
		return replaceTags(m.routeTables, id, tags, func(r *routeTableData, t map[string]string) { r.Tags = t })
	default:
		return false
	}
}

// setGatewayTags replaces the tags of a gateway or address resource.
func (m *Mock) setGatewayTags(id string, tags map[string]string) bool {
	switch ocidType(id) {
	case typeInternetGW:
		return replaceTags(m.igws, id, tags, func(g *igwData, t map[string]string) { g.Tags = t })
	case typeNATGateway:
		return replaceTags(m.natGateways, id, tags, func(g *natGatewayData, t map[string]string) { g.Tags = t })
	case typeServiceGateway:
		return replaceTags(m.serviceGWs, id, tags, func(g *serviceGatewayData, t map[string]string) { g.Tags = t })
	case typePublicIP:
		return replaceTags(m.publicIPs, id, tags, func(p *publicIPData, t map[string]string) { p.Tags = t })
	case typeVNIC:
		return replaceTags(m.vnics, id, tags, func(v *vnicData, t map[string]string) { v.Tags = t })
	case typeLocalPeering:
		return replaceTags(m.lpgs, id, tags, func(g *lpgData, t map[string]string) { g.Tags = t })
	default:
		return false
	}
}

// replaceTags swaps a stored resource's tag map under the store's lock.
func replaceTags[T any](
	store *memstore.Store[T], id string, tags map[string]string, assign func(T, map[string]string),
) bool {
	return store.Update(id, func(v T) T {
		assign(v, tags)

		return v
	})
}

// ocidType returns the resource type segment of an OCID.
func ocidType(id string) string {
	parts := strings.SplitN(id, ".", ocidPrefixParts+1)
	if len(parts) <= ocidPrefixParts {
		return ""
	}

	return parts[1]
}
