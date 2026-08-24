// Package driver defines the interface and plain-Go types for Amazon ECS
// (Elastic Container Service) mock implementations. It is deliberately free of
// wire concerns: the server package maps these types to the AWS JSON 1.1 shape.
package driver

import (
	"context"
	"encoding/json"
)

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

// Cluster is an ECS cluster. Configuration is stored and echoed verbatim as raw
// JSON (execute-command/managed-storage passthrough). CapacityProviders and
// DefaultCapacityProviderStrategy are set by PutClusterCapacityProviders and
// echoed but not resolved to real capacity-provider resources.
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
	Configuration                     json.RawMessage
	CapacityProviders                 []string
	DefaultCapacityProviderStrategy   []CapacityProviderStrategyItem
}

// Attribute is a name/value attribute attached to a resource (typically a
// container instance). TargetID identifies the resource and TargetType is the
// kind of target (e.g. container-instance).
type Attribute struct {
	Name       string
	Value      string
	TargetID   string
	TargetType string
}

// AccountSetting is an ECS account setting (name/value), e.g.
// containerInsights or serviceLongArnFormat.
type AccountSetting struct {
	Name  string
	Value string
}

// Resource is a container-instance resource advertisement (CPU, MEMORY, PORTS,
// …). Only the value carried by the resource's declared Type is meaningful.
type Resource struct {
	Name           string
	Type           string
	IntegerValue   int
	DoubleValue    float64
	LongValue      int64
	StringSetValue []string
}

// Session is a synthetic ExecuteCommand SSM session.
type Session struct {
	SessionID  string
	StreamURL  string
	TokenValue string
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
	Name          string
	AppProtocol   string
}

// Secret is a secret injected into a container: Name is the environment
// variable and ValueFrom is the ARN of the Secrets Manager secret or SSM
// parameter it is sourced from.
type Secret struct {
	Name      string
	ValueFrom string
}

// EnvironmentFile references an env-var file (an S3 object) whose contents are
// loaded into the container environment. Type is typically "s3".
type EnvironmentFile struct {
	Value string
	Type  string
}

// HealthCheck is a container health check command and its timing parameters
// (all durations in seconds).
type HealthCheck struct {
	Command     []string
	Interval    int
	Timeout     int
	Retries     int
	StartPeriod int
}

// ContainerDependency expresses a start/stop ordering dependency on another
// container in the same task (Condition: START|COMPLETE|SUCCESS|HEALTHY).
type ContainerDependency struct {
	ContainerName string
	Condition     string
}

// MountPoint mounts a task-level volume into a container filesystem.
type MountPoint struct {
	SourceVolume  string
	ContainerPath string
	ReadOnly      bool
}

// VolumeFrom mounts all volumes from another container in the same task.
type VolumeFrom struct {
	SourceContainer string
	ReadOnly        bool
}

// LogConfiguration is a container's log driver configuration.
type LogConfiguration struct {
	LogDriver     string
	Options       map[string]string
	SecretOptions []Secret
}

// Ulimit is a container ulimit setting (units depend on Name).
type Ulimit struct {
	Name      string
	SoftLimit int
	HardLimit int
}

// ResourceRequirement declares a GPU or InferenceAccelerator resource the
// container needs (Type: GPU|InferenceAccelerator).
type ResourceRequirement struct {
	Value string
	Type  string
}

// ContainerDefinition describes a single container within a task definition.
// LinuxParameters and FirelensConfiguration are stored and echoed verbatim as
// raw JSON (passthrough), preserving fidelity without modeling every sub-field.
type ContainerDefinition struct {
	Name                   string
	Image                  string
	CPU                    int
	Memory                 int
	MemoryReservation      int
	Essential              bool
	PortMappings           []PortMapping
	Command                []string
	EntryPoint             []string
	Environment            []KeyValue
	Secrets                []Secret
	EnvironmentFiles       []EnvironmentFile
	HealthCheck            *HealthCheck
	DependsOn              []ContainerDependency
	MountPoints            []MountPoint
	VolumesFrom            []VolumeFrom
	LogConfiguration       *LogConfiguration
	Ulimits                []Ulimit
	ResourceRequirements   []ResourceRequirement
	LinuxParameters        json.RawMessage
	FirelensConfiguration  json.RawMessage
	StopTimeout            int
	StartTimeout           int
	User                   string
	WorkingDirectory       string
	Hostname               string
	Privileged             bool
	ReadonlyRootFilesystem bool
}

// HostVolumeProperties is the host-path backing of a bind-mount volume.
type HostVolumeProperties struct {
	SourcePath string
}

// Volume is a task-level volume. EFSVolumeConfiguration and
// DockerVolumeConfiguration are stored and echoed verbatim as raw JSON.
type Volume struct {
	Name                      string
	Host                      *HostVolumeProperties
	EFSVolumeConfiguration    json.RawMessage
	DockerVolumeConfiguration json.RawMessage
}

// EphemeralStorage is the amount of ephemeral storage (in GiB) for a Fargate task.
type EphemeralStorage struct {
	SizeInGiB int
}

// RuntimePlatform pins the CPU architecture and operating-system family a task
// runs on.
type RuntimePlatform struct {
	CPUArchitecture       string
	OperatingSystemFamily string
}

// ProxyConfiguration configures an App Mesh proxy (Envoy) for the task.
type ProxyConfiguration struct {
	Type          string
	ContainerName string
	Properties    []KeyValue
}

// PlacementConstraint is a task-definition placement constraint
// (Type: memberOf|distinctInstance, with an optional cluster-query Expression).
type PlacementConstraint struct {
	Type       string
	Expression string
}

// InferenceAccelerator attaches an Elastic Inference accelerator device to the task.
type InferenceAccelerator struct {
	DeviceName string
	DeviceType string
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
	Volumes                 []Volume
	EphemeralStorage        *EphemeralStorage
	PidMode                 string
	IpcMode                 string
	RuntimePlatform         *RuntimePlatform
	ProxyConfiguration      *ProxyConfiguration
	PlacementConstraints    []PlacementConstraint
	InferenceAccelerators   []InferenceAccelerator
	EnableFaultInjection    bool
	RegisteredBy            string
	RegisteredAt            string
	DeregisteredAt          string
	Tags                    []Tag
}

// Container is a running container within a task. ExitCode, Reason, and
// RuntimeID are populated only when the task is backed by a real
// config.ContainerEngine; they stay zero for the default synthetic tasks.
type Container struct {
	Name       string
	Image      string
	LastStatus string
	ExitCode   int
	Reason     string
	RuntimeID  string
}

// Attachment is a resource attached to a task, such as the elastic network
// interface an awsvpc/Fargate task is placed behind. Details carries the
// attachment-specific key/value pairs (e.g. networkInterfaceId, privateIPv4Address).
type Attachment struct {
	Type    string
	Status  string
	Details []KeyValue
}

// AwsVpcConfiguration is the awsvpc networking parameters supplied to RunTask
// (subnets, security groups, and public-IP assignment).
type AwsVpcConfiguration struct {
	Subnets        []string
	SecurityGroups []string
	AssignPublicIP string
}

// NetworkConfiguration wraps the awsvpc configuration for a task or service.
type NetworkConfiguration struct {
	AwsVpcConfiguration *AwsVpcConfiguration
}

// CapacityProviderStrategyItem is one entry of a capacity-provider strategy.
// It is decoded and accepted but not yet resolved to real capacity-provider
// resources (Wave 4).
type CapacityProviderStrategyItem struct {
	CapacityProvider string
	Base             int
	Weight           int
}

// Task is a running or stopped ECS task.
type Task struct {
	ARN                  string
	ClusterARN           string
	TaskDefinitionARN    string
	ContainerInstanceARN string
	LastStatus           string
	DesiredStatus        string
	LaunchType           string
	PlatformVersion      string
	Group                string
	StartedBy            string
	CreatedAt            string
	StoppedReason        string
	StopCode             string
	Containers           []Container
	Attachments          []Attachment
	Tags                 []Tag
}

// Deployment is one rolling-update deployment of a service. In the synchronous
// service model a service always has one PRIMARY deployment; a new
// taskDefinition/desiredCount/forceNewDeployment update creates a new PRIMARY
// and moves the previous one to ACTIVE (drained to zero tasks).
type Deployment struct {
	ID                 string
	Status             string // PRIMARY | ACTIVE
	TaskDefinition     string
	DesiredCount       int
	RunningCount       int
	PendingCount       int
	LaunchType         string
	RolloutState       string // COMPLETED | IN_PROGRESS | FAILED
	RolloutStateReason string
	CreatedAt          string
	UpdatedAt          string
}

// ServiceEvent is one entry of a service's event log.
type ServiceEvent struct {
	ID        string
	CreatedAt string
	Message   string
}

// LoadBalancer associates a service's container/port with an Elastic Load
// Balancing target group. It is accepted and echoed but not wired to real
// target registration or health checks.
type LoadBalancer struct {
	TargetGroupARN   string
	LoadBalancerName string
	ContainerName    string
	ContainerPort    int
}

// ServiceRegistry associates a service with a service-discovery registry (Cloud
// Map). Accepted and echoed but not wired to real registration.
type ServiceRegistry struct {
	RegistryARN   string
	ContainerName string
	ContainerPort int
	Port          int
}

// DeploymentCircuitBreaker configures the rolling-update circuit breaker. It is
// accepted and echoed but not simulated (no automatic rollback is performed).
type DeploymentCircuitBreaker struct {
	Enable   bool
	Rollback bool
}

// DeploymentConfiguration carries the rolling-update batch bounds and circuit
// breaker. The percentages are accepted and echoed but not used to stage the
// synchronous convergence.
type DeploymentConfiguration struct {
	MaximumPercent           *int
	MinimumHealthyPercent    *int
	DeploymentCircuitBreaker *DeploymentCircuitBreaker
}

// Service is an ECS service.
type Service struct {
	ARN                           string
	Name                          string
	ClusterARN                    string
	TaskDefinition                string
	RoleARN                       string
	CreatedBy                     string
	DesiredCount                  int
	RunningCount                  int
	PendingCount                  int
	Status                        string
	LaunchType                    string
	SchedulingStrategy            string
	DeploymentController          string // ECS | CODE_DEPLOY | EXTERNAL
	PlatformVersion               string
	PropagateTags                 string
	EnableExecuteCommand          bool
	HealthCheckGracePeriodSeconds *int
	CreatedAt                     string
	DeploymentConfiguration       *DeploymentConfiguration
	NetworkConfiguration          *NetworkConfiguration
	CapacityProviderStrategy      []CapacityProviderStrategyItem
	LoadBalancers                 []LoadBalancer
	ServiceRegistries             []ServiceRegistry
	Deployments                   []Deployment
	Events                        []ServiceEvent
	Tags                          []Tag
}

// ContainerInstance is an EC2 instance registered into a cluster. It models a
// real capacity pool: RegisteredCPU/RegisteredMemory are the total resources the
// instance advertised at registration; RemainingCPU/RemainingMemory are what is
// still free after placed tasks reserved their share. Units are ECS CPU units
// and MiB of memory.
type ContainerInstance struct {
	ARN               string
	EC2InstanceID     string
	Status            string
	RunningTasksCount int
	PendingTasksCount int
	AgentConnected    bool
	RegisteredCPU     int
	RegisteredMemory  int
	RemainingCPU      int
	RemainingMemory   int
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
	Volumes                 []Volume
	EphemeralStorage        *EphemeralStorage
	PidMode                 string
	IpcMode                 string
	RuntimePlatform         *RuntimePlatform
	ProxyConfiguration      *ProxyConfiguration
	PlacementConstraints    []PlacementConstraint
	InferenceAccelerators   []InferenceAccelerator
	EnableFaultInjection    bool
	Tags                    []Tag
}

// RunTaskInput describes tasks to run. NetworkConfiguration is required for the
// FARGATE launch type (awsvpc). CapacityProviderStrategy is accepted but not yet
// resolved to real capacity-provider resources (Wave 4).
type RunTaskInput struct {
	TaskDefinition           string
	Cluster                  string
	Count                    int
	LaunchType               string
	PlatformVersion          string
	Group                    string
	StartedBy                string
	NetworkConfiguration     *NetworkConfiguration
	CapacityProviderStrategy []CapacityProviderStrategyItem
	Tags                     []Tag
}

// CreateServiceInput describes a service to create. CapacityProviderStrategy is
// accepted but not yet resolved to real capacity-provider resources (Wave 4).
type CreateServiceInput struct {
	ServiceName                   string
	Cluster                       string
	TaskDefinition                string
	DesiredCount                  int
	LaunchType                    string
	Role                          string
	SchedulingStrategy            string
	DeploymentController          string
	PlatformVersion               string
	PropagateTags                 string
	EnableExecuteCommand          bool
	HealthCheckGracePeriodSeconds *int
	DeploymentConfiguration       *DeploymentConfiguration
	NetworkConfiguration          *NetworkConfiguration
	CapacityProviderStrategy      []CapacityProviderStrategyItem
	LoadBalancers                 []LoadBalancer
	ServiceRegistries             []ServiceRegistry
	Tags                          []Tag
}

// UpdateServiceInput describes mutations to an existing service. Nil pointers
// leave the corresponding field unchanged; only taskDefinition, desiredCount,
// and forceNewDeployment drive task reconciliation, the rest are stored/echoed.
type UpdateServiceInput struct {
	Service                       string
	Cluster                       string
	TaskDefinition                string
	DesiredCount                  *int
	ForceNewDeployment            bool
	PlatformVersion               string
	PropagateTags                 string
	EnableExecuteCommand          *bool
	HealthCheckGracePeriodSeconds *int
	DeploymentConfiguration       *DeploymentConfiguration
	NetworkConfiguration          *NetworkConfiguration
	CapacityProviderStrategy      []CapacityProviderStrategyItem
	LoadBalancers                 []LoadBalancer
	ServiceRegistries             []ServiceRegistry
}

// RegisterContainerInstanceInput describes an EC2 container instance to register
// into a cluster's capacity pool. InstanceIdentityDocument, when present, is
// parsed for its instanceId to derive the EC2 instance id.
type RegisterContainerInstanceInput struct {
	Cluster                  string
	InstanceIdentityDocument string
	TotalResources           []Resource
	Attributes               []Attribute
}

// UpdateClusterInput describes mutations to a cluster's settings and
// execute-command configuration. A nil Settings leaves the settings unchanged;
// a nil Configuration leaves the configuration unchanged.
type UpdateClusterInput struct {
	Cluster       string
	Settings      []Setting
	Configuration json.RawMessage
}

// ExecuteCommandInput describes an execute-command request against a running
// task's container.
type ExecuteCommandInput struct {
	Cluster     string
	Task        string
	Container   string
	Command     string
	Interactive bool
}

// ExecuteCommandResult is the resolved target and synthetic SSM session returned
// by ExecuteCommand.
type ExecuteCommandResult struct {
	ClusterARN    string
	ContainerARN  string
	ContainerName string
	TaskARN       string
	Interactive   bool
	Session       Session
}

// ECS is the interface an ECS provider implementation must satisfy.
type ECS interface {
	CreateCluster(ctx context.Context, in CreateClusterInput) (*Cluster, error)
	ListClusters(ctx context.Context) ([]Cluster, error)
	DescribeClusters(ctx context.Context, ids []string) ([]Cluster, []Failure, error)
	DeleteCluster(ctx context.Context, id string) (*Cluster, error)

	RegisterTaskDefinition(ctx context.Context, in RegisterTaskDefinitionInput) (*TaskDefinition, error)
	ListTaskDefinitions(ctx context.Context, familyPrefix, status, sortOrder string) ([]TaskDefinition, error)
	DescribeTaskDefinition(ctx context.Context, id string) (*TaskDefinition, error)
	DeregisterTaskDefinition(ctx context.Context, id string) (*TaskDefinition, error)

	RunTask(ctx context.Context, in RunTaskInput) ([]Task, []Failure, error)
	StopTask(ctx context.Context, cluster, task, reason string) (*Task, error)
	ListTasks(ctx context.Context, cluster, family, desiredStatus, serviceName string) ([]Task, error)
	DescribeTasks(ctx context.Context, cluster string, ids []string) ([]Task, []Failure, error)

	CreateService(ctx context.Context, in CreateServiceInput) (*Service, error)
	UpdateService(ctx context.Context, in UpdateServiceInput) (*Service, error)
	ListServices(ctx context.Context, cluster string) ([]Service, error)
	DescribeServices(ctx context.Context, cluster string, ids []string) ([]Service, []Failure, error)
	DeleteService(ctx context.Context, cluster, service string, force bool) (*Service, error)

	ListContainerInstances(ctx context.Context, cluster string) ([]ContainerInstance, error)
	DescribeContainerInstances(ctx context.Context, cluster string, ids []string) ([]ContainerInstance, []Failure, error)
	RegisterContainerInstance(ctx context.Context, in RegisterContainerInstanceInput) (*ContainerInstance, error)
	DeregisterContainerInstance(ctx context.Context, cluster, containerInstance string, force bool) (*ContainerInstance, error)
	UpdateContainerInstancesState(ctx context.Context, cluster string, ids []string, status string) (
		[]ContainerInstance, []Failure, error)

	UpdateCluster(ctx context.Context, in UpdateClusterInput) (*Cluster, error)
	UpdateClusterSettings(ctx context.Context, cluster string, settings []Setting) (*Cluster, error)
	PutClusterCapacityProviders(ctx context.Context, cluster string, capacityProviders []string,
		defaultStrategy []CapacityProviderStrategyItem) (*Cluster, error)

	TagResource(ctx context.Context, resourceARN string, tags []Tag) error
	UntagResource(ctx context.Context, resourceARN string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) ([]Tag, error)

	PutAccountSetting(ctx context.Context, name, value string) (*AccountSetting, error)
	PutAccountSettingDefault(ctx context.Context, name, value string) (*AccountSetting, error)
	ListAccountSettings(ctx context.Context) ([]AccountSetting, error)
	DeleteAccountSetting(ctx context.Context, name string) (*AccountSetting, error)

	PutAttributes(ctx context.Context, cluster string, attrs []Attribute) ([]Attribute, error)
	DeleteAttributes(ctx context.Context, cluster string, attrs []Attribute) ([]Attribute, error)
	ListAttributes(ctx context.Context, cluster, targetType, attributeName, attributeValue string) ([]Attribute, error)

	ListTaskDefinitionFamilies(ctx context.Context, familyPrefix, status string) ([]string, error)

	ExecuteCommand(ctx context.Context, in ExecuteCommandInput) (*ExecuteCommandResult, error)
}
