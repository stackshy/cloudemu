package vnet

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// This file implements driver.AzureNetworkTagReplacer: the wholesale tag
// replacement the ARM resource-level UpdateTags PATCH needs (it SETS tags, it
// does not merge). Each method swaps in a fresh copy of the supplied map under
// the store's lock so concurrent readers iterating the old map are unaffected,
// and returns NotFound when the resource is absent. The wire handler folds any
// wire-internal cloudemu: anchor tags into the map it passes, so those survive.

// ReplaceVPCTags sets the virtual network's tags wholesale.
func (m *Mock) ReplaceVPCTags(_ context.Context, id string, tags map[string]string) error {
	if !m.vpcs.Update(id, func(v *vpcData) *vpcData {
		v.Tags = copyTags(tags)
		return v
	}) {
		return cerrors.Newf(cerrors.NotFound, "virtual network %q not found", id)
	}

	return nil
}

// ReplaceSecurityGroupTags sets the network security group's tags wholesale.
func (m *Mock) ReplaceSecurityGroupTags(_ context.Context, id string, tags map[string]string) error {
	if !m.securityGroups.Update(id, func(sg *sgData) *sgData {
		sg.Tags = copyTags(tags)
		return sg
	}) {
		return cerrors.Newf(cerrors.NotFound, "network security group %q not found", id)
	}

	return nil
}

// ReplaceNATGatewayTags sets the NAT gateway's tags wholesale.
func (m *Mock) ReplaceNATGatewayTags(_ context.Context, id string, tags map[string]string) error {
	if !m.natGateways.Update(id, func(n *natGatewayData) *natGatewayData {
		n.Tags = copyTags(tags)
		return n
	}) {
		return cerrors.Newf(cerrors.NotFound, "NAT gateway %q not found", id)
	}

	return nil
}

// ReplaceAddressTags sets the public IP address's tags wholesale, keyed by its
// Elastic-IP allocation id.
func (m *Mock) ReplaceAddressTags(_ context.Context, allocationID string, tags map[string]string) error {
	if !m.eips.Update(allocationID, func(e *eipData) *eipData {
		e.Tags = copyTags(tags)
		return e
	}) {
		return cerrors.Newf(cerrors.NotFound, "public IP %q not found", allocationID)
	}

	return nil
}

// ReplaceNetworkInterfaceTags sets the network interface's tags wholesale, keyed
// by the (resourceGroup, name) ARM addressing pair. It takes nicMu (the same
// lock CreateOrUpdateNetworkInterface holds) so a concurrent NIC write cannot
// interleave with the tag replacement.
func (m *Mock) ReplaceNetworkInterfaceTags(_ context.Context, resourceGroup, name string, tags map[string]string) error {
	m.nicMu.Lock()
	defer m.nicMu.Unlock()

	if !m.nics.Update(nicKey(resourceGroup, name), func(n *nicData) *nicData {
		n.Tags = copyTags(tags)
		return n
	}) {
		return cerrors.Newf(cerrors.NotFound, "network interface %q not found", name)
	}

	return nil
}
