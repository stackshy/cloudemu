// Package driver defines the interface and types for Amazon OpenSearch Service
// implementations. It models domains and their configuration, tags, engine
// versions, packages, VPC endpoints, cross-cluster inbound/outbound
// connections, data sources, applications, reserved instances, upgrades, and
// the read-only instance-type/limit catalogs.
//
// Types are plain Go (time.Time, maps, slices, nested structs). Rich
// configuration blocks the emulator does not interpret (advanced security,
// EBS, VPC, Cognito, auto-tune, etc.) are carried verbatim as
// map[string]json.RawMessage so a round-tripped domain reflects everything the
// caller sent without the driver modeling every nested shape.
package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Domain processing statuses reported via DomainProcessingStatus. The emulator
// provisions and deletes deterministically, so only the terminal Active state
// and the Deleting state a delete returns are modeled.
const (
	ProcessingActive   = "Active"
	ProcessingDeleting = "Deleting"
)

// Connection statuses for cross-cluster search.
const (
	ConnStatusPendingAcceptance = "PENDING_ACCEPTANCE"
	ConnStatusApproved          = "APPROVED"
	ConnStatusProvisioning      = "PROVISIONING"
	ConnStatusActive            = "ACTIVE"
	ConnStatusRejected          = "REJECTED"
	ConnStatusDeleting          = "DELETING"
	ConnStatusValidating        = "VALIDATING"
)

// Package statuses.
const (
	PackageStatusCopying          = "COPYING"
	PackageStatusCopyFailed       = "COPY_FAILED"
	PackageStatusValidating       = "VALIDATING"
	PackageStatusValidationFailed = "VALIDATION_FAILED"
	PackageStatusAvailable        = "AVAILABLE"
	PackageStatusDeleting         = "DELETING"
	PackageStatusDeleted          = "DELETED"
	PackageStatusDeleteFailed     = "DELETE_FAILED"
)

// VPC endpoint statuses.
const (
	VpcEndpointStatusCreating   = "CREATING"
	VpcEndpointStatusCreateFail = "CREATE_FAILED"
	VpcEndpointStatusActive     = "ACTIVE"
	VpcEndpointStatusUpdating   = "UPDATING"
	VpcEndpointStatusUpdateFail = "UPDATE_FAILED"
	VpcEndpointStatusDeleting   = "DELETING"
	VpcEndpointStatusDeleteFail = "DELETE_FAILED"
)

// ClusterConfig is the modeled subset of a domain's cluster configuration.
// Unmodeled fields survive via the domain's raw config passthrough.
type ClusterConfig struct {
	InstanceType           string
	InstanceCount          int32
	DedicatedMasterEnabled bool
	DedicatedMasterType    string
	DedicatedMasterCount   int32
	ZoneAwarenessEnabled   bool
	WarmEnabled            bool
	WarmType               string
	WarmCount              int32
}

// DomainStatus is the full description returned by DescribeDomain.
type DomainStatus struct {
	DomainID               string
	DomainName             string
	ARN                    string
	Created                bool
	Deleted                bool
	Processing             bool
	UpgradeProcessing      bool
	EngineVersion          string
	Endpoint               string
	Endpoints              map[string]string
	DomainProcessingStatus string
	ClusterConfig          ClusterConfig
	AccessPolicies         string
	AdvancedOptions        map[string]string
	IPAddressType          string
	EngineMode             string
	// RawOptions carries option blocks the emulator does not model, keyed by
	// their JSON field name (e.g. "EBSOptions"), so DescribeDomain reflects
	// exactly what CreateDomain/UpdateDomainConfig received.
	RawOptions map[string]json.RawMessage
	CreatedAt  time.Time
}

// CreateDomainInput describes a domain to create. RawOptions carries any
// option block not promoted to a typed field.
type CreateDomainInput struct {
	DomainName      string
	EngineVersion   string
	EngineMode      string
	IPAddressType   string
	ClusterConfig   ClusterConfig
	AccessPolicies  string
	AdvancedOptions map[string]string
	RawOptions      map[string]json.RawMessage
	Tags            map[string]string
}

// UpdateDomainConfigInput describes a config change. Only non-nil pointers are
// applied so callers can patch a single option.
type UpdateDomainConfigInput struct {
	DomainName      string
	ClusterConfig   *ClusterConfig
	AccessPolicies  *string
	AdvancedOptions map[string]string
	IPAddressType   *string
	RawOptions      map[string]json.RawMessage
	DryRun          bool
}

// DomainConfig is the change-tracked configuration returned by
// DescribeDomainConfig. Each field mirrors DomainStatus but is exposed as a
// separate view so the wire layer can wrap each in a status envelope.
type DomainConfig struct {
	EngineVersion   string
	ClusterConfig   ClusterConfig
	AccessPolicies  string
	AdvancedOptions map[string]string
	IPAddressType   string
	RawOptions      map[string]json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// DomainInfo is a light domain summary for ListDomainNames.
type DomainInfo struct {
	DomainName string
	EngineType string
}

// InboundConnection is a cross-cluster search connection as seen by the
// recipient (local) domain.
type InboundConnection struct {
	ConnectionID     string
	ConnectionStatus string
	StatusMessage    string
	LocalDomainName  string
	LocalRegion      string
	LocalOwnerID     string
	RemoteDomainName string
	RemoteRegion     string
	RemoteOwnerID    string
	Mode             string
}

// OutboundConnection is a cross-cluster search connection as seen by the
// initiating (source) domain.
type OutboundConnection struct {
	ConnectionID     string
	ConnectionAlias  string
	ConnectionMode   string
	StatusCode       string
	StatusMessage    string
	LocalDomainName  string
	LocalRegion      string
	LocalOwnerID     string
	RemoteDomainName string
	RemoteRegion     string
	RemoteOwnerID    string
}

// ConnectionEndpoint identifies one side of a cross-cluster connection.
type ConnectionEndpoint struct {
	OwnerID    string
	DomainName string
	Region     string
}

// CreateOutboundConnectionInput describes a new outbound connection.
type CreateOutboundConnectionInput struct {
	LocalDomain     ConnectionEndpoint
	RemoteDomain    ConnectionEndpoint
	ConnectionAlias string
	ConnectionMode  string
}

// Package is a package (dictionary/plugin) resource.
type Package struct {
	PackageID          string
	PackageName        string
	PackageType        string
	PackageDescription string
	PackageStatus      string
	CreatedAt          time.Time
	LastUpdatedAt      time.Time
	AvailableVersion   string
	EngineVersion      string
	S3BucketName       string
	S3Key              string
}

// CreatePackageInput describes a package to create.
type CreatePackageInput struct {
	PackageName        string
	PackageType        string
	PackageDescription string
	EngineVersion      string
	S3BucketName       string
	S3Key              string
}

// DomainPackageAssociation records a package associated with a domain.
type DomainPackageAssociation struct {
	PackageID           string
	PackageName         string
	PackageType         string
	DomainName          string
	DomainPackageStatus string
	PackageVersion      string
	ReferencePath       string
}

// VpcEndpoint is a VPC endpoint for a domain.
type VpcEndpoint struct {
	VpcEndpointID     string
	VpcEndpointOwner  string
	DomainARN         string
	Status            string
	SubnetIDs         []string
	SecurityGroupIDs  []string
	VPCID             string
	AvailabilityZones []string
	Endpoint          string
}

// VpcOptions describes the VPC placement of a VPC endpoint.
type VpcOptions struct {
	SubnetIDs        []string
	SecurityGroupIDs []string
}

// DataSource is an external (S3 Glue) data source attached to a domain.
type DataSource struct {
	Name           string
	DataSourceType map[string]json.RawMessage
	Description    string
	Status         string
}

// DirectQueryDataSource is a direct-query data source.
type DirectQueryDataSource struct {
	DataSourceName string
	DataSourceType map[string]json.RawMessage
	Description    string
	ARN            string
	OpenSearchArns []string
	TagList        map[string]string
}

// Application is an OpenSearch application.
type Application struct {
	ID                       string
	ARN                      string
	Name                     string
	Endpoint                 string
	Status                   string
	CreatedAt                time.Time
	LastUpdatedAt            time.Time
	DataSources              []map[string]json.RawMessage
	IamIdentityCenterOptions map[string]json.RawMessage
	AppConfigs               []map[string]json.RawMessage
	TagList                  map[string]string
}

// CreateApplicationInput describes an application to create.
type CreateApplicationInput struct {
	Name                     string
	DataSources              []map[string]json.RawMessage
	IamIdentityCenterOptions map[string]json.RawMessage
	AppConfigs               []map[string]json.RawMessage
	TagList                  map[string]string
}

// ReservedInstance is a purchased reserved instance.
type ReservedInstance struct {
	ReservationName            string
	ReservedInstanceID         string
	ReservedInstanceOfferingID string
	InstanceType               string
	InstanceCount              int32
	Duration                   int32
	FixedPrice                 float64
	UsagePrice                 float64
	CurrencyCode               string
	PaymentOption              string
	State                      string
	StartTime                  time.Time
}

// UpgradeStep records the progress of one upgrade step.
type UpgradeStep struct {
	UpgradeStep     string
	StepStatus      string
	ProgressPercent float64
}

// UpgradeHistory records one completed or in-progress upgrade.
type UpgradeHistory struct {
	UpgradeName   string
	StartTime     time.Time
	UpgradeStatus string
	StepsList     []UpgradeStep
}

// Page is a generic pagination request.
type Page struct {
	NextToken  string
	MaxResults int32
}

// OpenSearch is the interface an OpenSearch Service backend implements.
//
//nolint:interfacebloat // OpenSearch Service exposes 96 operations; full parity requires them all.
type OpenSearch interface {
	// Domains.
	CreateDomain(ctx context.Context, in CreateDomainInput) (*DomainStatus, error)
	DeleteDomain(ctx context.Context, name string) (*DomainStatus, error)
	DescribeDomain(ctx context.Context, name string) (*DomainStatus, error)
	DescribeDomains(ctx context.Context, names []string) ([]DomainStatus, error)
	DescribeDomainConfig(ctx context.Context, name string) (*DomainConfig, error)
	UpdateDomainConfig(ctx context.Context, in UpdateDomainConfigInput) (*DomainConfig, bool, error)
	ListDomainNames(ctx context.Context, engineType string) ([]DomainInfo, error)
	DescribeDomainChangeProgress(ctx context.Context, name, changeID string) (map[string]json.RawMessage, error)
	DescribeDomainHealth(ctx context.Context, name string) (map[string]json.RawMessage, error)
	DescribeDomainNodes(ctx context.Context, name string) ([]map[string]json.RawMessage, error)
	DescribeDomainAutoTunes(ctx context.Context, name string, page Page) ([]map[string]json.RawMessage, string, error)
	DescribeDryRunProgress(ctx context.Context, name string) (map[string]json.RawMessage, error)
	CancelDomainConfigChange(ctx context.Context, name string, dryRun bool) ([]map[string]json.RawMessage, error)

	// Versions & catalogs (read-only, synthesized).
	ListVersions(ctx context.Context, page Page) ([]string, string, error)
	GetCompatibleVersions(ctx context.Context, domainName string) (map[string][]string, error)
	DescribeInstanceTypeLimits(ctx context.Context, engineVersion, instanceType, domainName string) (map[string]json.RawMessage, error)
	ListInstanceTypeDetails(ctx context.Context, engineVersion string, page Page) ([]map[string]json.RawMessage, string, error)

	// Tags.
	AddTags(ctx context.Context, arn string, tags map[string]string) error
	RemoveTags(ctx context.Context, arn string, keys []string) error
	ListTags(ctx context.Context, arn string) (map[string]string, error)

	// Service software update.
	StartServiceSoftwareUpdate(ctx context.Context, name string) (map[string]json.RawMessage, error)
	CancelServiceSoftwareUpdate(ctx context.Context, name string) (map[string]json.RawMessage, error)
	RollbackServiceSoftwareUpdate(ctx context.Context, name string) (map[string]json.RawMessage, error)

	// Upgrades.
	UpgradeDomain(ctx context.Context, name, targetVersion string, perform bool, advanced map[string]string) (string, error)
	GetUpgradeStatus(ctx context.Context, name string) (*UpgradeStep, error)
	GetUpgradeHistory(ctx context.Context, name string, page Page) ([]UpgradeHistory, string, error)

	// Packages.
	CreatePackage(ctx context.Context, in CreatePackageInput) (*Package, error)
	DeletePackage(ctx context.Context, packageID string) (*Package, error)
	DescribePackages(ctx context.Context, page Page) ([]Package, string, error)
	UpdatePackage(ctx context.Context, packageID, description, bucket, key string) (*Package, error)
	UpdatePackageScope(ctx context.Context, packageID, operation string, users []string) (string, []string, error)
	GetPackageVersionHistory(ctx context.Context, packageID string, page Page) (string, []map[string]json.RawMessage, string, error)
	AssociatePackage(ctx context.Context, packageID, domainName string) (*DomainPackageAssociation, error)
	AssociatePackages(ctx context.Context, packageIDs []string, domainName string) ([]DomainPackageAssociation, error)
	DissociatePackage(ctx context.Context, packageID, domainName string) (*DomainPackageAssociation, error)
	DissociatePackages(ctx context.Context, packageIDs []string, domainName string) ([]DomainPackageAssociation, error)
	ListPackagesForDomain(ctx context.Context, domainName string, page Page) ([]DomainPackageAssociation, string, error)
	ListDomainsForPackage(ctx context.Context, packageID string, page Page) ([]DomainPackageAssociation, string, error)

	// VPC endpoints.
	CreateVpcEndpoint(ctx context.Context, domainARN string, opts VpcOptions, clientToken string) (*VpcEndpoint, error)
	UpdateVpcEndpoint(ctx context.Context, id string, opts VpcOptions) (*VpcEndpoint, error)
	DeleteVpcEndpoint(ctx context.Context, id string) (string, string, error)
	DescribeVpcEndpoints(ctx context.Context, ids []string) ([]VpcEndpoint, []map[string]json.RawMessage, error)
	ListVpcEndpoints(ctx context.Context, page Page) ([]map[string]json.RawMessage, string, error)
	ListVpcEndpointsForDomain(ctx context.Context, domainName string, page Page) ([]map[string]json.RawMessage, string, error)
	AuthorizeVpcEndpointAccess(ctx context.Context, domainName, account, service string) (map[string]json.RawMessage, error)
	RevokeVpcEndpointAccess(ctx context.Context, domainName, account, service string) error
	ListVpcEndpointAccess(ctx context.Context, domainName string, page Page) ([]map[string]json.RawMessage, string, error)

	// Cross-cluster connections.
	CreateOutboundConnection(ctx context.Context, in CreateOutboundConnectionInput) (*OutboundConnection, error)
	DeleteOutboundConnection(ctx context.Context, id string) (*OutboundConnection, error)
	DescribeOutboundConnections(ctx context.Context, page Page) ([]OutboundConnection, string, error)
	AcceptInboundConnection(ctx context.Context, id string) (*InboundConnection, error)
	RejectInboundConnection(ctx context.Context, id string) (*InboundConnection, error)
	DeleteInboundConnection(ctx context.Context, id string) (*InboundConnection, error)
	DescribeInboundConnections(ctx context.Context, page Page) ([]InboundConnection, string, error)

	// Data sources (per-domain).
	AddDataSource(ctx context.Context, domainName string, ds DataSource) (string, error)
	DeleteDataSource(ctx context.Context, domainName, name string) (string, error)
	GetDataSource(ctx context.Context, domainName, name string) (*DataSource, error)
	UpdateDataSource(ctx context.Context, domainName string, ds DataSource) (string, error)
	ListDataSources(ctx context.Context, domainName string) ([]DataSource, error)

	// Direct-query data sources.
	AddDirectQueryDataSource(ctx context.Context, ds DirectQueryDataSource) (string, error)
	DeleteDirectQueryDataSource(ctx context.Context, name string) error
	GetDirectQueryDataSource(ctx context.Context, name string) (*DirectQueryDataSource, error)
	UpdateDirectQueryDataSource(ctx context.Context, ds DirectQueryDataSource) (string, error)
	ListDirectQueryDataSources(ctx context.Context) ([]DirectQueryDataSource, error)

	// Applications.
	CreateApplication(ctx context.Context, in CreateApplicationInput) (*Application, error)
	GetApplication(ctx context.Context, id string) (*Application, error)
	UpdateApplication(ctx context.Context, id string,
		dataSources, appConfigs []map[string]json.RawMessage) (*Application, error)
	DeleteApplication(ctx context.Context, id string) error
	ListApplications(ctx context.Context, page Page) ([]Application, string, error)

	// Reserved instances.
	PurchaseReservedInstanceOffering(ctx context.Context, offeringID, reservationName string, instanceCount int32) (string, string, error)
	DescribeReservedInstances(ctx context.Context, reservedInstanceID string, page Page) ([]ReservedInstance, string, error)
	DescribeReservedInstanceOfferings(ctx context.Context, offeringID string, page Page) ([]map[string]json.RawMessage, string, error)

	// Domain maintenance.
	StartDomainMaintenance(ctx context.Context, domainName, action, nodeID string) (string, error)
	GetDomainMaintenanceStatus(ctx context.Context, domainName, maintenanceID string) (map[string]json.RawMessage, error)
	ListDomainMaintenances(ctx context.Context, domainName, action, status string, page Page) ([]map[string]json.RawMessage, string, error)

	// Scheduled actions.
	ListScheduledActions(ctx context.Context, domainName string, page Page) ([]map[string]json.RawMessage, string, error)
	UpdateScheduledAction(ctx context.Context, domainName, actionID, actionType, scheduleAt string,
		desiredStartTime int64) (map[string]json.RawMessage, error)

	// Indexes (data-plane convenience).
	CreateIndex(ctx context.Context, domainName, indexName string, settings map[string]json.RawMessage) (map[string]json.RawMessage, error)
	DeleteIndex(ctx context.Context, domainName, indexName string) (map[string]json.RawMessage, error)
	GetIndex(ctx context.Context, domainName, indexName string) (map[string]json.RawMessage, error)
	UpdateIndex(ctx context.Context, domainName, indexName string, settings map[string]json.RawMessage) (map[string]json.RawMessage, error)

	// Insights.
	ListInsights(ctx context.Context, page Page) ([]map[string]json.RawMessage, string, error)
	DescribeInsightDetails(ctx context.Context, insightID string) (map[string]json.RawMessage, error)
	InsightFeedback(ctx context.Context, insightID, feedback string) error

	// Migrations (serverless).
	StartMigration(ctx context.Context, in map[string]json.RawMessage) (string, error)
	GetMigration(ctx context.Context, migrationID string) (map[string]json.RawMessage, error)
	ListMigrations(ctx context.Context, page Page) ([]string, string, error)

	// Capabilities & default application setting.
	RegisterCapability(ctx context.Context, applicationID, capability string,
		payload map[string]json.RawMessage) (map[string]json.RawMessage, error)
	DeregisterCapability(ctx context.Context, applicationID, capability string) error
	GetCapability(ctx context.Context, applicationID, capability string) (map[string]json.RawMessage, error)
	GetDefaultApplicationSetting(ctx context.Context) (map[string]json.RawMessage, error)
	PutDefaultApplicationSetting(ctx context.Context, setting map[string]json.RawMessage) (map[string]json.RawMessage, error)

	// Data-source attachments (per-application).
	AttachDataSource(ctx context.Context, applicationID string, ds map[string]json.RawMessage) (map[string]json.RawMessage, error)
	DetachDataSource(ctx context.Context, applicationID, dataSourceARN string) (map[string]json.RawMessage, error)
	DescribeDataSourceAttachment(ctx context.Context, applicationID, attachmentID string) (map[string]json.RawMessage, error)
	ListDataSourceAttachments(ctx context.Context, applicationID string, page Page) ([]map[string]json.RawMessage, string, error)
}
