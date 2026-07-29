package ecs

import "github.com/stackshy/cloudemu/v2/services/ecs/driver"

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
}

type wireContainerDef struct {
	Name         string            `json:"name,omitempty"`
	Image        string            `json:"image,omitempty"`
	CPU          int               `json:"cpu,omitempty"`
	Memory       int               `json:"memory,omitempty"`
	Essential    bool              `json:"essential"`
	PortMappings []wirePortMapping `json:"portMappings,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Environment  []wireKeyValue    `json:"environment,omitempty"`
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
}

type wireTaskDef struct {
	TaskDefinitionArn       string             `json:"taskDefinitionArn"`
	Family                  string             `json:"family"`
	Revision                int                `json:"revision"`
	Status                  string             `json:"status"`
	ContainerDefinitions    []wireContainerDef `json:"containerDefinitions"`
	CPU                     string             `json:"cpu,omitempty"`
	Memory                  string             `json:"memory,omitempty"`
	NetworkMode             string             `json:"networkMode,omitempty"`
	RequiresCompatibilities []string           `json:"requiresCompatibilities,omitempty"`
	TaskRoleArn             string             `json:"taskRoleArn,omitempty"`
	ExecutionRoleArn        string             `json:"executionRoleArn,omitempty"`
	RegisteredAt            float64            `json:"registeredAt,omitempty"`
	DeregisteredAt          float64            `json:"deregisteredAt,omitempty"`
}

type wireContainer struct {
	Name       string `json:"name,omitempty"`
	Image      string `json:"image,omitempty"`
	LastStatus string `json:"lastStatus,omitempty"`
}

type wireTask struct {
	TaskArn           string          `json:"taskArn"`
	ClusterArn        string          `json:"clusterArn"`
	TaskDefinitionArn string          `json:"taskDefinitionArn"`
	LastStatus        string          `json:"lastStatus"`
	DesiredStatus     string          `json:"desiredStatus"`
	LaunchType        string          `json:"launchType,omitempty"`
	Group             string          `json:"group,omitempty"`
	StartedBy         string          `json:"startedBy,omitempty"`
	CreatedAt         float64         `json:"createdAt,omitempty"`
	StoppedReason     string          `json:"stoppedReason,omitempty"`
	StopCode          string          `json:"stopCode,omitempty"`
	Containers        []wireContainer `json:"containers,omitempty"`
	Tags              []wireTag       `json:"tags,omitempty"`
}

type wireService struct {
	ServiceArn         string    `json:"serviceArn"`
	ServiceName        string    `json:"serviceName"`
	ClusterArn         string    `json:"clusterArn"`
	TaskDefinition     string    `json:"taskDefinition,omitempty"`
	DesiredCount       int       `json:"desiredCount"`
	RunningCount       int       `json:"runningCount"`
	PendingCount       int       `json:"pendingCount"`
	Status             string    `json:"status"`
	LaunchType         string    `json:"launchType,omitempty"`
	SchedulingStrategy string    `json:"schedulingStrategy,omitempty"`
	CreatedAt          float64   `json:"createdAt,omitempty"`
	Tags               []wireTag `json:"tags,omitempty"`
}

type wireContainerInstance struct {
	ContainerInstanceArn string `json:"containerInstanceArn"`
	Ec2InstanceID        string `json:"ec2InstanceId,omitempty"`
	Status               string `json:"status"`
	RunningTasksCount    int    `json:"runningTasksCount"`
	PendingTasksCount    int    `json:"pendingTasksCount"`
	AgentConnected       bool   `json:"agentConnected"`
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
			Name:         c.Name,
			Image:        c.Image,
			CPU:          c.CPU,
			Memory:       c.Memory,
			Essential:    c.Essential,
			PortMappings: toPortMappings(c.PortMappings),
			Command:      c.Command,
			Environment:  toKeyValues(c.Environment),
		})
	}

	return out
}

func toPortMappings(in []wirePortMapping) []driver.PortMapping {
	out := make([]driver.PortMapping, 0, len(in))
	for _, p := range in {
		out = append(out, driver.PortMapping{ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: p.Protocol})
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
		out = append(out, wireContainerDef{
			Name:         c.Name,
			Image:        c.Image,
			CPU:          c.CPU,
			Memory:       c.Memory,
			Essential:    c.Essential,
			PortMappings: fromPortMappings(c.PortMappings),
			Command:      c.Command,
			Environment:  fromKeyValues(c.Environment),
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
		out = append(out, wirePortMapping{ContainerPort: p.ContainerPort, HostPort: p.HostPort, Protocol: p.Protocol})
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
	}
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
		TaskRoleArn:             t.TaskRoleARN,
		ExecutionRoleArn:        t.ExecutionRoleARN,
		RegisteredAt:            epoch(t.RegisteredAt),
		DeregisteredAt:          epoch(t.DeregisteredAt),
	}
}

func taskToWire(t *driver.Task) wireTask {
	containers := make([]wireContainer, 0, len(t.Containers))
	for _, c := range t.Containers {
		containers = append(containers, wireContainer{Name: c.Name, Image: c.Image, LastStatus: c.LastStatus})
	}

	return wireTask{
		TaskArn:           t.ARN,
		ClusterArn:        t.ClusterARN,
		TaskDefinitionArn: t.TaskDefinitionARN,
		LastStatus:        t.LastStatus,
		DesiredStatus:     t.DesiredStatus,
		LaunchType:        t.LaunchType,
		Group:             t.Group,
		StartedBy:         t.StartedBy,
		CreatedAt:         epoch(t.CreatedAt),
		StoppedReason:     t.StoppedReason,
		StopCode:          t.StopCode,
		Containers:        containers,
		Tags:              fromTags(t.Tags),
	}
}

func serviceToWire(s *driver.Service) wireService {
	return wireService{
		ServiceArn:         s.ARN,
		ServiceName:        s.Name,
		ClusterArn:         s.ClusterARN,
		TaskDefinition:     s.TaskDefinition,
		DesiredCount:       s.DesiredCount,
		RunningCount:       s.RunningCount,
		PendingCount:       s.PendingCount,
		Status:             s.Status,
		LaunchType:         s.LaunchType,
		SchedulingStrategy: s.SchedulingStrategy,
		CreatedAt:          epoch(s.CreatedAt),
		Tags:               fromTags(s.Tags),
	}
}

func instanceToWire(ci *driver.ContainerInstance) wireContainerInstance {
	return wireContainerInstance{
		ContainerInstanceArn: ci.ARN,
		Ec2InstanceID:        ci.EC2InstanceID,
		Status:               ci.Status,
		RunningTasksCount:    ci.RunningTasksCount,
		PendingTasksCount:    ci.PendingTasksCount,
		AgentConnected:       ci.AgentConnected,
	}
}
