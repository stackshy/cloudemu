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
// We deliberately ignore Code, Role (no IAM evaluation), VPCConfig, etc — the
// portable driver doesn't model them.
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
}
