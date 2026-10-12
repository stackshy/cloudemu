package driver

import (
	"context"
	"encoding/json"
)

// Endpoint lifecycle status values.
const (
	EndpointOutOfService   = "OutOfService"
	EndpointCreating       = "Creating"
	EndpointUpdating       = "Updating"
	EndpointSystemUpdating = "SystemUpdating"
	EndpointRollingBack    = "RollingBack"
	EndpointInService      = "InService"
	EndpointDeleting       = "Deleting"
	EndpointFailed         = "Failed"
	// EndpointUpdateRollbackFailed is reachable only through fault injection:
	// a rolling deployment and its automatic rollback both failed.
	EndpointUpdateRollbackFailed = "UpdateRollbackFailed"
)

// Production-variant status values (VariantStatus.Status).
const (
	VariantCreating = "Creating"
	VariantUpdating = "Updating"
	VariantDeleting = "Deleting"
)

// InferenceComponent status values.
const (
	ICreating  = "Creating"
	IInService = "InService"
	IUpdating  = "Updating"
	IFailed    = "Failed"
	IDeleting  = "Deleting"
)

// ContainerDefinition is one container in a model.
type ContainerDefinition struct {
	Image        string
	ModelDataURL string
	Environment  map[string]string
	Mode         string // SingleModel, MultiModel
}

// ModelConfig describes a model to create. Pipeline distinguishes an
// inference-pipeline model (created via Containers) from a single-container
// model (created via PrimaryContainer); the two echo back differently on
// Describe.
type ModelConfig struct {
	ModelName  string
	RoleARN    string
	Containers []ContainerDefinition
	Pipeline   bool
	Tags       []Tag
}

// Model describes a created model (a static metadata record, no status).
type Model struct {
	ModelName    string
	ModelARN     string
	RoleARN      string
	Containers   []ContainerDefinition
	Pipeline     bool
	CreationTime string
	Tags         []Tag
}

// ProductionVariant is one instance-backed serving variant.
type ProductionVariant struct {
	VariantName          string
	ModelName            string
	InstanceType         string
	InitialInstanceCount int
	InitialVariantWeight float64
}

// ServerlessConfig configures serverless inference for a variant.
type ServerlessConfig struct {
	MemorySizeInMB int
	MaxConcurrency int
}

// EndpointConfigSpec describes an endpoint configuration to create.
type EndpointConfigSpec struct {
	ConfigName         string
	ProductionVariants []ProductionVariant
	Serverless         *ServerlessConfig
	AsyncOutputS3URI   string
	Tags               []Tag
}

// EndpointConfig is a static endpoint configuration record.
type EndpointConfig struct {
	ConfigName         string
	ConfigARN          string
	ProductionVariants []ProductionVariant
	Serverless         *ServerlessConfig
	AsyncOutputS3URI   string
	CreationTime       string
	Tags               []Tag
}

// EndpointSpec describes an endpoint to create.
type EndpointSpec struct {
	EndpointName string
	ConfigName   string
	Tags         []Tag
}

// VariantWeight is a per-variant weight/capacity update. A nil field leaves
// that setting unchanged.
type VariantWeight struct {
	VariantName          string
	DesiredWeight        *float64
	DesiredInstanceCount *int
}

// DeployedImage is a container image a variant serves, with the digest it
// resolved to when the variant was deployed.
type DeployedImage struct {
	SpecifiedImage string
	ResolvedImage  string
	ResolutionTime string
}

// VariantStatus is one in-flight status entry of a production variant.
type VariantStatus struct {
	Status        string
	StatusMessage string
	StartTime     string
}

// VariantSummary is the runtime (Describe) view of a production variant: what
// is serving now (Current*) versus what the last update asked for (Desired*).
type VariantSummary struct {
	VariantName          string
	DeployedImages       []DeployedImage
	CurrentWeight        float64
	DesiredWeight        float64
	CurrentInstanceCount int
	DesiredInstanceCount int
	VariantStatus        []VariantStatus
}

// PendingDeployment describes the configuration an endpoint is moving to while
// it is Creating or Updating with a new endpoint configuration.
type PendingDeployment struct {
	ConfigName string
	Variants   []VariantSummary
	StartTime  string
}

// Endpoint is a deployed endpoint with a lifecycle status.
type Endpoint struct {
	EndpointName     string
	EndpointARN      string
	ConfigName       string
	Status           string
	Variants         []ProductionVariant
	FailureReason    string
	CreationTime     string
	LastModifiedTime string
	Tags             []Tag
	// VariantSummaries is the runtime view of Variants.
	VariantSummaries []VariantSummary
	// Pending is set only while the endpoint is Creating or Updating.
	Pending *PendingDeployment
	// LastDeploymentConfig echoes the most recent DeploymentConfig verbatim.
	LastDeploymentConfig json.RawMessage
}

// UpdateEndpointSpec describes an UpdateEndpoint request.
type UpdateEndpointSpec struct {
	EndpointName string
	ConfigName   string
	// RetainAllVariantProperties keeps each same-named variant's current
	// desired weight and instance count instead of taking the new config's.
	RetainAllVariantProperties bool
	// ExcludeRetained lists variant properties (DesiredInstanceCount,
	// DesiredWeight) not retained even when RetainAllVariantProperties is set.
	ExcludeRetained []string
	// DeploymentConfig is recorded as the endpoint's LastDeploymentConfig.
	DeploymentConfig json.RawMessage
	// RetainDeploymentConfig keeps the previous LastDeploymentConfig.
	RetainDeploymentConfig bool
}

// InferenceComponentSpec describes an inference component to create.
type InferenceComponentSpec struct {
	Name         string
	EndpointName string
	ModelName    string
	VariantName  string
	CopyCount    int
	Tags         []Tag
}

// InferenceComponent packs a model onto a shared endpoint.
type InferenceComponent struct {
	Name             string
	ARN              string
	EndpointName     string
	ModelName        string
	VariantName      string
	CopyCount        int
	Status           string
	CreationTime     string
	LastModifiedTime string
	Tags             []Tag
}

// inferenceAPI covers the model/endpoint serving stack.
type inferenceAPI interface {
	CreateModel(ctx context.Context, cfg ModelConfig) (*Model, error)
	DescribeModel(ctx context.Context, name string) (*Model, error)
	ListModels(ctx context.Context, opts ListOptions) (ListPage[Model], error)
	DeleteModel(ctx context.Context, name string) error

	CreateEndpointConfig(ctx context.Context, cfg EndpointConfigSpec) (*EndpointConfig, error)
	DescribeEndpointConfig(ctx context.Context, name string) (*EndpointConfig, error)
	ListEndpointConfigs(ctx context.Context, opts ListOptions) (ListPage[EndpointConfig], error)
	DeleteEndpointConfig(ctx context.Context, name string) error

	CreateEndpoint(ctx context.Context, cfg EndpointSpec) (*Endpoint, error)
	DescribeEndpoint(ctx context.Context, name string) (*Endpoint, error)
	ListEndpoints(ctx context.Context, opts ListOptions) (ListPage[Endpoint], error)
	UpdateEndpoint(ctx context.Context, spec UpdateEndpointSpec) (*Endpoint, error)
	UpdateEndpointWeightsAndCapacities(ctx context.Context, name string, weights []VariantWeight) (*Endpoint, error)
	DeleteEndpoint(ctx context.Context, name string) error

	CreateInferenceComponent(ctx context.Context, cfg InferenceComponentSpec) (*InferenceComponent, error)
	DescribeInferenceComponent(ctx context.Context, name string) (*InferenceComponent, error)
	ListInferenceComponents(ctx context.Context, opts ListOptions) (ListPage[InferenceComponent], error)
	DeleteInferenceComponent(ctx context.Context, name string) error
}

// InvokeEndpointInput is a synchronous invocation request.
type InvokeEndpointInput struct {
	EndpointName           string
	ContentType            string
	Accept                 string
	Body                   []byte
	InferenceComponentName string
	TargetModel            string
	// TargetVariant pins the call to one production variant.
	TargetVariant string
}

// InvokeEndpointOutput is a synchronous invocation response.
type InvokeEndpointOutput struct {
	ContentType    string
	Body           []byte
	InvokedVariant string
}

// InvokeEndpointAsyncInput is an asynchronous invocation request.
type InvokeEndpointAsyncInput struct {
	EndpointName string
	InputS3URI   string
	ContentType  string
}

// InvokeEndpointAsyncOutput carries the S3 location of the async result.
type InvokeEndpointAsyncOutput struct {
	OutputS3URI string
	InferenceID string
}
