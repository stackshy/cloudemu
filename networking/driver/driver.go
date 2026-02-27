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

// Networking is the interface that networking provider implementations must satisfy.
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
}
