package vpc

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// ModifyVPCEndpointSets applies the Add*/Remove* part of ModifyVpcEndpoint to
// the endpoint's current route tables, subnets and security groups. The read
// and the write happen under one lock hold, so parallel modifies of the same
// endpoint (Terraform creates route table associations concurrently) all land.
func (m *Mock) ModifyVPCEndpointSets(
	_ context.Context, id string, change *driver.VPCEndpointSetChange,
) (*driver.VPCEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ep, ok := m.endpoints.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "vpc endpoint %q not found", id)
	}

	rts := applyIDDelta(ep.RouteTableIDs, change.AddRouteTableIDs, change.RemoveRouteTableIDs)
	if err := m.setEndpointRouteTables(ep, rts); err != nil {
		return nil, err
	}

	m.syncEndpointRoutes(ep)

	subnets := applyIDDelta(ep.SubnetIDs, change.AddSubnetIDs, change.RemoveSubnetIDs)
	if ep.EndpointType == vpcEndpointTypeInterface {
		m.syncEndpointENIs(ep, subnets)
	}

	ep.SubnetIDs = subnets
	ep.SecurityGroupIDs = applyIDDelta(ep.SecurityGroupIDs, change.AddSecurityGroupIDs, change.RemoveSecurityGroupIDs)

	return copyEndpoint(ep), nil
}

// applyIDDelta returns cur without the removed ids and with the added ones
// appended, deduplicated. An id named in both lists is dropped.
func applyIDDelta(cur, add, remove []string) []string {
	drop := make(map[string]bool, len(remove))
	for _, id := range remove {
		drop[id] = true
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(cur)+len(add))

	for _, list := range [][]string{cur, add} {
		for _, id := range list {
			if drop[id] || seen[id] {
				continue
			}

			seen[id] = true

			out = append(out, id)
		}
	}

	return out
}

// setEndpointRouteTables sets ep's route tables to ids (deduplicated). A
// Gateway endpoint cannot take a table that already routes the same service
// through another endpoint: EC2 allows one endpoint route per service per
// route table and answers RouteAlreadyExists. The caller holds m.mu and syncs
// the routes afterwards.
func (m *Mock) setEndpointRouteTables(ep *driver.VPCEndpoint, ids []string) error {
	ids = applyIDDelta(nil, ids, nil)

	if plID := endpointPrefixList(ep); plID != "" {
		for _, rtID := range ids {
			if m.routeTableHasOtherEndpointRoute(rtID, plID, ep.ID) {
				return errors.Newf(errors.AlreadyExists,
					"route table %s already has a route with destination-prefix-list-id %s", rtID, plID)
			}
		}
	}

	ep.RouteTableIDs = ids

	return nil
}

// routeTableHasOtherEndpointRoute reports whether rtID routes plID to an
// endpoint other than endpointID. The caller holds m.mu.
func (m *Mock) routeTableHasOtherEndpointRoute(rtID, plID, endpointID string) bool {
	rt, ok := m.routeTables.Get(rtID)
	if !ok {
		return false
	}

	for _, r := range rt.Routes {
		if r.DestinationPrefixListID == plID && r.TargetID != endpointID {
			return true
		}
	}

	return false
}

// syncEndpointENIs gives an Interface endpoint exactly one ENI in each of
// subnets, releasing the ENIs of subnets it left. The caller holds m.mu.
func (m *Mock) syncEndpointENIs(ep *driver.VPCEndpoint, subnets []string) {
	want := make(map[string]bool, len(subnets))
	for _, s := range subnets {
		want[s] = true
	}

	desc := endpointENIDescription(ep.ID)
	have := map[string]bool{}

	var ids []string

	for id, eni := range m.enis.All() {
		if eni.Description != desc {
			continue
		}

		if !want[eni.SubnetID] || have[eni.SubnetID] {
			m.enis.Delete(id)
			continue
		}

		have[eni.SubnetID] = true

		ids = append(ids, id)
	}

	for _, s := range subnets {
		if !have[s] {
			ids = append(ids, m.attachManagedENI(ep.VPCID, s, desc).ID)
		}
	}

	sort.Strings(ids)
	ep.NetworkInterfaceIDs = ids
}

// dropRouteTableFromEndpoints removes a deleted route table from every
// endpoint that listed it. The caller holds m.mu.
func (m *Mock) dropRouteTableFromEndpoints(rtID string) {
	for _, ep := range m.endpoints.All() {
		ep.RouteTableIDs = applyIDDelta(ep.RouteTableIDs, nil, []string{rtID})
	}
}
