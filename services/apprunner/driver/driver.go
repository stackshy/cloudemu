package driver

import (
	"context"
	"encoding/json"
)

// AppRunner is the interface an App Runner backend implements, covering
// services, auto scaling configurations, connections, observability
// configurations, VPC connectors, VPC ingress connections, custom domains,
// per-service operations, and tags.
type AppRunner interface {
	// Service.
	CreateService(ctx context.Context, in CreateServiceInput) (*ServiceResult, error)
	DescribeService(ctx context.Context, arn string) (*Service, error)
	DeleteService(ctx context.Context, arn string) (*ServiceResult, error)
	UpdateService(ctx context.Context, in UpdateServiceInput) (*ServiceResult, error)
	ListServices(ctx context.Context, nextToken string, maxResults int32) ([]Service, string, error)
	PauseService(ctx context.Context, arn string) (*ServiceResult, error)
	ResumeService(ctx context.Context, arn string) (*ServiceResult, error)
	StartDeployment(ctx context.Context, arn string) (operationID string, err error)

	// AutoScalingConfiguration.
	CreateAutoScalingConfiguration(ctx context.Context, name string, maxConcurrency, maxSize, minSize int32,
	) (*AutoScalingConfiguration, error)
	DescribeAutoScalingConfiguration(ctx context.Context, arn string) (*AutoScalingConfiguration, error)
	DeleteAutoScalingConfiguration(ctx context.Context, arn string, deleteAllRevisions bool,
	) (*AutoScalingConfiguration, error)
	ListAutoScalingConfigurations(ctx context.Context, name string, latestOnly bool, nextToken string, maxResults int32,
	) ([]AutoScalingConfiguration, string, error)
	UpdateDefaultAutoScalingConfiguration(ctx context.Context, arn string) (*AutoScalingConfiguration, error)
	ListServicesForAutoScalingConfiguration(ctx context.Context, arn, nextToken string, maxResults int32,
	) ([]string, string, error)

	// Connection.
	CreateConnection(ctx context.Context, name, providerType string, tags map[string]string) (*Connection, error)
	DeleteConnection(ctx context.Context, arn string) (*Connection, error)
	ListConnections(ctx context.Context, name, nextToken string, maxResults int32) ([]Connection, string, error)

	// ObservabilityConfiguration.
	CreateObservabilityConfiguration(ctx context.Context, name string, trace json.RawMessage, tags map[string]string,
	) (*ObservabilityConfiguration, error)
	DescribeObservabilityConfiguration(ctx context.Context, arn string) (*ObservabilityConfiguration, error)
	DeleteObservabilityConfiguration(ctx context.Context, arn string) (*ObservabilityConfiguration, error)
	ListObservabilityConfigurations(ctx context.Context, name string, latestOnly bool, nextToken string, maxResults int32,
	) ([]ObservabilityConfiguration, string, error)

	// VpcConnector.
	CreateVpcConnector(ctx context.Context, name string, subnets, securityGroups []string, tags map[string]string,
	) (*VpcConnector, error)
	DescribeVpcConnector(ctx context.Context, arn string) (*VpcConnector, error)
	DeleteVpcConnector(ctx context.Context, arn string) (*VpcConnector, error)
	ListVpcConnectors(ctx context.Context, nextToken string, maxResults int32) ([]VpcConnector, string, error)

	// VpcIngressConnection.
	CreateVpcIngressConnection(ctx context.Context, name, serviceArn string, ingressVpcConfig json.RawMessage,
		tags map[string]string) (*VpcIngressConnection, error)
	DescribeVpcIngressConnection(ctx context.Context, arn string) (*VpcIngressConnection, error)
	DeleteVpcIngressConnection(ctx context.Context, arn string) (*VpcIngressConnection, error)
	ListVpcIngressConnections(ctx context.Context, serviceArn, vpcEndpointID, nextToken string, maxResults int32,
	) ([]VpcIngressConnection, string, error)
	UpdateVpcIngressConnection(ctx context.Context, arn string, ingressVpcConfig json.RawMessage,
	) (*VpcIngressConnection, error)

	// CustomDomain. The dnsTarget is the App Runner subdomain the
	// custom domain maps to (the service's own URL).
	AssociateCustomDomain(ctx context.Context, serviceArn, domainName string, enableWWW bool,
	) (cd *CustomDomain, dnsTarget string, err error)
	DisassociateCustomDomain(ctx context.Context, serviceArn, domainName string,
	) (cd *CustomDomain, dnsTarget string, err error)
	DescribeCustomDomains(ctx context.Context, serviceArn, nextToken string, maxResults int32,
	) (domains []CustomDomain, dnsTarget, token string, err error)

	// Operations.
	ListOperations(ctx context.Context, serviceArn, nextToken string, maxResults int32,
	) ([]OperationSummary, string, error)

	// Tags.
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
}
