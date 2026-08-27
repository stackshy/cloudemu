// Package driver defines the minimal interface an in-memory GCP Cloud Run
// backend must implement. Cloud Run is GCP-only, so there is a single provider
// implementation (providers/gcp/cloudrun) rather than the usual three; the
// interface still lives here so the wire handler (server/gcp/cloudrun) depends
// on an abstraction rather than the concrete mock.
//
// Scope covers both Cloud Run surfaces: Jobs (run-to-completion workloads —
// a Job stores a container template; running it creates an Execution whose
// tasks run the template's containers to completion) and Services (HTTP
// ingress workloads — a Service stores a revision template; creating or
// updating it materializes a Revision and serves traffic at a stable URL).
package driver

import (
	"context"
	"time"
)

// CloudRun is the control-plane surface for Cloud Run Jobs and Services.
type CloudRun interface {
	// CreateJob stores a job spec. It does not run anything — RunJob does.
	CreateJob(ctx context.Context, cfg JobConfig) (*Job, error)

	// GetJob returns a job by name. name may be the bare job id or a fully
	// qualified projects/…/jobs/{id} resource name.
	GetJob(ctx context.Context, name string) (*Job, error)

	// ListJobs returns every stored job.
	ListJobs(ctx context.Context) ([]Job, error)

	// UpdateJob applies an in-place update to an existing job, bumping its
	// generation, and returns the mutated job.
	UpdateJob(ctx context.Context, cfg JobConfig) (*Job, error)

	// DeleteJob removes a job and stops any container workloads its executions
	// started on a configured ContainerEngine.
	DeleteJob(ctx context.Context, name string) error

	// RunJob creates and runs one Execution of the named job, blocking until
	// its tasks complete, and returns the finished Execution.
	RunJob(ctx context.Context, name string) (*Execution, error)

	// GetExecution returns an execution by name (bare execution id or a fully
	// qualified …/executions/{id} resource name).
	GetExecution(ctx context.Context, name string) (*Execution, error)

	// ListExecutions returns every stored execution of the named job.
	ListExecutions(ctx context.Context, jobName string) ([]Execution, error)

	// CreateService stores a service spec and materializes its first Revision,
	// returning the reconciled service serving traffic at a stable URL.
	CreateService(ctx context.Context, cfg ServiceConfig) (*Service, error)

	// GetService returns a service by name (bare service id or a fully
	// qualified projects/…/services/{id} resource name).
	GetService(ctx context.Context, name string) (*Service, error)

	// ListServices returns every stored service.
	ListServices(ctx context.Context) ([]Service, error)

	// UpdateService applies an in-place update, materializing a new Revision
	// and bumping the service generation, and returns the reconciled service.
	UpdateService(ctx context.Context, cfg ServiceConfig) (*Service, error)

	// DeleteService removes a service and its revisions.
	DeleteService(ctx context.Context, name string) error

	// ListRevisions returns every stored revision of the named service.
	ListRevisions(ctx context.Context, serviceName string) ([]Revision, error)

	// GetRevision returns a revision by name (bare revision id or a fully
	// qualified …/revisions/{id} resource name).
	GetRevision(ctx context.Context, name string) (*Revision, error)

	// DeleteRevision removes a single revision of a service.
	DeleteRevision(ctx context.Context, name string) error
}

// JobConfig is the input to CreateJob / UpdateJob — the subset of the Cloud Run
// Job template CloudEmu models.
type JobConfig struct {
	Name                 string
	Containers           []Container
	TaskCount            int // tasks per execution; defaults to 1 when non-positive
	Parallelism          int
	MaxRetries           int
	Timeout              string // task timeout as a duration string, e.g. "600s"
	ServiceAccount       string
	ExecutionEnvironment string // EXECUTION_ENVIRONMENT_GEN1 / _GEN2
	VPCAccess            *VpcAccess
	Labels               map[string]string
	Annotations          map[string]string
}

// Container is one container in a job or service task template.
type Container struct {
	Name      string
	Image     string
	Command   []string              // entrypoint override
	Args      []string              // arguments to the entrypoint
	Env       []EnvVar              // environment variables, in declaration order
	Ports     []ContainerPort       // container ports (services)
	Resources *ResourceRequirements // cpu/memory limits and CPU behavior
}

// EnvVar is one container environment variable. Cloud Run env is an ordered
// list (not a map), so it round-trips in declaration order across GETs.
type EnvVar struct {
	Name  string
	Value string
}

// ContainerPort is one exposed container port. Name is optional and selects the
// application protocol (e.g. "h2c" for HTTP/2 cleartext); ContainerPort is the
// port the container listens on.
type ContainerPort struct {
	Name          string
	ContainerPort int
}

// ResourceRequirements is a container's compute allocation — the limits map
// (e.g. {"cpu":"1000m","memory":"512Mi"}) plus CPU behavior toggles. Every
// Terraform/gcloud deploy sends and reads these back, so they must round-trip.
type ResourceRequirements struct {
	Limits          map[string]string
	CPUIdle         *bool
	StartupCPUBoost *bool
}

// VpcAccess is the VPC connectivity configuration of a job or service template.
type VpcAccess struct {
	Connector         string
	Egress            string // ALL_TRAFFIC / PRIVATE_RANGES_ONLY
	NetworkInterfaces []VpcNetworkInterface
}

// VpcNetworkInterface is one direct-VPC network interface.
type VpcNetworkInterface struct {
	Network    string
	Subnetwork string
	Tags       []string
}

// Job is a stored Cloud Run job. Name is the bare job id; the wire handler
// composes the fully qualified resource name from the request path.
type Job struct {
	Name                   string
	UID                    string
	Generation             int64
	ObservedGeneration     int64
	CreateTime             time.Time
	UpdateTime             time.Time
	LaunchStage            string
	ExecutionCount         int
	TaskCount              int
	Parallelism            int
	MaxRetries             int
	Timeout                string
	ServiceAccount         string
	ExecutionEnvironment   string
	VPCAccess              *VpcAccess
	Containers             []Container
	Labels                 map[string]string
	Annotations            map[string]string
	Conditions             []Condition
	TerminalCondition      *Condition
	LatestCreatedExecution *ExecutionReference
	Etag                   string
	Reconciling            bool
}

// ExecutionReference is the LatestCreatedExecution summary carried on a Job.
type ExecutionReference struct {
	Name           string
	CreateTime     time.Time
	CompletionTime time.Time
}

// Execution is one run-to-completion run of a job. Its counts reflect the
// observed outcome of its tasks: a task whose containers all exit 0 succeeds,
// otherwise it fails.
type Execution struct {
	Name           string
	JobName        string
	UID            string
	Generation     int64
	CreateTime     time.Time
	StartTime      time.Time
	CompletionTime time.Time
	TaskCount      int
	SucceededCount int
	FailedCount    int
	RunningCount   int
	CancelledCount int
	Tasks          []Task
	Containers     []Container
	Conditions     []Condition
	LogURI         string
}

// Task is a single task instance within an execution. Cloud Run runs TaskCount
// tasks; each runs the execution's containers to completion.
type Task struct {
	Index      int
	State      string // "Succeeded" or "Failed"
	ExitCode   int    // the highest container exit code observed for the task
	Containers []ContainerStatus
}

// ContainerStatus is the observed outcome of one container in a task.
type ContainerStatus struct {
	Name     string
	State    string // "exited", "running", …
	ExitCode int
}

// Condition is a Cloud Run status condition (a subset of the real shape).
type Condition struct {
	Type    string // e.g. "Completed", "Ready"
	State   string // "CONDITION_SUCCEEDED", "CONDITION_FAILED", "CONDITION_PENDING"
	Message string
	Reason  string
}

// ServiceConfig is the input to CreateService / UpdateService — the subset of
// the Cloud Run Service the emulator models.
type ServiceConfig struct {
	Name                 string
	Location             string // GCP region from the request path, used for the *.run.app URL
	Description          string
	Ingress              string // INGRESS_TRAFFIC_ALL / _INTERNAL_ONLY / _INTERNAL_LOAD_BALANCER
	LaunchStage          string
	Containers           []Container
	ServiceAccount       string
	Timeout              string
	ExecutionEnvironment string
	VPCAccess            *VpcAccess
	Scaling              *ServiceScaling
	Traffic              []TrafficTarget
	Labels               map[string]string
	Annotations          map[string]string
	TemplateLabels       map[string]string // revisionTemplate.labels
	TemplateAnnotations  map[string]string // revisionTemplate.annotations (autoscaling.knative.dev/*)
	// UpdateMask names the top-level (or dotted template.*) field paths a PATCH
	// touches. Empty means full replace (a maskless PUT, as Terraform sends);
	// non-empty means merge only the named paths onto the existing service.
	UpdateMask []string
}

// ServiceScaling is a service's revision-template scaling bounds.
type ServiceScaling struct {
	MinInstanceCount int
	MaxInstanceCount int
}

// TrafficTarget assigns a percentage (or a latest pointer) of ingress traffic
// to a revision, optionally under a named tag.
type TrafficTarget struct {
	Type     string // TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST / _REVISION
	Revision string
	Percent  int
	Tag      string
	URI      string // populated on the observed traffic status
}

// Service is a stored Cloud Run service serving HTTP ingress. Name is the bare
// service id; the wire handler composes the fully qualified resource name.
type Service struct {
	Name                  string
	UID                   string
	Generation            int64
	ObservedGeneration    int64
	CreateTime            time.Time
	UpdateTime            time.Time
	LaunchStage           string
	Description           string
	Ingress               string
	Containers            []Container
	ServiceAccount        string
	Timeout               string
	ExecutionEnvironment  string
	VPCAccess             *VpcAccess
	Scaling               *ServiceScaling
	Traffic               []TrafficTarget
	TrafficStatuses       []TrafficTarget
	URI                   string
	LatestReadyRevision   string
	LatestCreatedRevision string
	Labels                map[string]string
	Annotations           map[string]string
	TemplateLabels        map[string]string // revisionTemplate.labels
	TemplateAnnotations   map[string]string // revisionTemplate.annotations
	Conditions            []Condition
	TerminalCondition     *Condition
	Etag                  string
	Reconciling           bool
}

// Revision is one immutable revision materialized from a service's template.
type Revision struct {
	Name                 string
	UID                  string
	Generation           int64
	Service              string
	CreateTime           time.Time
	UpdateTime           time.Time
	LaunchStage          string
	Containers           []Container
	ServiceAccount       string
	Timeout              string
	ExecutionEnvironment string
	VPCAccess            *VpcAccess
	Scaling              *ServiceScaling
	Conditions           []Condition
	Etag                 string
}
