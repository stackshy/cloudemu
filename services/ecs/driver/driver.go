// Package driver defines the interface and plain-Go types for Amazon ECS
// (Elastic Container Service) mock implementations. It is deliberately free of
// wire concerns: the server package maps these types to the AWS JSON 1.1 shape.
package driver

import "context"

// Tag is a resource tag key/value pair.
type Tag struct {
	Key   string
	Value string
}

// Setting is a cluster setting (name/value), e.g. containerInsights.
type Setting struct {
	Name  string
	Value string
}

// Cluster is an ECS cluster.
type Cluster struct {
	ARN                               string
	Name                              string
	Status                            string
	RegisteredContainerInstancesCount int
	RunningTasksCount                 int
	PendingTasksCount                 int
	ActiveServicesCount               int
	Tags                              []Tag
	Settings                          []Setting
}

// KeyValue is an environment variable entry (name/value).
type KeyValue struct {
	Name  string
	Value string
}

// PortMapping maps a container port to a host port.
type PortMapping struct {
	ContainerPort int
	HostPort      int
	Protocol      string
}

// ContainerDefinition describes a single container within a task definition.
type ContainerDefinition struct {
	Name         string
	Image        string
	CPU          int
	Memory       int
	Essential    bool
	PortMappings []PortMapping
	Command      []string
	Environment  []KeyValue
}

// TaskDefinition is a registered ECS task definition revision.
type TaskDefinition struct {
	ARN                     string
	Family                  string
	Revision                int
	Status                  string
	ContainerDefinitions    []ContainerDefinition
	CPU                     string
	Memory                  string
	NetworkMode             string
	RequiresCompatibilities []string
	TaskRoleARN             string
	ExecutionRoleARN        string
	RegisteredAt            string
	DeregisteredAt          string
	Tags                    []Tag
}

// Container is a running container within a task.
type Container struct {
	Name       string
	Image      string
	LastStatus string
}

// Task is a running or stopped ECS task.
type Task struct {
	ARN               string
	ClusterARN        string
	TaskDefinitionARN string
	LastStatus        string
	DesiredStatus     string
	LaunchType        string
	Group             string
	StartedBy         string
	CreatedAt         string
	StoppedReason     string
	StopCode          string
	Containers        []Container
	Tags              []Tag
}

// Service is an ECS service.
type Service struct {
	ARN                string
	Name               string
	ClusterARN         string
	TaskDefinition     string
	DesiredCount       int
	RunningCount       int
	PendingCount       int
	Status             string
	LaunchType         string
	SchedulingStrategy string
	CreatedAt          string
	Tags               []Tag
}

// ContainerInstance is an EC2 instance registered into a cluster.
type ContainerInstance struct {
	ARN               string
	EC2InstanceID     string
	Status            string
	RunningTasksCount int
	PendingTasksCount int
	AgentConnected    bool
}

// Failure describes a resource that could not be resolved in a batch
// Describe/RunTask call. Reason is typically "MISSING".
type Failure struct {
	ARN    string
	Reason string
	Detail string
}

// CreateClusterInput describes a cluster to create.
type CreateClusterInput struct {
	Name     string
	Tags     []Tag
	Settings []Setting
}

// RegisterTaskDefinitionInput describes a task definition revision to register.
type RegisterTaskDefinitionInput struct {
	Family                  string
	ContainerDefinitions    []ContainerDefinition
	CPU                     string
	Memory                  string
	NetworkMode             string
	RequiresCompatibilities []string
	TaskRoleARN             string
	ExecutionRoleARN        string
	Tags                    []Tag
}

// RunTaskInput describes tasks to run.
type RunTaskInput struct {
	TaskDefinition string
	Cluster        string
	Count          int
	LaunchType     string
	Group          string
	StartedBy      string
	Tags           []Tag
}

// CreateServiceInput describes a service to create.
type CreateServiceInput struct {
	ServiceName        string
	Cluster            string
	TaskDefinition     string
	DesiredCount       int
	LaunchType         string
	SchedulingStrategy string
	Tags               []Tag
}

// UpdateServiceInput describes mutations to an existing service. Nil pointers
// leave the corresponding field unchanged.
type UpdateServiceInput struct {
	Service        string
	Cluster        string
	TaskDefinition string
	DesiredCount   *int
}

// ECS is the interface an ECS provider implementation must satisfy.
type ECS interface {
	CreateCluster(ctx context.Context, in CreateClusterInput) (*Cluster, error)
	ListClusters(ctx context.Context) ([]Cluster, error)
	DescribeClusters(ctx context.Context, ids []string) ([]Cluster, []Failure, error)
	DeleteCluster(ctx context.Context, id string) (*Cluster, error)

	RegisterTaskDefinition(ctx context.Context, in RegisterTaskDefinitionInput) (*TaskDefinition, error)
	ListTaskDefinitions(ctx context.Context, familyPrefix, status string) ([]TaskDefinition, error)
	DescribeTaskDefinition(ctx context.Context, id string) (*TaskDefinition, error)
	DeregisterTaskDefinition(ctx context.Context, id string) (*TaskDefinition, error)

	RunTask(ctx context.Context, in RunTaskInput) ([]Task, []Failure, error)
	StopTask(ctx context.Context, cluster, task, reason string) (*Task, error)
	ListTasks(ctx context.Context, cluster, family, desiredStatus string) ([]Task, error)
	DescribeTasks(ctx context.Context, cluster string, ids []string) ([]Task, []Failure, error)

	CreateService(ctx context.Context, in CreateServiceInput) (*Service, error)
	UpdateService(ctx context.Context, in UpdateServiceInput) (*Service, error)
	ListServices(ctx context.Context, cluster string) ([]Service, error)
	DescribeServices(ctx context.Context, cluster string, ids []string) ([]Service, []Failure, error)
	DeleteService(ctx context.Context, cluster, service string) (*Service, error)

	ListContainerInstances(ctx context.Context, cluster string) ([]ContainerInstance, error)
	DescribeContainerInstances(ctx context.Context, cluster string, ids []string) ([]ContainerInstance, []Failure, error)
}
