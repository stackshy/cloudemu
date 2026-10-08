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
