// Package driver defines the interface for serverless function service implementations.
package driver

import "context"

// FunctionVersion represents a published version of a function.
type FunctionVersion struct {
	FunctionName string
	Version      string // "1", "2", etc. or "$LATEST"
	Description  string
	CodeSHA256   string
	CreatedAt    string
}

// AliasConfig configures a function alias.
type AliasConfig struct {
	FunctionName    string
	Name            string
	FunctionVersion string
	Description     string
	RoutingConfig   *AliasRoutingConfig // for weighted aliases
}

// AliasRoutingConfig defines weighted routing between versions.
type AliasRoutingConfig struct {
	AdditionalVersion string
	Weight            float64 // 0.0-1.0, traffic percentage to additional version
}

// Alias represents a function alias.
type Alias struct {
	FunctionName    string
	Name            string
	FunctionVersion string
	Description     string
	RoutingConfig   *AliasRoutingConfig
	AliasARN        string
	CreatedAt       string
}

// LayerConfig configures a new layer version.
type LayerConfig struct {
	Name               string
	Description        string
	Content            []byte
	CompatibleRuntimes []string
}

// LayerVersion represents a published layer version.
type LayerVersion struct {
	Name               string
	Version            int
	Description        string
	ContentSHA256      string
	ContentSize        int64
	CompatibleRuntimes []string
	CreatedAt          string
	ARN                string
}

// ConcurrencyConfig configures function concurrency.
type ConcurrencyConfig struct {
	FunctionName                 string
	ReservedConcurrentExecutions int
}

// ProvisionedConcurrencyConfig configures provisioned concurrency.
type ProvisionedConcurrencyConfig struct {
	FunctionName string
	Qualifier    string // version or alias
	Provisioned  int
}

// FunctionConfig describes a serverless function to create.
type FunctionConfig struct {
	Name        string
	Runtime     string
	Handler     string
	Memory      int // MB
	Timeout     int // seconds
	Environment map[string]string
	Tags        map[string]string
}

// FunctionInfo describes a serverless function.
type FunctionInfo struct {
	Name         string
	ARN          string
	Runtime      string
	Handler      string
	Memory       int
	Timeout      int
	State        string
	Environment  map[string]string
	Tags         map[string]string
	LastModified string
}

// InvokeInput configures a function invocation.
type InvokeInput struct {
	FunctionName string
	Payload      []byte
	InvokeType   string // "RequestResponse" or "Event"
}

// InvokeOutput is the result of a function invocation.
type InvokeOutput struct {
	StatusCode int
	Payload    []byte
	Error      string
}

// HandlerFunc is a function handler that processes invocations.
type HandlerFunc func(ctx context.Context, payload []byte) ([]byte, error)

// EventSourceMappingConfig describes an event source mapping to create.
type EventSourceMappingConfig struct {
	EventSourceArn   string
	FunctionName     string
	BatchSize        int
	Enabled          bool
	StartingPosition string // "LATEST", "TRIM_HORIZON"
}

// EventSourceMappingInfo describes an event source mapping.
type EventSourceMappingInfo struct {
	UUID             string
	EventSourceArn   string
	FunctionName     string
	BatchSize        int
	Enabled          bool
	StartingPosition string
	State            string // "Enabled", "Disabled", "Creating", "Deleting"
	CreatedAt        string
}

// Serverless is the interface that serverless provider implementations must satisfy.
type Serverless interface {
	CreateFunction(ctx context.Context, config FunctionConfig) (*FunctionInfo, error)
	DeleteFunction(ctx context.Context, name string) error
	GetFunction(ctx context.Context, name string) (*FunctionInfo, error)
	ListFunctions(ctx context.Context) ([]FunctionInfo, error)
	UpdateFunction(ctx context.Context, name string, config FunctionConfig) (*FunctionInfo, error)
	Invoke(ctx context.Context, input InvokeInput) (*InvokeOutput, error)
	RegisterHandler(name string, handler HandlerFunc)

	// Versions
	PublishVersion(ctx context.Context, functionName, description string) (*FunctionVersion, error)
	ListVersions(ctx context.Context, functionName string) ([]FunctionVersion, error)

	// Aliases
	CreateAlias(ctx context.Context, config AliasConfig) (*Alias, error)
	UpdateAlias(ctx context.Context, config AliasConfig) (*Alias, error)
	DeleteAlias(ctx context.Context, functionName, aliasName string) error
	GetAlias(ctx context.Context, functionName, aliasName string) (*Alias, error)
	ListAliases(ctx context.Context, functionName string) ([]Alias, error)

	// Layers
	PublishLayerVersion(ctx context.Context, config LayerConfig) (*LayerVersion, error)
	GetLayerVersion(ctx context.Context, name string, version int) (*LayerVersion, error)
	ListLayerVersions(ctx context.Context, name string) ([]LayerVersion, error)
	DeleteLayerVersion(ctx context.Context, name string, version int) error
	ListLayers(ctx context.Context) ([]LayerVersion, error)

	// Concurrency
	PutFunctionConcurrency(ctx context.Context, config ConcurrencyConfig) error
	GetFunctionConcurrency(ctx context.Context, functionName string) (*ConcurrencyConfig, error)
	DeleteFunctionConcurrency(ctx context.Context, functionName string) error

	// Event Source Mappings
	CreateEventSourceMapping(ctx context.Context, config EventSourceMappingConfig) (*EventSourceMappingInfo, error)
	DeleteEventSourceMapping(ctx context.Context, uuid string) error
	GetEventSourceMapping(ctx context.Context, uuid string) (*EventSourceMappingInfo, error)
	ListEventSourceMappings(ctx context.Context, functionName string) ([]EventSourceMappingInfo, error)
	UpdateEventSourceMapping(ctx context.Context, uuid string, config EventSourceMappingConfig) (*EventSourceMappingInfo, error)
}
