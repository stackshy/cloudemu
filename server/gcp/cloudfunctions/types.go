package cloudfunctions

// cloudFunction is the v1 GCP Cloud Functions resource shape returned by
// Get / Create / Update.
type cloudFunction struct {
	Name                       string            `json:"name"`
	Description                string            `json:"description,omitempty"`
	SourceArchiveURL           string            `json:"sourceArchiveUrl,omitempty"`
	SourceUploadURL            string            `json:"sourceUploadUrl,omitempty"`
	SourceRepository           *sourceRepository `json:"sourceRepository,omitempty"`
	HTTPSTrigger               *httpsTrigger     `json:"httpsTrigger,omitempty"`
	EventTrigger               *eventTrigger     `json:"eventTrigger,omitempty"`
	Status                     string            `json:"status"`
	EntryPoint                 string            `json:"entryPoint,omitempty"`
	Runtime                    string            `json:"runtime,omitempty"`
	Timeout                    string            `json:"timeout,omitempty"`
	AvailableMemory            int               `json:"availableMemoryMb,omitempty"`
	MaxInstances               int               `json:"maxInstances,omitempty"`
	MinInstances               int               `json:"minInstances,omitempty"`
	VPCConnector               string            `json:"vpcConnector,omitempty"`
	VPCConnectorEgressSettings string            `json:"vpcConnectorEgressSettings,omitempty"`
	Labels                     map[string]string `json:"labels,omitempty"`
	EnvVariables               map[string]string `json:"environmentVariables,omitempty"`
	UpdateTime                 string            `json:"updateTime,omitempty"`
	VersionID                  string            `json:"versionId,omitempty"`
	ServiceAccountEmail        string            `json:"serviceAccountEmail,omitempty"`
	IngressSettings            string            `json:"ingressSettings,omitempty"`
	DockerRegistry             string            `json:"dockerRegistry,omitempty"`
	BuildID                    string            `json:"buildId,omitempty"`
}

type httpsTrigger struct {
	URL string `json:"url"`
}

// eventTrigger is the v1 CloudFunction.eventTrigger: an event-driven function is
// triggered by eventType on resource (via service) rather than over HTTP. gen1
// functions carry exactly one of httpsTrigger or eventTrigger.
type eventTrigger struct {
	EventType     string         `json:"eventType,omitempty"`
	Resource      string         `json:"resource,omitempty"`
	Service       string         `json:"service,omitempty"`
	FailurePolicy *failurePolicy `json:"failurePolicy,omitempty"`
}

// failurePolicy is the v1 eventTrigger.failurePolicy; a present retry object asks
// Cloud Functions to retry a failed event-driven invocation.
type failurePolicy struct {
	Retry map[string]any `json:"retry,omitempty"`
}

// sourceRepository is the v1 CloudFunction.sourceRepository: a Cloud Source
// Repositories deploy source. url is the input; deployedUrl is echoed back.
type sourceRepository struct {
	URL         string `json:"url,omitempty"`
	DeployedURL string `json:"deployedUrl,omitempty"`
}

// listFunctionsResponse is the {functions: [...]} envelope returned by
// projects.locations.functions.list.
type listFunctionsResponse struct {
	Functions     []cloudFunction `json:"functions"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
}

// testIamPermissionsRequest / testIamPermissionsResponse are the bodies of
// functions/{name}:testIamPermissions. Real GCP returns the subset of the
// requested permissions the caller holds; CloudEmu does not enforce IAM (any
// credential is treated as an owner) so it echoes back the full requested set.
type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions,omitempty"`
}

type testIamPermissionsResponse struct {
	Permissions []string `json:"permissions,omitempty"`
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
