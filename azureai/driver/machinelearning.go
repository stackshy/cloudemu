package driver

import "context"

// MLWorkspaceConfig describes an Azure ML workspace to create or update.
type MLWorkspaceConfig struct {
	Name          string
	ResourceGroup string
	Location      string
	Kind          string // Default | Hub | Project | FeatureStore
	FriendlyName  string
	Description   string
	Tags          map[string]string
}

// MLWorkspace is a Microsoft.MachineLearningServices/workspaces resource.
type MLWorkspace struct {
	ID                string
	Name              string
	ResourceGroup     string
	Location          string
	Kind              string
	FriendlyName      string
	Description       string
	DiscoveryURL      string
	ProvisioningState string
	Tags              map[string]string
	CreatedAt         string
}

// ComputeConfig describes a workspace compute to create.
type ComputeConfig struct {
	Workspace     string
	ResourceGroup string
	Name          string
	ComputeType   string // AmlCompute | ComputeInstance | Kubernetes | ...
	VMSize        string
	MinNodes      int
	MaxNodes      int
}

// Compute is a workspaces/computes resource.
type Compute struct {
	ID                string
	Name              string
	ComputeType       string
	VMSize            string
	MinNodes          int
	MaxNodes          int
	State             string // Running | Stopped | Resizing | ...
	ProvisioningState string
	CreatedAt         string
}

// EndpointConfig describes an online/batch endpoint to create.
type EndpointConfig struct {
	Workspace     string
	ResourceGroup string
	Name          string
	Kind          string // online | batch
	AuthMode      string // Key | AMLToken | AADToken
	Description   string
}

// Endpoint is a workspaces/{online,batch}Endpoints resource.
type Endpoint struct {
	ID                string
	Name              string
	Kind              string
	AuthMode          string
	Description       string
	ScoringURI        string
	ProvisioningState string
	Traffic           map[string]int
	CreatedAt         string
}

// EndpointDeploymentConfig describes a deployment under an endpoint.
type EndpointDeploymentConfig struct {
	Workspace     string
	ResourceGroup string
	Endpoint      string
	EndpointKind  string
	Name          string
	Model         string
	InstanceType  string
	InstanceCount int
}

// EndpointDeployment is a {online,batch}Endpoints/{e}/deployments resource.
type EndpointDeployment struct {
	ID                string
	Name              string
	Model             string
	InstanceType      string
	InstanceCount     int
	ProvisioningState string
	CreatedAt         string
}

// JobConfig describes a workspace job to submit.
type JobConfig struct {
	Workspace     string
	ResourceGroup string
	Name          string
	JobType       string // Command | Sweep | Pipeline | AutoML
	DisplayName   string
	ComputeID     string
}

// Job is a workspaces/jobs resource.
type Job struct {
	ID          string
	Name        string
	JobType     string
	DisplayName string
	Status      string // NotStarted | Running | Completed | Failed | Canceled
	CreatedAt   string
}

// AssetConfig describes a versioned asset (model/data/environment/component)
// or a featureset to create. AssetType selects the collection.
type AssetConfig struct {
	Workspace     string
	ResourceGroup string
	AssetType     string // models | data | environments | components | featuresets
	Name          string
	Version       string
	Description   string
	Path          string // assetUri / image / etc.
	Properties    map[string]string
}

// Asset is one version of a versioned asset.
type Asset struct {
	ID          string
	Name        string
	Version     string
	AssetType   string
	Description string
	Path        string
	Properties  map[string]string
	CreatedAt   string
}

// DatastoreConfig describes a workspace datastore.
type DatastoreConfig struct {
	Workspace     string
	ResourceGroup string
	Name          string
	StoreType     string // AzureBlob | AzureFile | AzureDataLakeGen2 | ...
	AccountName   string
	Container     string
}

// Datastore is a workspaces/datastores resource.
type Datastore struct {
	ID          string
	Name        string
	StoreType   string
	AccountName string
	Container   string
	CreatedAt   string
}

// ConnectionConfig describes a workspace connection.
type ConnectionConfig struct {
	Workspace     string
	ResourceGroup string
	Name          string
	Category      string // AzureOpenAI | CognitiveSearch | Git | ...
	Target        string
	AuthType      string
}

// Connection is a workspaces/connections resource.
type Connection struct {
	ID        string
	Name      string
	Category  string
	Target    string
	AuthType  string
	CreatedAt string
}

// MLScheduleConfig describes a workspace schedule.
type MLScheduleConfig struct {
	Workspace     string
	ResourceGroup string
	Name          string
	Cron          string
	DisplayName   string
}

// MLSchedule is a workspaces/schedules resource.
type MLSchedule struct {
	ID          string
	Name        string
	Cron        string
	DisplayName string
	IsEnabled   bool
	CreatedAt   string
}

// RegistryConfig describes a cross-workspace registry.
type RegistryConfig struct {
	Name          string
	ResourceGroup string
	Location      string
	Description   string
	Tags          map[string]string
}

// Registry is a Microsoft.MachineLearningServices/registries resource.
type Registry struct {
	ID                string
	Name              string
	Location          string
	Description       string
	ProvisioningState string
	Tags              map[string]string
	CreatedAt         string
}

// MachineLearning is the Microsoft.MachineLearningServices control-plane
// surface (Azure ML).
type MachineLearning interface {
	// Workspaces.
	CreateMLWorkspace(ctx context.Context, cfg MLWorkspaceConfig) (*MLWorkspace, error)
	GetMLWorkspace(ctx context.Context, resourceGroup, name string) (*MLWorkspace, error)
	DeleteMLWorkspace(ctx context.Context, resourceGroup, name string) error
	UpdateMLWorkspaceTags(ctx context.Context, resourceGroup, name string, tags map[string]string) (*MLWorkspace, error)
	ListMLWorkspacesByResourceGroup(ctx context.Context, resourceGroup string) ([]MLWorkspace, error)
	ListMLWorkspaces(ctx context.Context) ([]MLWorkspace, error)

	// Computes.
	CreateCompute(ctx context.Context, cfg ComputeConfig) (*Compute, error)
	GetCompute(ctx context.Context, resourceGroup, workspace, name string) (*Compute, error)
	DeleteCompute(ctx context.Context, resourceGroup, workspace, name string) error
	ListComputes(ctx context.Context, resourceGroup, workspace string) ([]Compute, error)
	StartCompute(ctx context.Context, resourceGroup, workspace, name string) error
	StopCompute(ctx context.Context, resourceGroup, workspace, name string) error
	RestartCompute(ctx context.Context, resourceGroup, workspace, name string) error

	// Endpoints (online + batch) and their deployments.
	CreateEndpoint(ctx context.Context, cfg EndpointConfig) (*Endpoint, error)
	GetEndpoint(ctx context.Context, resourceGroup, workspace, kind, name string) (*Endpoint, error)
	DeleteEndpoint(ctx context.Context, resourceGroup, workspace, kind, name string) error
	ListEndpoints(ctx context.Context, resourceGroup, workspace, kind string) ([]Endpoint, error)
	CreateEndpointDeployment(ctx context.Context, cfg EndpointDeploymentConfig) (*EndpointDeployment, error)
	GetEndpointDeployment(ctx context.Context, resourceGroup, workspace, kind, endpoint, name string) (*EndpointDeployment, error)
	DeleteEndpointDeployment(ctx context.Context, resourceGroup, workspace, kind, endpoint, name string) error
	ListEndpointDeployments(ctx context.Context, resourceGroup, workspace, kind, endpoint string) ([]EndpointDeployment, error)

	// Jobs.
	CreateJob(ctx context.Context, cfg JobConfig) (*Job, error)
	GetJob(ctx context.Context, resourceGroup, workspace, name string) (*Job, error)
	ListJobs(ctx context.Context, resourceGroup, workspace string) ([]Job, error)
	CancelJob(ctx context.Context, resourceGroup, workspace, name string) error

	// Versioned assets (models / data / environments / components / featuresets).
	CreateAsset(ctx context.Context, cfg AssetConfig) (*Asset, error)
	GetAsset(ctx context.Context, resourceGroup, workspace, assetType, name, version string) (*Asset, error)
	DeleteAsset(ctx context.Context, resourceGroup, workspace, assetType, name, version string) error
	ListAssetVersions(ctx context.Context, resourceGroup, workspace, assetType, name string) ([]Asset, error)
	ListAssetContainers(ctx context.Context, resourceGroup, workspace, assetType string) ([]Asset, error)

	// Datastores.
	CreateDatastore(ctx context.Context, cfg DatastoreConfig) (*Datastore, error)
	GetDatastore(ctx context.Context, resourceGroup, workspace, name string) (*Datastore, error)
	DeleteDatastore(ctx context.Context, resourceGroup, workspace, name string) error
	ListDatastores(ctx context.Context, resourceGroup, workspace string) ([]Datastore, error)

	// Connections.
	CreateConnection(ctx context.Context, cfg ConnectionConfig) (*Connection, error)
	GetConnection(ctx context.Context, resourceGroup, workspace, name string) (*Connection, error)
	DeleteConnection(ctx context.Context, resourceGroup, workspace, name string) error
	ListConnections(ctx context.Context, resourceGroup, workspace string) ([]Connection, error)

	// Schedules.
	CreateMLSchedule(ctx context.Context, cfg MLScheduleConfig) (*MLSchedule, error)
	GetMLSchedule(ctx context.Context, resourceGroup, workspace, name string) (*MLSchedule, error)
	DeleteMLSchedule(ctx context.Context, resourceGroup, workspace, name string) error
	ListMLSchedules(ctx context.Context, resourceGroup, workspace string) ([]MLSchedule, error)

	// Registries (subscription/RG-scoped, cross-workspace).
	CreateRegistry(ctx context.Context, cfg RegistryConfig) (*Registry, error)
	GetRegistry(ctx context.Context, resourceGroup, name string) (*Registry, error)
	DeleteRegistry(ctx context.Context, resourceGroup, name string) error
	ListRegistries(ctx context.Context, resourceGroup string) ([]Registry, error)
}
