package driver

import (
	"context"
	"time"
)

// VPC ingress connection statuses.
const (
	IngressStatusAvailable       = "AVAILABLE"
	IngressStatusPendingCreation = "PENDING_CREATION"
	IngressStatusPendingUpdate   = "PENDING_UPDATE"
	IngressStatusPendingDeletion = "PENDING_DELETION"
	IngressStatusDeleted         = "DELETED"
)

// Custom domain association statuses. A new association waits for the customer to
// add the DNS records, so it ends in PENDING_CERTIFICATE_DNS_VALIDATION: the
// emulator performs no DNS validation and never reaches ACTIVE by itself.
const (
	DomainStatusCreating                      = "CREATING"
	DomainStatusPendingCertificateDNSValidate = "PENDING_CERTIFICATE_DNS_VALIDATION"
	DomainStatusDeleting                      = "DELETING"
)

// IngressVpcConfiguration is the VPC and the PrivateLink VPC endpoint a VPC
// ingress connection joins to a service.
type IngressVpcConfiguration struct {
	VpcID         string
	VpcEndpointID string
}

// VpcIngressConnection makes a private service reachable from a VPC endpoint.
type VpcIngressConnection struct {
	VpcIngressConnectionName string
	VpcIngressConnectionArn  string
	ServiceArn               string
	AccountID                string
	DomainName               string
	Status                   string
	IngressVpcConfiguration  IngressVpcConfiguration
	CreatedAt                time.Time
	DeletedAt                time.Time
	Tags                     []Tag
}

// CreateVpcIngressConnectionInput is the input to CreateVpcIngressConnection.
type CreateVpcIngressConnectionInput struct {
	VpcIngressConnectionName string
	ServiceArn               string
	IngressVpcConfiguration  IngressVpcConfiguration
	Tags                     []Tag
}

// ListVpcIngressConnectionsFilter narrows ListVpcIngressConnections.
type ListVpcIngressConnectionsFilter struct {
	ServiceArn    string
	VpcEndpointID string
}

// VpcIngressConnectionSummary is a ListVpcIngressConnections entry.
type VpcIngressConnectionSummary struct {
	VpcIngressConnectionArn string
	ServiceArn              string
}

// VpcIngressConnections is the optional VPC ingress connection capability.
type VpcIngressConnections interface {
	CreateVpcIngressConnection(ctx context.Context, in *CreateVpcIngressConnectionInput) (*VpcIngressConnection, error)
	DescribeVpcIngressConnection(ctx context.Context, arn string) (*VpcIngressConnection, error)
	UpdateVpcIngressConnection(ctx context.Context, arn string, cfg IngressVpcConfiguration) (*VpcIngressConnection, error)
	DeleteVpcIngressConnection(ctx context.Context, arn string) (*VpcIngressConnection, error)
	ListVpcIngressConnections(ctx context.Context, filter ListVpcIngressConnectionsFilter, page Page) (
		items []VpcIngressConnectionSummary, nextToken string, err error)
}

// CertificateValidationRecord is a CNAME record the customer adds to validate a
// custom domain.
type CertificateValidationRecord struct {
	Name   string
	Type   string
	Value  string
	Status string
}

// CustomDomain is a custom domain associated with a service.
type CustomDomain struct {
	DomainName                   string
	EnableWWWSubdomain           bool
	Status                       string
	CertificateValidationRecords []CertificateValidationRecord
}

// VpcDNSTarget maps a VPC ingress connection's domain to the VPC it serves.
type VpcDNSTarget struct {
	DomainName              string
	VpcID                   string
	VpcIngressConnectionArn string
}

// CustomDomainResult is the AssociateCustomDomain / DisassociateCustomDomain /
// DescribeCustomDomains result.
type CustomDomainResult struct {
	ServiceArn    string
	DNSTarget     string
	CustomDomain  *CustomDomain
	CustomDomains []CustomDomain
	VpcDNSTargets []VpcDNSTarget
}

// CustomDomains is the optional custom domain capability.
type CustomDomains interface {
	AssociateCustomDomain(ctx context.Context, serviceArn, domainName string, enableWWW *bool) (*CustomDomainResult, error)
	DisassociateCustomDomain(ctx context.Context, serviceArn, domainName string) (*CustomDomainResult, error)
	DescribeCustomDomains(ctx context.Context, serviceArn string, page Page) (res *CustomDomainResult, nextToken string, err error)
}

// DefaultAutoScaling is the optional capability to move the account's default auto
// scaling configuration and to see which services use a configuration.
type DefaultAutoScaling interface {
	UpdateDefaultAutoScalingConfiguration(ctx context.Context, arn string) (*AutoScalingConfiguration, error)
	ListServicesForAutoScalingConfiguration(ctx context.Context, arn string, page Page) (
		serviceArns []string, nextToken string, err error)
}
