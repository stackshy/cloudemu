// Package driver defines the portable interface for AWS Network Firewall, a
// managed stateful/stateless firewall. It is a distinct service (its own
// AWS JSON API), separate from the EC2 VPC networking surface.
package driver

import "context"

// Firewall is a Network Firewall attached to a VPC across subnets.
type Firewall struct {
	Name             string
	ARN              string
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

// FirewallPolicy groups stateless/stateful rule-group references + default actions.
type FirewallPolicy struct {
	Name                            string
	ARN                             string
	ID                              string
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	Tags                            map[string]string
}

// CreateFirewallPolicyConfig is the input to CreateFirewallPolicy.
type CreateFirewallPolicyConfig struct {
	Name                            string
	Description                     string
	StatelessDefaultActions         []string
	StatelessFragmentDefaultActions []string
	Tags                            map[string]string
}

// RuleGroup is a reusable collection of stateful or stateless rules.
type RuleGroup struct {
	Name        string
	ARN         string
	ID          string
	Type        string // STATEFUL | STATELESS
	Capacity    int
	Description string
	Tags        map[string]string
}

// CreateRuleGroupConfig is the input to CreateRuleGroup.
type CreateRuleGroupConfig struct {
	Name        string
	Type        string
	Capacity    int
	Description string
	Tags        map[string]string
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
	DeleteFirewallPolicy(ctx context.Context, name, arn string) (*FirewallPolicy, error)
	ListFirewallPolicies(ctx context.Context) ([]FirewallPolicy, error)

	CreateRuleGroup(ctx context.Context, cfg CreateRuleGroupConfig) (*RuleGroup, error)
	DescribeRuleGroup(ctx context.Context, name, arn, ruleType string) (*RuleGroup, error)
	DeleteRuleGroup(ctx context.Context, name, arn, ruleType string) (*RuleGroup, error)
	ListRuleGroups(ctx context.Context) ([]RuleGroup, error)
}
