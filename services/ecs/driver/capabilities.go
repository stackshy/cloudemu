package driver

import "context"

// The interfaces in this file are optional capabilities of an ECS provider:
// the wire handler discovers them by type assertion, so they extend the ECS
// interface without widening it.

// ProtectedTask is a task's scale-in protection state. ExpirationDate is an
// RFC3339 instant, empty when protection is disabled or has expired.
type ProtectedTask struct {
	TaskARN           string
	ProtectionEnabled bool
	ExpirationDate    string
}

// UpdateTaskProtectionInput describes a scale-in protection change. A nil
// ExpiresInMinutes means the default (120 minutes) when enabling.
type UpdateTaskProtectionInput struct {
	Cluster           string
	Tasks             []string
	ProtectionEnabled bool
	ExpiresInMinutes  *int
}

// TaskProtection reads and changes the scale-in protection of service tasks.
type TaskProtection interface {
	GetTaskProtection(ctx context.Context, cluster string, tasks []string) ([]ProtectedTask, []Failure, error)
	UpdateTaskProtection(ctx context.Context, in UpdateTaskProtectionInput) ([]ProtectedTask, []Failure, error)
}

// ScaleUnitPercent is the only task-set scale unit ECS models.
const ScaleUnitPercent = "PERCENT"

// Scale is the share of a service's desired count a task set runs. Value is
// 0-100 and Unit is ScaleUnitPercent.
type Scale struct {
	Unit  string
	Value float64
}

// TaskSet is a set of tasks in a service that uses the EXTERNAL deployment
// controller. ComputedDesiredCount is the service desired count scaled by Scale
// and rounded up. Status is PRIMARY, ACTIVE or DRAINING; StabilityStatus is
// STEADY_STATE or STABILIZING. ClientToken is the creation idempotency token.
type TaskSet struct {
	ID                       string
	ARN                      string
	ServiceARN               string
	ClusterARN               string
	ExternalID               string
	TaskDefinition           string
	Status                   string
	StabilityStatus          string
	StabilityStatusAt        string
	LaunchType               string
	PlatformVersion          string
	PlatformFamily           string
	CapacityProviderStrategy []CapacityProviderStrategyItem
	NetworkConfiguration     *NetworkConfiguration
	LoadBalancers            []LoadBalancer
	ServiceRegistries        []ServiceRegistry
	Scale                    Scale
	ComputedDesiredCount     int
	PendingCount             int
	RunningCount             int
	StartedBy                string
	ClientToken              string
	CreatedAt                string
	UpdatedAt                string
	Tags                     []Tag
}

// CreateTaskSetInput describes a task set to create. A nil Scale means 100
// percent.
type CreateTaskSetInput struct {
	Cluster                  string
	Service                  string
	TaskDefinition           string
	ExternalID               string
	LaunchType               string
	PlatformVersion          string
	CapacityProviderStrategy []CapacityProviderStrategyItem
	NetworkConfiguration     *NetworkConfiguration
	LoadBalancers            []LoadBalancer
	ServiceRegistries        []ServiceRegistry
	Scale                    *Scale
	ClientToken              string
	Tags                     []Tag
}

// UpdateTaskSetInput changes a task set's scale, the only mutable field.
type UpdateTaskSetInput struct {
	Cluster string
	Service string
	TaskSet string
	Scale   Scale
}

// DeleteTaskSetInput identifies a task set to delete. Force is accepted for API
// compatibility and has no visible effect: the set drains immediately either way.
type DeleteTaskSetInput struct {
	Cluster string
	Service string
	TaskSet string
	Force   bool
}

// TaskSets manages task sets, the task groups of EXTERNAL-controller services.
type TaskSets interface {
	CreateTaskSet(ctx context.Context, in CreateTaskSetInput) (*TaskSet, error)
	UpdateTaskSet(ctx context.Context, in UpdateTaskSetInput) (*TaskSet, error)
	DeleteTaskSet(ctx context.Context, in DeleteTaskSetInput) (*TaskSet, error)
	DescribeTaskSets(ctx context.Context, cluster, service string, ids []string) ([]TaskSet, []Failure, error)
	UpdateServicePrimaryTaskSet(ctx context.Context, cluster, service, primaryTaskSet string) (*TaskSet, error)
}

// Service deployment statuses (the ECS ServiceDeploymentStatus enum).
const (
	DeploymentStatusPending            = "PENDING"
	DeploymentStatusSuccessful         = "SUCCESSFUL"
	DeploymentStatusStopped            = "STOPPED"
	DeploymentStatusStopRequested      = "STOP_REQUESTED"
	DeploymentStatusInProgress         = "IN_PROGRESS"
	DeploymentStatusRollbackRequested  = "ROLLBACK_REQUESTED"
	DeploymentStatusRollbackInProgress = "ROLLBACK_IN_PROGRESS"
	DeploymentStatusRollbackSuccessful = "ROLLBACK_SUCCESSFUL"
	DeploymentStatusRollbackFailed     = "ROLLBACK_FAILED"
)

// Stop types accepted by StopServiceDeployment.
const (
	StopTypeAbort    = "ABORT"
	StopTypeRollback = "ROLLBACK"
)

// ServiceConnectConfiguration is a service's Service Connect setting. Namespace
// is the Cloud Map namespace (name or ARN) the service joins; Raw is the whole
// configuration as supplied, echoed verbatim.
type ServiceConnectConfiguration struct {
	Namespace string
	Raw       []byte
}

// ServiceRevision is an immutable snapshot of the service configuration one
// deployment rolled out.
type ServiceRevision struct {
	ARN                      string
	ServiceARN               string
	ClusterARN               string
	TaskDefinition           string
	LaunchType               string
	PlatformVersion          string
	CapacityProviderStrategy []CapacityProviderStrategyItem
	NetworkConfiguration     *NetworkConfiguration
	LoadBalancers            []LoadBalancer
	ServiceRegistries        []ServiceRegistry
	ServiceConnect           *ServiceConnectConfiguration
	CreatedAt                string
}

// ServiceRevisionSummary is a deployment's view of a revision with its task
// counts.
type ServiceRevisionSummary struct {
	ARN                string
	RequestedTaskCount int
	RunningTaskCount   int
	PendingTaskCount   int
}

// ServiceDeploymentRollback records why and to which revision a deployment
// rolled back.
type ServiceDeploymentRollback struct {
	Reason             string
	ServiceRevisionARN string
	StartedAt          string
}

// ServiceDeployment is one rollout of a service to a target service revision.
type ServiceDeployment struct {
	// Sequence orders a service's deployments (higher = newer); it breaks ties
	// between deployments created in the same instant and is not on the wire.
	Sequence                uint64
	ARN                     string
	ServiceARN              string
	ClusterARN              string
	Status                  string
	StatusReason            string
	LifecycleStage          string
	TargetServiceRevision   ServiceRevisionSummary
	SourceServiceRevisions  []ServiceRevisionSummary
	DeploymentConfiguration *DeploymentConfiguration
	Rollback                *ServiceDeploymentRollback
	CreatedAt               string
	StartedAt               string
	FinishedAt              string
	StoppedAt               string
	UpdatedAt               string
}

// ListServiceDeploymentsInput filters and pages a service's deployments.
// MaxResults 0 means the default (20); the maximum is 100. CreatedAtBefore and
// CreatedAtAfter are RFC3339 instants (empty = unset).
type ListServiceDeploymentsInput struct {
	Cluster         string
	Service         string
	Statuses        []string
	CreatedAtBefore string
	CreatedAtAfter  string
	MaxResults      int
	NextToken       string
}

// ServiceDeployments exposes the deployment history of ECS-controller services.
type ServiceDeployments interface {
	ListServiceDeployments(ctx context.Context, in ListServiceDeploymentsInput) ([]ServiceDeployment, string, error)
	DescribeServiceDeployments(ctx context.Context, arns []string) ([]ServiceDeployment, []Failure, error)
	DescribeServiceRevisions(ctx context.Context, arns []string) ([]ServiceRevision, []Failure, error)
	StopServiceDeployment(ctx context.Context, arn, stopType string) (string, error)
}

// ServiceNamespaces lists the services that joined a Service Connect namespace.
type ServiceNamespaces interface {
	ListServicesByNamespace(ctx context.Context, namespace string, maxResults int, nextToken string) ([]string, string, error)
}
