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
	ClusterArn           string                `json:"clusterArn"`
	ContainerInstanceArn string                `json:"containerInstanceArn,omitempty"`
	Containers           []taskEventContainer  `json:"containers"`
	CreatedAt            string                `json:"createdAt"`
	DesiredStatus        string                `json:"desiredStatus"`
	Group                string                `json:"group,omitempty"`
	LastStatus           string                `json:"lastStatus"`
	LaunchType           string                `json:"launchType"`
	PlatformVersion      string                `json:"platformVersion,omitempty"`
	StartedBy            string                `json:"startedBy,omitempty"`
	StopCode             string                `json:"stopCode,omitempty"`
	StoppedReason        string                `json:"stoppedReason,omitempty"`
	TaskArn              string                `json:"taskArn"`
	TaskDefinitionArn    string                `json:"taskDefinitionArn"`
	UpdatedAt            string                `json:"updatedAt"`
	Version              int                   `json:"version"`
}

type taskEventContainer struct {
	Image      string `json:"image,omitempty"`
	LastStatus string `json:"lastStatus"`
	Name       string `json:"name"`
	Reason     string `json:"reason,omitempty"`
	TaskArn    string `json:"taskArn"`
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

// SetEventPublisher wires the EventBridge default bus that task state changes
// and service actions are published to. Safe to leave unset — no events are
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
		containers = append(containers, taskEventContainer{
			Image: c.Image, LastStatus: c.LastStatus, Name: c.Name, Reason: c.Reason, TaskArn: t.ARN,
		})
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
		Attachments: attachments, ClusterArn: t.ClusterARN, ContainerInstanceArn: t.ContainerInstanceARN,
		Containers: containers, CreatedAt: t.CreatedAt, DesiredStatus: t.DesiredStatus, Group: t.Group,
		LastStatus: t.LastStatus, LaunchType: t.LaunchType, PlatformVersion: t.PlatformVersion,
		StartedBy: t.StartedBy, StopCode: t.StopCode, StoppedReason: t.StoppedReason, TaskArn: t.ARN,
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
