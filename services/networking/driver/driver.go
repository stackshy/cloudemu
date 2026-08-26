// Package driver defines the interface for networking service implementations.
package driver

import "context"

// VPCConfig describes a VPC to create.
type VPCConfig struct {
	CIDRBlock string
	Tags      map[string]string
	// InstanceTenancy is the default tenancy of instances launched into the VPC
	// ("default" or "dedicated"). An AWS concept; empty means "default". Azure and
	// GCP do not model per-network tenancy and leave it unset.
	InstanceTenancy string
}

// VPCInfo describes a VPC.
type VPCInfo struct {
	ID        string
	CIDRBlock string
	State     string
	Tags      map[string]string
	// DNS attributes, settable after creation via ModifyVPCAttribute. Real
	// EC2 defaults DNS support on and DNS hostnames off for a new VPC.
	EnableDNSSupport   bool
	EnableDNSHostnames bool
	// DhcpOptionsID is the DHCP option set associated with the VPC. Empty means
	// the Amazon-provided default set; AssociateDhcpOptions changes it.
	DhcpOptionsID string
	// InstanceTenancy is the default tenancy of instances launched into the VPC
	// ("default" or "dedicated"). An AWS concept; Azure and GCP leave it empty.
	InstanceTenancy string
}

// SubnetConfig describes a subnet to create.
type SubnetConfig struct {
	VPCID            string
	CIDRBlock        string
	AvailabilityZone string
	Tags             map[string]string
}

// SubnetInfo describes a subnet.
type SubnetInfo struct {
	ID               string
	VPCID            string
	CIDRBlock        string
	AvailabilityZone string
	State            string
	Tags             map[string]string
	// MapPublicIPOnLaunch reports whether instances launched into the subnet
	// receive a public IPv4 address by default. Real EC2 defaults it off for a
	// non-default subnet and lets ModifySubnetAttribute flip it — the only way
	// to turn a subnet public.
	MapPublicIPOnLaunch bool
}

// SecurityGroupConfig describes a security group to create.
type SecurityGroupConfig struct {
	Name        string
	Description string
	VPCID       string
	Tags        map[string]string
}

// SecurityGroupInfo describes a security group.
type SecurityGroupInfo struct {
	ID           string
	Name         string
	Description  string
	VPCID        string
	IngressRules []SecurityRule
	EgressRules  []SecurityRule
	Tags         map[string]string
}

// SecurityRule describes a security group rule.
type SecurityRule struct {
	Protocol string // "tcp", "udp", "icmp", "-1" (all)
	FromPort int
	ToPort   int
	CIDR     string
	// IPv6CIDR is an IPv6 source/destination range (Ipv6Ranges.CidrIpv6),
	// PrefixListID references a managed prefix list (PrefixListIds.PrefixListId),
	// and ReferencedGroupID / ReferencedGroupOwnerID capture a source-group
	// reference (UserIdGroupPairs). Exactly one of CIDR, IPv6CIDR, PrefixListID
	// or ReferencedGroupID identifies a single rule's target on the AWS wire.
	IPv6CIDR               string
	PrefixListID           string
	ReferencedGroupID      string
	ReferencedGroupOwnerID string
	// Description is the optional free-text note attached to a rule. It is not
	// part of a rule's identity — AWS ignores it when revoking or deduplicating.
	Description string
	// RuleID is the service-assigned "sgr-" identifier for the rule. It is empty
	// for rules created outside the AWS wire layer (Azure/GCP, portable API).
	RuleID string
	// Tags are the service-assigned tags applied to the rule. They are populated
	// only via the AWS wire layer (AuthorizeSecurityGroup* TagSpecifications /
	// CreateTags on the sgr- id) and are nil for Azure/GCP/OCI and the portable
	// API. Tags are not part of a rule's identity — Matches() ignores them, the
	// same as RuleID and Description.
	Tags map[string]string
}

// Matches reports whether two rules describe the same permission, ignoring the
// service-assigned RuleID and the free-text Description — the fields AWS does
// not treat as part of a rule's identity when revoking or deduplicating.
func (r *SecurityRule) Matches(o *SecurityRule) bool {
	return r.Protocol == o.Protocol &&
		r.FromPort == o.FromPort &&
		r.ToPort == o.ToPort &&
		r.CIDR == o.CIDR &&
		r.IPv6CIDR == o.IPv6CIDR &&
		r.PrefixListID == o.PrefixListID &&
		r.ReferencedGroupID == o.ReferencedGroupID
}

// Equal reports whether two rules are identical in every field except the
// service-assigned Tags map. Tags is a map (not comparable with ==) and is not
// part of a rule's identity, so it is excluded. Equal preserves the exact
// full-struct equality that Azure/GCP/OCI relied on before Tags was added,
// which — unlike Matches — also distinguishes RuleID, Description and the
// referenced-group owner.
func (r *SecurityRule) Equal(o *SecurityRule) bool {
	return r.Matches(o) &&
		r.ReferencedGroupOwnerID == o.ReferencedGroupOwnerID &&
		r.Description == o.Description &&
		r.RuleID == o.RuleID
}

// PeeringConnection represents a VPC peering connection.
type PeeringConnection struct {
	ID           string
	RequesterVPC string
	AccepterVPC  string
	Status       string // "pending-acceptance", "active", "rejected", "deleted"
	CreatedAt    string
	Tags         map[string]string
}

// PeeringConfig configures a peering connection.
type PeeringConfig struct {
	RequesterVPC string
	AccepterVPC  string
	Tags         map[string]string
}

// NATGateway represents a NAT gateway.
type NATGateway struct {
	ID        string
	SubnetID  string
	VPCID     string
	PublicIP  string
	State     string // "pending", "available", "deleting", "deleted", "failed"
	CreatedAt string
	Tags      map[string]string
	// AllocationID is the Elastic IP allocation bound to a public NAT gateway;
	// PrivateIP and NetworkInterfaceID describe the ENI it occupies. These
	// populate the NatGatewayAddress set AWS returns. ConnectivityType is
	// "public" or "private".
	AllocationID       string
	PrivateIP          string
	NetworkInterfaceID string
	ConnectivityType   string
}

// NATGatewayConfig configures a NAT gateway.
type NATGatewayConfig struct {
	SubnetID string
	Tags     map[string]string
	// AllocationID echoes the Elastic IP the caller bound to a public NAT
	// gateway; ConnectivityType selects "public" (default) or "private".
	AllocationID     string
	ConnectivityType string
}

// FlowLog represents a VPC flow log configuration.
type FlowLog struct {
	ID           string
	ResourceID   string
	ResourceType string // "VPC", "Subnet", "NetworkInterface"
	TrafficType  string // "ACCEPT", "REJECT", "ALL"
	Status       string // "ACTIVE", "INACTIVE"
	CreatedAt    string
	Tags         map[string]string
}

// FlowLogConfig configures a flow log.
type FlowLogConfig struct {
	ResourceID   string
	ResourceType string
	TrafficType  string
	Tags         map[string]string
}

// FlowLogRecord represents a single flow log entry.
type FlowLogRecord struct {
	Timestamp  string
	SourceIP   string
	DestIP     string
	SourcePort int
	DestPort   int
	Protocol   string
	Packets    int
	Bytes      int
	Action     string // "ACCEPT" or "REJECT"
	FlowLogID  string
}

// RouteTable represents a route table.
type RouteTable struct {
	ID     string
	VPCID  string
	Routes []Route
	Tags   map[string]string
	// Associations lists the subnet associations pointing at this route
	// table. Describe is the only way a caller can discover association IDs,
	// and it needs them to disassociate before deleting the table.
	Associations []RouteTableAssociation
}

// Route represents a route in a route table.
type Route struct {
	DestinationCIDR string
	TargetID        string // gateway ID, NAT gateway ID, peering connection ID, etc.
	TargetType      string // "gateway", "nat-gateway", "peering", "local"
	State           string // "active", "blackhole"
}

// RouteTableConfig configures a route table.
type RouteTableConfig struct {
	VPCID string
	Tags  map[string]string
}

// NetworkACL represents a network ACL.
type NetworkACL struct {
	ID    string
	VPCID string
	Rules []NetworkACLRule
	Tags  map[string]string
	// Associations lists the subnets this ACL is associated with. It is an AWS
	// concept the AWS backend populates; other clouds leave it empty.
	Associations []NetworkACLAssociation
	IsDefault    bool
}

// NetworkACLAssociation binds a subnet to a network ACL. Replacing the ACL for a
// subnet yields a new association ID. This is an AWS concept (see
// NetworkACLAssociator); other clouds do not populate it.
type NetworkACLAssociation struct {
	ID           string
	NetworkACLID string
	SubnetID     string
}

// NetworkACLRule represents a rule in a network ACL.
type NetworkACLRule struct {
	RuleNumber int
	Protocol   string
	Action     string // "allow" or "deny"
	CIDR       string
	FromPort   int
	ToPort     int
	Egress     bool
}

// InternetGatewayConfig configures an internet gateway.
type InternetGatewayConfig struct {
	Tags map[string]string
}

// InternetGateway represents an internet gateway.
type InternetGateway struct {
	ID    string
	VpcID string
	State string // "detached", "attached"
	Tags  map[string]string
}

// ElasticIPConfig configures an elastic IP allocation.
type ElasticIPConfig struct {
	Tags map[string]string
	// SKU (Azure public IP: Basic/Standard) and AllocationMethod (Static/
	// Dynamic) are cost/behavior inputs a discoverer reads; optional.
	SKU              string
	AllocationMethod string
	// Zones, IdleTimeoutMinutes and DNSDomainNameLabel are Azure public-IP-only
	// fields (availability zones, TCP idle timeout, DNS label) the AWS-shaped
	// fields above cannot represent; empty/zero for AWS and GCP.
	Zones              []string
	IdleTimeoutMinutes int
	DNSDomainNameLabel string
}

// ElasticIP represents an elastic IP address.
type ElasticIP struct {
	AllocationID  string
	PublicIP      string
	AssociationID string
	InstanceID    string
	Tags          map[string]string
	// NetworkInterfaceID / PrivateIP describe the ENI target of an AWS
	// association: AssociateAddress can bind an EIP to a network interface
	// (and an optional secondary private address) rather than an instance, and
	// DescribeAddresses reports both back. Empty for Azure/GCP.
	NetworkInterfaceID string
	PrivateIP          string
	// SKU is the Azure public-IP SKU (Basic/Standard), echoed as sku.name.
	SKU string
	// AllocationMethod is Static/Dynamic (Azure publicIPAllocationMethod).
	AllocationMethod string
	// Zones, IdleTimeoutMinutes, DNSDomainNameLabel and DNSFQDN round-trip the
	// Azure-only public-IP fields set in ElasticIPConfig; DNSFQDN is derived by
	// the provider from DNSDomainNameLabel. Empty/zero for AWS and GCP.
	Zones              []string
	IdleTimeoutMinutes int
	DNSDomainNameLabel string
	DNSFQDN            string
}

// AssociateAddressInput carries the target of an AssociateAddress call. Exactly
// one of InstanceID or NetworkInterfaceID identifies the target; PrivateIP
// optionally pins the association to a specific private address on the
// interface. The ENI form (NetworkInterfaceID/PrivateIP) is an AWS concept —
// Azure/GCP read only InstanceID.
type AssociateAddressInput struct {
	InstanceID         string
	NetworkInterfaceID string
	PrivateIP          string
	// AllowReassociation mirrors the AWS request parameter: reassociation is
	// automatic by default (nil), but an explicit false makes the call fail with
	// Resource.AlreadyAssociated when the EIP is already bound to another target.
	AllowReassociation *bool
}

// RouteTableAssociation represents an association between
// a route table and a subnet.
type RouteTableAssociation struct {
	ID           string
	RouteTableID string
	SubnetID     string
	// Main reports whether this is the VPC's main-route-table association —
	// the implicit one EC2 creates with the VPC, carrying no subnet. Callers
	// tearing a VPC down disassociate every non-main association and leave the
	// main one to die with the VPC, so the distinction has to survive the
	// projection.
	Main bool
}

// NetworkInterface represents an elastic network interface.
//
// Managed services (NAT gateways, load balancers, managed databases) attach
// interfaces of their own, and those interfaces outlive the parent resource
// briefly. A caller deleting a VPC has to drain them first or the delete is
// refused, so they have to be observable.
type NetworkInterface struct {
	ID           string
	VPCID        string
	SubnetID     string
	Status       string // "available", "in-use"
	AttachmentID string
	Description  string
	Tags         map[string]string
	// InstanceID and DeviceIndex describe the attachment when the interface is
	// attached to an instance (ec2:AttachNetworkInterface); both are empty/zero
	// for an available interface.
	InstanceID  string
	DeviceIndex int
	// PrivateIP is the primary private IPv4 address the interface holds inside
	// its subnet, MacAddress its hardware address, and SourceDestCheck the
	// source/destination check flag. Real EC2 auto-assigns a private IP and MAC
	// on create and defaults SourceDestCheck to true — the flag a NAT-instance /
	// firewall / router VM disables via ModifyNetworkInterfaceAttribute.
	PrivateIP       string
	MacAddress      string
	SourceDestCheck bool
}

// Azure network interface (Microsoft.Network/networkInterfaces) is an
// Azure-specific optional capability, kept out of the cross-cloud Networking
// interface (Azure NICs carry ipConfigurations, a location, and per-config
// private-IP allocation that the AWS-shaped ENI model does not). A provider
// exposes it by implementing AzureNetworkInterfaces; the wire handler reaches
// it by type assertion, mirroring the AWS-specific NetworkInterfaceCreator
// surface below.

// AzureIPConfig is one ipConfiguration on an Azure network interface.
type AzureIPConfig struct {
	Name             string
	SubnetID         string // ARM resource id of the referenced subnet
	SubnetCIDR       string // resolved address prefix, used to allocate a dynamic private IP
	PrivateIP        string // set for Static; assigned by the mock for Dynamic
	AllocationMethod string // "Dynamic" (default) or "Static"
	PublicIPID       string // ARM resource id of an associated public IP, optional
	Primary          bool
	// LBBackendPoolIDs are the ARM resource ids of the load-balancer backend
	// address pools this ipConfiguration has joined
	// (properties.loadBalancerBackendAddressPools). This is the single source of
	// truth for NIC↔LB backend-pool membership: the NIC echoes these back on
	// GET, and a load balancer projects each backend pool's read-only
	// backendIPConfigurations by reverse-lookup against this field, so both
	// sides of the association resolve consistently from one stored reference.
	LBBackendPoolIDs []string
}

// AzureNICConfig is the create-or-update payload for an Azure network interface.
type AzureNICConfig struct {
	Location     string
	Tags         map[string]string
	IPConfigs    []AzureIPConfig
	IPForwarding bool
	// NetworkSecurityGroupID is the ARM resource id of the NSG associated with
	// the whole interface (properties.networkSecurityGroup) — an Azure NIC
	// binds its NSG at this top level, not per ipConfiguration.
	NetworkSecurityGroupID string
}

// AzureNIC is the stored/returned Azure network interface.
type AzureNIC struct {
	Name                   string
	ResourceGroup          string
	Location               string
	Tags                   map[string]string
	IPConfigs              []AzureIPConfig
	IPForwarding           bool
	NetworkSecurityGroupID string
	MACAddress             string
	ResourceGUID           string
	ProvisioningState      string
	ETag                   string
	VirtualMachineID       string // set while attached to a VM
}

// AzureNetworkInterfaces is the Azure-specific network-interface surface,
// keyed by (resourceGroup, name) to match ARM's addressing and give idempotent
// createOrUpdate. Nil resource group on List means subscription-wide.
type AzureNetworkInterfaces interface {
	CreateOrUpdateNetworkInterface(ctx context.Context, resourceGroup, name string, cfg AzureNICConfig) (*AzureNIC, error)
	GetNetworkInterface(ctx context.Context, resourceGroup, name string) (*AzureNIC, error)
	DeleteNetworkInterface(ctx context.Context, resourceGroup, name string) error
	ListNetworkInterfaces(ctx context.Context, resourceGroup string) ([]AzureNIC, error)
}

// VPCEndpointConfig describes a VPC endpoint to create.
type VPCEndpointConfig struct {
	VPCID            string
	ServiceName      string
	EndpointType     string // "Gateway" or "Interface"
	SubnetIDs        []string
	SecurityGroupIDs []string
	RouteTableIDs    []string
	Tags             map[string]string
}

// VPCEndpoint describes a VPC endpoint.
type VPCEndpoint struct {
	ID               string
	VPCID            string
	ServiceName      string
	EndpointType     string
	State            string // "available", "pending", "deleting"
	SubnetIDs        []string
	SecurityGroupIDs []string
	RouteTableIDs    []string
	Tags             map[string]string
	CreatedAt        string
}

// Networking is the interface that networking provider
// implementations must satisfy.
type Networking interface {
	CreateVPC(ctx context.Context, config VPCConfig) (*VPCInfo, error)
	DeleteVPC(ctx context.Context, id string) error
	DescribeVPCs(ctx context.Context, ids []string) ([]VPCInfo, error)

	CreateSubnet(ctx context.Context, config SubnetConfig) (*SubnetInfo, error)
	DeleteSubnet(ctx context.Context, id string) error
	DescribeSubnets(ctx context.Context, ids []string) ([]SubnetInfo, error)

	CreateSecurityGroup(ctx context.Context, config SecurityGroupConfig) (*SecurityGroupInfo, error)
	DeleteSecurityGroup(ctx context.Context, id string) error
	DescribeSecurityGroups(ctx context.Context, ids []string) ([]SecurityGroupInfo, error)

	AddIngressRule(ctx context.Context, groupID string, rule SecurityRule) error
	AddEgressRule(ctx context.Context, groupID string, rule SecurityRule) error
	RemoveIngressRule(ctx context.Context, groupID string, rule SecurityRule) error
	RemoveEgressRule(ctx context.Context, groupID string, rule SecurityRule) error

	// VPC Peering
	CreatePeeringConnection(ctx context.Context, config PeeringConfig) (*PeeringConnection, error)
	AcceptPeeringConnection(ctx context.Context, peeringID string) error
	RejectPeeringConnection(ctx context.Context, peeringID string) error
	DeletePeeringConnection(ctx context.Context, peeringID string) error
	DescribePeeringConnections(ctx context.Context, ids []string) ([]PeeringConnection, error)

	// NAT Gateways
	CreateNATGateway(ctx context.Context, config NATGatewayConfig) (*NATGateway, error)
	DeleteNATGateway(ctx context.Context, id string) error
	DescribeNATGateways(ctx context.Context, ids []string) ([]NATGateway, error)

	// Flow Logs
	CreateFlowLog(ctx context.Context, config FlowLogConfig) (*FlowLog, error)
	DeleteFlowLog(ctx context.Context, id string) error
	DescribeFlowLogs(ctx context.Context, ids []string) ([]FlowLog, error)
	GetFlowLogRecords(ctx context.Context, flowLogID string, limit int) ([]FlowLogRecord, error)

	// Route Tables
	CreateRouteTable(ctx context.Context, config RouteTableConfig) (*RouteTable, error)
	DeleteRouteTable(ctx context.Context, id string) error
	DescribeRouteTables(ctx context.Context, ids []string) ([]RouteTable, error)
	CreateRoute(ctx context.Context, routeTableID, destinationCIDR, targetID, targetType string) error
	DeleteRoute(ctx context.Context, routeTableID, destinationCIDR string) error

	// Network ACLs
	CreateNetworkACL(ctx context.Context, vpcID string, tags map[string]string) (*NetworkACL, error)
	DeleteNetworkACL(ctx context.Context, id string) error
	DescribeNetworkACLs(ctx context.Context, ids []string) ([]NetworkACL, error)
	AddNetworkACLRule(ctx context.Context, aclID string, rule *NetworkACLRule) error
	RemoveNetworkACLRule(ctx context.Context, aclID string, ruleNumber int, egress bool) error

	// Internet Gateways
	CreateInternetGateway(ctx context.Context, cfg InternetGatewayConfig) (*InternetGateway, error)
	DeleteInternetGateway(ctx context.Context, id string) error
	DescribeInternetGateways(ctx context.Context, ids []string) ([]InternetGateway, error)
	AttachInternetGateway(ctx context.Context, igwID, vpcID string) error
	DetachInternetGateway(ctx context.Context, igwID, vpcID string) error

	// Elastic IPs
	AllocateAddress(ctx context.Context, cfg ElasticIPConfig) (*ElasticIP, error)
	ReleaseAddress(ctx context.Context, allocationID string) error
	DescribeAddresses(ctx context.Context, ids []string) ([]ElasticIP, error)
	AssociateAddress(ctx context.Context, allocationID string, in AssociateAddressInput) (string, error)
	DisassociateAddress(ctx context.Context, associationID string) error

	// Route Table Associations
	AssociateRouteTable(ctx context.Context, routeTableID, subnetID string) (*RouteTableAssociation, error)
	DisassociateRouteTable(ctx context.Context, associationID string) error

	// VPC Endpoints
	CreateVPCEndpoint(ctx context.Context, config VPCEndpointConfig) (*VPCEndpoint, error)
	DeleteVPCEndpoint(ctx context.Context, id string) error
	DescribeVPCEndpoints(ctx context.Context, ids []string) ([]VPCEndpoint, error)
	ModifyVPCEndpoint(ctx context.Context, id string, config VPCEndpointConfig) (*VPCEndpoint, error)

	// Tag mutation. Update* merges keys into the resource's existing Tags
	// (overlapping keys overwritten, others preserved); Remove* deletes the
	// listed keys. Required by the Resource Groups Tagging API surface.
	UpdateVPCTags(ctx context.Context, id string, tags map[string]string) error
	RemoveVPCTags(ctx context.Context, id string, keys []string) error
	UpdateSubnetTags(ctx context.Context, id string, tags map[string]string) error
	RemoveSubnetTags(ctx context.Context, id string, keys []string) error
	UpdateSecurityGroupTags(ctx context.Context, id string, tags map[string]string) error
	RemoveSecurityGroupTags(ctx context.Context, id string, keys []string) error
}

// VPCAttributeUpdate carries the attributes a caller wants changed. A nil
// pointer leaves that attribute alone, matching an API that accepts one
// attribute per call — a caller enabling DNS hostnames must not have its
// DNS-support setting reset as a side effect.
//
// A struct rather than positional pointers so a new attribute can be added
// without breaking every implementation.
type VPCAttributeUpdate struct {
	EnableDNSSupport   *bool
	EnableDNSHostnames *bool
}

// VPCAttributes is an OPTIONAL capability, discovered by type assertion.
// Per-VPC DNS attributes are an AWS concept; other clouds configure name
// resolution elsewhere, so this is kept out of the Networking interface rather
// than forcing them to carry a method they cannot implement meaningfully.
type VPCAttributes interface {
	ModifyVPCAttribute(ctx context.Context, id string, update VPCAttributeUpdate) error
}

// NetworkACLAssociator is an OPTIONAL capability, discovered by type assertion.
// Moving a subnet from one network ACL to another (yielding a new association
// ID) is an AWS concept; other clouds model network security differently, so it
// is kept out of the Networking interface rather than forcing a mirror.
type NetworkACLAssociator interface {
	ReplaceNetworkACLAssociation(ctx context.Context, associationID, newACLID string) (*NetworkACLAssociation, error)
}

// SubnetAttributeUpdate carries the subnet attributes a caller wants changed. A
// nil pointer leaves that attribute alone, matching an API that accepts one
// attribute per ModifySubnetAttribute call.
type SubnetAttributeUpdate struct {
	MapPublicIPOnLaunch *bool
}

// SubnetAttributes is an OPTIONAL capability, discovered by type assertion.
// Per-subnet launch attributes (map-public-ip-on-launch) are an AWS concept;
// kept out of the Networking interface so providers that do not model them are
// not forced to carry an implementation.
type SubnetAttributes interface {
	ModifySubnetAttribute(ctx context.Context, id string, update SubnetAttributeUpdate) error
}

// SubnetCIDRUpdater is an OPTIONAL capability, discovered by type assertion. It
// changes a subnet's address prefix in place — the Azure ARM
// Subnets.CreateOrUpdate re-PUT path allows editing a subnet's addressPrefix,
// unlike AWS where a subnet CIDR is immutable. Providers that model an
// immutable subnet CIDR do not implement it.
type SubnetCIDRUpdater interface {
	UpdateSubnetCIDR(ctx context.Context, id, cidr string) error
}

// NetworkInterfaces is an OPTIONAL capability, discovered by type assertion.
//
// Kept out of the Networking interface for the same reason as VPCAttributes:
// requiring it would have every provider carry an implementation, and the two
// that do not model interfaces would carry identical copies of one that does
// nothing for them.
type NetworkInterfaces interface {
	DescribeNetworkInterfaces(ctx context.Context, ids []string) ([]NetworkInterface, error)
	DetachNetworkInterface(ctx context.Context, attachmentID string, force bool) error
	DeleteNetworkInterface(ctx context.Context, id string) error
}

// NetworkInterfaceCreator is the AWS-specific ENI-creation surface. It's kept
// out of NetworkInterfaces so that adding it doesn't break subset assertions
// (e.g. resourcediscovery's read-only walker, which only needs Describe).
type NetworkInterfaceCreator interface {
	CreateNetworkInterface(ctx context.Context, subnetID, description string, groups []string, tags map[string]string) (*NetworkInterface, error)
}

// NetworkInterfaceAttributeUpdate carries the ENI attributes a caller wants
// changed via ec2:ModifyNetworkInterfaceAttribute. A nil pointer (or nil Groups)
// leaves that attribute untouched, matching an API that accepts one attribute
// per call.
type NetworkInterfaceAttributeUpdate struct {
	SourceDestCheck *bool
	Description     *string
	Groups          []string
}

// NetworkInterfaceModifier is the AWS-specific ENI attribute-modify surface
// (ec2:ModifyNetworkInterfaceAttribute). It is kept out of NetworkInterfaces so
// adding it doesn't break subset assertions, mirroring NetworkInterfaceCreator.
type NetworkInterfaceModifier interface {
	ModifyNetworkInterfaceAttribute(ctx context.Context, id string, update NetworkInterfaceAttributeUpdate) error
}

// NetworkInterfaceAttacher is the AWS-specific ENI attach surface
// (ec2:AttachNetworkInterface), completing the ENI attach/detach lifecycle. It
// returns the new attachment id. The instance's existence is verified by the
// wire layer (which holds the compute driver); the networking provider only
// records the attachment.
type NetworkInterfaceAttacher interface {
	AttachNetworkInterface(ctx context.Context, networkInterfaceID, instanceID string, deviceIndex int) (string, error)
}
