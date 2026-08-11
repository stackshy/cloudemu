// Package driver defines the interface and types for AWS App Runner
// implementations. It models App Runner services (with their source,
// instance, network, and observability configuration), auto scaling
// configurations (with revision management), connections, observability
// configurations, VPC connectors, VPC ingress connections, custom domains,
// per-service operations, and tags.
//
// The emulator does NOT actually build or run containers: CreateService
// completes deterministically (Status immediately RUNNING) with a synthesized
// ServiceUrl, and lifecycle transitions (Pause/Resume/Delete) mutate stored
// state directly. Large, open-ended nested configuration blocks
// (SourceConfiguration, NetworkConfiguration, health-check, etc.) are carried
// verbatim as json.RawMessage so the SDK wire shapes round-trip without
// modeling every leaf field.
package driver

import (
	"encoding/json"
	"time"
)

// Service statuses (types.ServiceStatus).
const (
	ServiceStatusCreateFailed        = "CREATE_FAILED"
	ServiceStatusRunning             = "RUNNING"
	ServiceStatusDeleted             = "DELETED"
	ServiceStatusDeleteFailed        = "DELETE_FAILED"
	ServiceStatusPaused              = "PAUSED"
	ServiceStatusOperationInProgress = "OPERATION_IN_PROGRESS"
)

// Auto scaling / observability / VPC-connector statuses.
const (
	StatusActive   = "ACTIVE"
	StatusInactive = "INACTIVE"
)

// Connection statuses (types.ConnectionStatus).
const (
	ConnectionStatusPendingHandshake = "PENDING_HANDSHAKE"
	ConnectionStatusAvailable        = "AVAILABLE"
	ConnectionStatusError            = "ERROR"
	ConnectionStatusDeleted          = "DELETED"
)

// Provider types (types.ProviderType).
const (
	ProviderTypeGitHub    = "GITHUB"
	ProviderTypeBitbucket = "BITBUCKET"
)

// VPC ingress connection statuses (types.VpcIngressConnectionStatus).
const (
	VpcIngressStatusAvailable = "AVAILABLE"
	VpcIngressStatusDeleted   = "DELETED"
)

// Custom domain association statuses (types.CustomDomainAssociationStatus). The
// emulator synthesizes the DNS-validation flow: a domain is created pending
// validation and immediately settles ACTIVE.
const (
	CustomDomainStatusPendingCertValidation = "PENDING_CERTIFICATE_DNS_VALIDATION"
	CustomDomainStatusActive                = "ACTIVE"
	CustomDomainStatusDeleting              = "DELETING"
)

// CertificateValidationRecordStatus values.
const (
	CertValidationStatusSuccess = "SUCCESS"
)

// Operation types (types.OperationType).
const (
	OperationTypeStartDeployment = "START_DEPLOYMENT"
	OperationTypeCreateService   = "CREATE_SERVICE"
	OperationTypePauseService    = "PAUSE_SERVICE"
	OperationTypeResumeService   = "RESUME_SERVICE"
	OperationTypeDeleteService   = "DELETE_SERVICE"
	OperationTypeUpdateService   = "UPDATE_SERVICE"
)

// Operation statuses (types.OperationStatus).
const (
	OperationStatusPending    = "PENDING"
	OperationStatusInProgress = "IN_PROGRESS"
	OperationStatusSucceeded  = "SUCCEEDED"
	OperationStatusFailed     = "FAILED"
)

// Service is the full description of an App Runner service. Large nested
// configuration blocks are stored verbatim as json.RawMessage so the SDK wire
// shapes round-trip without modeling every leaf field.
type Service struct {
	ServiceArn                 string
	ServiceID                  string
	ServiceName                string
	ServiceURL                 string
	Status                     string
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	DeletedAt                  time.Time
	AutoScalingConfigArn       string
	AutoScalingConfigName      string
	AutoScalingConfigRevision  int32
	SourceConfiguration        json.RawMessage
	InstanceConfiguration      json.RawMessage
	NetworkConfiguration       json.RawMessage
	HealthCheckConfiguration   json.RawMessage
	EncryptionConfiguration    json.RawMessage
	ObservabilityConfiguration json.RawMessage
	Tags                       map[string]string
}

// CreateServiceInput describes a service to create.
type CreateServiceInput struct {
	ServiceName                string
	SourceConfiguration        json.RawMessage
	InstanceConfiguration      json.RawMessage
	NetworkConfiguration       json.RawMessage
	HealthCheckConfiguration   json.RawMessage
	EncryptionConfiguration    json.RawMessage
	ObservabilityConfiguration json.RawMessage
	AutoScalingConfigArn       string
	Tags                       map[string]string
}

// UpdateServiceInput describes a mutation of an existing service. Empty
// json.RawMessage / string fields leave the corresponding block unchanged.
type UpdateServiceInput struct {
	ServiceArn                 string
	SourceConfiguration        json.RawMessage
	InstanceConfiguration      json.RawMessage
	NetworkConfiguration       json.RawMessage
	HealthCheckConfiguration   json.RawMessage
	ObservabilityConfiguration json.RawMessage
	AutoScalingConfigArn       string
}

// AutoScalingConfiguration is an auto scaling configuration revision.
type AutoScalingConfiguration struct {
	Arn                  string
	Name                 string
	Revision             int32
	Status               string
	MaxConcurrency       int32
	MaxSize              int32
	MinSize              int32
	IsDefault            bool
	Latest               bool
	HasAssociatedService bool
	CreatedAt            time.Time
	DeletedAt            time.Time
	Tags                 map[string]string
}

// Connection is an App Runner source-repository connection.
type Connection struct {
	Arn          string
	Name         string
	ProviderType string
	Status       string
	CreatedAt    time.Time
	Tags         map[string]string
}

// ObservabilityConfiguration is an observability configuration revision.
type ObservabilityConfiguration struct {
	Arn                string
	Name               string
	Revision           int32
	Status             string
	Latest             bool
	TraceConfiguration json.RawMessage
	CreatedAt          time.Time
	DeletedAt          time.Time
	Tags               map[string]string
}

// VpcConnector is an App Runner VPC connector revision.
type VpcConnector struct {
	Arn            string
	Name           string
	Revision       int32
	Status         string
	Subnets        []string
	SecurityGroups []string
	CreatedAt      time.Time
	DeletedAt      time.Time
	Tags           map[string]string
}

// VpcIngressConnection is an App Runner VPC ingress connection.
type VpcIngressConnection struct {
	Arn                     string
	Name                    string
	Status                  string
	AccountID               string
	DomainName              string
	ServiceArn              string
	IngressVpcConfiguration json.RawMessage
	CreatedAt               time.Time
	DeletedAt               time.Time
	Tags                    map[string]string
}

// CustomDomain is a custom domain associated with a service.
type CustomDomain struct {
	DomainName                   string
	EnableWWWSubdomain           bool
	Status                       string
	ServiceArn                   string
	CertificateValidationRecords []CertificateValidationRecord
}

// CertificateValidationRecord is a CNAME record the customer adds to their DNS
// to validate a custom-domain certificate.
type CertificateValidationRecord struct {
	Name   string
	Type   string
	Value  string
	Status string
}

// OperationSummary summarizes an operation that occurred on a service.
type OperationSummary struct {
	ID        string
	Type      string
	Status    string
	TargetArn string
	StartedAt time.Time
	EndedAt   time.Time
	UpdatedAt time.Time
}

// ServiceResult carries a service plus the operation id a mutating op returns.
type ServiceResult struct {
	Service     *Service
	OperationID string
}
