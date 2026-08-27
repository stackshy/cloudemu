package lambda

// envEnvelope is the AWS Lambda Environment shape: {"Variables": {k: v}}.
type envEnvelope struct {
	Variables map[string]string `json:"Variables,omitempty"`
}

// vpcConfigEnvelope is the Lambda VpcConfig request / VpcConfigResponse shape.
// Real AWS resolves VpcId from the configured SubnetIds and echoes it in the
// response; this emulator does not model the EC2 subnet->VPC mapping, so VpcId
// is left empty (not accepted on input, never mistaken for AWS-accurate output).
type vpcConfigEnvelope struct {
	SubnetIDs        []string `json:"SubnetIds,omitempty"`
	SecurityGroupIDs []string `json:"SecurityGroupIds,omitempty"`
	VpcID            string   `json:"VpcId,omitempty"`
}

// deadLetterConfigEnvelope is the Lambda DeadLetterConfig shape.
type deadLetterConfigEnvelope struct {
	TargetArn string `json:"TargetArn,omitempty"`
}

// tracingConfigEnvelope is the Lambda TracingConfig request / TracingConfigResponse
// shape. Mode is "Active" or "PassThrough".
type tracingConfigEnvelope struct {
	Mode string `json:"Mode,omitempty"`
}

// ephemeralStorageEnvelope is the Lambda EphemeralStorage request/response shape:
// the function's /tmp size in MB.
type ephemeralStorageEnvelope struct {
	Size int `json:"Size"`
}

// layerReference is one entry of the function configuration's Layers list (the
// AWS Layer output shape). Only Arn and CodeSize are modeled.
type layerReference struct {
	Arn      string `json:"Arn"`
	CodeSize int64  `json:"CodeSize,omitempty"`
}

// functionConfiguration is the response body shared by Create / Get / Update.
// Field set is the minimum the AWS SDK populates for a function description.
type functionConfiguration struct {
	FunctionName string `json:"FunctionName"`
	FunctionArn  string `json:"FunctionArn"`
	Runtime      string `json:"Runtime,omitempty"`
	Role         string `json:"Role,omitempty"`
	Handler      string `json:"Handler,omitempty"`
	Description  string `json:"Description,omitempty"`
	MemorySize   int    `json:"MemorySize,omitempty"`
	Timeout      int    `json:"Timeout,omitempty"`
	LastModified string `json:"LastModified,omitempty"`
	State        string `json:"State,omitempty"`
	// LastUpdateStatus is the terminal status of the last create/update ("Successful").
	// SDK waiters (FunctionUpdatedV2) poll GetFunctionConfiguration for it.
	LastUpdateStatus string                    `json:"LastUpdateStatus,omitempty"`
	CodeSha256       string                    `json:"CodeSha256,omitempty"`
	CodeSize         int64                     `json:"CodeSize,omitempty"`
	RevisionID       string                    `json:"RevisionId,omitempty"`
	Environment      *envEnvelope              `json:"Environment,omitempty"`
	PackageType      string                    `json:"PackageType,omitempty"`
	Version          string                    `json:"Version,omitempty"`
	VpcConfig        *vpcConfigEnvelope        `json:"VpcConfig,omitempty"`
	DeadLetterConfig *deadLetterConfigEnvelope `json:"DeadLetterConfig,omitempty"`
	TracingConfig    *tracingConfigEnvelope    `json:"TracingConfig,omitempty"`
	// Architectures echoes the function's instruction set (["x86_64"] or
	// ["arm64"]); EphemeralStorage its /tmp size; Layers the imported layer
	// versions. All default when the function was created without them.
	Architectures    []string                  `json:"Architectures,omitempty"`
	EphemeralStorage *ephemeralStorageEnvelope `json:"EphemeralStorage,omitempty"`
	Layers           []layerReference          `json:"Layers,omitempty"`
}

// updateFunctionConfigurationRequest captures the mutable fields of
// UpdateFunctionConfiguration (PUT .../{name}/configuration).
type updateFunctionConfigurationRequest struct {
	Runtime          string                    `json:"Runtime"`
	Role             string                    `json:"Role"`
	Handler          string                    `json:"Handler"`
	Description      string                    `json:"Description"`
	MemorySize       int                       `json:"MemorySize"`
	Timeout          int                       `json:"Timeout"`
	Environment      *envEnvelope              `json:"Environment"`
	VpcConfig        *vpcConfigEnvelope        `json:"VpcConfig"`
	DeadLetterConfig *deadLetterConfigEnvelope `json:"DeadLetterConfig"`
	TracingConfig    *tracingConfigEnvelope    `json:"TracingConfig"`
	// Layers replaces the function's imported layer versions (their ARNs).
	Layers []string `json:"Layers"`
}

// updateFunctionCodeRequest captures the deployment-package fields of
// UpdateFunctionCode (PUT .../{name}/code). Like CreateFunction, an inline
// ZipFile arrives base64-decoded into []byte, or an uploaded artifact is
// referenced via S3Bucket/S3Key. Layers, when present, are overlaid into the
// package the same way the create path does.
type updateFunctionCodeRequest struct {
	ZipFile         []byte   `json:"ZipFile"`
	S3Bucket        string   `json:"S3Bucket"`
	S3Key           string   `json:"S3Key"`
	S3ObjectVersion string   `json:"S3ObjectVersion"`
	Layers          []string `json:"Layers"`
	// Publish, when true, cuts a new version from the updated code and returns
	// that version's configuration (qualified ARN), matching AWS.
	Publish bool `json:"Publish"`
}

// publishVersionRequest is the body of PublishVersion (POST .../{name}/versions).
type publishVersionRequest struct {
	Description string `json:"Description"`
}

// listVersionsResponse is the ListVersionsByFunction envelope.
type listVersionsResponse struct {
	Versions   []functionConfiguration `json:"Versions"`
	NextMarker string                  `json:"NextMarker,omitempty"`
}

// aliasRequest is the body of Create/UpdateAlias.
type aliasRequest struct {
	Name            string              `json:"Name"`
	FunctionVersion string              `json:"FunctionVersion"`
	Description     string              `json:"Description"`
	RoutingConfig   *aliasRoutingConfig `json:"RoutingConfig"`
}

// aliasResponse is the AWS AliasConfiguration shape.
type aliasResponse struct {
	AliasArn        string              `json:"AliasArn"`
	Name            string              `json:"Name"`
	FunctionVersion string              `json:"FunctionVersion"`
	Description     string              `json:"Description,omitempty"`
	RevisionID      string              `json:"RevisionId,omitempty"`
	RoutingConfig   *aliasRoutingConfig `json:"RoutingConfig,omitempty"`
}

// aliasRoutingConfig is the AWS AliasRoutingConfiguration shape: a map of
// additional version -> weight.
type aliasRoutingConfig struct {
	AdditionalVersionWeights map[string]float64 `json:"AdditionalVersionWeights,omitempty"`
}

// listAliasesResponse is the ListAliases envelope.
type listAliasesResponse struct {
	Aliases    []aliasResponse `json:"Aliases"`
	NextMarker string          `json:"NextMarker,omitempty"`
}

// addPermissionRequest is the body of AddPermission (POST .../{name}/policy).
type addPermissionRequest struct {
	StatementID string `json:"StatementId"`
	Action      string `json:"Action"`
	Principal   string `json:"Principal"`
	SourceArn   string `json:"SourceArn"`
}

// functionResource is the shape returned by GetFunction:
// {Configuration, Code, Concurrency, Tags}. Code is a placeholder since the
// driver doesn't persist deployment artifacts. Concurrency is present only once
// reserved concurrency has been set via PutFunctionConcurrency, matching AWS.
type functionResource struct {
	Configuration functionConfiguration `json:"Configuration"`
	Code          codeLocation          `json:"Code,omitempty"`
	Concurrency   *concurrencyEnvelope  `json:"Concurrency,omitempty"`
	Tags          map[string]string     `json:"Tags,omitempty"`
}

// concurrencyEnvelope is the GetFunction Concurrency object. Terraform/CDK read
// reserved_concurrent_executions from it.
type concurrencyEnvelope struct {
	ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
}

type codeLocation struct {
	RepositoryType string `json:"RepositoryType,omitempty"`
	Location       string `json:"Location,omitempty"`
}

// listFunctionsResponse is the ListFunctions response envelope.
type listFunctionsResponse struct {
	Functions  []functionConfiguration `json:"Functions"`
	NextMarker string                  `json:"NextMarker,omitempty"`
}

// createFunctionRequest captures the fields we read from a CreateFunction body.
// Role/Description are stored and echoed back (Terraform reads Role every plan)
// though IAM is not evaluated; VpcConfig/DeadLetterConfig/TracingConfig are also
// stored and echoed back by Get. Code.ZipFile is read so a configured
// FunctionEngine can run the real handler; with no engine it is stored only for
// the invoke stub.
type createFunctionRequest struct {
	FunctionName string            `json:"FunctionName"`
	Runtime      string            `json:"Runtime"`
	Role         string            `json:"Role"`
	Handler      string            `json:"Handler"`
	Description  string            `json:"Description"`
	MemorySize   int               `json:"MemorySize"`
	Timeout      int               `json:"Timeout"`
	Environment  *envEnvelope      `json:"Environment"`
	Tags         map[string]string `json:"Tags"`
	PackageType  string            `json:"PackageType"`
	Code         *functionCode     `json:"Code"`
	// Layers is the list of layer version ARNs the function imports. Their code
	// is overlaid into the deployment package so imports resolve at real invoke.
	Layers           []string                  `json:"Layers"`
	VpcConfig        *vpcConfigEnvelope        `json:"VpcConfig"`
	DeadLetterConfig *deadLetterConfigEnvelope `json:"DeadLetterConfig"`
	TracingConfig    *tracingConfigEnvelope    `json:"TracingConfig"`
	Architectures    []string                  `json:"Architectures"`
	EphemeralStorage *ephemeralStorageEnvelope `json:"EphemeralStorage"`
	// Publish, when true, publishes version 1 from the created function and
	// returns that version's configuration (Version "1", qualified ARN).
	Publish bool `json:"Publish"`
}

// functionCode is the deployment package in a CreateFunction body. The AWS SDK
// sends an inline zip as base64 in ZipFile, which Go unmarshals into []byte for
// us. Terraform/SAM/CDK instead upload the artifact to S3 and reference it via
// S3Bucket/S3Key; those are fetched from the in-process S3 backend so real code
// runs instead of falling back to the echo stub.
type functionCode struct {
	ZipFile         []byte `json:"ZipFile"`
	S3Bucket        string `json:"S3Bucket"`
	S3Key           string `json:"S3Key"`
	S3ObjectVersion string `json:"S3ObjectVersion"`
}
