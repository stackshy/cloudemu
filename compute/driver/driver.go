// Package driver defines the interface for compute service implementations.
package driver

import "context"

// InstanceConfig describes a virtual machine instance to create.
type InstanceConfig struct {
	ImageID      string
	InstanceType string
	Tags         map[string]string
	SubnetID     string
	SecurityGroups []string
	KeyName      string
	UserData     string
}

// Instance describes a running virtual machine.
type Instance struct {
	ID            string
	ImageID       string
	InstanceType  string
	State         string
	PrivateIP     string
	PublicIP      string
	SubnetID      string
	VPCID         string
	SecurityGroups []string
	Tags          map[string]string
	LaunchTime    string
}

// ModifyInstanceInput holds modifiable instance attributes.
type ModifyInstanceInput struct {
	InstanceType string
	Tags         map[string]string
}

// DescribeFilter is a filter for describing instances.
type DescribeFilter struct {
	Name   string
	Values []string
}

// Compute is the interface that compute provider implementations must satisfy.
type Compute interface {
	RunInstances(ctx context.Context, config InstanceConfig, count int) ([]Instance, error)
	StartInstances(ctx context.Context, instanceIDs []string) error
	StopInstances(ctx context.Context, instanceIDs []string) error
	RebootInstances(ctx context.Context, instanceIDs []string) error
	TerminateInstances(ctx context.Context, instanceIDs []string) error
	DescribeInstances(ctx context.Context, instanceIDs []string, filters []DescribeFilter) ([]Instance, error)
	ModifyInstance(ctx context.Context, instanceID string, input ModifyInstanceInput) error
}
