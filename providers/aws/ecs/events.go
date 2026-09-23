package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// EventBridge identity of ECS's native events. See "Amazon ECS events" in the
// ECS developer guide (task state change and service action events).
const (
	eventSource            = "aws.ecs"
	eventTaskStateChange   = "ECS Task State Change"
	eventServiceAction     = "ECS Service Action"
	serviceEventTypeInfo   = "INFO"
	serviceSteadyStateName = "SERVICE_STEADY_STATE"

	// taskEventVersionLaunch / taskEventVersionStop are the detail.version of a
	// task's launch and stop state-change events. Real ECS increments version on
	// every task state change so consumers can drop out-of-order events; the
	// emulator publishes one event per stored transition, in order.
	taskEventVersionLaunch = 1
	taskEventVersionStop   = 2
)

// taskStateChangeDetail is the detail payload of an "ECS Task State Change"
// event: the task record as DescribeTasks reports it.
type taskStateChangeDetail struct {
	Attachments          []taskEventAttachment `json:"attachments"`
	AvailabilityZone     string                `json:"availabilityZone,omitempty"`
	ClusterArn           string                `json:"clusterArn"`
	Connectivity         string                `json:"connectivity,omitempty"`
	ContainerInstanceArn string                `json:"containerInstanceArn,omitempty"`
	Containers           []taskEventContainer  `json:"containers"`
	CPU                  string                `json:"cpu,omitempty"`
	CreatedAt            string                `json:"createdAt"`
	DesiredStatus        string                `json:"desiredStatus"`
	Group                string                `json:"group,omitempty"`
	LastStatus           string                `json:"lastStatus"`
	LaunchType           string                `json:"launchType"`
	Memory               string                `json:"memory,omitempty"`
	PlatformVersion      string                `json:"platformVersion,omitempty"`
	StartedAt            string                `json:"startedAt,omitempty"`
	StartedBy            string                `json:"startedBy,omitempty"`
	StopCode             string                `json:"stopCode,omitempty"`
	StoppedAt            string                `json:"stoppedAt,omitempty"`
	StoppedReason        string                `json:"stoppedReason,omitempty"`
	StoppingAt           string                `json:"stoppingAt,omitempty"`
	TaskArn              string                `json:"taskArn"`
	TaskDefinitionArn    string                `json:"taskDefinitionArn"`
	UpdatedAt            string                `json:"updatedAt"`
	Version              int                   `json:"version"`
}

// taskEventContainer is one entry of the event's containers[]. exitCode is
// present only once the container has stopped, as in the real event.
type taskEventContainer struct {
	ContainerArn string `json:"containerArn,omitempty"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	Image        string `json:"image,omitempty"`
	LastStatus   string `json:"lastStatus"`
	Name         string `json:"name"`
	Reason       string `json:"reason,omitempty"`
	TaskArn      string `json:"taskArn"`
}

type taskEventAttachment struct {
	Details []taskEventKeyValue `json:"details"`
	Status  string              `json:"status"`
	Type    string              `json:"type"`
}

type taskEventKeyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// serviceActionDetail is the detail payload of an "ECS Service Action" event.
type serviceActionDetail struct {
	EventType  string `json:"eventType"`
	EventName  string `json:"eventName"`
	ClusterArn string `json:"clusterArn"`
	CreatedAt  string `json:"createdAt"`
}

// pendingTaskEvents buffers task state changes made while a service record is
// not yet committed (CreateService/UpdateService/DeleteService converging or
// draining it). They are published only after the caller stores the service:
// an event target that stops a task re-enters reconcileServiceAfterStop, which
// must see the committed record; against the half-built or superseded one, it
// computes the wrong shortfall and over-provisions the service.
type pendingTaskEvents struct {
	tasks    []*driver.Task
	versions []int
}

func (p *pendingTaskEvents) add(t *driver.Task, version int) {
	p.tasks = append(p.tasks, t)
	p.versions = append(p.versions, version)
}

// publish emits the buffered events in the order they happened.
func (m *Mock) publish(ctx context.Context, p *pendingTaskEvents) {
	for i, t := range p.tasks {
		m.emitTaskStateChange(ctx, t, p.versions[i])
	}
}

// SetEventPublisher wires the EventBridge default bus that task state changes
// and service actions are published to. Safe to leave unset. No events are
// emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// emitTaskStateChange publishes an "ECS Task State Change" event carrying the
// task's stored (final) state. It must be called without placeMu held.
func (m *Mock) emitTaskStateChange(ctx context.Context, t *driver.Task, version int) {
	containers := make([]taskEventContainer, 0, len(t.Containers))

	for i := range t.Containers {
		c := &t.Containers[i]
		ec := taskEventContainer{
			ContainerArn: c.ARN, Image: c.Image, LastStatus: c.LastStatus, Name: c.Name, Reason: c.Reason, TaskArn: t.ARN,
		}

		if c.LastStatus == statusStopped {
			code := c.ExitCode
			ec.ExitCode = &code
		}

		containers = append(containers, ec)
	}

	attachments := make([]taskEventAttachment, 0, len(t.Attachments))

	for _, a := range t.Attachments {
		details := make([]taskEventKeyValue, 0, len(a.Details))
		for _, kv := range a.Details {
			details = append(details, taskEventKeyValue{Name: kv.Name, Value: kv.Value})
		}

		attachments = append(attachments, taskEventAttachment{Details: details, Status: a.Status, Type: a.Type})
	}

	m.events.Emit(ctx, eventSource, eventTaskStateChange, taskStateChangeDetail{
		Attachments: attachments, AvailabilityZone: t.AvailabilityZone, ClusterArn: t.ClusterARN,
		Connectivity: t.Connectivity, ContainerInstanceArn: t.ContainerInstanceARN, Containers: containers,
		CPU: t.CPU, CreatedAt: t.CreatedAt, DesiredStatus: t.DesiredStatus, Group: t.Group,
		LastStatus: t.LastStatus, LaunchType: t.LaunchType, Memory: t.Memory, PlatformVersion: t.PlatformVersion,
		StartedAt: t.StartedAt, StartedBy: t.StartedBy, StopCode: t.StopCode, StoppedAt: t.StoppedAt,
		StoppedReason: t.StoppedReason, StoppingAt: t.StoppingAt, TaskArn: t.ARN,
		TaskDefinitionArn: t.TaskDefinitionARN, UpdatedAt: m.now(), Version: version,
	}, t.ARN)
}

// emitServiceSteadyState publishes the SERVICE_STEADY_STATE service action once
// a service's running count has converged to its desired count, as real ECS
// does when a deployment settles.
func (m *Mock) emitServiceSteadyState(ctx context.Context, svc *driver.Service) {
	if svc.RunningCount != svc.DesiredCount || svc.PendingCount != 0 {
		return
	}

	m.events.Emit(ctx, eventSource, eventServiceAction, serviceActionDetail{
		EventType: serviceEventTypeInfo, EventName: serviceSteadyStateName,
		ClusterArn: svc.ClusterARN, CreatedAt: m.now(),
	}, svc.ARN)
}
