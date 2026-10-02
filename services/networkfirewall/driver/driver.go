// Package driver defines the portable interface for AWS Network Firewall, a
// managed stateful/stateless firewall. It is a distinct service (its own
// AWS JSON API), separate from the EC2 VPC networking surface.
package driver

import (
	"context"
	"encoding/json"
)

// Firewall is a Network Firewall attached to a VPC across subnets.
type Firewall struct {
	Name             string
	ARN              string
	ID               string
	PolicyARN        string
	VPCID            string
	SubnetIDs        []string
	Description      string
	DeleteProtection bool
	Status           string
	Tags             map[string]string
}

// CreateFirewallConfig is the input to CreateFirewall.
type CreateFirewallConfig struct {
	Name             string
	PolicyARN        string
	VPCID            string
	SubnetIDs        []string
	Description      string
	DeleteProtection bool
	Tags             map[string]string
}

// RuleGroupReference links a firewall policy to a rule group, by ARN. Priority
// orders stateless rule groups (required by AWS) and, for STRICT_ORDER
// stateful policies, stateful rule groups too.
type RuleGroupReference struct {
	ResourceARN string
	Priority    int
}

// FirewallPolicy groups stateless/stateful rule-group references + default actions.
type FirewallPolicy struct {
	Name                            string
	ARN                             string
	ID                              string
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	StatefulRuleGroupReferences     []RuleGroupReference
	StatelessRuleGroupReferences    []RuleGroupReference
	Tags                            map[string]string
}

// CreateFirewallPolicyConfig is the input to CreateFirewallPolicy.
type CreateFirewallPolicyConfig struct {
	Name                            string
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	StatefulRuleGroupReferences     []RuleGroupReference
	StatelessRuleGroupReferences    []RuleGroupReference
	Tags                            map[string]string
}

// RuleGroup is a reusable collection of stateful or stateless rules.
//
// Rules holds the full "RuleGroup" payload (RulesSource, RuleVariables,
// ReferenceSets, StatefulRuleOptions) exactly as the caller provided it. The
// emulator doesn't interpret Suricata/stateful/stateless rule content, so it
// is stored and returned byte-faithfully rather than modeled field-by-field.
type RuleGroup struct {
	Name        string
	ARN         string
	ID          string
	Type        string // STATEFUL | STATELESS
	Capacity    int
	Description string
	Rules       json.RawMessage
	Tags        map[string]string
}

// CreateRuleGroupConfig is the input to CreateRuleGroup.
type CreateRuleGroupConfig struct {
	Name        string
	Type        string
	Capacity    int
	Description string
	Rules       json.RawMessage
	Tags        map[string]string
}

// UpdateFirewallPolicyConfig is the input to UpdateFirewallPolicy.
type UpdateFirewallPolicyConfig struct {
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	StatefulRuleGroupReferences     []RuleGroupReference
	StatelessRuleGroupReferences    []RuleGroupReference
}

// UpdateRuleGroupConfig is the input to UpdateRuleGroup. Capacity is immutable
// in real Network Firewall. Rules is left nil when the caller didn't send a
// RuleGroup/Rules payload, so an update that only changes the description
// doesn't wipe existing rule content.
type UpdateRuleGroupConfig struct {
	Description string
	Rules       json.RawMessage
}

// LogDestinationConfig configures where one log type (ALERT/FLOW/TLS) is
// delivered. LogDestination holds the destination-specific key/value pairs
// (e.g. bucketName for S3, logGroup for CloudWatch Logs).
type LogDestinationConfig struct {
	LogType            string
	LogDestinationType string
	LogDestination     map[string]string
}

// NetworkFirewall is the AWS Network Firewall control plane.
//
//nolint:interfacebloat // mirrors the network-firewall API surface.
type NetworkFirewall interface {
	CreateFirewall(ctx context.Context, cfg CreateFirewallConfig) (*Firewall, error)
	DescribeFirewall(ctx context.Context, name, arn string) (*Firewall, error)
	DeleteFirewall(ctx context.Context, name, arn string) (*Firewall, error)
	ListFirewalls(ctx context.Context) ([]Firewall, error)

	CreateFirewallPolicy(ctx context.Context, cfg CreateFirewallPolicyConfig) (*FirewallPolicy, error)
	DescribeFirewallPolicy(ctx context.Context, name, arn string) (*FirewallPolicy, error)
	UpdateFirewallPolicy(ctx context.Context, name, arn string, cfg UpdateFirewallPolicyConfig) (*FirewallPolicy, error)
	DeleteFirewallPolicy(ctx context.Context, name, arn string) (*FirewallPolicy, error)
	ListFirewallPolicies(ctx context.Context) ([]FirewallPolicy, error)

	CreateRuleGroup(ctx context.Context, cfg CreateRuleGroupConfig) (*RuleGroup, error)
	DescribeRuleGroup(ctx context.Context, name, arn, ruleType string) (*RuleGroup, error)
	UpdateRuleGroup(ctx context.Context, name, arn, ruleType string, cfg UpdateRuleGroupConfig) (*RuleGroup, error)
	DeleteRuleGroup(ctx context.Context, name, arn, ruleType string) (*RuleGroup, error)
	ListRuleGroups(ctx context.Context) ([]RuleGroup, error)

	AssociateFirewallPolicy(ctx context.Context, firewallName, policyARN string) (*Firewall, error)
	AssociateSubnets(ctx context.Context, firewallName string, subnetIDs []string) (*Firewall, error)
	DisassociateSubnets(ctx context.Context, firewallName string, subnetIDs []string) (*Firewall, error)
	UpdateFirewallDeleteProtection(ctx context.Context, firewallName string, enabled bool) (*Firewall, error)
	UpdateLoggingConfiguration(ctx context.Context, firewallName string, configs []LogDestinationConfig) error
	DescribeLoggingConfiguration(ctx context.Context, firewallName string) ([]LogDestinationConfig, error)
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, keys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
}
