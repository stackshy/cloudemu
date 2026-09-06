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
