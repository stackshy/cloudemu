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
	// RevisionID is the revision the version was published from.
	RevisionID string
	// Config fields snapshotted at publish time. A published version is
	// immutable, so these reflect the function state when it was cut, not the
	// current $LATEST configuration.
	Runtime string
	Handler string
	Memory  int
	Timeout int
	Role    string
}

// PermissionStatement is one statement of a function's resource-based policy,
// added via AddPermission (Terraform's aws_lambda_permission).
type PermissionStatement struct {
	StatementID string
	Action      string
	Principal   string
	SourceARN   string
}

// FunctionURLConfig is a Lambda Function URL configuration. Function URLs are
// an AWS-specific concept (not part of the portable Serverless interface), so
// providers expose them through an optional type assertion rather than the
// shared driver.
type FunctionURLConfig struct {
	FunctionName string
	Qualifier    string
	FunctionArn  string
	FunctionURL  string
	AuthType     string // "NONE" or "AWS_IAM"
	InvokeMode   string // "BUFFERED" or "RESPONSE_STREAM"
	Cors         *FunctionURLCors
	CreationTime string
	LastModified string
}

// FunctionURLCors is the CORS configuration of a Function URL.
type FunctionURLCors struct {
	AllowCredentials bool
	AllowHeaders     []string
	AllowMethods     []string
	AllowOrigins     []string
	ExposeHeaders    []string
	MaxAge           int
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
	// RevisionID changes on every alias mutation (create/update).
	RevisionID string
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

// VPCConfig is a function's networking configuration (AWS Lambda VpcConfig).
type VPCConfig struct {
	SubnetIDs        []string
	SecurityGroupIDs []string
	// VpcID is derived by AWS from the subnets and echoed back in the response.
	VpcID string
}

// DeadLetterConfig is a function's dead-letter queue target (AWS Lambda).
type DeadLetterConfig struct {
	TargetArn string
}

// TracingConfig is a function's AWS X-Ray tracing configuration. Mode is
// "Active" or "PassThrough" (the create-time default).
type TracingConfig struct {
	Mode string
}

// FunctionConfig describes a serverless function to create.
type FunctionConfig struct {
	Name        string
	Runtime     string
	Handler     string
	Memory      int // MB
	Timeout     int // seconds
	Role        string
	Description string
	Environment map[string]string
	Tags        map[string]string
	Code        []byte // deployment package (.zip); deployed to a FunctionEngine (if configured) on create/update to run real code
	// Framework selects the FunctionEngine invocation contract. "" (default) is
	// the event contract fn(event, context) used by AWS Lambda / Azure
	// Functions; "http" is the functions-framework request/response contract
	// used by GCP Cloud Functions gen1.
	Framework string
	// VpcConfig, DeadLetterConfig and TracingConfig are AWS Lambda function
	// settings stored at create/update and echoed back by Get. Nil means the
	// client omitted them. AWS defaults TracingConfig to {Mode: "PassThrough"}.
	VpcConfig        *VPCConfig
	DeadLetterConfig *DeadLetterConfig
	TracingConfig    *TracingConfig
}

// FunctionInfo describes a serverless function.
type FunctionInfo struct {
	Name         string
	ARN          string
	Runtime      string
	Handler      string
	Role         string
	Description  string
	Memory       int
	Timeout      int
	State        string
	Environment  map[string]string
	Tags         map[string]string
	LastModified string
	// CodeSHA256 is the base64-encoded SHA-256 of the deployment package, the
	// value Terraform compares against its locally computed source_code_hash.
	CodeSHA256 string
	// CodeSize is the deployment package size in bytes.
	CodeSize int64
	// Version is the function's published version ("$LATEST" for the mutable
	// current code).
	Version string
	// RevisionID changes on every configuration or code update.
	RevisionID string
	// VpcConfig, DeadLetterConfig and TracingConfig echo the AWS Lambda settings
	// supplied at create/update. TracingConfig is always populated (defaulting to
	// {Mode: "PassThrough"}); the others are nil when never set.
	VpcConfig        *VPCConfig
	DeadLetterConfig *DeadLetterConfig
	TracingConfig    *TracingConfig
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
	UUID           string
	EventSourceArn string
	FunctionName   string
	// FunctionArn is the full ARN of the target function, resolved at create
	// time. The Lambda wire protocol returns the ARN, not the bare name.
	FunctionArn      string
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
