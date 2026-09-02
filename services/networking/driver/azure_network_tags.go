package driver

import "context"

// AzureNetworkTagReplacer replaces the entire stored tag collection on the core
// Azure VNet-family resources, backing the ARM resource-level UpdateTags PATCH
// (Microsoft.Network's *Client.UpdateTags), which SETS the tag set wholesale
// rather than merging. It is an OPTIONAL, type-asserted capability that only the
// Azure provider implements (the same pattern as AzureNetworkMetadata /
// AzureNetworkInterfaces); AWS and GCP do not.
//
// Each method overwrites the resource's tags with exactly the supplied map (a
// nil or empty map clears them) in a single atomic store update, and returns
// NotFound when the resource is absent. The VNet-family resources anchored in
// the cross-cloud store carry wire-internal cloudemu: tags the (resourceGroup,
// name) lookup depends on; the wire handler folds those anchors into the map it
// passes here, so the replace never drops them.
type AzureNetworkTagReplacer interface {
	// ReplaceVPCTags sets the virtual network's tags, keyed by its driver id.
	ReplaceVPCTags(ctx context.Context, id string, tags map[string]string) error
	// ReplaceSecurityGroupTags sets the network security group's tags, keyed by
	// its driver id.
	ReplaceSecurityGroupTags(ctx context.Context, id string, tags map[string]string) error
	// ReplaceNATGatewayTags sets the NAT gateway's tags, keyed by its driver id.
	ReplaceNATGatewayTags(ctx context.Context, id string, tags map[string]string) error
	// ReplaceAddressTags sets the public IP address's tags, keyed by its
	// Elastic-IP allocation id.
	ReplaceAddressTags(ctx context.Context, allocationID string, tags map[string]string) error
	// ReplaceNetworkInterfaceTags sets the network interface's tags, keyed by the
	// (resourceGroup, name) ARM addressing pair the NIC store uses.
	ReplaceNetworkInterfaceTags(ctx context.Context, resourceGroup, name string, tags map[string]string) error
}
