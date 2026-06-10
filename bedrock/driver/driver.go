// Package driver defines the interface for Bedrock-style foundation-model
// services: a catalog of foundation models, a custom-model fine-tuning
// lifecycle, and a runtime that performs (emulated) inference.
package driver

import "context"

// Model lifecycle status values.
const (
	LifecycleActive = "ACTIVE"
	LifecycleLegacy = "LEGACY"
)

// Customization job status values.
const (
	JobInProgress = "InProgress"
	JobCompleted  = "Completed"
	JobFailed     = "Failed"
	JobStopping   = "Stopping"
	JobStopped    = "Stopped"
)

// Custom model status values.
const (
	ModelCreating = "Creating"
	ModelActive   = "Active"
	ModelFailed   = "Failed"
)

// FoundationModel describes a base model offered by the provider.
type FoundationModel struct {
	ModelARN                   string
	ModelID                    string
	ModelName                  string
	ProviderName               string
	InputModalities            []string
	OutputModalities           []string
	ResponseStreamingSupported bool
	CustomizationsSupported    []string
	InferenceTypesSupported    []string
	LifecycleStatus            string
}

// CustomizationJobConfig describes a model-customization (fine-tuning) job to
// create.
type CustomizationJobConfig struct {
	JobName             string
	CustomModelName     string
	RoleARN             string
	BaseModelIdentifier string
	CustomizationType   string
	HyperParameters     map[string]string
	ClientRequestToken  string
	TrainingDataURI     string
	OutputDataURI       string
}

// CustomizationJob describes a model-customization job.
type CustomizationJob struct {
	JobARN             string
	JobName            string
	OutputModelName    string
	OutputModelARN     string
	RoleARN            string
	BaseModelARN       string
	Status             string
	CustomizationType  string
	HyperParameters    map[string]string
	ClientRequestToken string
	TrainingDataURI    string
	OutputDataURI      string
	FailureMessage     string
	CreationTime       string
	LastModifiedTime   string
	EndTime            string
}

// CustomModel describes a model produced by a completed customization job.
type CustomModel struct {
	ModelARN          string
	ModelName         string
	BaseModelARN      string
	BaseModelName     string
	CustomizationType string
	ModelStatus       string
	JobARN            string
	JobName           string
	HyperParameters   map[string]string
	TrainingDataURI   string
	OutputDataURI     string
	OwnerAccountID    string
	CreationTime      string
}

// InvokeModelInput is the raw-payload request for the runtime InvokeModel
// operation. Body is the model-native request envelope (its shape depends on
// the model family).
type InvokeModelInput struct {
	ModelID     string
	ContentType string
	Accept      string
	Body        []byte
}

// InvokeModelResult carries the model-native response envelope.
type InvokeModelResult struct {
	ContentType string
	Body        []byte
}

// Message is a single turn in a Converse exchange. Only text content blocks
// are modeled.
type Message struct {
	Role string
	Text []string
}

// InferenceConfig holds the optional sampling controls for Converse.
type InferenceConfig struct {
	MaxTokens     *int32
	Temperature   *float64
	TopP          *float64
	StopSequences []string
}

// ConverseInput is the request for the runtime Converse operation.
type ConverseInput struct {
	ModelID         string
	System          []string
	Messages        []Message
	InferenceConfig *InferenceConfig
}

// ConverseOutput is the response from the runtime Converse operation.
type ConverseOutput struct {
	Message      Message
	StopReason   string
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	LatencyMs    int
}

// Bedrock is the interface that foundation-model service implementations must
// satisfy. It spans the control plane (model catalog, customization jobs,
// custom models) and the runtime (InvokeModel, Converse).
type Bedrock interface {
	ListFoundationModels(ctx context.Context) ([]FoundationModel, error)
	GetFoundationModel(ctx context.Context, modelID string) (*FoundationModel, error)

	CreateModelCustomizationJob(ctx context.Context, cfg CustomizationJobConfig) (*CustomizationJob, error)
	GetModelCustomizationJob(ctx context.Context, jobIdentifier string) (*CustomizationJob, error)
	ListModelCustomizationJobs(ctx context.Context) ([]CustomizationJob, error)

	ListCustomModels(ctx context.Context) ([]CustomModel, error)
	GetCustomModel(ctx context.Context, modelIdentifier string) (*CustomModel, error)
	DeleteCustomModel(ctx context.Context, modelIdentifier string) error

	InvokeModel(ctx context.Context, in InvokeModelInput) (*InvokeModelResult, error)
	Converse(ctx context.Context, in ConverseInput) (*ConverseOutput, error)
}
