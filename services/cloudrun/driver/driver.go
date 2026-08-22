// Package driver defines the minimal interface an in-memory GCP Cloud Run
// (Jobs) backend must implement. Cloud Run is GCP-only, so there is a single
// provider implementation (providers/gcp/cloudrun) rather than the usual
// three; the interface still lives here so the wire handler
// (server/gcp/cloudrun) depends on an abstraction rather than the concrete
// mock.
//
// Scope is Jobs — run-to-completion workloads — not Services (HTTP ingress /
// revisions). A Job stores a container template; running it creates an
// Execution whose tasks run the template's containers to completion.
package driver

import (
	"context"
	"time"
)

// CloudRun is the control-plane surface for Cloud Run Jobs.
type CloudRun interface {
	// CreateJob stores a job spec. It does not run anything — RunJob does.
	CreateJob(ctx context.Context, cfg JobConfig) (*Job, error)

	// GetJob returns a job by name. name may be the bare job id or a fully
	// qualified projects/…/jobs/{id} resource name.
	GetJob(ctx context.Context, name string) (*Job, error)

	// ListJobs returns every stored job.
	ListJobs(ctx context.Context) ([]Job, error)

	// DeleteJob removes a job and stops any container workloads its executions
	// started on a configured ContainerEngine.
	DeleteJob(ctx context.Context, name string) error

	// RunJob creates and runs one Execution of the named job, blocking until
	// its tasks complete, and returns the finished Execution.
	RunJob(ctx context.Context, name string) (*Execution, error)

	// GetExecution returns an execution by name (bare execution id or a fully
	// qualified …/executions/{id} resource name).
	GetExecution(ctx context.Context, name string) (*Execution, error)
}

// JobConfig is the input to CreateJob — the subset of the Cloud Run Job
// template CloudEmu models.
type JobConfig struct {
	Name        string
	Containers  []Container
	TaskCount   int // tasks per execution; defaults to 1 when non-positive
	Labels      map[string]string
	Annotations map[string]string
}

// Container is one container in a job's task template.
type Container struct {
	Name    string
	Image   string
	Command []string          // entrypoint override
	Args    []string          // arguments to the entrypoint
	Env     map[string]string // environment variables
}

// Job is a stored Cloud Run job. Name is the bare job id; the wire handler
// composes the fully qualified resource name from the request path.
type Job struct {
	Name           string
	UID            string
	Generation     int64
	CreateTime     time.Time
	UpdateTime     time.Time
	LaunchStage    string
	ExecutionCount int
	TaskCount      int
	Containers     []Container
	Labels         map[string]string
	Annotations    map[string]string
	Conditions     []Condition
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
