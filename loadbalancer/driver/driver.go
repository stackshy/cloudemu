// Package driver defines the interface for load balancer service implementations.
package driver

import "context"

// LBConfig describes a load balancer to create.
type LBConfig struct {
	Name      string
	Type      string // "application", "network"
	Scheme    string // "internet-facing", "internal"
	Subnets   []string
	Tags      map[string]string
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
	Tags    map[string]string
}

// TargetGroupConfig describes a target group to create.
type TargetGroupConfig struct {
	Name       string
	Protocol   string
	Port       int
	VPCID      string
	HealthPath string
	Tags       map[string]string
}

// TargetGroupInfo describes a target group.
type TargetGroupInfo struct {
	ID         string
	ARN        string
	Name       string
	Protocol   string
	Port       int
	VPCID      string
	HealthPath string
	Tags       map[string]string
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

// Target identifies a target (e.g., instance) in a target group.
type Target struct {
	ID   string
	Port int
}

// TargetHealth describes the health status of a target.
type TargetHealth struct {
	Target Target
	State  string // "healthy", "unhealthy", "draining", "initial"
	Reason string
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

	RegisterTargets(ctx context.Context, targetGroupARN string, targets []Target) error
	DeregisterTargets(ctx context.Context, targetGroupARN string, targets []Target) error
	DescribeTargetHealth(ctx context.Context, targetGroupARN string) ([]TargetHealth, error)
	SetTargetHealth(ctx context.Context, targetGroupARN string, targetID string, state string) error
}
