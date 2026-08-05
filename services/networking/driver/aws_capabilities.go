package driver

import (
	"context"
	"time"
)

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
	Routes            []VPNConnectionRoute
	Tags              map[string]string
}

// VPNConnectionRoute is a static route on a site-to-site VPN connection.
type VPNConnectionRoute struct {
	DestinationCIDR string
	State           string
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
	CreateVPNConnectionRoute(ctx context.Context, vpnConnectionID, destinationCIDR string) error
	DeleteVPNConnectionRoute(ctx context.Context, vpnConnectionID, destinationCIDR string) error
	ModifyVPNConnection(ctx context.Context, id, transitGatewayID, vpnGatewayID string) (*VPNConnection, error)
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
	ModifyManagedPrefixList(ctx context.Context, id string, addEntries []PrefixListEntry, removeCIDRs []string) (*PrefixList, error)
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
	ModifyVPCEndpointServicePermissions(ctx context.Context, serviceID string, addPrincipals, removePrincipals []string) error
	DescribeVPCEndpointServicePermissions(ctx context.Context, serviceID string) ([]string, error)
}

// ---- Client VPN ----

// ClientVPNEndpoint is a managed endpoint remote clients connect to.
type ClientVPNEndpoint struct {
	ID                   string
	Description          string
	ClientCIDRBlock      string
	ServerCertificateARN string
	AuthenticationTypes  []string
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
	AuthenticationTypes  []string
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

// ClientVPNAuthorizationRule authorizes a client CIDR to reach a target network.
type ClientVPNAuthorizationRule struct {
	EndpointID string
	TargetCIDR string
	GroupID    string
	AccessAll  bool
	Status     string
}

// ClientVPNRoute is a route on a Client VPN endpoint.
type ClientVPNRoute struct {
	EndpointID      string
	DestinationCIDR string
	TargetSubnetID  string
	Status          string
}

// ClientVPN is an OPTIONAL AWS capability (type-asserted).
type ClientVPN interface {
	CreateClientVPNEndpoint(ctx context.Context, cfg ClientVPNEndpointConfig) (*ClientVPNEndpoint, error)
	DeleteClientVPNEndpoint(ctx context.Context, id string) error
	DescribeClientVPNEndpoints(ctx context.Context, ids []string) ([]ClientVPNEndpoint, error)
	AssociateClientVPNTargetNetwork(ctx context.Context, endpointID, subnetID string) (*ClientVPNTargetNetwork, error)
	DisassociateClientVPNTargetNetwork(ctx context.Context, endpointID, associationID string) error
	DescribeClientVPNTargetNetworks(ctx context.Context, endpointID string) ([]ClientVPNTargetNetwork, error)
	AuthorizeClientVPNIngress(ctx context.Context, endpointID, targetCIDR, groupID string, accessAll bool) (*ClientVPNAuthorizationRule, error)
	RevokeClientVPNIngress(ctx context.Context, endpointID, targetCIDR string) error
	DescribeClientVPNAuthorizationRules(ctx context.Context, endpointID string) ([]ClientVPNAuthorizationRule, error)
	CreateClientVPNRoute(ctx context.Context, endpointID, destinationCIDR, targetSubnetID string) (*ClientVPNRoute, error)
	DeleteClientVPNRoute(ctx context.Context, endpointID, destinationCIDR, targetSubnetID string) error
	DescribeClientVPNRoutes(ctx context.Context, endpointID string) ([]ClientVPNRoute, error)
}

// ---- Traffic Mirroring ----

// TrafficMirrorTarget is the destination (ENI, NLB, or GWLB endpoint) that
// mirrored packets are copied to.
type TrafficMirrorTarget struct {
	ID                            string
	Description                   string
	NetworkInterfaceID            string
	NetworkLoadBalancerARN        string
	GatewayLoadBalancerEndpointID string
	Type                          string
	OwnerID                       string
	Tags                          map[string]string
}

// TrafficMirrorTargetConfig is the input to CreateTrafficMirrorTarget.
type TrafficMirrorTargetConfig struct {
	Description                   string
	NetworkInterfaceID            string
	NetworkLoadBalancerARN        string
	GatewayLoadBalancerEndpointID string
	Tags                          map[string]string
}

// TrafficMirrorPortRange is a from/to TCP or UDP port range on a filter rule.
type TrafficMirrorPortRange struct {
	FromPort int32
	ToPort   int32
}

// TrafficMirrorFilterRule is one ingress or egress rule within a filter.
type TrafficMirrorFilterRule struct {
	ID                   string
	FilterID             string
	TrafficDirection     string // ingress | egress
	RuleNumber           int32
	RuleAction           string // accept | reject
	Protocol             int32
	DestinationCIDR      string
	SourceCIDR           string
	DestinationPortRange *TrafficMirrorPortRange
	SourcePortRange      *TrafficMirrorPortRange
	Description          string
}

// TrafficMirrorFilterRuleConfig is the input to Create/ModifyTrafficMirrorFilterRule.
type TrafficMirrorFilterRuleConfig struct {
	FilterID             string
	TrafficDirection     string
	RuleNumber           int32
	RuleAction           string
	Protocol             int32
	DestinationCIDR      string
	SourceCIDR           string
	DestinationPortRange *TrafficMirrorPortRange
	SourcePortRange      *TrafficMirrorPortRange
	Description          string
}

// TrafficMirrorFilter groups the rules that select which traffic to mirror.
type TrafficMirrorFilter struct {
	ID              string
	Description     string
	NetworkServices []string
	IngressRules    []TrafficMirrorFilterRule
	EgressRules     []TrafficMirrorFilterRule
	Tags            map[string]string
}

// TrafficMirrorSession binds a source ENI to a target and filter.
type TrafficMirrorSession struct {
	ID                    string
	NetworkInterfaceID    string
	TrafficMirrorTargetID string
	TrafficMirrorFilterID string
	PacketLength          int32
	SessionNumber         int32
	VirtualNetworkID      int32
	Description           string
	OwnerID               string
	Tags                  map[string]string
}

// TrafficMirrorSessionConfig is the input to Create/ModifyTrafficMirrorSession.
type TrafficMirrorSessionConfig struct {
	NetworkInterfaceID    string
	TrafficMirrorTargetID string
	TrafficMirrorFilterID string
	PacketLength          int32
	SessionNumber         int32
	VirtualNetworkID      int32
	Description           string
	Tags                  map[string]string
}

// TrafficMirroring is an OPTIONAL AWS capability (type-asserted).
type TrafficMirroring interface {
	CreateTrafficMirrorTarget(ctx context.Context, cfg TrafficMirrorTargetConfig) (*TrafficMirrorTarget, error)
	DeleteTrafficMirrorTarget(ctx context.Context, id string) error
	DescribeTrafficMirrorTargets(ctx context.Context, ids []string) ([]TrafficMirrorTarget, error)

	CreateTrafficMirrorFilter(ctx context.Context, description string, tags map[string]string) (*TrafficMirrorFilter, error)
	DeleteTrafficMirrorFilter(ctx context.Context, id string) error
	DescribeTrafficMirrorFilters(ctx context.Context, ids []string) ([]TrafficMirrorFilter, error)
	ModifyTrafficMirrorFilterNetworkServices(ctx context.Context, filterID string, add, remove []string) (*TrafficMirrorFilter, error)

	CreateTrafficMirrorFilterRule(ctx context.Context, cfg TrafficMirrorFilterRuleConfig) (*TrafficMirrorFilterRule, error)
	ModifyTrafficMirrorFilterRule(
		ctx context.Context, id string, cfg TrafficMirrorFilterRuleConfig, removeFields []string,
	) (*TrafficMirrorFilterRule, error)
	DeleteTrafficMirrorFilterRule(ctx context.Context, id string) error
	DescribeTrafficMirrorFilterRules(ctx context.Context, filterID string, ruleIDs []string) ([]TrafficMirrorFilterRule, error)

	CreateTrafficMirrorSession(ctx context.Context, cfg TrafficMirrorSessionConfig) (*TrafficMirrorSession, error)
	ModifyTrafficMirrorSession(
		ctx context.Context, id string, cfg TrafficMirrorSessionConfig, removeFields []string,
	) (*TrafficMirrorSession, error)
	DeleteTrafficMirrorSession(ctx context.Context, id string) error
	DescribeTrafficMirrorSessions(ctx context.Context, ids []string) ([]TrafficMirrorSession, error)
}

// ---- Network Insights (Reachability Analyzer & Network Access Analyzer) ----

// NetworkInsightsPath describes a source→destination path to analyze for
// reachability.
type NetworkInsightsPath struct {
	ID              string
	ARN             string
	Protocol        string
	Source          string
	SourceARN       string
	SourceIP        string
	Destination     string
	DestinationARN  string
	DestinationIP   string
	DestinationPort int32
	CreatedDate     time.Time
	Tags            map[string]string
}

// NetworkInsightsPathConfig is the input to CreateNetworkInsightsPath.
type NetworkInsightsPathConfig struct {
	Protocol        string
	Source          string
	Destination     string
	SourceIP        string
	DestinationIP   string
	DestinationPort int32
	Tags            map[string]string
}

// NetworkInsightsAnalysis is the result of running reachability analysis on a
// path. The mock completes analyses synchronously.
type NetworkInsightsAnalysis struct {
	ID                 string
	ARN                string
	PathID             string
	StartDate          time.Time
	Status             string
	StatusMessage      string
	NetworkPathFound   bool
	FilterInARNs       []string
	FilterOutARNs      []string
	AdditionalAccounts []string
	Tags               map[string]string
}

// NetworkInsightsAnalysisConfig is the input to StartNetworkInsightsAnalysis.
type NetworkInsightsAnalysisConfig struct {
	PathID             string
	FilterInARNs       []string
	FilterOutARNs      []string
	AdditionalAccounts []string
	Tags               map[string]string
}

// AccessScopeResourceStatement selects resources by type and/or id.
type AccessScopeResourceStatement struct {
	ResourceTypes []string
	Resources     []string
}

// AccessScopeStatement is one end (source or destination) of an access-scope path.
type AccessScopeStatement struct {
	ResourceStatement *AccessScopeResourceStatement
}

// AccessScopePath is one match/exclude path in a Network Access Analyzer scope.
type AccessScopePath struct {
	Source      *AccessScopeStatement
	Destination *AccessScopeStatement
}

// NetworkInsightsAccessScope is a Network Access Analyzer scope definition.
type NetworkInsightsAccessScope struct {
	ID           string
	ARN          string
	MatchPaths   []AccessScopePath
	ExcludePaths []AccessScopePath
	CreatedDate  time.Time
	UpdatedDate  time.Time
	Tags         map[string]string
}

// NetworkInsightsAccessScopeConfig is the input to CreateNetworkInsightsAccessScope.
type NetworkInsightsAccessScopeConfig struct {
	MatchPaths   []AccessScopePath
	ExcludePaths []AccessScopePath
	Tags         map[string]string
}

// NetworkInsightsAccessScopeAnalysis is the result of analyzing an access scope.
type NetworkInsightsAccessScopeAnalysis struct {
	ID               string
	ARN              string
	AccessScopeID    string
	Status           string
	StatusMessage    string
	StartDate        time.Time
	EndDate          time.Time
	FindingsFound    string
	AnalyzedEniCount int32
	Tags             map[string]string
}

// AccessScopeAnalysisFinding is one finding from an access-scope analysis.
type AccessScopeAnalysisFinding struct {
	FindingID     string
	AnalysisID    string
	AccessScopeID string
}

// NetworkInsights is an OPTIONAL AWS capability (type-asserted). It covers both
// Reachability Analyzer (paths + analyses) and Network Access Analyzer (access
// scopes + scope analyses).
type NetworkInsights interface {
	CreateNetworkInsightsPath(ctx context.Context, cfg NetworkInsightsPathConfig) (*NetworkInsightsPath, error)
	DeleteNetworkInsightsPath(ctx context.Context, id string) error
	DescribeNetworkInsightsPaths(ctx context.Context, ids []string) ([]NetworkInsightsPath, error)

	StartNetworkInsightsAnalysis(ctx context.Context, cfg NetworkInsightsAnalysisConfig) (*NetworkInsightsAnalysis, error)
	DeleteNetworkInsightsAnalysis(ctx context.Context, id string) error
	DescribeNetworkInsightsAnalyses(ctx context.Context, ids []string, pathID string) ([]NetworkInsightsAnalysis, error)

	CreateNetworkInsightsAccessScope(ctx context.Context, cfg NetworkInsightsAccessScopeConfig) (*NetworkInsightsAccessScope, error)
	DeleteNetworkInsightsAccessScope(ctx context.Context, id string) error
	DescribeNetworkInsightsAccessScopes(ctx context.Context, ids []string) ([]NetworkInsightsAccessScope, error)
	GetNetworkInsightsAccessScopeContent(ctx context.Context, id string) (*NetworkInsightsAccessScope, error)

	StartNetworkInsightsAccessScopeAnalysis(
		ctx context.Context, accessScopeID string, tags map[string]string,
	) (*NetworkInsightsAccessScopeAnalysis, error)
	DeleteNetworkInsightsAccessScopeAnalysis(ctx context.Context, id string) error
	DescribeNetworkInsightsAccessScopeAnalyses(
		ctx context.Context, ids []string, accessScopeID string,
	) ([]NetworkInsightsAccessScopeAnalysis, error)
	GetNetworkInsightsAccessScopeAnalysisFindings(ctx context.Context, analysisID string) ([]AccessScopeAnalysisFinding, string, error)
}

// ---- VPC Block Public Access ----

// VPCBlockPublicAccessOptions is the account/region-level BPA configuration
// singleton.
type VPCBlockPublicAccessOptions struct {
	AWSAccountID             string
	AWSRegion                string
	State                    string
	InternetGatewayBlockMode string
	ExclusionsAllowed        string
	ManagedBy                string
	Reason                   string
	LastUpdateTimestamp      time.Time
}

// VPCBlockPublicAccessExclusion exempts a VPC or subnet from the BPA options.
type VPCBlockPublicAccessExclusion struct {
	ExclusionID                  string
	InternetGatewayExclusionMode string
	ResourceARN                  string
	State                        string
	Reason                       string
	CreationTimestamp            time.Time
	LastUpdateTimestamp          time.Time
	Tags                         map[string]string
}

// VPCBlockPublicAccessExclusionConfig is the input to CreateVPCBlockPublicAccessExclusion.
type VPCBlockPublicAccessExclusionConfig struct {
	VPCID                        string
	SubnetID                     string
	InternetGatewayExclusionMode string
	Tags                         map[string]string
}

// VPCBlockPublicAccess is an OPTIONAL AWS capability (type-asserted).
type VPCBlockPublicAccess interface {
	DescribeVPCBlockPublicAccessOptions(ctx context.Context) (*VPCBlockPublicAccessOptions, error)
	ModifyVPCBlockPublicAccessOptions(ctx context.Context, internetGatewayBlockMode string) (*VPCBlockPublicAccessOptions, error)

	CreateVPCBlockPublicAccessExclusion(ctx context.Context, cfg VPCBlockPublicAccessExclusionConfig) (*VPCBlockPublicAccessExclusion, error)
	ModifyVPCBlockPublicAccessExclusion(ctx context.Context, id, internetGatewayExclusionMode string) (*VPCBlockPublicAccessExclusion, error)
	DeleteVPCBlockPublicAccessExclusion(ctx context.Context, id string) (*VPCBlockPublicAccessExclusion, error)
	DescribeVPCBlockPublicAccessExclusions(ctx context.Context, ids []string) ([]VPCBlockPublicAccessExclusion, error)
}
