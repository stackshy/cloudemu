// Package driver defines the portable interface for AWS Network Firewall, a
// managed stateful/stateless firewall. It is a distinct service (its own
// AWS JSON API), separate from the EC2 VPC networking surface.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Firewall is a Network Firewall attached to a VPC across subnets, or to a
// transit gateway across Availability Zones.
//
// UpdateToken is the optimistic-locking token of the firewall: it is minted at
// create and rotated by every mutation. NumberOfAssociations is computed (the
// firewall's VPC endpoint associations).
type Firewall struct {
	Name                             string
	ARN                              string
	ID                               string
	PolicyARN                        string
	VPCID                            string
	SubnetIDs                        []string
	SubnetDetails                    map[string]SubnetDetail
	AvailabilityZones                []string
	ZoneEndpoints                    map[string]string
	TransitGatewayID                 string
	Description                      string
	DeleteProtection                 bool
	SubnetChangeProtection           bool
	FirewallPolicyChangeProtection   bool
	AvailabilityZoneChangeProtection bool
	EnabledAnalysisTypes             []string
	NumberOfAssociations             int
	Status                           string
	UpdateToken                      string
	Tags                             map[string]string
}

// SubnetMapping names a subnet to attach a firewall to, with the optional IP
// address type AWS lets a caller pick per subnet.
type SubnetMapping struct {
	SubnetID      string
	IPAddressType string
}

// SubnetDetail is what the firewall records for each attached subnet: its IP
// address type, the Availability Zone it sits in and that zone's endpoint id.
// The zone and endpoint are assigned when the subnet is attached and never move
// while it stays attached.
type SubnetDetail struct {
	IPAddressType    string
	AvailabilityZone string
	EndpointID       string
}

// CreateFirewallConfig is the input to CreateFirewall. SubnetIPTypes optionally
// maps a subnet id in SubnetIDs to its IP address type.
type CreateFirewallConfig struct {
	Name                             string
	PolicyARN                        string
	VPCID                            string
	SubnetIDs                        []string
	SubnetIPTypes                    map[string]string
	AvailabilityZones                []string
	TransitGatewayID                 string
	Description                      string
	DeleteProtection                 bool
	SubnetChangeProtection           bool
	FirewallPolicyChangeProtection   bool
	AvailabilityZoneChangeProtection bool
	EnabledAnalysisTypes             []string
	Tags                             map[string]string
}

// RuleGroupReference links a firewall policy to a rule group, by ARN. Priority
// orders stateless rule groups (required by AWS) and, for STRICT_ORDER
// stateful policies, stateful rule groups too. Override and
// DeepThreatInspection are stateful-only settings kept byte-faithfully.
type RuleGroupReference struct {
	ResourceARN          string
	Priority             int
	Override             json.RawMessage
	DeepThreatInspection *bool
}

// PolicyExtra holds the firewall-policy settings the emulator stores and
// returns verbatim without interpreting them.
type PolicyExtra struct {
	StatelessCustomActions  json.RawMessage
	PolicyVariables         json.RawMessage
	StatefulEngineOptions   json.RawMessage
	EnableTLSSessionHolding *bool
}

// FirewallPolicy groups stateless/stateful rule-group references + default
// actions. The Consumed* capacities, NumberOfAssociations, Status and
// LastModified are computed by the provider.
type FirewallPolicy struct {
	Name                            string
	ARN                             string
	ID                              string
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	StatefulDefaultActions          []string
	StatefulRuleGroupReferences     []RuleGroupReference
	StatelessRuleGroupReferences    []RuleGroupReference
	TLSInspectionConfigurationARN   string
	Extra                           PolicyExtra
	Status                          string
	NumberOfAssociations            int
	ConsumedStatelessRuleCapacity   int
	ConsumedStatefulRuleCapacity    int
	ConsumedStatefulDomainCapacity  int
	LastModified                    time.Time
	UpdateToken                     string
	Tags                            map[string]string
}

// CreateFirewallPolicyConfig is the input to CreateFirewallPolicy.
type CreateFirewallPolicyConfig struct {
	Name                            string
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	StatefulDefaultActions          []string
	StatefulRuleGroupReferences     []RuleGroupReference
	StatelessRuleGroupReferences    []RuleGroupReference
	TLSInspectionConfigurationARN   string
	Extra                           PolicyExtra
	Tags                            map[string]string
}

// UpdateFirewallPolicyConfig is the input to UpdateFirewallPolicy. UpdateToken
// is required (AWS marks it required on this operation).
type UpdateFirewallPolicyConfig struct {
	UpdateToken                     string
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	StatefulDefaultActions          []string
	StatefulRuleGroupReferences     []RuleGroupReference
	StatelessRuleGroupReferences    []RuleGroupReference
	TLSInspectionConfigurationARN   string
	Extra                           PolicyExtra
}

// RuleGroup is a reusable collection of stateful or stateless rules.
//
// Rules holds the full "RuleGroup" payload (RulesSource, RuleVariables,
// ReferenceSets, StatefulRuleOptions) exactly as the caller provided it. The
// emulator doesn't interpret Suricata/stateful/stateless rule content beyond
// computing the capacity it consumes and the rule summaries, so it is stored
// and returned byte-faithfully rather than modeled field-by-field.
type RuleGroup struct {
	Name                 string
	ARN                  string
	ID                   string
	Type                 string // STATEFUL | STATELESS | STATEFUL_DOMAIN
	Capacity             int
	ConsumedCapacity     int
	NumberOfAssociations int
	Description          string
	Rules                json.RawMessage
	SummaryRuleOptions   []string
	Status               string
	LastModified         time.Time
	UpdateToken          string
	Tags                 map[string]string
}

// CreateRuleGroupConfig is the input to CreateRuleGroup.
type CreateRuleGroupConfig struct {
	Name               string
	Type               string
	Capacity           int
	Description        string
	Rules              json.RawMessage
	SummaryRuleOptions []string
	Tags               map[string]string
}

// UpdateRuleGroupConfig is the input to UpdateRuleGroup. Capacity is immutable
// in real Network Firewall. Rules is left nil when the caller didn't send a
// RuleGroup/Rules payload, so an update that only changes the description
// doesn't wipe existing rule content. UpdateToken is required.
type UpdateRuleGroupConfig struct {
	UpdateToken        string
	Description        string
	Rules              json.RawMessage
	SummaryRuleOptions []string
}

// LogDestinationConfig configures where one log type (ALERT/FLOW/TLS) is
// delivered. LogDestination holds the destination-specific key/value pairs
// (e.g. bucketName for S3, logGroup for CloudWatch Logs).
type LogDestinationConfig struct {
	LogType            string
	LogDestinationType string
	LogDestination     map[string]string
}

// LoggingConfiguration is a firewall's log destinations plus the monitoring
// dashboard switch. A nil EnableMonitoringDashboard on update leaves the stored
// switch unchanged.
type LoggingConfiguration struct {
	Destinations              []LogDestinationConfig
	EnableMonitoringDashboard *bool
}

// ListOptions carries MaxResults/NextToken for a list operation. A zero
// MaxResults means "the service default" (100).
type ListOptions struct {
	MaxResults int
	NextToken  string
}

// RuleGroupFilter narrows ListRuleGroups. Scope (MANAGED | ACCOUNT) and
// ManagedType select AWS-managed rule groups, which the emulator does not have,
// so MANAGED or a ManagedType yields an empty list.
type RuleGroupFilter struct {
	Type        string
	Scope       string
	ManagedType string
}

// NetworkFirewall is the AWS Network Firewall control plane (the operations
// every driver implements). Further operation families are optional
// capability interfaces in capabilities.go.
//
// Mutations take the update token the caller last read ("" = unconditional,
// permitted only where AWS marks the token optional): a stale token fails with
// InvalidToken.
//
//nolint:interfacebloat // mirrors the network-firewall API surface.
type NetworkFirewall interface {
	CreateFirewall(ctx context.Context, cfg CreateFirewallConfig) (*Firewall, error)
	DescribeFirewall(ctx context.Context, name, arn string) (*Firewall, error)
	DeleteFirewall(ctx context.Context, name, arn string) (*Firewall, error)
	ListFirewalls(ctx context.Context, vpcIDs []string, opts ListOptions) ([]Firewall, string, error)

	CreateFirewallPolicy(ctx context.Context, cfg CreateFirewallPolicyConfig) (*FirewallPolicy, error)
	DescribeFirewallPolicy(ctx context.Context, name, arn string) (*FirewallPolicy, error)
	UpdateFirewallPolicy(ctx context.Context, name, arn string, cfg UpdateFirewallPolicyConfig) (*FirewallPolicy, error)
	DeleteFirewallPolicy(ctx context.Context, name, arn string) (*FirewallPolicy, error)
	ListFirewallPolicies(ctx context.Context, opts ListOptions) ([]FirewallPolicy, string, error)

	CreateRuleGroup(ctx context.Context, cfg CreateRuleGroupConfig) (*RuleGroup, error)
	DescribeRuleGroup(ctx context.Context, name, arn, ruleType string) (*RuleGroup, error)
	UpdateRuleGroup(ctx context.Context, name, arn, ruleType string, cfg UpdateRuleGroupConfig) (*RuleGroup, error)
	DeleteRuleGroup(ctx context.Context, name, arn, ruleType string) (*RuleGroup, error)
	ListRuleGroups(ctx context.Context, filter RuleGroupFilter, opts ListOptions) ([]RuleGroup, string, error)

	AssociateFirewallPolicy(ctx context.Context, firewallName, policyARN, updateToken string) (*Firewall, error)
	AssociateSubnets(ctx context.Context, firewallName string, subnets []SubnetMapping, updateToken string) (*Firewall, error)
	DisassociateSubnets(ctx context.Context, firewallName string, subnetIDs []string, updateToken string) (*Firewall, error)
	UpdateFirewallDeleteProtection(ctx context.Context, firewallName string, enabled bool, updateToken string) (*Firewall, error)
	UpdateLoggingConfiguration(ctx context.Context, firewallName string, cfg *LoggingConfiguration) error
	DescribeLoggingConfiguration(ctx context.Context, firewallName string) (*LoggingConfiguration, error)
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
}
