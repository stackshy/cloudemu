package ecs

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// --- shared wire shapes ---

type wireTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type wireSetting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type wireKeyValue struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type wirePortMapping struct {
	ContainerPort int    `json:"containerPort,omitempty"`
	HostPort      int    `json:"hostPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	Name          string `json:"name,omitempty"`
	AppProtocol   string `json:"appProtocol,omitempty"`
}

type wireSecret struct {
	Name      string `json:"name,omitempty"`
	ValueFrom string `json:"valueFrom,omitempty"`
}

type wireEnvironmentFile struct {
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

type wireHealthCheck struct {
	Command     []string `json:"command,omitempty"`
	Interval    int      `json:"interval,omitempty"`
	Timeout     int      `json:"timeout,omitempty"`
	Retries     int      `json:"retries,omitempty"`
	StartPeriod int      `json:"startPeriod,omitempty"`
}

type wireContainerDependency struct {
	ContainerName string `json:"containerName,omitempty"`
	Condition     string `json:"condition,omitempty"`
}

type wireMountPoint struct {
	SourceVolume  string `json:"sourceVolume,omitempty"`
	ContainerPath string `json:"containerPath,omitempty"`
	ReadOnly      bool   `json:"readOnly,omitempty"`
}

type wireVolumeFrom struct {
	SourceContainer string `json:"sourceContainer,omitempty"`
	ReadOnly        bool   `json:"readOnly,omitempty"`
}

type wireLogConfiguration struct {
	LogDriver     string            `json:"logDriver,omitempty"`
	Options       map[string]string `json:"options,omitempty"`
	SecretOptions []wireSecret      `json:"secretOptions,omitempty"`
}

type wireUlimit struct {
	Name      string `json:"name,omitempty"`
	SoftLimit int    `json:"softLimit,omitempty"`
	HardLimit int    `json:"hardLimit,omitempty"`
}

type wireResourceRequirement struct {
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

type wireContainerDef struct {
	Name                   string                    `json:"name,omitempty"`
	Image                  string                    `json:"image,omitempty"`
	CPU                    int                       `json:"cpu,omitempty"`
	Memory                 int                       `json:"memory,omitempty"`
	MemoryReservation      int                       `json:"memoryReservation,omitempty"`
	Essential              *bool                     `json:"essential,omitempty"`
	PortMappings           []wirePortMapping         `json:"portMappings,omitempty"`
	Command                []string                  `json:"command,omitempty"`
	EntryPoint             []string                  `json:"entryPoint,omitempty"`
	Environment            []wireKeyValue            `json:"environment,omitempty"`
	Secrets                []wireSecret              `json:"secrets,omitempty"`
	EnvironmentFiles       []wireEnvironmentFile     `json:"environmentFiles,omitempty"`
	HealthCheck            *wireHealthCheck          `json:"healthCheck,omitempty"`
	DependsOn              []wireContainerDependency `json:"dependsOn,omitempty"`
	MountPoints            []wireMountPoint          `json:"mountPoints,omitempty"`
	VolumesFrom            []wireVolumeFrom          `json:"volumesFrom,omitempty"`
	LogConfiguration       *wireLogConfiguration     `json:"logConfiguration,omitempty"`
	Ulimits                []wireUlimit              `json:"ulimits,omitempty"`
	ResourceRequirements   []wireResourceRequirement `json:"resourceRequirements,omitempty"`
	LinuxParameters        json.RawMessage           `json:"linuxParameters,omitempty"`
	FirelensConfiguration  json.RawMessage           `json:"firelensConfiguration,omitempty"`
	StopTimeout            int                       `json:"stopTimeout,omitempty"`
	StartTimeout           int                       `json:"startTimeout,omitempty"`
	User                   string                    `json:"user,omitempty"`
	WorkingDirectory       string                    `json:"workingDirectory,omitempty"`
	Hostname               string                    `json:"hostname,omitempty"`
	Privileged             bool                      `json:"privileged,omitempty"`
	ReadonlyRootFilesystem bool                      `json:"readonlyRootFilesystem,omitempty"`
}

type wireHostVolumeProperties struct {
	SourcePath string `json:"sourcePath,omitempty"`
}

type wireVolume struct {
	Name                      string                    `json:"name,omitempty"`
	Host                      *wireHostVolumeProperties `json:"host,omitempty"`
	EFSVolumeConfiguration    json.RawMessage           `json:"efsVolumeConfiguration,omitempty"`
	DockerVolumeConfiguration json.RawMessage           `json:"dockerVolumeConfiguration,omitempty"`
}

type wireEphemeralStorage struct {
	SizeInGiB int `json:"sizeInGiB,omitempty"`
}

type wireRuntimePlatform struct {
	CPUArchitecture       string `json:"cpuArchitecture,omitempty"`
	OperatingSystemFamily string `json:"operatingSystemFamily,omitempty"`
}

type wireProxyConfiguration struct {
	Type          string         `json:"type,omitempty"`
	ContainerName string         `json:"containerName,omitempty"`
	Properties    []wireKeyValue `json:"properties,omitempty"`
}

type wirePlacementConstraint struct {
	Type       string `json:"type,omitempty"`
	Expression string `json:"expression,omitempty"`
}

type wireInferenceAccelerator struct {
	DeviceName string `json:"deviceName,omitempty"`
	DeviceType string `json:"deviceType,omitempty"`
}

type wireFailure struct {
	ARN    string `json:"arn,omitempty"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type wireCluster struct {
	ClusterArn                        string        `json:"clusterArn"`
	ClusterName                       string        `json:"clusterName"`
	Status                            string        `json:"status"`
	RegisteredContainerInstancesCount int           `json:"registeredContainerInstancesCount"`
	RunningTasksCount                 int           `json:"runningTasksCount"`
	PendingTasksCount                 int           `json:"pendingTasksCount"`
	ActiveServicesCount               int           `json:"activeServicesCount"`
	Tags                              []wireTag     `json:"tags,omitempty"`
	Settings                          []wireSetting `json:"settings,omitempty"`
	CapacityProviders                 []string      `json:"capacityProviders,omitempty"`

	DefaultCapacityProviderStrategy []wireCapacityProviderStrategyItem `json:"defaultCapacityProviderStrategy,omitempty"`
}

type wireTaskDef struct {
	TaskDefinitionArn       string                     `json:"taskDefinitionArn"`
	Family                  string                     `json:"family"`
	Revision                int                        `json:"revision"`
	Status                  string                     `json:"status"`
	ContainerDefinitions    []wireContainerDef         `json:"containerDefinitions"`
	CPU                     string                     `json:"cpu,omitempty"`
	Memory                  string                     `json:"memory,omitempty"`
	NetworkMode             string                     `json:"networkMode,omitempty"`
	RequiresCompatibilities []string                   `json:"requiresCompatibilities,omitempty"`
	Compatibilities         []string                   `json:"compatibilities,omitempty"`
	RequiresAttributes      []wireAttribute            `json:"requiresAttributes,omitempty"`
	TaskRoleArn             string                     `json:"taskRoleArn,omitempty"`
	ExecutionRoleArn        string                     `json:"executionRoleArn,omitempty"`
	Volumes                 []wireVolume               `json:"volumes,omitempty"`
	EphemeralStorage        *wireEphemeralStorage      `json:"ephemeralStorage,omitempty"`
	PidMode                 string                     `json:"pidMode,omitempty"`
	IpcMode                 string                     `json:"ipcMode,omitempty"`
	RuntimePlatform         *wireRuntimePlatform       `json:"runtimePlatform,omitempty"`
	ProxyConfiguration      *wireProxyConfiguration    `json:"proxyConfiguration,omitempty"`
	PlacementConstraints    []wirePlacementConstraint  `json:"placementConstraints,omitempty"`
	InferenceAccelerators   []wireInferenceAccelerator `json:"inferenceAccelerators,omitempty"`
	EnableFaultInjection    bool                       `json:"enableFaultInjection,omitempty"`
	RegisteredBy            string                     `json:"registeredBy,omitempty"`
	RegisteredAt            float64                    `json:"registeredAt,omitempty"`
	DeregisteredAt          float64                    `json:"deregisteredAt,omitempty"`
}

// containerStatusStopped is the ECS lastStatus of a finished container.
const containerStatusStopped = "STOPPED"

// wireNetworkBinding mirrors the ECS NetworkBinding shape: the host IP/port a
// bridge- or host-mode container port mapping was bound to.
type wireNetworkBinding struct {
	BindIP        string `json:"bindIP,omitempty"`
	ContainerPort int    `json:"containerPort,omitempty"`
	HostPort      int    `json:"hostPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
}

type wireContainer struct {
	Name       string `json:"name,omitempty"`
	Image      string `json:"image,omitempty"`
	LastStatus string `json:"lastStatus,omitempty"`
	// ExitCode is a pointer so a real exit 0 on a STOPPED container serializes
	// (real ECS reports it), while a running container omits it entirely.
	ExitCode        *int                 `json:"exitCode,omitempty"`
	Reason          string               `json:"reason,omitempty"`
	RuntimeID       string               `json:"runtimeId,omitempty"`
	NetworkBindings []wireNetworkBinding `json:"networkBindings,omitempty"`
}

// wireAttachment mirrors the ECS Attachment shape (type/status/details), where
// details is a list of name/value pairs (KeyValuePair).
type wireAttachment struct {
	Type    string         `json:"type,omitempty"`
	Status  string         `json:"status,omitempty"`
	Details []wireKeyValue `json:"details,omitempty"`
}

// wireResource mirrors the ECS Resource shape. Container-instance capacity is
// serialized as INTEGER resources named CPU and MEMORY; the long/double/string
// variants round-trip RegisterContainerInstance's totalResources.
type wireResource struct {
	Name           string   `json:"name,omitempty"`
	Type           string   `json:"type,omitempty"`
	IntegerValue   int      `json:"integerValue,omitempty"`
	DoubleValue    float64  `json:"doubleValue,omitempty"`
	LongValue      int64    `json:"longValue,omitempty"`
	StringSetValue []string `json:"stringSetValue,omitempty"`
}

// wireAttribute mirrors the ECS Attribute shape (name/value/targetId/targetType).
type wireAttribute struct {
	Name       string `json:"name,omitempty"`
	Value      string `json:"value,omitempty"`
	TargetID   string `json:"targetId,omitempty"`
	TargetType string `json:"targetType,omitempty"`
}

// wireAccountSetting mirrors the ECS Setting shape (name/value).
type wireAccountSetting struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type wireTask struct {
	TaskArn              string           `json:"taskArn"`
	ClusterArn           string           `json:"clusterArn"`
	TaskDefinitionArn    string           `json:"taskDefinitionArn"`
	ContainerInstanceArn string           `json:"containerInstanceArn,omitempty"`
	LastStatus           string           `json:"lastStatus"`
	DesiredStatus        string           `json:"desiredStatus"`
	LaunchType           string           `json:"launchType,omitempty"`
	PlatformVersion      string           `json:"platformVersion,omitempty"`
	Group                string           `json:"group,omitempty"`
	StartedBy            string           `json:"startedBy,omitempty"`
	CreatedAt            float64          `json:"createdAt,omitempty"`
	StoppedReason        string           `json:"stoppedReason,omitempty"`
	StopCode             string           `json:"stopCode,omitempty"`
	Containers           []wireContainer  `json:"containers,omitempty"`
	Attachments          []wireAttachment `json:"attachments,omitempty"`
	Tags                 []wireTag        `json:"tags,omitempty"`
}

// wireAwsVpcConfiguration and wireNetworkConfiguration decode the awsvpc
// networking parameters supplied to RunTask.
type wireAwsVpcConfiguration struct {
	Subnets        []string `json:"subnets"`
	SecurityGroups []string `json:"securityGroups"`
	AssignPublicIP string   `json:"assignPublicIp"`
}

type wireNetworkConfiguration struct {
	AwsvpcConfiguration *wireAwsVpcConfiguration `json:"awsvpcConfiguration"`
}

// wireCapacityProviderStrategyItem decodes one capacity-provider strategy entry.
type wireCapacityProviderStrategyItem struct {
	CapacityProvider string `json:"capacityProvider"`
	Base             int    `json:"base"`
	Weight           int    `json:"weight"`
}

// wireLoadBalancer / wireServiceRegistry round-trip a service's load-balancer
// and service-discovery bindings (decoded on create/update, echoed on describe).
type wireLoadBalancer struct {
	TargetGroupArn   string `json:"targetGroupArn,omitempty"`
	LoadBalancerName string `json:"loadBalancerName,omitempty"`
	ContainerName    string `json:"containerName,omitempty"`
	ContainerPort    int    `json:"containerPort,omitempty"`
}

type wireServiceRegistry struct {
	RegistryArn   string `json:"registryArn,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	ContainerPort int    `json:"containerPort,omitempty"`
	Port          int    `json:"port,omitempty"`
}

// wireDeploymentCircuitBreaker / wireDeploymentConfiguration decode the
// rolling-update batch bounds and circuit breaker.
type wireDeploymentCircuitBreaker struct {
	Enable   bool `json:"enable"`
	Rollback bool `json:"rollback"`
}

type wireDeploymentConfiguration struct {
	MaximumPercent           *int                          `json:"maximumPercent"`
	MinimumHealthyPercent    *int                          `json:"minimumHealthyPercent"`
	DeploymentCircuitBreaker *wireDeploymentCircuitBreaker `json:"deploymentCircuitBreaker"`
}

// wireDeployment / wireServiceEvent are response-only shapes for a service's
// deployments[] and events[].
type wireDeployment struct {
	ID                 string  `json:"id"`
	Status             string  `json:"status"`
	TaskDefinition     string  `json:"taskDefinition,omitempty"`
	DesiredCount       int     `json:"desiredCount"`
	RunningCount       int     `json:"runningCount"`
	PendingCount       int     `json:"pendingCount"`
	LaunchType         string  `json:"launchType,omitempty"`
	RolloutState       string  `json:"rolloutState,omitempty"`
	RolloutStateReason string  `json:"rolloutStateReason,omitempty"`
	CreatedAt          float64 `json:"createdAt,omitempty"`
	UpdatedAt          float64 `json:"updatedAt,omitempty"`
}

type wireServiceEvent struct {
	ID        string  `json:"id"`
	CreatedAt float64 `json:"createdAt,omitempty"`
	Message   string  `json:"message"`
}

type wireService struct {
	ServiceArn                    string                       `json:"serviceArn"`
	ServiceName                   string                       `json:"serviceName"`
	ClusterArn                    string                       `json:"clusterArn"`
	TaskDefinition                string                       `json:"taskDefinition,omitempty"`
	RoleArn                       string                       `json:"roleArn,omitempty"`
	CreatedBy                     string                       `json:"createdBy,omitempty"`
	DesiredCount                  int                          `json:"desiredCount"`
	RunningCount                  int                          `json:"runningCount"`
	PendingCount                  int                          `json:"pendingCount"`
	Status                        string                       `json:"status"`
	LaunchType                    string                       `json:"launchType,omitempty"`
	SchedulingStrategy            string                       `json:"schedulingStrategy,omitempty"`
	DeploymentController          *wireDeploymentController    `json:"deploymentController,omitempty"`
	PlatformVersion               string                       `json:"platformVersion,omitempty"`
	PropagateTags                 string                       `json:"propagateTags,omitempty"`
	EnableExecuteCommand          bool                         `json:"enableExecuteCommand,omitempty"`
	HealthCheckGracePeriodSeconds *int                         `json:"healthCheckGracePeriodSeconds,omitempty"`
	DeploymentConfiguration       *wireDeploymentConfiguration `json:"deploymentConfiguration,omitempty"`
	NetworkConfiguration          *wireNetworkConfiguration    `json:"networkConfiguration,omitempty"`
	LoadBalancers                 []wireLoadBalancer           `json:"loadBalancers,omitempty"`
	ServiceRegistries             []wireServiceRegistry        `json:"serviceRegistries,omitempty"`
	Deployments                   []wireDeployment             `json:"deployments,omitempty"`
	Events                        []wireServiceEvent           `json:"events,omitempty"`
	CreatedAt                     float64                      `json:"createdAt,omitempty"`
	Tags                          []wireTag                    `json:"tags,omitempty"`
}

// wireDeploymentController echoes the service's deployment controller type.
type wireDeploymentController struct {
	Type string `json:"type,omitempty"`
}

type wireContainerInstance struct {
	ContainerInstanceArn string         `json:"containerInstanceArn"`
	Ec2InstanceID        string         `json:"ec2InstanceId,omitempty"`
	Status               string         `json:"status"`
	RunningTasksCount    int            `json:"runningTasksCount"`
	PendingTasksCount    int            `json:"pendingTasksCount"`
	AgentConnected       bool           `json:"agentConnected"`
	RegisteredResources  []wireResource `json:"registeredResources,omitempty"`
	RemainingResources   []wireResource `json:"remainingResources,omitempty"`
}

// --- request -> driver converters ---

func toTags(in []wireTag) []driver.Tag {
	out := make([]driver.Tag, 0, len(in))
	for _, t := range in {
		out = append(out, driver.Tag{Key: t.Key, Value: t.Value})
	}

	return out
}

func toSettings(in []wireSetting) []driver.Setting {
	out := make([]driver.Setting, 0, len(in))
	for _, s := range in {
		out = append(out, driver.Setting{Name: s.Name, Value: s.Value})
	}

	return out
}

func toContainerDefs(in []wireContainerDef) []driver.ContainerDefinition {
	out := make([]driver.ContainerDefinition, 0, len(in))

	for i := range in {
		c := &in[i]
		out = append(out, driver.ContainerDefinition{
			Name:                   c.Name,
			Image:                  c.Image,
			CPU:                    c.CPU,
			Memory:                 c.Memory,
			MemoryReservation:      c.MemoryReservation,
			Essential:              essentialOrDefault(c.Essential),
			PortMappings:           toPortMappings(c.PortMappings),
			Command:                c.Command,
			EntryPoint:             c.EntryPoint,
			Environment:            toKeyValues(c.Environment),
			Secrets:                toSecrets(c.Secrets),
			EnvironmentFiles:       toEnvironmentFiles(c.EnvironmentFiles),
			HealthCheck:            toHealthCheck(c.HealthCheck),
			DependsOn:              toContainerDependencies(c.DependsOn),
			MountPoints:            toMountPoints(c.MountPoints),
			VolumesFrom:            toVolumesFrom(c.VolumesFrom),
			LogConfiguration:       toLogConfiguration(c.LogConfiguration),
			Ulimits:                toUlimits(c.Ulimits),
			ResourceRequirements:   toResourceRequirements(c.ResourceRequirements),
			LinuxParameters:        c.LinuxParameters,
			FirelensConfiguration:  c.FirelensConfiguration,
			StopTimeout:            c.StopTimeout,
			StartTimeout:           c.StartTimeout,
			User:                   c.User,
			WorkingDirectory:       c.WorkingDirectory,
			Hostname:               c.Hostname,
			Privileged:             c.Privileged,
			ReadonlyRootFilesystem: c.ReadonlyRootFilesystem,
		})
	}

	return out
}

// essentialOrDefault replicates the AWS default: a container definition whose
// essential flag is unset is treated as essential.
func essentialOrDefault(in *bool) bool {
	if in == nil {
		return true
	}

	return *in
}

func toPortMappings(in []wirePortMapping) []driver.PortMapping {
	out := make([]driver.PortMapping, 0, len(in))
	for _, p := range in {
		out = append(out, driver.PortMapping{
			ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: p.Protocol,
			Name: p.Name, AppProtocol: p.AppProtocol,
		})
	}

	return out
}

func toSecrets(in []wireSecret) []driver.Secret {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Secret, 0, len(in))
	for _, s := range in {
		out = append(out, driver.Secret{Name: s.Name, ValueFrom: s.ValueFrom})
	}

	return out
}

func toEnvironmentFiles(in []wireEnvironmentFile) []driver.EnvironmentFile {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.EnvironmentFile, 0, len(in))
	for _, f := range in {
		out = append(out, driver.EnvironmentFile{Value: f.Value, Type: f.Type})
	}

	return out
}

func toHealthCheck(in *wireHealthCheck) *driver.HealthCheck {
	if in == nil {
		return nil
	}

	return &driver.HealthCheck{
		Command: in.Command, Interval: in.Interval, Timeout: in.Timeout,
		Retries: in.Retries, StartPeriod: in.StartPeriod,
	}
}

func toContainerDependencies(in []wireContainerDependency) []driver.ContainerDependency {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.ContainerDependency, 0, len(in))
	for _, d := range in {
		out = append(out, driver.ContainerDependency{ContainerName: d.ContainerName, Condition: d.Condition})
	}

	return out
}

func toMountPoints(in []wireMountPoint) []driver.MountPoint {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.MountPoint, 0, len(in))
	for _, m := range in {
		out = append(out, driver.MountPoint{SourceVolume: m.SourceVolume, ContainerPath: m.ContainerPath, ReadOnly: m.ReadOnly})
	}

	return out
}

func toVolumesFrom(in []wireVolumeFrom) []driver.VolumeFrom {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.VolumeFrom, 0, len(in))
	for _, v := range in {
		out = append(out, driver.VolumeFrom{SourceContainer: v.SourceContainer, ReadOnly: v.ReadOnly})
	}

	return out
}

func toLogConfiguration(in *wireLogConfiguration) *driver.LogConfiguration {
	if in == nil {
		return nil
	}

	return &driver.LogConfiguration{
		LogDriver: in.LogDriver, Options: in.Options, SecretOptions: toSecrets(in.SecretOptions),
	}
}

func toUlimits(in []wireUlimit) []driver.Ulimit {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Ulimit, 0, len(in))
	for _, u := range in {
		out = append(out, driver.Ulimit{Name: u.Name, SoftLimit: u.SoftLimit, HardLimit: u.HardLimit})
	}

	return out
}

func toResourceRequirements(in []wireResourceRequirement) []driver.ResourceRequirement {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.ResourceRequirement, 0, len(in))
	for _, r := range in {
		out = append(out, driver.ResourceRequirement{Value: r.Value, Type: r.Type})
	}

	return out
}

func toVolumes(in []wireVolume) []driver.Volume {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Volume, 0, len(in))

	for i := range in {
		v := driver.Volume{
			Name:                      in[i].Name,
			EFSVolumeConfiguration:    in[i].EFSVolumeConfiguration,
			DockerVolumeConfiguration: in[i].DockerVolumeConfiguration,
		}

		if in[i].Host != nil {
			v.Host = &driver.HostVolumeProperties{SourcePath: in[i].Host.SourcePath}
		}

		out = append(out, v)
	}

	return out
}

func toEphemeralStorage(in *wireEphemeralStorage) *driver.EphemeralStorage {
	if in == nil {
		return nil
	}

	return &driver.EphemeralStorage{SizeInGiB: in.SizeInGiB}
}

func toRuntimePlatform(in *wireRuntimePlatform) *driver.RuntimePlatform {
	if in == nil {
		return nil
	}

	return &driver.RuntimePlatform{CPUArchitecture: in.CPUArchitecture, OperatingSystemFamily: in.OperatingSystemFamily}
}

func toProxyConfiguration(in *wireProxyConfiguration) *driver.ProxyConfiguration {
	if in == nil {
		return nil
	}

	return &driver.ProxyConfiguration{Type: in.Type, ContainerName: in.ContainerName, Properties: toKeyValues(in.Properties)}
}

func toPlacementConstraints(in []wirePlacementConstraint) []driver.PlacementConstraint {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.PlacementConstraint, 0, len(in))
	for _, c := range in {
		out = append(out, driver.PlacementConstraint{Type: c.Type, Expression: c.Expression})
	}

	return out
}

func toInferenceAccelerators(in []wireInferenceAccelerator) []driver.InferenceAccelerator {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.InferenceAccelerator, 0, len(in))
	for _, a := range in {
		out = append(out, driver.InferenceAccelerator{DeviceName: a.DeviceName, DeviceType: a.DeviceType})
	}

	return out
}

func toKeyValues(in []wireKeyValue) []driver.KeyValue {
	out := make([]driver.KeyValue, 0, len(in))
	for _, kv := range in {
		out = append(out, driver.KeyValue{Name: kv.Name, Value: kv.Value})
	}

	return out
}

// toNetworkConfiguration converts the decoded awsvpc configuration to the driver
// shape, returning nil when no configuration was supplied.
func toNetworkConfiguration(in *wireNetworkConfiguration) *driver.NetworkConfiguration {
	if in == nil || in.AwsvpcConfiguration == nil {
		return nil
	}

	v := in.AwsvpcConfiguration

	return &driver.NetworkConfiguration{
		AwsVpcConfiguration: &driver.AwsVpcConfiguration{
			Subnets:        v.Subnets,
			SecurityGroups: v.SecurityGroups,
			AssignPublicIP: v.AssignPublicIP,
		},
	}
}

// toCapacityProviderStrategy converts decoded strategy entries to the driver
// shape. The values are accepted and threaded through but not yet resolved to
// real capacity-provider resources (Wave 4).
func toCapacityProviderStrategy(in []wireCapacityProviderStrategyItem) []driver.CapacityProviderStrategyItem {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.CapacityProviderStrategyItem, 0, len(in))
	for _, s := range in {
		out = append(out, driver.CapacityProviderStrategyItem{
			CapacityProvider: s.CapacityProvider,
			Base:             s.Base,
			Weight:           s.Weight,
		})
	}

	return out
}

// --- driver -> response converters ---

func fromTags(in []driver.Tag) []wireTag {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireTag, 0, len(in))
	for _, t := range in {
		out = append(out, wireTag{Key: t.Key, Value: t.Value})
	}

	return out
}

func fromSettings(in []driver.Setting) []wireSetting {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireSetting, 0, len(in))
	for _, s := range in {
		out = append(out, wireSetting{Name: s.Name, Value: s.Value})
	}

	return out
}

func fromContainerDefs(in []driver.ContainerDefinition) []wireContainerDef {
	out := make([]wireContainerDef, 0, len(in))

	for i := range in {
		c := &in[i]
		essential := c.Essential
		out = append(out, wireContainerDef{
			Name:                   c.Name,
			Image:                  c.Image,
			CPU:                    c.CPU,
			Memory:                 c.Memory,
			MemoryReservation:      c.MemoryReservation,
			Essential:              &essential,
			PortMappings:           fromPortMappings(c.PortMappings),
			Command:                c.Command,
			EntryPoint:             c.EntryPoint,
			Environment:            fromKeyValues(c.Environment),
			Secrets:                fromSecrets(c.Secrets),
			EnvironmentFiles:       fromEnvironmentFiles(c.EnvironmentFiles),
			HealthCheck:            fromHealthCheck(c.HealthCheck),
			DependsOn:              fromContainerDependencies(c.DependsOn),
			MountPoints:            fromMountPoints(c.MountPoints),
			VolumesFrom:            fromVolumesFrom(c.VolumesFrom),
			LogConfiguration:       fromLogConfiguration(c.LogConfiguration),
			Ulimits:                fromUlimits(c.Ulimits),
			ResourceRequirements:   fromResourceRequirements(c.ResourceRequirements),
			LinuxParameters:        c.LinuxParameters,
			FirelensConfiguration:  c.FirelensConfiguration,
			StopTimeout:            c.StopTimeout,
			StartTimeout:           c.StartTimeout,
			User:                   c.User,
			WorkingDirectory:       c.WorkingDirectory,
			Hostname:               c.Hostname,
			Privileged:             c.Privileged,
			ReadonlyRootFilesystem: c.ReadonlyRootFilesystem,
		})
	}

	return out
}

func fromPortMappings(in []driver.PortMapping) []wirePortMapping {
	if len(in) == 0 {
		return nil
	}

	out := make([]wirePortMapping, 0, len(in))
	for _, p := range in {
		out = append(out, wirePortMapping{
			ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: p.Protocol,
			Name: p.Name, AppProtocol: p.AppProtocol,
		})
	}

	return out
}

func fromSecrets(in []driver.Secret) []wireSecret {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireSecret, 0, len(in))
	for _, s := range in {
		out = append(out, wireSecret{Name: s.Name, ValueFrom: s.ValueFrom})
	}

	return out
}

func fromEnvironmentFiles(in []driver.EnvironmentFile) []wireEnvironmentFile {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireEnvironmentFile, 0, len(in))
	for _, f := range in {
		out = append(out, wireEnvironmentFile{Value: f.Value, Type: f.Type})
	}

	return out
}

func fromHealthCheck(in *driver.HealthCheck) *wireHealthCheck {
	if in == nil {
		return nil
	}

	return &wireHealthCheck{
		Command: in.Command, Interval: in.Interval, Timeout: in.Timeout,
		Retries: in.Retries, StartPeriod: in.StartPeriod,
	}
}

func fromContainerDependencies(in []driver.ContainerDependency) []wireContainerDependency {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireContainerDependency, 0, len(in))
	for _, d := range in {
		out = append(out, wireContainerDependency{ContainerName: d.ContainerName, Condition: d.Condition})
	}

	return out
}

func fromMountPoints(in []driver.MountPoint) []wireMountPoint {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireMountPoint, 0, len(in))
	for _, m := range in {
		out = append(out, wireMountPoint{SourceVolume: m.SourceVolume, ContainerPath: m.ContainerPath, ReadOnly: m.ReadOnly})
	}

	return out
}

func fromVolumesFrom(in []driver.VolumeFrom) []wireVolumeFrom {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireVolumeFrom, 0, len(in))
	for _, v := range in {
		out = append(out, wireVolumeFrom{SourceContainer: v.SourceContainer, ReadOnly: v.ReadOnly})
	}

	return out
}

func fromLogConfiguration(in *driver.LogConfiguration) *wireLogConfiguration {
	if in == nil {
		return nil
	}

	return &wireLogConfiguration{
		LogDriver: in.LogDriver, Options: in.Options, SecretOptions: fromSecrets(in.SecretOptions),
	}
}

func fromUlimits(in []driver.Ulimit) []wireUlimit {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireUlimit, 0, len(in))
	for _, u := range in {
		out = append(out, wireUlimit{Name: u.Name, SoftLimit: u.SoftLimit, HardLimit: u.HardLimit})
	}

	return out
}

func fromResourceRequirements(in []driver.ResourceRequirement) []wireResourceRequirement {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireResourceRequirement, 0, len(in))
	for _, r := range in {
		out = append(out, wireResourceRequirement{Value: r.Value, Type: r.Type})
	}

	return out
}

func fromVolumes(in []driver.Volume) []wireVolume {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireVolume, 0, len(in))

	for i := range in {
		v := wireVolume{
			Name:                      in[i].Name,
			EFSVolumeConfiguration:    in[i].EFSVolumeConfiguration,
			DockerVolumeConfiguration: in[i].DockerVolumeConfiguration,
		}

		if in[i].Host != nil {
			v.Host = &wireHostVolumeProperties{SourcePath: in[i].Host.SourcePath}
		}

		out = append(out, v)
	}

	return out
}

func fromEphemeralStorage(in *driver.EphemeralStorage) *wireEphemeralStorage {
	if in == nil {
		return nil
	}

	return &wireEphemeralStorage{SizeInGiB: in.SizeInGiB}
}

func fromRuntimePlatform(in *driver.RuntimePlatform) *wireRuntimePlatform {
	if in == nil {
		return nil
	}

	return &wireRuntimePlatform{CPUArchitecture: in.CPUArchitecture, OperatingSystemFamily: in.OperatingSystemFamily}
}

func fromProxyConfiguration(in *driver.ProxyConfiguration) *wireProxyConfiguration {
	if in == nil {
		return nil
	}

	return &wireProxyConfiguration{Type: in.Type, ContainerName: in.ContainerName, Properties: fromKeyValues(in.Properties)}
}

func fromPlacementConstraints(in []driver.PlacementConstraint) []wirePlacementConstraint {
	if len(in) == 0 {
		return nil
	}

	out := make([]wirePlacementConstraint, 0, len(in))
	for _, c := range in {
		out = append(out, wirePlacementConstraint{Type: c.Type, Expression: c.Expression})
	}

	return out
}

func fromInferenceAccelerators(in []driver.InferenceAccelerator) []wireInferenceAccelerator {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireInferenceAccelerator, 0, len(in))
	for _, a := range in {
		out = append(out, wireInferenceAccelerator{DeviceName: a.DeviceName, DeviceType: a.DeviceType})
	}

	return out
}

func fromKeyValues(in []driver.KeyValue) []wireKeyValue {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireKeyValue, 0, len(in))
	for _, kv := range in {
		out = append(out, wireKeyValue{Name: kv.Name, Value: kv.Value})
	}

	return out
}

func fromFailures(in []driver.Failure) []wireFailure {
	out := make([]wireFailure, 0, len(in))
	for _, f := range in {
		out = append(out, wireFailure{ARN: f.ARN, Reason: f.Reason, Detail: f.Detail})
	}

	return out
}

func clusterToWire(c *driver.Cluster) wireCluster {
	return wireCluster{
		ClusterArn:                        c.ARN,
		ClusterName:                       c.Name,
		Status:                            c.Status,
		RegisteredContainerInstancesCount: c.RegisteredContainerInstancesCount,
		RunningTasksCount:                 c.RunningTasksCount,
		PendingTasksCount:                 c.PendingTasksCount,
		ActiveServicesCount:               c.ActiveServicesCount,
		Tags:                              fromTags(c.Tags),
		Settings:                          fromSettings(c.Settings),
		CapacityProviders:                 c.CapacityProviders,
		DefaultCapacityProviderStrategy:   fromCapacityProviderStrategy(c.DefaultCapacityProviderStrategy),
	}
}

// fromCapacityProviderStrategy serializes a cluster's default capacity-provider
// strategy to the wire shape.
func fromCapacityProviderStrategy(in []driver.CapacityProviderStrategyItem) []wireCapacityProviderStrategyItem {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireCapacityProviderStrategyItem, 0, len(in))
	for i := range in {
		out = append(out, wireCapacityProviderStrategyItem{
			CapacityProvider: in[i].CapacityProvider, Base: in[i].Base, Weight: in[i].Weight,
		})
	}

	return out
}

// toAttributes / fromAttributes convert between the wire and driver attribute
// shapes.
func toAttributes(in []wireAttribute) []driver.Attribute {
	out := make([]driver.Attribute, 0, len(in))
	for _, a := range in {
		out = append(out, driver.Attribute{Name: a.Name, Value: a.Value, TargetID: a.TargetID, TargetType: a.TargetType})
	}

	return out
}

func fromAttributes(in []driver.Attribute) []wireAttribute {
	out := make([]wireAttribute, 0, len(in))
	for i := range in {
		out = append(out, wireAttribute{
			Name: in[i].Name, Value: in[i].Value, TargetID: in[i].TargetID, TargetType: in[i].TargetType,
		})
	}

	return out
}

// toResources converts wire resources to the driver shape (used by
// RegisterContainerInstance's totalResources).
func toResources(in []wireResource) []driver.Resource {
	out := make([]driver.Resource, 0, len(in))
	for _, r := range in {
		out = append(out, driver.Resource{
			Name: r.Name, Type: r.Type, IntegerValue: r.IntegerValue,
			DoubleValue: r.DoubleValue, LongValue: r.LongValue, StringSetValue: r.StringSetValue,
		})
	}

	return out
}

// fromAccountSetting / fromAccountSettings serialize account settings to wire.
func fromAccountSetting(s *driver.AccountSetting) wireAccountSetting {
	return wireAccountSetting{Name: s.Name, Value: s.Value}
}

func fromAccountSettings(in []driver.AccountSetting) []wireAccountSetting {
	out := make([]wireAccountSetting, 0, len(in))
	for i := range in {
		out = append(out, wireAccountSetting{Name: in[i].Name, Value: in[i].Value})
	}

	return out
}

// Launch-type and derived-attribute constants for the compatibilities and
// requiresAttributes fields ECS derives (not stored) for a task definition.
const (
	launchTypeEC2      = "EC2"
	launchTypeFargate  = "FARGATE"
	launchTypeExternal = "EXTERNAL"
	networkModeAwsvpc  = "awsvpc"

	attrDockerRemoteAPI = "com.amazonaws.ecs.capability.docker-remote-api.1.18"
	attrTaskIAMRole     = "com.amazonaws.ecs.capability.task-iam-role"
	attrExecRoleECRPull = "ecs.capability.execution-role-ecr-pull"
)

// deriveCompatibilities computes the launch types a task definition is
// compatible with (distinct from the caller's requiresCompatibilities): EC2 is
// always compatible, plus every explicitly required type, plus FARGATE when the
// definition satisfies the Fargate requirements (awsvpc networking with task
// cpu and memory). The result is emitted in a stable EC2, FARGATE, EXTERNAL order.
func deriveCompatibilities(t *driver.TaskDefinition) []string {
	set := map[string]bool{launchTypeEC2: true}
	for _, c := range t.RequiresCompatibilities {
		set[c] = true
	}

	if t.NetworkMode == networkModeAwsvpc && t.CPU != "" && t.Memory != "" {
		set[launchTypeFargate] = true
	}

	out := make([]string, 0, len(set))

	for _, lt := range []string{launchTypeEC2, launchTypeFargate, launchTypeExternal} {
		if set[lt] {
			out = append(out, lt)
		}
	}

	return out
}

// isFargateOnly reports whether a task definition targets only Fargate, i.e. it
// requires FARGATE and no EC2/EXTERNAL launch type. Such definitions carry no
// requiresAttributes on the wire.
func isFargateOnly(t *driver.TaskDefinition) bool {
	requiresFargate := false

	for _, c := range t.RequiresCompatibilities {
		if c == launchTypeEC2 || c == launchTypeExternal {
			return false
		}

		if c == launchTypeFargate {
			requiresFargate = true
		}
	}

	return requiresFargate
}

// deriveRequiresAttributes computes the minimal set of container-agent
// attributes ECS reports a task definition requires. Fargate-only definitions
// report none; others always require the docker-remote-api capability, plus the
// task-iam-role / execution-role-ecr-pull capabilities when the corresponding
// role ARNs are set.
func deriveRequiresAttributes(t *driver.TaskDefinition) []wireAttribute {
	if isFargateOnly(t) {
		return nil
	}

	attrs := []wireAttribute{{Name: attrDockerRemoteAPI}}

	if t.TaskRoleARN != "" {
		attrs = append(attrs, wireAttribute{Name: attrTaskIAMRole})
	}

	if t.ExecutionRoleARN != "" {
		attrs = append(attrs, wireAttribute{Name: attrExecRoleECRPull})
	}

	return attrs
}

func taskDefToWire(t *driver.TaskDefinition) wireTaskDef {
	return wireTaskDef{
		TaskDefinitionArn:       t.ARN,
		Family:                  t.Family,
		Revision:                t.Revision,
		Status:                  t.Status,
		ContainerDefinitions:    fromContainerDefs(t.ContainerDefinitions),
		CPU:                     t.CPU,
		Memory:                  t.Memory,
		NetworkMode:             t.NetworkMode,
		RequiresCompatibilities: t.RequiresCompatibilities,
		Compatibilities:         deriveCompatibilities(t),
		RequiresAttributes:      deriveRequiresAttributes(t),
		TaskRoleArn:             t.TaskRoleARN,
		ExecutionRoleArn:        t.ExecutionRoleARN,
		Volumes:                 fromVolumes(t.Volumes),
		EphemeralStorage:        fromEphemeralStorage(t.EphemeralStorage),
		PidMode:                 t.PidMode,
		IpcMode:                 t.IpcMode,
		RuntimePlatform:         fromRuntimePlatform(t.RuntimePlatform),
		ProxyConfiguration:      fromProxyConfiguration(t.ProxyConfiguration),
		PlacementConstraints:    fromPlacementConstraints(t.PlacementConstraints),
		InferenceAccelerators:   fromInferenceAccelerators(t.InferenceAccelerators),
		EnableFaultInjection:    t.EnableFaultInjection,
		RegisteredBy:            t.RegisteredBy,
		RegisteredAt:            epoch(t.RegisteredAt),
		DeregisteredAt:          epoch(t.DeregisteredAt),
	}
}

// fromNetworkBindings converts a container's resolved bridge/host port
// bindings to the wire shape.
func fromNetworkBindings(in []driver.NetworkBinding) []wireNetworkBinding {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireNetworkBinding, 0, len(in))
	for _, nb := range in {
		out = append(out, wireNetworkBinding{
			BindIP: nb.BindIP, ContainerPort: nb.ContainerPort, HostPort: nb.HostPort, Protocol: nb.Protocol,
		})
	}

	return out
}

func taskToWire(t *driver.Task) wireTask {
	containers := make([]wireContainer, 0, len(t.Containers))

	for i := range t.Containers {
		c := t.Containers[i]
		wc := wireContainer{
			Name: c.Name, Image: c.Image, LastStatus: c.LastStatus,
			Reason: c.Reason, RuntimeID: c.RuntimeID,
			NetworkBindings: fromNetworkBindings(c.NetworkBindings),
		}
		// Surface the exit code only once the container has stopped, so a genuine
		// exit 0 is reported while a running container has none.
		if c.LastStatus == containerStatusStopped {
			ec := c.ExitCode
			wc.ExitCode = &ec
		}

		containers = append(containers, wc)
	}

	return wireTask{
		TaskArn:              t.ARN,
		ClusterArn:           t.ClusterARN,
		TaskDefinitionArn:    t.TaskDefinitionARN,
		ContainerInstanceArn: t.ContainerInstanceARN,
		LastStatus:           t.LastStatus,
		DesiredStatus:        t.DesiredStatus,
		LaunchType:           t.LaunchType,
		PlatformVersion:      t.PlatformVersion,
		Group:                t.Group,
		StartedBy:            t.StartedBy,
		CreatedAt:            epoch(t.CreatedAt),
		StoppedReason:        t.StoppedReason,
		StopCode:             t.StopCode,
		Containers:           containers,
		Attachments:          fromAttachments(t.Attachments),
		Tags:                 fromTags(t.Tags),
	}
}

func fromAttachments(in []driver.Attachment) []wireAttachment {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireAttachment, 0, len(in))
	for i := range in {
		out = append(out, wireAttachment{
			Type:    in[i].Type,
			Status:  in[i].Status,
			Details: fromKeyValues(in[i].Details),
		})
	}

	return out
}

// resourcesFromCapacity builds the CPU and MEMORY INTEGER resource objects the
// ECS SDK expects for a container instance's registered/remaining resources.
func resourcesFromCapacity(cpu, memory int) []wireResource {
	return []wireResource{
		{Name: "CPU", Type: "INTEGER", IntegerValue: cpu},
		{Name: "MEMORY", Type: "INTEGER", IntegerValue: memory},
	}
}

func serviceToWire(s *driver.Service) wireService {
	out := wireService{
		ServiceArn:                    s.ARN,
		ServiceName:                   s.Name,
		ClusterArn:                    s.ClusterARN,
		TaskDefinition:                s.TaskDefinition,
		RoleArn:                       s.RoleARN,
		CreatedBy:                     s.CreatedBy,
		DesiredCount:                  s.DesiredCount,
		RunningCount:                  s.RunningCount,
		PendingCount:                  s.PendingCount,
		Status:                        s.Status,
		LaunchType:                    s.LaunchType,
		SchedulingStrategy:            s.SchedulingStrategy,
		PlatformVersion:               s.PlatformVersion,
		PropagateTags:                 s.PropagateTags,
		EnableExecuteCommand:          s.EnableExecuteCommand,
		HealthCheckGracePeriodSeconds: s.HealthCheckGracePeriodSeconds,
		DeploymentConfiguration:       fromDeploymentConfiguration(s.DeploymentConfiguration),
		NetworkConfiguration:          fromNetworkConfiguration(s.NetworkConfiguration),
		LoadBalancers:                 fromLoadBalancers(s.LoadBalancers),
		ServiceRegistries:             fromServiceRegistries(s.ServiceRegistries),
		Deployments:                   fromDeployments(s.Deployments),
		Events:                        fromServiceEvents(s.Events),
		CreatedAt:                     epoch(s.CreatedAt),
		Tags:                          fromTags(s.Tags),
	}
	if s.DeploymentController != "" {
		out.DeploymentController = &wireDeploymentController{Type: s.DeploymentController}
	}

	return out
}

func fromLoadBalancers(in []driver.LoadBalancer) []wireLoadBalancer {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireLoadBalancer, 0, len(in))
	for i := range in {
		out = append(out, wireLoadBalancer{
			TargetGroupArn:   in[i].TargetGroupARN,
			LoadBalancerName: in[i].LoadBalancerName,
			ContainerName:    in[i].ContainerName,
			ContainerPort:    in[i].ContainerPort,
		})
	}

	return out
}

func fromServiceRegistries(in []driver.ServiceRegistry) []wireServiceRegistry {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireServiceRegistry, 0, len(in))
	for i := range in {
		out = append(out, wireServiceRegistry{
			RegistryArn:   in[i].RegistryARN,
			ContainerName: in[i].ContainerName,
			ContainerPort: in[i].ContainerPort,
			Port:          in[i].Port,
		})
	}

	return out
}

func fromDeployments(in []driver.Deployment) []wireDeployment {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireDeployment, 0, len(in))
	for i := range in {
		out = append(out, wireDeployment{
			ID: in[i].ID, Status: in[i].Status, TaskDefinition: in[i].TaskDefinition,
			DesiredCount: in[i].DesiredCount, RunningCount: in[i].RunningCount, PendingCount: in[i].PendingCount,
			LaunchType: in[i].LaunchType, RolloutState: in[i].RolloutState, RolloutStateReason: in[i].RolloutStateReason,
			CreatedAt: epoch(in[i].CreatedAt), UpdatedAt: epoch(in[i].UpdatedAt),
		})
	}

	return out
}

func fromServiceEvents(in []driver.ServiceEvent) []wireServiceEvent {
	if len(in) == 0 {
		return nil
	}

	out := make([]wireServiceEvent, 0, len(in))
	for i := range in {
		out = append(out, wireServiceEvent{ID: in[i].ID, CreatedAt: epoch(in[i].CreatedAt), Message: in[i].Message})
	}

	return out
}

func fromDeploymentConfiguration(in *driver.DeploymentConfiguration) *wireDeploymentConfiguration {
	if in == nil {
		return nil
	}

	out := &wireDeploymentConfiguration{MaximumPercent: in.MaximumPercent, MinimumHealthyPercent: in.MinimumHealthyPercent}
	if cb := in.DeploymentCircuitBreaker; cb != nil {
		out.DeploymentCircuitBreaker = &wireDeploymentCircuitBreaker{Enable: cb.Enable, Rollback: cb.Rollback}
	}

	return out
}

func fromNetworkConfiguration(in *driver.NetworkConfiguration) *wireNetworkConfiguration {
	if in == nil || in.AwsVpcConfiguration == nil {
		return nil
	}

	v := in.AwsVpcConfiguration

	return &wireNetworkConfiguration{AwsvpcConfiguration: &wireAwsVpcConfiguration{
		Subnets: v.Subnets, SecurityGroups: v.SecurityGroups, AssignPublicIP: v.AssignPublicIP,
	}}
}

// --- request loadBalancer/serviceRegistry/deploymentConfiguration converters ---

func toLoadBalancers(in []wireLoadBalancer) []driver.LoadBalancer {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.LoadBalancer, 0, len(in))
	for i := range in {
		out = append(out, driver.LoadBalancer{
			TargetGroupARN: in[i].TargetGroupArn, LoadBalancerName: in[i].LoadBalancerName,
			ContainerName: in[i].ContainerName, ContainerPort: in[i].ContainerPort,
		})
	}

	return out
}

func toServiceRegistries(in []wireServiceRegistry) []driver.ServiceRegistry {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.ServiceRegistry, 0, len(in))
	for i := range in {
		out = append(out, driver.ServiceRegistry{
			RegistryARN: in[i].RegistryArn, ContainerName: in[i].ContainerName,
			ContainerPort: in[i].ContainerPort, Port: in[i].Port,
		})
	}

	return out
}

func toDeploymentConfiguration(in *wireDeploymentConfiguration) *driver.DeploymentConfiguration {
	if in == nil {
		return nil
	}

	out := &driver.DeploymentConfiguration{MaximumPercent: in.MaximumPercent, MinimumHealthyPercent: in.MinimumHealthyPercent}
	if cb := in.DeploymentCircuitBreaker; cb != nil {
		out.DeploymentCircuitBreaker = &driver.DeploymentCircuitBreaker{Enable: cb.Enable, Rollback: cb.Rollback}
	}

	return out
}

func instanceToWire(ci *driver.ContainerInstance) wireContainerInstance {
	return wireContainerInstance{
		ContainerInstanceArn: ci.ARN,
		Ec2InstanceID:        ci.EC2InstanceID,
		Status:               ci.Status,
		RunningTasksCount:    ci.RunningTasksCount,
		PendingTasksCount:    ci.PendingTasksCount,
		AgentConnected:       ci.AgentConnected,
		RegisteredResources:  resourcesFromCapacity(ci.RegisteredCPU, ci.RegisteredMemory),
		RemainingResources:   resourcesFromCapacity(ci.RemainingCPU, ci.RemainingMemory),
	}
}
