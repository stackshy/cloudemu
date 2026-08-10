// Package driver defines the in-memory contract for the AWS VPC Lattice control
// plane. The AWS REST-JSON server handler depends on this interface; the aws
// provider implements it.
//
// The interface is composed from one sub-interface per resource group (service
// networks, services, listeners, rules, target groups, associations, resource
// configurations/gateways, access-log subscriptions, auth/resource policies,
// domain verifications, tagging) so groups can be added incrementally.
package driver

import "context"

// VPCLattice is the full VPC Lattice control-plane surface.
type VPCLattice interface {
	ServiceNetworks
	Services
	Listeners
	Rules
	TargetGroups
	Associations
	ResourceConfigurations
	ResourceGateways
	ResourceEndpointAssociations
	AccessLogSubscriptions
	Policies
	DomainVerifications
	Tagging
}

// ---- Service Networks ------------------------------------------------------

// ServiceNetwork is a VPC Lattice service network.
type ServiceNetwork struct {
	ID                                       string
	ARN                                      string
	Name                                     string
	AuthType                                 string
	SharingConfigEnabled                     bool
	NumberOfAssociatedServices               int64
	NumberOfAssociatedVPCs                   int64
	NumberOfAssociatedResourceConfigurations int64
	CreatedAt                                string
	LastUpdatedAt                            string
}

// CreateServiceNetworkInput carries the create request fields.
type CreateServiceNetworkInput struct {
	Name                 string
	AuthType             string
	SharingConfigEnabled bool
	Tags                 map[string]string
}

// ServiceNetworks is the service-network resource group.
type ServiceNetworks interface {
	CreateServiceNetwork(ctx context.Context, in *CreateServiceNetworkInput) (*ServiceNetwork, error)
	GetServiceNetwork(ctx context.Context, id string) (*ServiceNetwork, error)
	UpdateServiceNetwork(ctx context.Context, id, authType string) (*ServiceNetwork, error)
	DeleteServiceNetwork(ctx context.Context, id string) error
	ListServiceNetworks(ctx context.Context) ([]ServiceNetwork, error)
}

// ---- Services --------------------------------------------------------------

// Service is a VPC Lattice service.
type Service struct {
	ID                 string
	ARN                string
	Name               string
	AuthType           string
	CertificateARN     string
	CustomDomainName   string
	DNSName            string
	HostedZoneID       string
	IdleTimeoutSeconds int32
	Status             string
	CreatedAt          string
	LastUpdatedAt      string
}

// CreateServiceInput carries the create request fields.
type CreateServiceInput struct {
	Name               string
	AuthType           string
	CertificateARN     string
	CustomDomainName   string
	IdleTimeoutSeconds int32
	Tags               map[string]string
}

// UpdateServiceInput carries the mutable service fields; zero = unchanged.
type UpdateServiceInput struct {
	ID                 string
	AuthType           string
	CertificateARN     string
	IdleTimeoutSeconds int32
}

// Services is the service resource group.
type Services interface {
	CreateService(ctx context.Context, in *CreateServiceInput) (*Service, error)
	GetService(ctx context.Context, id string) (*Service, error)
	UpdateService(ctx context.Context, in *UpdateServiceInput) (*Service, error)
	DeleteService(ctx context.Context, id string) error
	ListServices(ctx context.Context) ([]Service, error)
}

// ---- Listeners -------------------------------------------------------------

// Listener is a VPC Lattice service listener. DefaultAction is stored as raw
// JSON (a forward/fixedResponse union) and echoed back verbatim.
type Listener struct {
	ID            string
	ARN           string
	Name          string
	ServiceID     string
	ServiceARN    string
	Protocol      string
	Port          int32
	DefaultAction []byte
	CreatedAt     string
	LastUpdatedAt string
}

// CreateListenerInput carries the create request fields.
type CreateListenerInput struct {
	ServiceID     string
	Name          string
	Protocol      string
	Port          int32
	DefaultAction []byte
	Tags          map[string]string
}

// Listeners is the listener resource group (nested under a service).
type Listeners interface {
	CreateListener(ctx context.Context, in *CreateListenerInput) (*Listener, error)
	GetListener(ctx context.Context, serviceID, listenerID string) (*Listener, error)
	UpdateListener(ctx context.Context, serviceID, listenerID string, defaultAction []byte) (*Listener, error)
	DeleteListener(ctx context.Context, serviceID, listenerID string) error
	ListListeners(ctx context.Context, serviceID string) ([]Listener, error)
}

// ---- Rules -----------------------------------------------------------------

// Rule is a listener rule. Match and Action are stored as raw JSON (unions)
// and echoed back verbatim.
type Rule struct {
	ID            string
	ARN           string
	Name          string
	ServiceID     string
	ListenerID    string
	Priority      int32
	IsDefault     bool
	Match         []byte
	Action        []byte
	CreatedAt     string
	LastUpdatedAt string
}

// CreateRuleInput carries the create request fields.
type CreateRuleInput struct {
	ServiceID  string
	ListenerID string
	Name       string
	Priority   int32
	Match      []byte
	Action     []byte
	Tags       map[string]string
}

// RuleUpdate is one entry of a BatchUpdateRule request.
type RuleUpdate struct {
	RuleID   string
	Priority int32
	Match    []byte
	Action   []byte
}

// RuleUpdateFailure describes a rejected BatchUpdateRule entry.
type RuleUpdateFailure struct {
	RuleID         string
	FailureCode    string
	FailureMessage string
}

// Rules is the listener-rule resource group (nested under a listener).
type Rules interface {
	CreateRule(ctx context.Context, in *CreateRuleInput) (*Rule, error)
	GetRule(ctx context.Context, serviceID, listenerID, ruleID string) (*Rule, error)
	UpdateRule(ctx context.Context, serviceID, listenerID, ruleID string, priority int32, match, action []byte) (*Rule, error)
	DeleteRule(ctx context.Context, serviceID, listenerID, ruleID string) error
	ListRules(ctx context.Context, serviceID, listenerID string) ([]Rule, error)
	BatchUpdateRules(ctx context.Context, serviceID, listenerID string, updates []RuleUpdate) ([]Rule, []RuleUpdateFailure, error)
}

// ---- Target Groups & Targets -----------------------------------------------

// TargetGroup is a VPC Lattice target group. Config is stored as raw JSON and
// echoed back verbatim; the summary fields are extracted from it on create.
type TargetGroup struct {
	ID                          string
	ARN                         string
	Name                        string
	Type                        string
	Status                      string
	Config                      []byte
	Port                        int32
	Protocol                    string
	VpcID                       string
	IPAddressType               string
	LambdaEventStructureVersion string
	ServiceARNs                 []string
	CreatedAt                   string
	LastUpdatedAt               string
}

// CreateTargetGroupInput carries the create request fields.
type CreateTargetGroupInput struct {
	Name   string
	Type   string
	Config []byte
	Tags   map[string]string
}

// RegisteredTarget is a target registered with a target group.
type RegisteredTarget struct {
	ID         string
	Port       int32
	Status     string
	ReasonCode string
}

// TargetFailure describes a rejected register/deregister target entry.
type TargetFailure struct {
	ID             string
	Port           int32
	FailureCode    string
	FailureMessage string
}

// TargetGroups is the target-group resource group and its target membership.
type TargetGroups interface {
	CreateTargetGroup(ctx context.Context, in *CreateTargetGroupInput) (*TargetGroup, error)
	GetTargetGroup(ctx context.Context, id string) (*TargetGroup, error)
	UpdateTargetGroup(ctx context.Context, id string, healthCheck []byte) (*TargetGroup, error)
	DeleteTargetGroup(ctx context.Context, id string) error
	ListTargetGroups(ctx context.Context) ([]TargetGroup, error)
	RegisterTargets(ctx context.Context, tgID string, targets []RegisteredTarget) ([]RegisteredTarget, []TargetFailure, error)
	DeregisterTargets(ctx context.Context, tgID string, targets []RegisteredTarget) ([]RegisteredTarget, []TargetFailure, error)
	ListTargets(ctx context.Context, tgID string) ([]RegisteredTarget, error)
}

// ---- Service-Network Associations ------------------------------------------

// SNVpcAssociation associates a VPC with a service network.
type SNVpcAssociation struct {
	ID                 string
	ARN                string
	CreatedBy          string
	ServiceNetworkID   string
	ServiceNetworkARN  string
	ServiceNetworkName string
	VpcID              string
	SecurityGroupIDs   []string
	PrivateDNSEnabled  bool
	Status             string
	CreatedAt          string
	LastUpdatedAt      string
}

// SNServiceAssociation associates a service with a service network.
type SNServiceAssociation struct {
	ID                 string
	ARN                string
	CreatedBy          string
	CustomDomainName   string
	DNSName            string
	HostedZoneID       string
	ServiceID          string
	ServiceARN         string
	ServiceName        string
	ServiceNetworkID   string
	ServiceNetworkARN  string
	ServiceNetworkName string
	Status             string
	CreatedAt          string
}

// SNResourceAssociation associates a resource configuration with a network.
type SNResourceAssociation struct {
	ID                        string
	ARN                       string
	CreatedBy                 string
	ResourceConfigurationID   string
	ResourceConfigurationARN  string
	ResourceConfigurationName string
	ServiceNetworkID          string
	ServiceNetworkARN         string
	ServiceNetworkName        string
	PrivateDNSEnabled         bool
	Status                    string
	CreatedAt                 string
	LastUpdatedAt             string
}

// CreateSNVpcAssociationInput carries the SN↔VPC create fields.
type CreateSNVpcAssociationInput struct {
	ServiceNetworkID  string
	VpcID             string
	SecurityGroupIDs  []string
	PrivateDNSEnabled bool
	Tags              map[string]string
}

// Associations is the service-network association surface (VPC, service, and
// resource-configuration associations).
type Associations interface {
	CreateSNVpcAssociation(ctx context.Context, in *CreateSNVpcAssociationInput) (*SNVpcAssociation, error)
	GetSNVpcAssociation(ctx context.Context, id string) (*SNVpcAssociation, error)
	UpdateSNVpcAssociation(ctx context.Context, id string, securityGroupIDs []string) (*SNVpcAssociation, error)
	DeleteSNVpcAssociation(ctx context.Context, id string) error
	ListSNVpcAssociations(ctx context.Context) ([]SNVpcAssociation, error)
	ListSNVpcEndpointAssociations(ctx context.Context, serviceNetworkID string) ([]SNVpcAssociation, error)

	CreateSNServiceAssociation(ctx context.Context, serviceNetworkID, serviceID string, tags map[string]string) (*SNServiceAssociation, error)
	GetSNServiceAssociation(ctx context.Context, id string) (*SNServiceAssociation, error)
	DeleteSNServiceAssociation(ctx context.Context, id string) error
	ListSNServiceAssociations(ctx context.Context) ([]SNServiceAssociation, error)

	CreateSNResourceAssociation(
		ctx context.Context, serviceNetworkID, resourceConfigID string, privateDNS bool, tags map[string]string,
	) (*SNResourceAssociation, error)
	GetSNResourceAssociation(ctx context.Context, id string) (*SNResourceAssociation, error)
	DeleteSNResourceAssociation(ctx context.Context, id string) error
	ListSNResourceAssociations(ctx context.Context) ([]SNResourceAssociation, error)
}

// ---- Resource Configurations -----------------------------------------------

// ResourceConfiguration is a VPC Lattice resource configuration. Definition is
// stored as raw JSON (a union) and echoed back verbatim.
type ResourceConfiguration struct {
	ID                       string
	ARN                      string
	Name                     string
	Type                     string
	Status                   string
	Protocol                 string
	CustomDomainName         string
	GroupDomain              string
	PortRanges               []string
	Definition               []byte
	ResourceGatewayID        string
	ResourceConfigGroupID    string
	AllowAssociationToShared bool
	CreatedAt                string
	LastUpdatedAt            string
}

// CreateResourceConfigurationInput carries the create request fields.
type CreateResourceConfigurationInput struct {
	Name                     string
	Type                     string
	Protocol                 string
	CustomDomainName         string
	GroupDomain              string
	PortRanges               []string
	Definition               []byte
	ResourceGatewayID        string
	ResourceConfigGroupID    string
	AllowAssociationToShared bool
	Tags                     map[string]string
}

// UpdateResourceConfigurationInput carries the mutable fields. A nil pointer
// means "absent, leave unchanged" so a partial update never resets a
// previously-true value.
type UpdateResourceConfigurationInput struct {
	ID                       string
	PortRanges               []string
	Definition               []byte
	AllowAssociationToShared *bool
}

// ResourceConfigurations is the resource-configuration resource group.
type ResourceConfigurations interface {
	CreateResourceConfiguration(ctx context.Context, in *CreateResourceConfigurationInput) (*ResourceConfiguration, error)
	GetResourceConfiguration(ctx context.Context, id string) (*ResourceConfiguration, error)
	UpdateResourceConfiguration(ctx context.Context, in *UpdateResourceConfigurationInput) (*ResourceConfiguration, error)
	DeleteResourceConfiguration(ctx context.Context, id string) error
	ListResourceConfigurations(ctx context.Context) ([]ResourceConfiguration, error)
}

// ---- Resource Gateways -----------------------------------------------------

// ResourceGateway is a VPC Lattice resource gateway.
type ResourceGateway struct {
	ID                          string
	ARN                         string
	Name                        string
	Status                      string
	IPAddressType               string
	Ipv4AddressesPerEni         int32
	ResourceConfigDNSResolution string
	SecurityGroupIDs            []string
	SubnetIDs                   []string
	VpcID                       string
	CreatedAt                   string
	LastUpdatedAt               string
}

// CreateResourceGatewayInput carries the create request fields.
type CreateResourceGatewayInput struct {
	Name                        string
	IPAddressType               string
	Ipv4AddressesPerEni         int32
	ResourceConfigDNSResolution string
	SecurityGroupIDs            []string
	SubnetIDs                   []string
	VpcID                       string
	Tags                        map[string]string
}

// ResourceGateways is the resource-gateway resource group.
type ResourceGateways interface {
	CreateResourceGateway(ctx context.Context, in *CreateResourceGatewayInput) (*ResourceGateway, error)
	GetResourceGateway(ctx context.Context, id string) (*ResourceGateway, error)
	UpdateResourceGateway(ctx context.Context, id string, securityGroupIDs []string) (*ResourceGateway, error)
	DeleteResourceGateway(ctx context.Context, id string) error
	ListResourceGateways(ctx context.Context) ([]ResourceGateway, error)
}

// ---- Resource Endpoint Associations ----------------------------------------

// ResourceEndpointAssociation associates a VPC endpoint with a resource
// configuration. The mock does not synthesize these (they are created out of
// band by VPC endpoint services), so the list is empty and delete is a no-op
// resolving to NotFound.
type ResourceEndpointAssociation struct {
	ID  string
	ARN string
}

// ResourceEndpointAssociations is the resource-endpoint-association group.
type ResourceEndpointAssociations interface {
	ListResourceEndpointAssociations(ctx context.Context) ([]ResourceEndpointAssociation, error)
	DeleteResourceEndpointAssociation(ctx context.Context, id string) error
}

// ---- Access-Log Subscriptions ----------------------------------------------

// AccessLogSubscription streams access logs for a service network or service.
type AccessLogSubscription struct {
	ID                    string
	ARN                   string
	DestinationARN        string
	ResourceID            string
	ResourceARN           string
	ServiceNetworkLogType string
	CreatedAt             string
	LastUpdatedAt         string
}

// AccessLogSubscriptions is the access-log-subscription resource group.
type AccessLogSubscriptions interface {
	CreateAccessLogSubscription(
		ctx context.Context, resourceIdentifier, destinationARN, logType string, tags map[string]string,
	) (*AccessLogSubscription, error)
	GetAccessLogSubscription(ctx context.Context, id string) (*AccessLogSubscription, error)
	UpdateAccessLogSubscription(ctx context.Context, id, destinationARN string) (*AccessLogSubscription, error)
	DeleteAccessLogSubscription(ctx context.Context, id string) error
	ListAccessLogSubscriptions(ctx context.Context) ([]AccessLogSubscription, error)
}

// ---- Auth & Resource Policies ----------------------------------------------

// AuthPolicy is the IAM auth policy attached to a service network or service.
type AuthPolicy struct {
	Policy        string
	State         string
	CreatedAt     string
	LastUpdatedAt string
}

// Policies is the auth-policy and resource-policy surface.
type Policies interface {
	PutAuthPolicy(ctx context.Context, resourceID, policy string) (*AuthPolicy, error)
	GetAuthPolicy(ctx context.Context, resourceID string) (*AuthPolicy, error)
	DeleteAuthPolicy(ctx context.Context, resourceID string) error
	PutResourcePolicy(ctx context.Context, resourceARN, policy string) error
	GetResourcePolicy(ctx context.Context, resourceARN string) (string, error)
	DeleteResourcePolicy(ctx context.Context, resourceARN string) error
}

// ---- Domain Verifications --------------------------------------------------

// DomainVerification tracks ownership verification of a custom domain.
type DomainVerification struct {
	ID         string
	ARN        string
	DomainName string
	Status     string
	CreatedAt  string
}

// DomainVerifications is the domain-verification resource group.
type DomainVerifications interface {
	StartDomainVerification(ctx context.Context, domainName string, tags map[string]string) (*DomainVerification, error)
	GetDomainVerification(ctx context.Context, id string) (*DomainVerification, error)
	DeleteDomainVerification(ctx context.Context, id string) error
	ListDomainVerifications(ctx context.Context) ([]DomainVerification, error)
}

// ---- Tagging ---------------------------------------------------------------

// Tagging is the resource-tagging surface (ARN-addressed).
type Tagging interface {
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
}
