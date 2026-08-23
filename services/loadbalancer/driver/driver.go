// Package driver defines the interface for load balancer service implementations.
package driver

import (
	"context"
	"time"
)

// LBConfig describes a load balancer to create.
type LBConfig struct {
	Name           string
	Type           string // "application", "network"
	Scheme         string // "internet-facing", "internal"
	Subnets        []string
	SecurityGroups []string
	IPAddressType  string // "ipv4", "dualstack"
	Tags           map[string]string
}

// LBInfo describes a load balancer.
type LBInfo struct {
	ID      string
	ARN     string
	Name    string
	Type    string
	Scheme  string
	State   string
	DNSName string
	Subnets []string
	// SecurityGroups are the security-group IDs attached to an application
	// load balancer. Network load balancers have none.
	SecurityGroups []string
	// IPAddressType is "ipv4" or "dualstack"; defaults to "ipv4".
	IPAddressType string
	// CanonicalHostedZoneID is the Route 53 hosted-zone id an alias record must
	// target to point at this load balancer. Route 53 alias records break
	// without it.
	CanonicalHostedZoneID string
	// VPCID is the VPC the load balancer's subnets belong to.
	VPCID string
	// CreatedTime is when the load balancer was created.
	CreatedTime time.Time
	Tags        map[string]string
}

// HealthCheck describes the health-check configuration of a target group. AWS
// applies protocol-derived defaults for any field left unset on create.
type HealthCheck struct {
	Protocol           string // "HTTP", "HTTPS", "TCP", ...
	Port               string // "traffic-port" or an explicit port number
	Path               string // for HTTP/HTTPS health checks
	IntervalSeconds    int
	TimeoutSeconds     int
	HealthyThreshold   int
	UnhealthyThreshold int
	Matcher            string // e.g. "200" or "200-299" for HTTP matchers
}

// TargetGroupConfig describes a target group to create.
type TargetGroupConfig struct {
	Name        string
	Protocol    string
	Port        int
	VPCID       string
	TargetType  string // "instance", "ip", "lambda", "alb"
	HealthPath  string
	HealthCheck HealthCheck
	Tags        map[string]string
}

// TargetGroupInfo describes a target group.
type TargetGroupInfo struct {
	ID          string
	ARN         string
	Name        string
	Protocol    string
	Port        int
	VPCID       string
	TargetType  string
	HealthPath  string
	HealthCheck HealthCheck
	Tags        map[string]string
}

// ModifyTargetGroupInput carries the mutable health-check fields of a target
// group. A zero/empty field means "leave unchanged".
type ModifyTargetGroupInput struct {
	TargetGroupARN     string
	HealthCheckProto   string
	HealthCheckPort    string
	HealthCheckPath    string
	IntervalSeconds    int
	TimeoutSeconds     int
	HealthyThreshold   int
	UnhealthyThreshold int
	Matcher            string
}

// ModifyRuleInput carries the mutable fields of a listener rule.
type ModifyRuleInput struct {
	RuleARN    string
	Conditions []RuleCondition
	Actions    []RuleAction
}

// RulePriorityPair reprioritizes a single rule.
type RulePriorityPair struct {
	RuleARN  string
	Priority int
}

// ListenerConfig describes a listener to create.
type ListenerConfig struct {
	LBARN          string
	Protocol       string
	Port           int
	TargetGroupARN string
}

// ListenerInfo describes a listener.
type ListenerInfo struct {
	ARN            string
	LBARN          string
	Protocol       string
	Port           int
	TargetGroupARN string
}

// RuleCondition describes a condition for a listener rule (e.g., path-pattern or host-header).
type RuleCondition struct {
	Field  string // "path-pattern" or "host-header"
	Values []string
}

// RuleAction describes an action for a listener rule (e.g., forward to a target group).
type RuleAction struct {
	Type           string // "forward"
	TargetGroupARN string
}

// RuleConfig describes a listener rule to create.
type RuleConfig struct {
	ListenerARN string
	Priority    int
	Conditions  []RuleCondition
	Actions     []RuleAction
}

// RuleInfo describes a listener rule.
type RuleInfo struct {
	ARN         string
	ListenerARN string
	Priority    int
	Conditions  []RuleCondition
	Actions     []RuleAction
	IsDefault   bool
}

// ModifyListenerInput describes modifications to apply to a listener.
type ModifyListenerInput struct {
	ListenerARN    string
	Port           int
	Protocol       string
	DefaultActions []RuleAction
}

// LBAttributes describes configurable attributes of a load balancer.
type LBAttributes struct {
	IdleTimeout        int
	DeletionProtection bool
	AccessLogsEnabled  bool
	AccessLogsBucket   string
	// Extra holds attributes outside the typed set above, keyed by their AWS
	// attribute name (load_balancing.cross_zone.enabled and friends). AWS
	// models attributes as open key/value pairs and adds new ones over time,
	// so a fixed struct silently drops whatever it has not been taught — and a
	// caller reading back its own write would get a wrong answer.
	Extra map[string]string
}

// Target identifies a target (e.g., instance) in a target group.
type Target struct {
	ID   string
	Port int
}

// TargetHealth describes the health status of a target.
type TargetHealth struct {
	Target      Target
	State       string // "healthy", "unhealthy", "draining", "initial"
	Reason      string // AWS reason code, e.g. "Elb.RegistrationInProgress"
	Description string
}

// LoadBalancer is the interface that load balancer provider implementations must satisfy.
type LoadBalancer interface {
	CreateLoadBalancer(ctx context.Context, config LBConfig) (*LBInfo, error)
	DeleteLoadBalancer(ctx context.Context, arn string) error
	DescribeLoadBalancers(ctx context.Context, arns []string) ([]LBInfo, error)

	CreateTargetGroup(ctx context.Context, config TargetGroupConfig) (*TargetGroupInfo, error)
	DeleteTargetGroup(ctx context.Context, arn string) error
	DescribeTargetGroups(ctx context.Context, arns []string) ([]TargetGroupInfo, error)

	CreateListener(ctx context.Context, config ListenerConfig) (*ListenerInfo, error)
	DeleteListener(ctx context.Context, arn string) error
	DescribeListeners(ctx context.Context, lbARN string) ([]ListenerInfo, error)

	CreateRule(ctx context.Context, config RuleConfig) (*RuleInfo, error)
	DeleteRule(ctx context.Context, ruleARN string) error
	DescribeRules(ctx context.Context, listenerARN string) ([]RuleInfo, error)

	ModifyListener(ctx context.Context, input ModifyListenerInput) error

	GetLBAttributes(ctx context.Context, lbARN string) (*LBAttributes, error)
	PutLBAttributes(ctx context.Context, lbARN string, attrs LBAttributes) error

	RegisterTargets(ctx context.Context, targetGroupARN string, targets []Target) error
	DeregisterTargets(ctx context.Context, targetGroupARN string, targets []Target) error
	DescribeTargetHealth(ctx context.Context, targetGroupARN string) ([]TargetHealth, error)
	SetTargetHealth(ctx context.Context, targetGroupARN string, targetID string, state string) error
}

// LBAttributeUpdater is implemented by drivers that can apply a partial
// attribute update atomically.
//
// Attribute modification is a read-modify-write, and doing it as a Get
// followed by a Put drops the lock in between: two overlapping updates on one
// load balancer each read the same base and the second write silently
// discards the first. A driver that can hold the lock across both halves
// implements this; callers fall back to Get/Put when it does not.
type LBAttributeUpdater interface {
	UpdateLBAttributes(
		ctx context.Context, lbARN string, apply func(*LBAttributes),
	) (*LBAttributes, error)
}

// TargetGroupModifier is implemented by drivers that can apply a partial
// health-check update to an existing target group (ELBv2 ModifyTargetGroup).
type TargetGroupModifier interface {
	ModifyTargetGroup(ctx context.Context, input ModifyTargetGroupInput) (*TargetGroupInfo, error)
}

// RuleModifier is implemented by drivers that can modify a listener rule's
// conditions/actions and reprioritize rules (ELBv2 ModifyRule /
// SetRulePriorities).
type RuleModifier interface {
	ModifyRule(ctx context.Context, input ModifyRuleInput) (*RuleInfo, error)
	SetRulePriorities(ctx context.Context, pairs []RulePriorityPair) ([]RuleInfo, error)
}

// ListenerGetter is implemented by drivers that can fetch a single listener by
// ARN, letting a handler echo the full listener after a ModifyListener call
// (the base ModifyListener returns only an error).
type ListenerGetter interface {
	GetListener(ctx context.Context, listenerARN string) (*ListenerInfo, error)
}

// LBNetworkModifier is implemented by drivers that can replace the security
// groups or subnets of an existing load balancer (ELBv2 SetSecurityGroups /
// SetSubnets). SetSubnets returns the resulting subnet list.
type LBNetworkModifier interface {
	SetSecurityGroups(ctx context.Context, lbARN string, securityGroups []string) error
	SetSubnets(ctx context.Context, lbARN string, subnets []string) ([]string, error)
}
