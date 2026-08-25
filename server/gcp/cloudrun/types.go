package cloudrun

// jobResource is the google.cloud.run.v2.Job wire shape (the subset CloudEmu
// models) returned by Get and inlined in create's Operation response.
type jobResource struct {
	Name                   string              `json:"name"`
	UID                    string              `json:"uid,omitempty"`
	Generation             string              `json:"generation,omitempty"`
	CreateTime             string              `json:"createTime,omitempty"`
	UpdateTime             string              `json:"updateTime,omitempty"`
	LaunchStage            string              `json:"launchStage,omitempty"`
	ExecutionCount         int                 `json:"executionCount,omitempty"`
	Labels                 map[string]string   `json:"labels,omitempty"`
	Annotations            map[string]string   `json:"annotations,omitempty"`
	Template               execTemplate        `json:"template"`
	ObservedGeneration     string              `json:"observedGeneration,omitempty"`
	TerminalCondition      *condition          `json:"terminalCondition,omitempty"`
	Conditions             []condition         `json:"conditions,omitempty"`
	LatestCreatedExecution *executionReference `json:"latestCreatedExecution,omitempty"`
	Reconciling            bool                `json:"reconciling,omitempty"`
	Etag                   string              `json:"etag,omitempty"`
}

// execTemplate is Job.template — an ExecutionTemplate wrapping a TaskTemplate.
type execTemplate struct {
	Parallelism int          `json:"parallelism,omitempty"`
	TaskCount   int          `json:"taskCount,omitempty"`
	Template    taskTemplate `json:"template"`
}

// taskTemplate is the per-task container spec plus its execution settings.
type taskTemplate struct {
	Containers           []container `json:"containers,omitempty"`
	MaxRetries           int         `json:"maxRetries,omitempty"`
	Timeout              string      `json:"timeout,omitempty"`
	ServiceAccount       string      `json:"serviceAccount,omitempty"`
	ExecutionEnvironment string      `json:"executionEnvironment,omitempty"`
	VPCAccess            *vpcAccess  `json:"vpcAccess,omitempty"`
}

type container struct {
	Name    string          `json:"name,omitempty"`
	Image   string          `json:"image"`
	Command []string        `json:"command,omitempty"`
	Args    []string        `json:"args,omitempty"`
	Env     []envVar        `json:"env,omitempty"`
	Ports   []containerPort `json:"ports,omitempty"`
}

type containerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"containerPort,omitempty"`
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type vpcAccess struct {
	Connector         string             `json:"connector,omitempty"`
	Egress            string             `json:"egress,omitempty"`
	NetworkInterfaces []networkInterface `json:"networkInterfaces,omitempty"`
}

type networkInterface struct {
	Network    string   `json:"network,omitempty"`
	Subnetwork string   `json:"subnetwork,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

type condition struct {
	Type    string `json:"type,omitempty"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// executionReference is Job.latestCreatedExecution.
type executionReference struct {
	Name           string `json:"name,omitempty"`
	CreateTime     string `json:"createTime,omitempty"`
	CompletionTime string `json:"completionTime,omitempty"`
}

// executionResource is the google.cloud.run.v2.Execution wire shape inlined in
// the :run Operation response and returned by executions.get.
type executionResource struct {
	Name           string       `json:"name"`
	UID            string       `json:"uid,omitempty"`
	Generation     string       `json:"generation,omitempty"`
	Job            string       `json:"job,omitempty"`
	CreateTime     string       `json:"createTime,omitempty"`
	StartTime      string       `json:"startTime,omitempty"`
	CompletionTime string       `json:"completionTime,omitempty"`
	TaskCount      int          `json:"taskCount,omitempty"`
	SucceededCount int          `json:"succeededCount,omitempty"`
	FailedCount    int          `json:"failedCount,omitempty"`
	RunningCount   int          `json:"runningCount,omitempty"`
	CancelledCount int          `json:"cancelledCount,omitempty"`
	LogURI         string       `json:"logUri,omitempty"`
	Template       taskTemplate `json:"template,omitempty"`
	Conditions     []condition  `json:"conditions,omitempty"`
}

// listJobsResponse is the {jobs: [...]} envelope from jobs.list.
type listJobsResponse struct {
	Jobs          []jobResource `json:"jobs"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

// listExecutionsResponse is the {executions: [...]} envelope from
// jobs.executions.list.
type listExecutionsResponse struct {
	Executions    []executionResource `json:"executions"`
	NextPageToken string              `json:"nextPageToken,omitempty"`
}

// operation is the google.longrunning.Operation envelope every mutating
// endpoint returns. Real Cloud Run returns done=false and clients poll; the
// emulator completes synchronously and returns done=true with the result
// inlined in response.
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
