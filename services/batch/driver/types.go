package driver

import "encoding/json"

// Compute-environment types.
const (
	CETypeManaged   = "MANAGED"
	CETypeUnmanaged = "UNMANAGED"
)

// Resource states (compute environments and job queues).
const (
	StateEnabled  = "ENABLED"
	StateDisabled = "DISABLED"
)

// Compute-environment / job-queue status. The emulator provisions
// synchronously, so a freshly created resource is VALID immediately (real AWS
// transitions CREATING -> VALID asynchronously, which Terraform waits on).
const (
	StatusValid    = "VALID"
	StatusCreating = "CREATING"
	StatusDeleting = "DELETING"
	StatusInvalid  = "INVALID"
)

// Job-definition status. A registered revision is ACTIVE until deregistered.
const (
	JDStatusActive   = "ACTIVE"
	JDStatusInactive = "INACTIVE"
)

// Job-definition types.
const (
	JDTypeContainer = "container"
	JDTypeMultiNode = "multinode"
)

// ComputeEnvironment is the server-side detail of an AWS Batch compute
// environment (the DescribeComputeEnvironments ComputeEnvironmentDetail shape).
//
// ComputeResources is carried verbatim as the raw JSON the caller supplied so
// every nested field (type, min/max/desired vCpus, subnets, security groups,
// allocation strategy, instance types, …) round-trips exactly with no injected
// defaults that would show as Terraform drift.
type ComputeEnvironment struct {
	Name             string
	ARN              string
	EcsClusterARN    string
	Type             string
	State            string
	Status           string
	StatusReason     string
	ComputeResources json.RawMessage
	ServiceRole      string
	UUID             string
	UnmanagedvCpus   *int32
	Tags             map[string]string
}

// CreateComputeEnvironmentInput describes a compute environment to create.
type CreateComputeEnvironmentInput struct {
	Name             string
	Type             string
	State            string
	ComputeResources json.RawMessage
	ServiceRole      string
	UnmanagedvCpus   *int32
	Tags             map[string]string
}

// UpdateComputeEnvironmentInput describes an in-place change to a compute
// environment. ComputeResources carries the ComputeResourceUpdate the caller
// supplied; its non-null members are shallow-merged over the stored resources
// so unchanged fields (subnets, security groups, …) are preserved.
type UpdateComputeEnvironmentInput struct {
	Name             string
	State            string
	ServiceRole      string
	ComputeResources json.RawMessage
}

// ComputeEnvironmentOrder associates a compute environment with a job queue at
// a given priority order. The slice order is preserved on round-trip.
type ComputeEnvironmentOrder struct {
	Order              int32
	ComputeEnvironment string
}

// JobQueue is the server-side detail of an AWS Batch job queue
// (DescribeJobQueues JobQueueDetail shape).
type JobQueue struct {
	Name                    string
	ARN                     string
	State                   string
	Status                  string
	StatusReason            string
	Priority                int32
	ComputeEnvironmentOrder []ComputeEnvironmentOrder
	SchedulingPolicyARN     string
	Tags                    map[string]string
}

// CreateJobQueueInput describes a job queue to create.
type CreateJobQueueInput struct {
	Name                    string
	State                   string
	Priority                int32
	ComputeEnvironmentOrder []ComputeEnvironmentOrder
	SchedulingPolicyARN     string
	Tags                    map[string]string
}

// UpdateJobQueueInput describes an in-place change to a job queue. A nil
// pointer field means "unchanged"; a nil ComputeEnvironmentOrder likewise
// leaves the association untouched.
type UpdateJobQueueInput struct {
	Name                    string
	State                   *string
	Priority                *int32
	ComputeEnvironmentOrder []ComputeEnvironmentOrder
	SchedulingPolicyARN     *string
}

// JobDefinition is the server-side detail of one revision of an AWS Batch job
// definition (DescribeJobDefinitions JobDefinitionDetail shape).
//
// The nested container/node/orchestration/retry/timeout blobs are carried
// verbatim as raw JSON so they round-trip exactly (Terraform stores
// container_properties as a JSON string and diffs it semantically).
type JobDefinition struct {
	Name                 string
	ARN                  string
	Revision             int32
	Status               string
	Type                 string
	ContainerProperties  json.RawMessage
	NodeProperties       json.RawMessage
	EcsProperties        json.RawMessage
	EksProperties        json.RawMessage
	RetryStrategy        json.RawMessage
	Timeout              json.RawMessage
	Parameters           map[string]string
	PlatformCapabilities []string
	PropagateTags        *bool
	SchedulingPriority   *int32
	Tags                 map[string]string
}

// RegisterJobDefinitionInput describes a job definition revision to register.
type RegisterJobDefinitionInput struct {
	Name                 string
	Type                 string
	ContainerProperties  json.RawMessage
	NodeProperties       json.RawMessage
	EcsProperties        json.RawMessage
	EksProperties        json.RawMessage
	RetryStrategy        json.RawMessage
	Timeout              json.RawMessage
	Parameters           map[string]string
	PlatformCapabilities []string
	PropagateTags        *bool
	SchedulingPriority   *int32
	Tags                 map[string]string
}

// DescribeJobDefinitionsInput filters a DescribeJobDefinitions request. When
// JobDefinitions (ARNs or name:revision) is set those exact revisions are
// returned regardless of status; otherwise Name and Status (default ACTIVE)
// filter the set.
type DescribeJobDefinitionsInput struct {
	JobDefinitions []string
	Name           string
	Status         string
}

// SchedulingPolicy is the server-side detail of an AWS Batch scheduling policy
// (DescribeSchedulingPolicies SchedulingPolicyDetail shape). The fair-share and
// quota-share policy blobs are carried verbatim as raw JSON so every nested
// field round-trips exactly.
type SchedulingPolicy struct {
	Name             string
	ARN              string
	FairsharePolicy  json.RawMessage
	QuotaSharePolicy json.RawMessage
	Tags             map[string]string
}

// CreateSchedulingPolicyInput describes a scheduling policy to create.
type CreateSchedulingPolicyInput struct {
	Name             string
	FairsharePolicy  json.RawMessage
	QuotaSharePolicy json.RawMessage
	Tags             map[string]string
}

// UpdateSchedulingPolicyInput replaces a scheduling policy's fair-share or
// quota-share details. A nil blob leaves that policy unchanged.
type UpdateSchedulingPolicyInput struct {
	ARN              string
	FairsharePolicy  json.RawMessage
	QuotaSharePolicy json.RawMessage
}

// Job statuses (the JobStatus enum). A job moves SUBMITTED -> (PENDING while
// it waits on dependencies) -> RUNNABLE -> STARTING -> RUNNING and ends in
// SUCCEEDED or FAILED.
const (
	JobStatusSubmitted = "SUBMITTED"
	JobStatusPending   = "PENDING"
	JobStatusRunnable  = "RUNNABLE"
	JobStatusStarting  = "STARTING"
	JobStatusRunning   = "RUNNING"
	JobStatusSucceeded = "SUCCEEDED"
	JobStatusFailed    = "FAILED"
)

// Array-job dependency types (the ArrayJobDependency enum).
const (
	DependencyNToN       = "N_TO_N"
	DependencySequential = "SEQUENTIAL"
)

// ListJobs filter names (the KeyValuesPair names ListJobs accepts).
const (
	FilterJobName         = "JOB_NAME"
	FilterJobDefinition   = "JOB_DEFINITION"
	FilterBeforeCreatedAt = "BEFORE_CREATED_AT"
	FilterAfterCreatedAt  = "AFTER_CREATED_AT"
	FilterShareIdentifier = "SHARE_IDENTIFIER"
)

// JobDependency is one entry of a job's dependsOn list.
type JobDependency struct {
	JobID string
	Type  string
}

// ArrayProperties is the array-job section of a job. A parent carries Size and
// StatusSummary (child count per status); a child carries Index.
type ArrayProperties struct {
	Size          int32
	Index         *int32
	StatusSummary map[string]int32
}

// NodeDetails identifies one node of a multi-node parallel job: the node job
// "<jobId>#<NodeIndex>" that DescribeJobs and ListJobs (multiNodeJobId) return.
// NumNodes is the job's node count, which ListJobs reports in the node's
// JobSummary nodeProperties; DescribeJobs reports NodeIndex and IsMainNode.
type NodeDetails struct {
	NodeIndex  int32
	IsMainNode bool
	NumNodes   int32
}

// SubmitJobInput describes a job to submit. JobQueue and JobDefinition accept
// a name or ARN (a job definition with or without a revision). A nil ArraySize
// submits a single (non-array) job.
type SubmitJobInput struct {
	JobName                    string
	JobQueue                   string
	JobDefinition              string
	ArraySize                  *int32
	DependsOn                  []JobDependency
	Parameters                 map[string]string
	ContainerOverrides         json.RawMessage
	NodeOverrides              json.RawMessage
	EcsPropertiesOverride      json.RawMessage
	EksPropertiesOverride      json.RawMessage
	RetryStrategy              json.RawMessage
	Timeout                    json.RawMessage
	PropagateTags              *bool
	SchedulingPriorityOverride *int32
	ShareIdentifier            string
	Tags                       map[string]string
}

// Job is the server-side detail of an AWS Batch job as DescribeJobs reports it
// (JobDetail shape) at the instant it is observed. Times are epoch
// milliseconds; zero means unset. Container, NodeProperties, EcsProperties and
// EksProperties are the job definition's blobs (with container overrides
// applied) carried as raw JSON. NodeDetails is set only on a node of a
// multi-node parallel job.
type Job struct {
	JobID                string
	JobARN               string
	JobName              string
	JobQueue             string
	JobDefinition        string
	Status               string
	StatusReason         string
	CreatedAt            int64
	StartedAt            int64
	StoppedAt            int64
	DependsOn            []JobDependency
	ArrayProperties      *ArrayProperties
	NodeDetails          *NodeDetails
	Parameters           map[string]string
	Container            json.RawMessage
	NodeProperties       json.RawMessage
	EcsProperties        json.RawMessage
	EksProperties        json.RawMessage
	RetryStrategy        json.RawMessage
	Timeout              json.RawMessage
	PlatformCapabilities []string
	PropagateTags        *bool
	SchedulingPriority   *int32
	ShareIdentifier      string
	IsCancelled          bool
	IsTerminated         bool
	Tags                 map[string]string
}

// JobFilter is one ListJobs filter (KeyValuesPair).
type JobFilter struct {
	Name   string
	Values []string
}

// ListJobsInput selects the jobs ListJobs returns. Exactly one of JobQueue,
// ArrayJobID or MultiNodeJobID must be set. JobStatus defaults to RUNNING and
// is ignored when a filter (other than SHARE_IDENTIFIER) is given. A nil
// MaxResults pages at the service default; NextToken continues a listing.
type ListJobsInput struct {
	JobQueue       string
	ArrayJobID     string
	MultiNodeJobID string
	JobStatus      string
	Filters        []JobFilter
	MaxResults     *int32
	NextToken      string
}

// ListJobsOutput is one page of ListJobs. NextToken is empty on the last page.
type ListJobsOutput struct {
	Jobs      []Job
	NextToken string
}
