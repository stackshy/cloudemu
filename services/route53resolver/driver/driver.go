// Package driver defines the in-memory contract for the AWS Route 53 Resolver
// control plane. The portable service and the AWS JSON 1.1 server handler both
// depend on this interface; the aws provider implements it.
//
// The interface is composed from one sub-interface per resource group
// (resolver endpoints, resolver rules, query-log configs, resolver/DNSSEC
// configs, DNS firewall, outpost resolvers, tagging) so groups can be added
// incrementally without widening a single monolithic interface.
package driver

import "context"

// Route53Resolver is the full Route 53 Resolver control-plane surface.
type Route53Resolver interface {
	ResolverEndpoints
	ResolverRules
	QueryLogConfigs
	ResolverConfigs
	DnssecConfigs
	FirewallService
	OutpostResolvers
	Tagging
}

// Tag is a resource tag.
type Tag struct {
	Key   string
	Value string
}

// ---- Resolver Endpoints ----------------------------------------------------

// IPAddress is one IP the endpoint uses for DNS queries. IP/IPv6 are the
// addresses; SubnetID is required on create; IPID/Status are server-assigned.
type IPAddress struct {
	IPID       string
	SubnetID   string
	IP         string
	IPv6       string
	Status     string
	CreatedAt  string
	ModifiedAt string
}

// ResolverEndpoint is an inbound/outbound Resolver endpoint.
type ResolverEndpoint struct {
	ID                        string
	ARN                       string
	Name                      string
	CreatorRequestID          string
	Direction                 string
	HostVPCID                 string
	IPAddressCount            int32
	SecurityGroupIDs          []string
	IPAddresses               []IPAddress
	Status                    string
	StatusMessage             string
	ResolverEndpointType      string
	Protocols                 []string
	OutpostARN                string
	PreferredInstanceType     string
	DNS64Enabled              bool
	IPv6InternetAccessEnabled bool
	CreatedAt                 string
	ModifiedAt                string
}

// CreateResolverEndpointInput carries the create request fields.
type CreateResolverEndpointInput struct {
	CreatorRequestID          string
	Name                      string
	Direction                 string
	IPAddresses               []IPAddress
	SecurityGroupIDs          []string
	ResolverEndpointType      string
	Protocols                 []string
	OutpostARN                string
	PreferredInstanceType     string
	DNS64Enabled              bool
	IPv6InternetAccessEnabled bool
	Tags                      []Tag
}

// UpdateResolverEndpointInput carries the mutable fields; a nil pointer (or nil
// slice) means "absent from the request, leave unchanged" — distinct from an
// explicit empty value.
type UpdateResolverEndpointInput struct {
	Name                 *string
	ResolverEndpointType *string
	Protocols            []string
}

// ResolverEndpoints is the endpoint resource group.
type ResolverEndpoints interface {
	CreateResolverEndpoint(ctx context.Context, in *CreateResolverEndpointInput) (*ResolverEndpoint, error)
	GetResolverEndpoint(ctx context.Context, id string) (*ResolverEndpoint, error)
	UpdateResolverEndpoint(ctx context.Context, id string, in UpdateResolverEndpointInput) (*ResolverEndpoint, error)
	DeleteResolverEndpoint(ctx context.Context, id string) (*ResolverEndpoint, error)
	ListResolverEndpoints(ctx context.Context) ([]ResolverEndpoint, error)
	AssociateResolverEndpointIPAddress(ctx context.Context, id string, ip *IPAddress) (*ResolverEndpoint, error)
	DisassociateResolverEndpointIPAddress(ctx context.Context, id string, ip *IPAddress) (*ResolverEndpoint, error)
	ListResolverEndpointIPAddresses(ctx context.Context, id string) ([]IPAddress, error)
}

// ---- Resolver Rules --------------------------------------------------------

// TargetAddress is one forwarding target for a FORWARD rule.
type TargetAddress struct {
	IP                   string
	IPv6                 string
	Port                 int32
	Protocol             string
	ServerNameIndication string
}

// ResolverRule routes DNS queries for a domain to target IPs via an endpoint.
type ResolverRule struct {
	ID                 string
	ARN                string
	CreatorRequestID   string
	DomainName         string
	Name               string
	OwnerID            string
	ResolverEndpointID string
	RuleType           string
	ShareStatus        string
	Status             string
	StatusMessage      string
	TargetIPs          []TargetAddress
	CreatedAt          string
	ModifiedAt         string
}

// ResolverRuleAssociation associates a rule with a VPC.
type ResolverRuleAssociation struct {
	ID             string
	Name           string
	ResolverRuleID string
	VPCID          string
	Status         string
	StatusMessage  string
}

// CreateResolverRuleInput carries the create request fields.
type CreateResolverRuleInput struct {
	CreatorRequestID   string
	Name               string
	RuleType           string
	DomainName         string
	ResolverEndpointID string
	TargetIPs          []TargetAddress
	Tags               []Tag
}

// UpdateResolverRuleInput carries the mutable fields (ResolverRuleConfig); a
// nil pointer (or nil slice) means "absent, leave unchanged".
type UpdateResolverRuleInput struct {
	Name               *string
	ResolverEndpointID *string
	TargetIPs          []TargetAddress
}

// ResolverRules is the resolver-rule resource group.
type ResolverRules interface {
	CreateResolverRule(ctx context.Context, in *CreateResolverRuleInput) (*ResolverRule, error)
	GetResolverRule(ctx context.Context, id string) (*ResolverRule, error)
	UpdateResolverRule(ctx context.Context, id string, in UpdateResolverRuleInput) (*ResolverRule, error)
	DeleteResolverRule(ctx context.Context, id string) (*ResolverRule, error)
	ListResolverRules(ctx context.Context) ([]ResolverRule, error)
	AssociateResolverRule(ctx context.Context, ruleID, vpcID, name string) (*ResolverRuleAssociation, error)
	DisassociateResolverRule(ctx context.Context, ruleID, vpcID string) (*ResolverRuleAssociation, error)
	GetResolverRuleAssociation(ctx context.Context, assocID string) (*ResolverRuleAssociation, error)
	ListResolverRuleAssociations(ctx context.Context) ([]ResolverRuleAssociation, error)
	PutResolverRulePolicy(ctx context.Context, arn, policy string) error
	GetResolverRulePolicy(ctx context.Context, arn string) (string, error)
}

// ---- Query-Log Configs -----------------------------------------------------

// QueryLogConfig logs DNS queries from associated VPCs to a destination.
type QueryLogConfig struct {
	ID               string
	ARN              string
	AssociationCount int32
	CreatorRequestID string
	DestinationARN   string
	Name             string
	OwnerID          string
	ShareStatus      string
	Status           string
	CreatedAt        string
}

// QueryLogConfigAssociation associates a query-log config with a VPC/resource.
type QueryLogConfigAssociation struct {
	ID                       string
	ResolverQueryLogConfigID string
	ResourceID               string
	Status                   string
	Error                    string
	ErrorMessage             string
	CreatedAt                string
}

// CreateQueryLogConfigInput carries the create request fields.
type CreateQueryLogConfigInput struct {
	CreatorRequestID string
	DestinationARN   string
	Name             string
	Tags             []Tag
}

// QueryLogConfigs is the query-log-config resource group.
type QueryLogConfigs interface {
	CreateResolverQueryLogConfig(ctx context.Context, in *CreateQueryLogConfigInput) (*QueryLogConfig, error)
	GetResolverQueryLogConfig(ctx context.Context, id string) (*QueryLogConfig, error)
	DeleteResolverQueryLogConfig(ctx context.Context, id string) (*QueryLogConfig, error)
	ListResolverQueryLogConfigs(ctx context.Context) ([]QueryLogConfig, error)
	AssociateResolverQueryLogConfig(ctx context.Context, configID, resourceID string) (*QueryLogConfigAssociation, error)
	DisassociateResolverQueryLogConfig(ctx context.Context, configID, resourceID string) (*QueryLogConfigAssociation, error)
	GetResolverQueryLogConfigAssociation(ctx context.Context, assocID string) (*QueryLogConfigAssociation, error)
	ListResolverQueryLogConfigAssociations(ctx context.Context) ([]QueryLogConfigAssociation, error)
	PutResolverQueryLogConfigPolicy(ctx context.Context, arn, policy string) error
	GetResolverQueryLogConfigPolicy(ctx context.Context, arn string) (string, error)
}

// ---- Resolver Configs ------------------------------------------------------

// ResolverConfig is the per-VPC Resolver behavior configuration.
type ResolverConfig struct {
	ID                 string
	OwnerID            string
	ResourceID         string
	AutodefinedReverse string
}

// ResolverConfigs is the Resolver-config resource group (per-VPC autodefined
// reverse-DNS behavior).
type ResolverConfigs interface {
	GetResolverConfig(ctx context.Context, resourceID string) (*ResolverConfig, error)
	UpdateResolverConfig(ctx context.Context, resourceID, autodefinedReverseFlag string) (*ResolverConfig, error)
	ListResolverConfigs(ctx context.Context) ([]ResolverConfig, error)
}

// ---- DNSSEC Configs --------------------------------------------------------

// ResolverDnssecConfig is the per-VPC DNSSEC validation configuration.
type ResolverDnssecConfig struct {
	ID               string
	OwnerID          string
	ResourceID       string
	ValidationStatus string
}

// DnssecConfigs is the DNSSEC-config resource group (per-VPC DNSSEC validation).
type DnssecConfigs interface {
	GetResolverDnssecConfig(ctx context.Context, resourceID string) (*ResolverDnssecConfig, error)
	UpdateResolverDnssecConfig(ctx context.Context, resourceID, validation string) (*ResolverDnssecConfig, error)
	ListResolverDnssecConfigs(ctx context.Context) ([]ResolverDnssecConfig, error)
}

// ---- Outpost Resolvers -----------------------------------------------------

// OutpostResolver is a Resolver running on an AWS Outpost.
type OutpostResolver struct {
	ID                    string
	ARN                   string
	Name                  string
	CreatorRequestID      string
	OutpostARN            string
	PreferredInstanceType string
	InstanceCount         int32
	Status                string
	StatusMessage         string
	CreatedAt             string
	ModifiedAt            string
}

// CreateOutpostResolverInput carries the create request fields.
type CreateOutpostResolverInput struct {
	CreatorRequestID      string
	Name                  string
	OutpostARN            string
	PreferredInstanceType string
	InstanceCount         int32
	Tags                  []Tag
}

// UpdateOutpostResolverInput carries the mutable fields; a nil pointer means
// "absent, leave unchanged". ID identifies the target resolver.
type UpdateOutpostResolverInput struct {
	ID                    string
	Name                  *string
	PreferredInstanceType *string
	InstanceCount         *int32
}

// OutpostResolvers is the Outpost-resolver resource group.
type OutpostResolvers interface {
	CreateOutpostResolver(ctx context.Context, in *CreateOutpostResolverInput) (*OutpostResolver, error)
	GetOutpostResolver(ctx context.Context, id string) (*OutpostResolver, error)
	UpdateOutpostResolver(ctx context.Context, in *UpdateOutpostResolverInput) (*OutpostResolver, error)
	DeleteOutpostResolver(ctx context.Context, id string) (*OutpostResolver, error)
	ListOutpostResolvers(ctx context.Context) ([]OutpostResolver, error)
}

// ---- Tagging ---------------------------------------------------------------

// Tagging is the shared tag surface (ARN-addressed, like real Route 53 Resolver).
type Tagging interface {
	TagResource(ctx context.Context, arn string, tags []Tag) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListTagsForResource(ctx context.Context, arn string) ([]Tag, error)
}
