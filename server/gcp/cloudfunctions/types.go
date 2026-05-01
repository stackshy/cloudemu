package cloudfunctions

// cloudFunction is the v1 GCP Cloud Functions resource shape returned by
// Get / Create / Update.
type cloudFunction struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	SourceArchiveURL string            `json:"sourceArchiveUrl,omitempty"`
	HTTPSTrigger     *httpsTrigger     `json:"httpsTrigger,omitempty"`
	Status           string            `json:"status"`
	EntryPoint       string            `json:"entryPoint,omitempty"`
	Runtime          string            `json:"runtime,omitempty"`
	Timeout          string            `json:"timeout,omitempty"`
	AvailableMemory  int               `json:"availableMemoryMb,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	EnvVariables     map[string]string `json:"environmentVariables,omitempty"`
	UpdateTime       string            `json:"updateTime,omitempty"`
	VersionID        string            `json:"versionId,omitempty"`
}

type httpsTrigger struct {
	URL string `json:"url"`
}

// listFunctionsResponse is the {functions: [...]} envelope returned by
// projects.locations.functions.list.
type listFunctionsResponse struct {
	Functions []cloudFunction `json:"functions"`
}

// operation is the google.longrunning.Operation envelope used by mutating
// endpoints. Real GCP returns done=false initially and clients poll; our mock
// returns done=true immediately so SDKs see completion on the first call.
type operation struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Done     bool           `json:"done"`
	Response map[string]any `json:"response,omitempty"`
	Error    *opError       `json:"error,omitempty"`
}

type opError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callRequest is the body of POST functions/{name}:call.
type callRequest struct {
	Data string `json:"data"`
}

// callResponse is the body of a successful :call.
type callResponse struct {
	ExecutionID string `json:"executionId"`
	Result      string `json:"result,omitempty"`
	Error       string `json:"error,omitempty"`
}
