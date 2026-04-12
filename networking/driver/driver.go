// Package driver defines the interface for networking service implementations.
package driver

import "context"

// VPCConfig describes a VPC to create.
type VPCConfig struct {
	CIDRBlock string
	Tags      map[string]string
}

// VPCInfo describes a VPC.
type VPCInfo struct {
	ID        string
	CIDRBlock string
	State     string
	Tags      map[string]string
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
}

// NATGatewayConfig configures a NAT gateway.
type NATGatewayConfig struct {
	SubnetID string
	Tags     map[string]string
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
	ID        string
	VPCID     string
	Rules     []NetworkACLRule
	Tags      map[string]string
	IsDefault bool
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
}

// ElasticIP represents an elastic IP address.
type ElasticIP struct {
	AllocationID  string
	PublicIP      string
	AssociationID string
	InstanceID    string
	Tags          map[string]string
}

// RouteTableAssociation represents an association between
// a route table and a subnet.
type RouteTableAssociation struct {
	ID           string
	RouteTableID string
	SubnetID     string
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
	AssociateAddress(ctx context.Context, allocationID, instanceID string) (string, error)
	DisassociateAddress(ctx context.Context, associationID string) error

	// Route Table Associations
	AssociateRouteTable(ctx context.Context, routeTableID, subnetID string) (*RouteTableAssociation, error)
	DisassociateRouteTable(ctx context.Context, associationID string) error

	// VPC Endpoints
	CreateVPCEndpoint(ctx context.Context, config VPCEndpointConfig) (*VPCEndpoint, error)
	DeleteVPCEndpoint(ctx context.Context, id string) error
	DescribeVPCEndpoints(ctx context.Context, ids []string) ([]VPCEndpoint, error)
	ModifyVPCEndpoint(ctx context.Context, id string, config VPCEndpointConfig) (*VPCEndpoint, error)
}
