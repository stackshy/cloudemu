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

// Version-weight bounds real Lambda enforces on an alias's
// AdditionalVersionWeights: each weight is a traffic fraction in [0.0, 1.0], and
// the additional weights sum to at most 1.0 (the remainder is the primary
// version's share).
const (
	MinVersionWeight = 0.0
	MaxVersionWeight = 1.0
)

// AliasRoutingConfig defines weighted routing between versions. It mirrors the
// AWS Lambda AliasRoutingConfiguration shape: AdditionalVersionWeights maps a
// published version to the fraction of traffic (0.0-1.0) it receives; the
// remaining traffic goes to the alias's primary FunctionVersion.
type AliasRoutingConfig struct {
	AdditionalVersionWeights map[string]float64
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
	// VpcID is the VPC that AWS resolves from the configured subnets and echoes
	// back in VpcConfigResponse. This emulator does not model the EC2 subnet->VPC
	// mapping, so it is left empty unless a caller sets it.
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

// Destination is one async-invoke destination target (AWS Lambda Destination):
// an SQS queue, SNS topic, Lambda function, or EventBridge event bus ARN that a
// finished asynchronous invocation is routed to.
type Destination struct {
	Destination string // target ARN
}

// DestinationConfig routes the result of an asynchronous invocation (AWS Lambda
// DestinationConfig): OnSuccess receives successful invocations, OnFailure
// receives invocations that failed after exhausting their retries. Either may be
// nil (no destination configured for that outcome).
type DestinationConfig struct {
	OnSuccess *Destination
	OnFailure *Destination
}

// EventInvokeConfig is a function's asynchronous-invocation configuration (AWS
// Lambda PutFunctionEventInvokeConfig), scoped to a version or alias via
// Qualifier. It has no Azure Functions or GCP Cloud Functions equivalent, so it
// is kept off the portable Serverless interface and applied/read through an
// AWS-only optional interface, the same way Function URLs and DeadLetterConfig
// are.
type EventInvokeConfig struct {
	FunctionName string
	// Qualifier scopes the config to a published version or alias; empty (or
	// "$LATEST") is the unqualified function config.
	Qualifier string
	// FunctionArn is the qualified function ARN echoed back in the response.
	FunctionArn string
	// MaximumRetryAttempts is the number of times Lambda retries a failed
	// asynchronous invocation (0-2) before routing the event to the DLQ /
	// OnFailure destination. Nil means the AWS default of 2.
	MaximumRetryAttempts *int
	// MaximumEventAgeInSeconds is how long (60-21600) Lambda keeps an
	// unprocessed asynchronous event before discarding it. Nil means unset.
	MaximumEventAgeInSeconds *int
	// DestinationConfig routes the finished invocation's outcome to an
	// OnSuccess / OnFailure target. Nil means no destinations configured.
	DestinationConfig *DestinationConfig
	// LastModified is the RFC3339 timestamp of the last Put/Update.
	LastModified string
}

// EphemeralStorage is a function's /tmp size in MB (AWS Lambda EphemeralStorage).
// Real Lambda accepts 512–10240 and defaults to 512 when the client omits it.
type EphemeralStorage struct {
	Size int
}

// FunctionLayer is one layer version imported by a function, echoed back in the
// function configuration's Layers list (AWS Lambda Layer). CodeSize is the layer
// version's deployment-package size.
type FunctionLayer struct {
	ARN      string
	CodeSize int64
}

// AWSFunctionConfig bundles the AWS Lambda-only function settings — VpcConfig,
// DeadLetterConfig, TracingConfig, Architectures, EphemeralStorage and the
// imported Layers. These have no Azure Functions or GCP Cloud Functions
// equivalent, so they are kept off the shared FunctionConfig/FunctionInfo structs
// and applied/read back through an AWS-only optional interface (type-asserted by
// the AWS Lambda server handler), the same way Function URLs are exposed.
type AWSFunctionConfig struct {
	VPCConfig        *VPCConfig
	DeadLetterConfig *DeadLetterConfig
	TracingConfig    *TracingConfig
	// Architectures is the instruction-set list (["x86_64"] or ["arm64"]). Empty
	// means the handler emits the AWS default ["x86_64"].
	Architectures []string
	// EphemeralStorage is the /tmp size; nil means the handler emits the default 512.
	EphemeralStorage *EphemeralStorage
	// Layers is the ordered list of imported layer versions echoed back on the
	// function configuration.
	Layers []FunctionLayer
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
}

// InvokeInput configures a function invocation.
type InvokeInput struct {
	FunctionName string
	Payload      []byte
	InvokeType   string // "RequestResponse", "Event", or "DryRun"
	// Qualifier selects a published version (a numeric version string) or
	// alias to invoke instead of the mutable $LATEST code. Empty invokes
	// $LATEST, matching the AWS Lambda Invoke Qualifier parameter.
	Qualifier string
}

// InvokeOutput is the result of a function invocation.
type InvokeOutput struct {
	StatusCode int
	Payload    []byte
	Error      string
	// ExecutedVersion is the version that actually ran: the alias's target
	// version when Qualifier names an alias, the Qualifier itself when it
	// names a version, or "$LATEST" for an unqualified invoke. Mirrors the
	// AWS Lambda Invoke ExecutedVersion response field.
	ExecutedVersion string
	// Logs is the stdout/stderr the invocation produced on the real-engine
	// path (empty for stub/handler invocations). It is surfaced to the log
	// service (CloudWatch Logs / Cloud Logging / Log Analytics) rather than
	// returned on the wire.
	Logs string
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
