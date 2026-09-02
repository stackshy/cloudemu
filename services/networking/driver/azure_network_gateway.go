package driver

import "context"

// Microsoft.Network's site-to-site VPN surface — virtualNetworkGateways,
// localNetworkGateways and connections — has no equivalent in the cross-cloud
// Networking model, so — like AzureApplicationSecurityGroups and
// AzurePublicIPPrefixes — the Azure provider stores it through this OPTIONAL,
// type-asserted capability. AWS and GCP do not implement it. All three resource
// types are addressed by (resourceGroup, name) to match ARM; an empty
// resourceGroup on a List means subscription-wide.

// AzureGatewayBgpSettings is the BGP speaker configuration shared by virtual and
// local network gateways.
type AzureGatewayBgpSettings struct {
	ASN               int64
	BgpPeeringAddress string
	PeerWeight        int32
}

// AzureGatewayIPConfiguration is one ipConfiguration of a virtual network
// gateway, referencing a GatewaySubnet and (usually) a public IP.
type AzureGatewayIPConfiguration struct {
	Name                      string
	SubnetID                  string
	PublicIPAddressID         string
	PrivateIPAllocationMethod string
}

// AzureVirtualNetworkGateway is one Microsoft.Network/virtualNetworkGateways
// resource.
type AzureVirtualNetworkGateway struct {
	Name             string
	ResourceGroup    string
	Location         string
	Tags             map[string]string
	GatewayType      string
	VPNType          string
	VPNGeneration    string
	SKUName          string
	SKUTier          string
	EnableBGP        bool
	ActiveActive     bool
	IPConfigurations []AzureGatewayIPConfiguration
	BgpSettings      *AzureGatewayBgpSettings
}

// AzureLocalNetworkGateway is one Microsoft.Network/localNetworkGateways
// resource — the on-premises end of a site-to-site tunnel.
type AzureLocalNetworkGateway struct {
	Name             string
	ResourceGroup    string
	Location         string
	Tags             map[string]string
	GatewayIPAddress string
	FQDN             string
	AddressPrefixes  []string
	BgpSettings      *AzureGatewayBgpSettings
}

// AzureVirtualNetworkGatewayConnection is one Microsoft.Network/connections
// resource, joining a virtual network gateway to either a local network gateway
// (IPsec) or a second virtual network gateway (Vnet2Vnet).
type AzureVirtualNetworkGatewayConnection struct {
	Name                     string
	ResourceGroup            string
	Location                 string
	Tags                     map[string]string
	ConnectionType           string
	ConnectionProtocol       string
	VirtualNetworkGateway1ID string
	VirtualNetworkGateway2ID string
	LocalNetworkGateway2ID   string
	SharedKey                string
	RoutingWeight            int32
	EnableBGP                bool
}

// AzureNetworkGateways is the Azure-only site-to-site VPN surface. Each resource
// type is keyed by (resourceGroup, name) for idempotent createOrUpdate.
type AzureNetworkGateways interface {
	// PutAzureVirtualNetworkGateway creates or replaces a virtual network gateway
	// in place (a repeat createOrUpdate PUT updates rather than duplicating),
	// returning the stored value.
	PutAzureVirtualNetworkGateway(ctx context.Context, gw AzureVirtualNetworkGateway) AzureVirtualNetworkGateway
	// GetAzureVirtualNetworkGateway returns the gateway identified by (resourceGroup, name).
	GetAzureVirtualNetworkGateway(ctx context.Context, resourceGroup, name string) (AzureVirtualNetworkGateway, bool)
	// DeleteAzureVirtualNetworkGateway removes the gateway, reporting whether it existed.
	DeleteAzureVirtualNetworkGateway(ctx context.Context, resourceGroup, name string) bool
	// ListAzureVirtualNetworkGateways returns the gateways in a resource group, or
	// all when resourceGroup is empty, ordered by key.
	ListAzureVirtualNetworkGateways(ctx context.Context, resourceGroup string) []AzureVirtualNetworkGateway

	// PutAzureLocalNetworkGateway creates or replaces a local network gateway in place.
	PutAzureLocalNetworkGateway(ctx context.Context, gw AzureLocalNetworkGateway) AzureLocalNetworkGateway
	// GetAzureLocalNetworkGateway returns the local gateway identified by (resourceGroup, name).
	GetAzureLocalNetworkGateway(ctx context.Context, resourceGroup, name string) (AzureLocalNetworkGateway, bool)
	// DeleteAzureLocalNetworkGateway removes the local gateway, reporting whether it existed.
	DeleteAzureLocalNetworkGateway(ctx context.Context, resourceGroup, name string) bool
	// ListAzureLocalNetworkGateways returns the local gateways in a resource group,
	// or all when resourceGroup is empty, ordered by key.
	ListAzureLocalNetworkGateways(ctx context.Context, resourceGroup string) []AzureLocalNetworkGateway

	// PutAzureVirtualNetworkGatewayConnection creates or replaces a connection in place.
	PutAzureVirtualNetworkGatewayConnection(
		ctx context.Context, conn AzureVirtualNetworkGatewayConnection,
	) AzureVirtualNetworkGatewayConnection
	// GetAzureVirtualNetworkGatewayConnection returns the connection identified by (resourceGroup, name).
	GetAzureVirtualNetworkGatewayConnection(
		ctx context.Context, resourceGroup, name string,
	) (AzureVirtualNetworkGatewayConnection, bool)
	// DeleteAzureVirtualNetworkGatewayConnection removes the connection, reporting whether it existed.
	DeleteAzureVirtualNetworkGatewayConnection(ctx context.Context, resourceGroup, name string) bool
	// ListAzureVirtualNetworkGatewayConnections returns the connections in a resource
	// group, or all when resourceGroup is empty, ordered by key.
	ListAzureVirtualNetworkGatewayConnections(ctx context.Context, resourceGroup string) []AzureVirtualNetworkGatewayConnection
}
