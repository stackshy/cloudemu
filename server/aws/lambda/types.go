package lambda

// envEnvelope is the AWS Lambda Environment shape: {"Variables": {k: v}}.
type envEnvelope struct {
	Variables map[string]string `json:"Variables,omitempty"`
}

// functionConfiguration is the response body shared by Create / Get / Update.
// Field set is the minimum the AWS SDK populates for a function description.
type functionConfiguration struct {
	FunctionName string       `json:"FunctionName"`
	FunctionArn  string       `json:"FunctionArn"`
	Runtime      string       `json:"Runtime,omitempty"`
	Role         string       `json:"Role,omitempty"`
	Handler      string       `json:"Handler,omitempty"`
	Description  string       `json:"Description,omitempty"`
	MemorySize   int          `json:"MemorySize,omitempty"`
	Timeout      int          `json:"Timeout,omitempty"`
	LastModified string       `json:"LastModified,omitempty"`
	State        string       `json:"State,omitempty"`
	CodeSha256   string       `json:"CodeSha256,omitempty"`
	Environment  *envEnvelope `json:"Environment,omitempty"`
	PackageType  string       `json:"PackageType,omitempty"`
	Version      string       `json:"Version,omitempty"`
}

// updateFunctionConfigurationRequest captures the mutable fields of
// UpdateFunctionConfiguration (PUT .../{name}/configuration).
type updateFunctionConfigurationRequest struct {
	Runtime     string       `json:"Runtime"`
	Role        string       `json:"Role"`
	Handler     string       `json:"Handler"`
	Description string       `json:"Description"`
	MemorySize  int          `json:"MemorySize"`
	Timeout     int          `json:"Timeout"`
	Environment *envEnvelope `json:"Environment"`
}

// publishVersionRequest is the body of PublishVersion (POST .../{name}/versions).
type publishVersionRequest struct {
	Description string `json:"Description"`
}

// listVersionsResponse is the ListVersionsByFunction envelope.
type listVersionsResponse struct {
	Versions []functionConfiguration `json:"Versions"`
}

// aliasRequest is the body of Create/UpdateAlias.
type aliasRequest struct {
	Name            string `json:"Name"`
	FunctionVersion string `json:"FunctionVersion"`
	Description     string `json:"Description"`
}

// aliasResponse is the AWS AliasConfiguration shape.
type aliasResponse struct {
	AliasArn        string `json:"AliasArn"`
	Name            string `json:"Name"`
	FunctionVersion string `json:"FunctionVersion"`
	Description     string `json:"Description,omitempty"`
}

// listAliasesResponse is the ListAliases envelope.
type listAliasesResponse struct {
	Aliases []aliasResponse `json:"Aliases"`
}

// addPermissionRequest is the body of AddPermission (POST .../{name}/policy).
type addPermissionRequest struct {
	StatementID string `json:"StatementId"`
	Action      string `json:"Action"`
	Principal   string `json:"Principal"`
	SourceArn   string `json:"SourceArn"`
}

// functionResource is the shape returned by GetFunction:
// {Configuration, Code, Tags}. Code is a placeholder since the driver
// doesn't persist deployment artifacts.
type functionResource struct {
	Configuration functionConfiguration `json:"Configuration"`
	Code          codeLocation          `json:"Code,omitempty"`
	Tags          map[string]string     `json:"Tags,omitempty"`
}

type codeLocation struct {
	RepositoryType string `json:"RepositoryType,omitempty"`
	Location       string `json:"Location,omitempty"`
}

// listFunctionsResponse is the ListFunctions response envelope.
type listFunctionsResponse struct {
	Functions []functionConfiguration `json:"Functions"`
}

// createFunctionRequest captures the fields we read from a CreateFunction body.
// We deliberately ignore Role (no IAM evaluation), VPCConfig, etc — the portable
// driver doesn't model them. Code.ZipFile is read so a configured FunctionEngine
// can run the real handler; with no engine it is stored only for the invoke stub.
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
}

// functionCode is the deployment package in a CreateFunction body. The AWS SDK
// sends the zip as base64 in ZipFile, which Go unmarshals into []byte for us.
type functionCode struct {
	ZipFile []byte `json:"ZipFile"`
}
