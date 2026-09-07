// Package driver defines the interface and types for the AWS App Runner
// control-plane API (AWS JSON 1.0, X-Amz-Target prefix "AppRunner."). It models
// App Runner services and their operations, together with the auto scaling
// configuration, connection, VPC connector and observability configuration
// resources a service composes.
//
// The emulator is control-plane only: it does NOT run any container runtime or
// data plane. A service is created synchronously into the terminal RUNNING
// state (App Runner's own create is asynchronous, transitioning through
// OPERATION_IN_PROGRESS to RUNNING; the emulator lands directly on RUNNING so an
// IaC apply completes without a provisioning wait). PauseService moves a RUNNING
// service to PAUSED, ResumeService moves a PAUSED service back to RUNNING, and
// DeleteService removes it (reporting DELETED). Illegal transitions are rejected
// with InvalidStateException, and operating on an unknown service ARN yields
// ResourceNotFoundException.
//
// The computed fields clients and IaC read back are minted once at create and
// stored, so repeated Describe/List reads never drift: a service's ServiceId
// (arn:aws:apprunner:<region>:<acct>:service/<name>/<id>), ServiceArn, ServiceURL
// (<id>.<region>.awsapprunner.com), Status, CreatedAt and UpdatedAt; the
// versioned AutoScalingConfigurationArn, VpcConnectorArn and
// ObservabilityConfigurationArn; and the ConnectionArn. The SourceConfiguration,
// InstanceConfiguration, HealthCheckConfiguration and NetworkConfiguration blocks
// round-trip verbatim. Every mutating service operation appends an Operation
// record with a stable id, so ListOperations reports the service's history.
package driver

import (
	"context"
	"time"
)

// Service lifecycle statuses App Runner reports. The emulator activates a
// service synchronously (RUNNING), so an IaC create waiter never hangs.
const (
	StatusRunning             = "RUNNING"
	StatusPaused              = "PAUSED"
	StatusDeleted             = "DELETED"
	StatusOperationInProgress = "OPERATION_IN_PROGRESS"
	StatusCreateFailed        = "CREATE_FAILED"
	StatusDeleteFailed        = "DELETE_FAILED"
)

// Operation types recorded for a service's history.
const (
	OpCreateService   = "CREATE_SERVICE"
	OpUpdateService   = "UPDATE_SERVICE"
	OpPauseService    = "PAUSE_SERVICE"
	OpResumeService   = "RESUME_SERVICE"
	OpDeleteService   = "DELETE_SERVICE"
	OpStartDeployment = "START_DEPLOYMENT"
)

// Operation statuses. The emulator completes every operation synchronously, so a
// recorded operation is SUCCEEDED at once.
const (
	OpStatusSucceeded = "SUCCEEDED"
)

// Resource statuses for the VPC connector and observability configuration
// versioned resources. A live resource is ACTIVE; a deleted one reports INACTIVE.
const (
	ResourceStatusActive   = "ACTIVE"
	ResourceStatusInactive = "INACTIVE"
)

// Auto scaling configuration statuses. Unlike the other versioned resources, the
// real App Runner API reports the auto scaling configuration status in lowercase
// ("active"/"inactive"); terraform-provider-aws matches on those exact lowercase
// values, so the emulator must emit them verbatim.
const (
	AutoScalingStatusActive   = "active"
	AutoScalingStatusInactive = "inactive"
)

// Connection statuses. A newly created connection is pending a handshake the
// customer completes out of band, matching real App Runner.
const (
	ConnectionStatusPendingHandshake = "PENDING_HANDSHAKE"
	ConnectionStatusAvailable        = "AVAILABLE"
	ConnectionStatusDeleted          = "DELETED"
)

// Tag is a resource tag (key/value pair). App Runner models tags as an array of
// {Key,Value} objects.
type Tag struct {
	Key   string
	Value string
}

// SourceCodeVersion identifies the source code version for a code repository.
type SourceCodeVersion struct {
	Type  string
	Value string
}

// CodeConfigurationValues holds the build/run settings for a code repository.
type CodeConfigurationValues struct {
	Runtime                     string
	BuildCommand                string
	StartCommand                string
	Port                        string
	RuntimeEnvironmentVariables map[string]string
	RuntimeEnvironmentSecrets   map[string]string
}

// CodeConfiguration describes how a code repository is built and run.
type CodeConfiguration struct {
	ConfigurationSource     string
	CodeConfigurationValues *CodeConfigurationValues
}

// CodeRepository is a source code repository deployed to a service.
type CodeRepository struct {
	RepositoryURL     string
	SourceCodeVersion *SourceCodeVersion
	CodeConfiguration *CodeConfiguration
	SourceDirectory   string
}

// ImageConfiguration holds the runtime settings for a source image.
type ImageConfiguration struct {
	Port                        string
	StartCommand                string
	RuntimeEnvironmentVariables map[string]string
	RuntimeEnvironmentSecrets   map[string]string
}

// ImageRepository is a source image repository deployed to a service.
type ImageRepository struct {
	ImageIdentifier     string
	ImageConfiguration  *ImageConfiguration
	ImageRepositoryType string
}

// AuthenticationConfiguration holds the credentials App Runner uses to access a
// source repository or image.
type AuthenticationConfiguration struct {
	ConnectionArn string
	AccessRoleArn string
}

// SourceConfiguration is the source deployed to a service: a code or image
// repository. It round-trips verbatim.
type SourceConfiguration struct {
	CodeRepository              *CodeRepository
	ImageRepository             *ImageRepository
	AutoDeploymentsEnabled      *bool
	AuthenticationConfiguration *AuthenticationConfiguration
}

// InstanceConfiguration is the runtime configuration of a service's instances.
type InstanceConfiguration struct {
	CPU             string
	Memory          string
	InstanceRoleArn string
}

// HealthCheckConfiguration is the health-check configuration of a service. The
// numeric members are pointers so a caller-supplied zero round-trips.
type HealthCheckConfiguration struct {
	Protocol           string
	Path               string
	Interval           *int32
	Timeout            *int32
	HealthyThreshold   *int32
	UnhealthyThreshold *int32
}

// EgressConfiguration describes a service's outbound network traffic.
type EgressConfiguration struct {
	EgressType      string
	VpcConnectorArn string
}

// IngressConfiguration describes a service's inbound network traffic.
type IngressConfiguration struct {
	IsPubliclyAccessible *bool
}

// NetworkConfiguration is a service's network configuration. It round-trips
// verbatim.
type NetworkConfiguration struct {
	EgressConfiguration  *EgressConfiguration
	IngressConfiguration *IngressConfiguration
	IPAddressType        string
}

// ServiceObservabilityConfiguration is a service's observability configuration.
type ServiceObservabilityConfiguration struct {
	ObservabilityEnabled          *bool
	ObservabilityConfigurationArn string
}

// EncryptionConfiguration is a service's encryption configuration.
type EncryptionConfiguration struct {
	KmsKey string
}

// AutoScalingConfigurationSummary is the summary of the auto scaling
// configuration associated with a service.
type AutoScalingConfigurationSummary struct {
	AutoScalingConfigurationArn      string
	AutoScalingConfigurationName     string
	AutoScalingConfigurationRevision int32
}

// Operation records a mutating action performed on a service, so ListOperations
// can report the service's history. ID is stable once minted.
type Operation struct {
	ID        string
	Type      string
	Status    string
	TargetArn string
	StartedAt time.Time
	EndedAt   time.Time
	UpdatedAt time.Time
}

// Service is an App Runner service. ServiceId, ServiceArn, ServiceURL, Status,
// CreatedAt and UpdatedAt are minted once at create (UpdatedAt advances on an
// update) and stable across reads. The configuration blocks round-trip verbatim.
// Operations holds the service's operation history, newest last.
type Service struct {
	ServiceName                     string
	ServiceID                       string
	ServiceArn                      string
	ServiceURL                      string
	Status                          string
	CreatedAt                       time.Time
	UpdatedAt                       time.Time
	DeletedAt                       time.Time
	SourceConfiguration             *SourceConfiguration
	InstanceConfiguration           *InstanceConfiguration
	HealthCheckConfiguration        *HealthCheckConfiguration
	NetworkConfiguration            *NetworkConfiguration
	ObservabilityConfiguration      *ServiceObservabilityConfiguration
	EncryptionConfiguration         *EncryptionConfiguration
	AutoScalingConfigurationSummary *AutoScalingConfigurationSummary
	Tags                            []Tag
	Operations                      []Operation
}

// AutoScalingConfiguration is a versioned App Runner auto scaling configuration.
// The Arn, Name, Revision, Status, Latest flag and CreatedAt are minted once and
// stable across reads.
type AutoScalingConfiguration struct {
	AutoScalingConfigurationArn      string
	AutoScalingConfigurationName     string
	AutoScalingConfigurationRevision int32
	Latest                           bool
	Status                           string
	MaxConcurrency                   int32
	MinSize                          int32
	MaxSize                          int32
	HasAssociatedService             bool
	IsDefault                        bool
	CreatedAt                        time.Time
	DeletedAt                        time.Time
	Tags                             []Tag
}

// Connection is an App Runner connection to a source-code provider.
type Connection struct {
	ConnectionName string
	ConnectionArn  string
	ProviderType   string
	Status         string
	CreatedAt      time.Time
	Tags           []Tag
}

// VpcConnector is a versioned App Runner VPC connector.
type VpcConnector struct {
	VpcConnectorName     string
	VpcConnectorArn      string
	VpcConnectorRevision int32
	Subnets              []string
	SecurityGroups       []string
	Status               string
	CreatedAt            time.Time
	DeletedAt            time.Time
	Tags                 []Tag
}

// TraceConfiguration is the tracing configuration of an observability
// configuration.
type TraceConfiguration struct {
	Vendor string
}

// ObservabilityConfiguration is a versioned App Runner observability
// configuration.
type ObservabilityConfiguration struct {
	ObservabilityConfigurationArn      string
	ObservabilityConfigurationName     string
	ObservabilityConfigurationRevision int32
	Latest                             bool
	Status                             string
	TraceConfiguration                 *TraceConfiguration
	CreatedAt                          time.Time
	DeletedAt                          time.Time
	Tags                               []Tag
}

// CreateServiceInput is the input to CreateService.
type CreateServiceInput struct {
	ServiceName                 string
	SourceConfiguration         *SourceConfiguration
	InstanceConfiguration       *InstanceConfiguration
	HealthCheckConfiguration    *HealthCheckConfiguration
	NetworkConfiguration        *NetworkConfiguration
	ObservabilityConfiguration  *ServiceObservabilityConfiguration
	EncryptionConfiguration     *EncryptionConfiguration
	AutoScalingConfigurationArn string
	Tags                        []Tag
}

// UpdateServiceInput is the input to UpdateService. A nil pointer member is
// absent from the request and left unchanged; a non-nil member replaces the
// stored value.
type UpdateServiceInput struct {
	ServiceArn                  string
	SourceConfiguration         *SourceConfiguration
	InstanceConfiguration       *InstanceConfiguration
	HealthCheckConfiguration    *HealthCheckConfiguration
	NetworkConfiguration        *NetworkConfiguration
	ObservabilityConfiguration  *ServiceObservabilityConfiguration
	AutoScalingConfigurationArn string
}

// ServiceResult pairs a service with the id of the operation a mutating call
// started, mirroring App Runner's {Service, OperationId} responses.
type ServiceResult struct {
	Service     *Service
	OperationID string
}

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// CreateAutoScalingConfigurationInput is the input to
// CreateAutoScalingConfiguration. Nil numeric members take the service defaults.
type CreateAutoScalingConfigurationInput struct {
	AutoScalingConfigurationName string
	MaxConcurrency               *int32
	MinSize                      *int32
	MaxSize                      *int32
	Tags                         []Tag
}

// CreateConnectionInput is the input to CreateConnection.
type CreateConnectionInput struct {
	ConnectionName string
	ProviderType   string
	Tags           []Tag
}

// CreateVpcConnectorInput is the input to CreateVpcConnector.
type CreateVpcConnectorInput struct {
	VpcConnectorName string
	Subnets          []string
	SecurityGroups   []string
	Tags             []Tag
}

// CreateObservabilityConfigurationInput is the input to
// CreateObservabilityConfiguration.
type CreateObservabilityConfigurationInput struct {
	ObservabilityConfigurationName string
	TraceConfiguration             *TraceConfiguration
	Tags                           []Tag
}

// AppRunner is the AWS App Runner control-plane surface.
type AppRunner interface {
	CreateService(ctx context.Context, in *CreateServiceInput) (*ServiceResult, error)
	DescribeService(ctx context.Context, serviceArn string) (*Service, error)
	UpdateService(ctx context.Context, in *UpdateServiceInput) (*ServiceResult, error)
	DeleteService(ctx context.Context, serviceArn string) (*ServiceResult, error)
	ListServices(ctx context.Context, page Page) (services []*Service, nextToken string, err error)

	PauseService(ctx context.Context, serviceArn string) (*ServiceResult, error)
	ResumeService(ctx context.Context, serviceArn string) (*ServiceResult, error)
	StartDeployment(ctx context.Context, serviceArn string) (operationID string, err error)
	ListOperations(ctx context.Context, serviceArn string, page Page) (ops []Operation, nextToken string, err error)

	CreateAutoScalingConfiguration(ctx context.Context, in *CreateAutoScalingConfigurationInput) (*AutoScalingConfiguration, error)
	DescribeAutoScalingConfiguration(ctx context.Context, arn string) (*AutoScalingConfiguration, error)
	DeleteAutoScalingConfiguration(ctx context.Context, arn string) (*AutoScalingConfiguration, error)
	ListAutoScalingConfigurations(ctx context.Context, name string, latestOnly bool, page Page) (
		configs []*AutoScalingConfiguration, nextToken string, err error)
	ListAutoScalingConfigurationRevisions(ctx context.Context, name string, page Page) (
		configs []*AutoScalingConfiguration, nextToken string, err error)

	CreateConnection(ctx context.Context, in *CreateConnectionInput) (*Connection, error)
	DeleteConnection(ctx context.Context, arn string) (*Connection, error)
	ListConnections(ctx context.Context, name string, page Page) (connections []*Connection, nextToken string, err error)

	CreateVpcConnector(ctx context.Context, in *CreateVpcConnectorInput) (*VpcConnector, error)
	DescribeVpcConnector(ctx context.Context, arn string) (*VpcConnector, error)
	DeleteVpcConnector(ctx context.Context, arn string) (*VpcConnector, error)
	ListVpcConnectors(ctx context.Context, page Page) (connectors []*VpcConnector, nextToken string, err error)

	CreateObservabilityConfiguration(ctx context.Context, in *CreateObservabilityConfigurationInput) (
		*ObservabilityConfiguration, error)
	DescribeObservabilityConfiguration(ctx context.Context, arn string) (*ObservabilityConfiguration, error)
	DeleteObservabilityConfiguration(ctx context.Context, arn string) (*ObservabilityConfiguration, error)
	ListObservabilityConfigurations(ctx context.Context, name string, latestOnly bool, page Page) (
		configs []*ObservabilityConfiguration, nextToken string, err error)
	ListObservabilityConfigurationRevisions(ctx context.Context, name string, page Page) (
		configs []*ObservabilityConfiguration, nextToken string, err error)

	TagResource(ctx context.Context, resourceArn string, tags []Tag) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) ([]Tag, error)
}
