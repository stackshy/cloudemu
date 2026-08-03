package driver

import "context"

// This file defines AWS-specific networking capabilities as OPTIONAL interfaces
// discovered by type assertion, following the VPCAttributes / NetworkInterfaces
// precedent. These resources (Transit Gateways, VPN, DHCP option sets, managed
// prefix lists, egress-only internet gateways, endpoint services, Client VPN)
// don't map cleanly across clouds, so they stay out of the portable Networking
// interface — only the AWS mock implements them, and the EC2 handler serves
// them when the driver satisfies the capability.

// ---- Transit Gateway ----

// TransitGateway is a regional hub that interconnects VPCs and on-prem networks.
type TransitGateway struct {
	ID          string
	State       string
	ASN         int64
	Description string
	OwnerID     string
	Tags        map[string]string
}

// TransitGatewayConfig is the input to CreateTransitGateway.
type TransitGatewayConfig struct {
	ASN         int64
	Description string
	Tags        map[string]string
}

// TransitGatewayVPCAttachment attaches a VPC (via subnets) to a transit gateway.
type TransitGatewayVPCAttachment struct {
	ID               string
	TransitGatewayID string
	VPCID            string
	SubnetIDs        []string
	State            string
	Tags             map[string]string
}

// TransitGatewayVPCAttachmentConfig is the input to CreateTransitGatewayVPCAttachment.
type TransitGatewayVPCAttachmentConfig struct {
	TransitGatewayID string
	VPCID            string
	SubnetIDs        []string
	Tags             map[string]string
}

// TransitGatewayRouteTable is a route table owned by a transit gateway.
type TransitGatewayRouteTable struct {
	ID               string
	TransitGatewayID string
	State            string
	Tags             map[string]string
}

// TransitGatewayRoute is a route within a transit gateway route table.
type TransitGatewayRoute struct {
	DestinationCIDR string
	AttachmentID    string
	Type            string // static | propagated
	State           string
}

// TransitGatewayRouteTableAssociation links an attachment to a TGW route table.
type TransitGatewayRouteTableAssociation struct {
	RouteTableID string
	AttachmentID string
	ResourceID   string
	ResourceType string
	State        string
}

// TransitGateways is an OPTIONAL AWS capability (type-asserted).
type TransitGateways interface {
	CreateTransitGateway(ctx context.Context, cfg TransitGatewayConfig) (*TransitGateway, error)
	DeleteTransitGateway(ctx context.Context, id string) (*TransitGateway, error)
	DescribeTransitGateways(ctx context.Context, ids []string) ([]TransitGateway, error)

	CreateTransitGatewayVPCAttachment(ctx context.Context, cfg TransitGatewayVPCAttachmentConfig) (*TransitGatewayVPCAttachment, error)
	DeleteTransitGatewayVPCAttachment(ctx context.Context, id string) (*TransitGatewayVPCAttachment, error)
	DescribeTransitGatewayVPCAttachments(ctx context.Context, ids []string) ([]TransitGatewayVPCAttachment, error)

	CreateTransitGatewayRouteTable(ctx context.Context, transitGatewayID string, tags map[string]string) (*TransitGatewayRouteTable, error)
	DeleteTransitGatewayRouteTable(ctx context.Context, id string) (*TransitGatewayRouteTable, error)
	DescribeTransitGatewayRouteTables(ctx context.Context, ids []string) ([]TransitGatewayRouteTable, error)

	CreateTransitGatewayRoute(ctx context.Context, routeTableID, destinationCIDR, attachmentID string) (*TransitGatewayRoute, error)
	DeleteTransitGatewayRoute(ctx context.Context, routeTableID, destinationCIDR string) (*TransitGatewayRoute, error)
	SearchTransitGatewayRoutes(ctx context.Context, routeTableID string) ([]TransitGatewayRoute, error)
	AssociateTransitGatewayRouteTable(ctx context.Context, routeTableID, attachmentID string) (*TransitGatewayRouteTableAssociation, error)
	EnableTransitGatewayRouteTablePropagation(ctx context.Context, routeTableID, attachmentID string) error
	DisableTransitGatewayRouteTablePropagation(ctx context.Context, routeTableID, attachmentID string) error
}

// ---- VPN (Customer Gateway / VPN Gateway / VPN Connection) ----

// CustomerGateway is the on-prem side of a site-to-site VPN.
type CustomerGateway struct {
	ID        string
	IPAddress string
	BGPASN    int64
	Type      string
	State     string
	Tags      map[string]string
}

// CustomerGatewayConfig is the input to CreateCustomerGateway.
type CustomerGatewayConfig struct {
	IPAddress string
	BGPASN    int64
	Type      string
	Tags      map[string]string
}

// VPNGateway is the AWS side of a site-to-site VPN (virtual private gateway).
type VPNGateway struct {
	ID              string
	Type            string
	State           string
	AmazonSideASN   int64
	AttachedVPCID   string
	AttachmentState string
	Tags            map[string]string
}

// VPNGatewayConfig is the input to CreateVPNGateway.
type VPNGatewayConfig struct {
	Type          string
	AmazonSideASN int64
	Tags          map[string]string
}

// VPNConnection is a site-to-site VPN between a VPN gateway and a customer gateway.
type VPNConnection struct {
	ID                string
	CustomerGatewayID string
	VPNGatewayID      string
	TransitGatewayID  string
	Type              string
	State             string
	StaticRoutesOnly  bool
	Tags              map[string]string
}

// VPNConnectionConfig is the input to CreateVPNConnection.
type VPNConnectionConfig struct {
	CustomerGatewayID string
	VPNGatewayID      string
	TransitGatewayID  string
	Type              string
	StaticRoutesOnly  bool
	Tags              map[string]string
}

// VPNConnections is an OPTIONAL AWS capability (type-asserted).
type VPNConnections interface {
	CreateCustomerGateway(ctx context.Context, cfg CustomerGatewayConfig) (*CustomerGateway, error)
	DeleteCustomerGateway(ctx context.Context, id string) error
	DescribeCustomerGateways(ctx context.Context, ids []string) ([]CustomerGateway, error)

	CreateVPNGateway(ctx context.Context, cfg VPNGatewayConfig) (*VPNGateway, error)
	DeleteVPNGateway(ctx context.Context, id string) error
	DescribeVPNGateways(ctx context.Context, ids []string) ([]VPNGateway, error)
	AttachVPNGateway(ctx context.Context, vpnGatewayID, vpcID string) (*VPNGateway, error)
	DetachVPNGateway(ctx context.Context, vpnGatewayID, vpcID string) error

	CreateVPNConnection(ctx context.Context, cfg VPNConnectionConfig) (*VPNConnection, error)
	DeleteVPNConnection(ctx context.Context, id string) error
	DescribeVPNConnections(ctx context.Context, ids []string) ([]VPNConnection, error)
}

// ---- DHCP Option Sets ----

// DHCPOptions is a set of DHCP options associable with a VPC.
type DHCPOptions struct {
	ID            string
	Configuration map[string][]string // key → values (e.g. "domain-name-servers")
	Tags          map[string]string
}

// DHCPOptionsConfig is the input to CreateDHCPOptions.
type DHCPOptionsConfig struct {
	Configuration map[string][]string
	Tags          map[string]string
}

// DHCPOptionSets is an OPTIONAL AWS capability (type-asserted).
type DHCPOptionSets interface {
	CreateDHCPOptions(ctx context.Context, cfg DHCPOptionsConfig) (*DHCPOptions, error)
	DeleteDHCPOptions(ctx context.Context, id string) error
	DescribeDHCPOptions(ctx context.Context, ids []string) ([]DHCPOptions, error)
	AssociateDHCPOptions(ctx context.Context, dhcpOptionsID, vpcID string) error
}

// ---- Managed Prefix Lists ----

// PrefixListEntry is one CIDR entry in a managed prefix list.
type PrefixListEntry struct {
	CIDR        string
	Description string
}

// PrefixList is a customer-managed collection of CIDR blocks.
type PrefixList struct {
	ID            string
	Name          string
	AddressFamily string
	MaxEntries    int
	State         string
	Version       int
	Entries       []PrefixListEntry
	Tags          map[string]string
}

// PrefixListConfig is the input to CreateManagedPrefixList.
type PrefixListConfig struct {
	Name          string
	AddressFamily string
	MaxEntries    int
	Entries       []PrefixListEntry
	Tags          map[string]string
}

// PrefixLists is an OPTIONAL AWS capability (type-asserted).
type PrefixLists interface {
	CreateManagedPrefixList(ctx context.Context, cfg PrefixListConfig) (*PrefixList, error)
	DeleteManagedPrefixList(ctx context.Context, id string) (*PrefixList, error)
	DescribeManagedPrefixLists(ctx context.Context, ids []string) ([]PrefixList, error)
	GetManagedPrefixListEntries(ctx context.Context, id string) ([]PrefixListEntry, error)
}

// ---- Egress-only Internet Gateway (IPv6) ----

// EgressOnlyInternetGateway provides outbound-only IPv6 for private subnets.
type EgressOnlyInternetGateway struct {
	ID            string
	AttachedVPCID string
	State         string
	Tags          map[string]string
}

// EgressOnlyInternetGateways is an OPTIONAL AWS capability (type-asserted).
type EgressOnlyInternetGateways interface {
	CreateEgressOnlyInternetGateway(ctx context.Context, vpcID string, tags map[string]string) (*EgressOnlyInternetGateway, error)
	DeleteEgressOnlyInternetGateway(ctx context.Context, id string) error
	DescribeEgressOnlyInternetGateways(ctx context.Context, ids []string) ([]EgressOnlyInternetGateway, error)
}

// ---- VPC Endpoint Services (PrivateLink provider side) ----

// EndpointService is a PrivateLink service configuration a provider publishes.
type EndpointService struct {
	ID                      string
	ServiceName             string
	State                   string
	NetworkLoadBalancerARNs []string
	AcceptanceRequired      bool
	AvailabilityZones       []string
	Tags                    map[string]string
}

// EndpointServiceConfig is the input to CreateVPCEndpointServiceConfiguration.
type EndpointServiceConfig struct {
	NetworkLoadBalancerARNs []string
	AcceptanceRequired      bool
	Tags                    map[string]string
}

// VPCEndpointServices is an OPTIONAL AWS capability (type-asserted).
type VPCEndpointServices interface {
	CreateVPCEndpointServiceConfiguration(ctx context.Context, cfg EndpointServiceConfig) (*EndpointService, error)
	DeleteVPCEndpointServiceConfiguration(ctx context.Context, id string) error
	DescribeVPCEndpointServiceConfigurations(ctx context.Context, ids []string) ([]EndpointService, error)
}

// ---- Client VPN ----

// ClientVPNEndpoint is a managed endpoint remote clients connect to.
type ClientVPNEndpoint struct {
	ID                   string
	Description          string
	ClientCIDRBlock      string
	ServerCertificateARN string
	State                string
	SplitTunnel          bool
	VPCID                string
	Tags                 map[string]string
}

// ClientVPNEndpointConfig is the input to CreateClientVPNEndpoint.
type ClientVPNEndpointConfig struct {
	Description          string
	ClientCIDRBlock      string
	ServerCertificateARN string
	SplitTunnel          bool
	Tags                 map[string]string
}

// ClientVPNTargetNetwork associates a subnet with a Client VPN endpoint.
type ClientVPNTargetNetwork struct {
	AssociationID string
	EndpointID    string
	SubnetID      string
	VPCID         string
	State         string
}

// ClientVPN is an OPTIONAL AWS capability (type-asserted).
type ClientVPN interface {
	CreateClientVPNEndpoint(ctx context.Context, cfg ClientVPNEndpointConfig) (*ClientVPNEndpoint, error)
	DeleteClientVPNEndpoint(ctx context.Context, id string) error
	DescribeClientVPNEndpoints(ctx context.Context, ids []string) ([]ClientVPNEndpoint, error)
	AssociateClientVPNTargetNetwork(ctx context.Context, endpointID, subnetID string) (*ClientVPNTargetNetwork, error)
	DisassociateClientVPNTargetNetwork(ctx context.Context, endpointID, associationID string) error
}
