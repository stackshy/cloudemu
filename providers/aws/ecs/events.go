package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// EventBridge identity of ECS's native events. See "Amazon ECS events" in the
// ECS developer guide (task, container instance, service deployment and service
// action events).
const (
	eventSource                       = "aws.ecs"
	eventTaskStateChange              = "ECS Task State Change"
	eventServiceAction                = "ECS Service Action"
	eventDeploymentStateChange        = "ECS Deployment State Change"
	eventContainerInstanceStateChange = "ECS Container Instance State Change"

	serviceEventTypeInfo  = "INFO"
	serviceEventTypeError = "ERROR"

	serviceSteadyStateName        = "SERVICE_STEADY_STATE"
	serviceDesiredCountUpdated    = "SERVICE_DESIRED_COUNT_UPDATED"
	serviceDeploymentInProgress   = "SERVICE_DEPLOYMENT_IN_PROGRESS"
	serviceDeploymentCompleted    = "SERVICE_DEPLOYMENT_COMPLETED"
	serviceTaskPlacementFailure   = "SERVICE_TASK_PLACEMENT_FAILURE"
	containerInstanceResourceType = "INTEGER"
)

// Task lifecycle states, from the ECS task lifecycle in the developer guide:
// PROVISIONING (awsvpc only) -> PENDING -> ACTIVATING -> RUNNING -> DEACTIVATING
// -> STOPPING -> DEPROVISIONING (awsvpc only) -> STOPPED. The emulator moves a
// task through them synchronously and publishes one event per state.
const (
	taskStatusActivating   = "ACTIVATING"
	taskStatusDeactivating = "DEACTIVATING"

	// maxLaunchSteps bounds the launch-then-stop state list of one task.
	maxLaunchSteps = 8
)

// launchSteps lists the lastStatus values a task passes through when it is
// launched, ending at the task's current state: an unplaced EC2 task stays
// PENDING, a task that already ran to completion continues through its stop.
func launchSteps(t *driver.Task) []string {
	steps := make([]string, 0, maxLaunchSteps)

	if len(t.Attachments) > 0 {
		steps = append(steps, taskStatusProvisioning)
	}

	steps = append(steps, statusPending)
	if t.LastStatus == statusPending {
		return steps
	}

	steps = append(steps, taskStatusActivating, statusRunning)
	if t.LastStatus == statusStopped {
		steps = append(steps, stopSteps(t)...)
	}

	return steps
}

// stopSteps lists the lastStatus values a task passes through when it stops.
func stopSteps(t *driver.Task) []string {
	steps := []string{taskStatusDeactivating, taskStatusStopping}
	if len(t.Attachments) > 0 {
		steps = append(steps, taskStatusDeprovisioning)
	}

	return append(steps, statusStopped)
}

// stoppingPhase reports whether step belongs to the stop half of the lifecycle.
func stoppingPhase(step string) bool {
	switch step {
	case taskStatusDeactivating, taskStatusStopping, taskStatusDeprovisioning, statusStopped:
		return true
	default:
		return false
	}
}

// runningOrLater reports whether the task has reached RUNNING by step.
func runningOrLater(step string) bool {
	return step == statusRunning || stoppingPhase(step)
}

// containerStatusAt is the lastStatus a task's containers report while the task
// is in step.
func containerStatusAt(step string) string {
	switch step {
	case statusStopped:
		return statusStopped
	case statusRunning, taskStatusDeactivating, taskStatusStopping, taskStatusDeprovisioning:
		return statusRunning
	default:
		return statusPending
	}
}

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
	ID      string              `json:"id,omitempty"`
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
	Reason     string `json:"reason,omitempty"`
}

// deploymentStateChangeDetail is the detail payload of an "ECS Deployment State
// Change" event.
type deploymentStateChangeDetail struct {
	EventType    string `json:"eventType"`
	EventName    string `json:"eventName"`
	DeploymentID string `json:"deploymentId"`
	UpdatedAt    string `json:"updatedAt"`
	Reason       string `json:"reason"`
}

// containerInstanceDetail is the detail payload of an "ECS Container Instance
// State Change" event.
type containerInstanceDetail struct {
	AgentConnected       bool                        `json:"agentConnected"`
	Attributes           []containerInstanceAttr     `json:"attributes"`
	ClusterArn           string                      `json:"clusterArn"`
	ContainerInstanceArn string                      `json:"containerInstanceArn"`
	EC2InstanceID        string                      `json:"ec2InstanceId"`
	RegisteredResources  []containerInstanceResource `json:"registeredResources"`
	RemainingResources   []containerInstanceResource `json:"remainingResources"`
	Status               string                      `json:"status"`
	Version              int                         `json:"version"`
	UpdatedAt            string                      `json:"updatedAt"`
}

type containerInstanceAttr struct {
	Name string `json:"name"`
}

type containerInstanceResource struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	IntegerValue int    `json:"integerValue"`
}

// pendingTaskEvents buffers events produced while a service record or task-set
// lock is held (CreateService/UpdateService/DeleteService converging or draining
// a service, task-set scaling). They are published only after the caller commits
// and releases its locks: an event target that stops a task re-enters
// reconcileServiceAfterStop, which must see the committed record and can take
// the same non-reentrant locks, and against a half-built or superseded record it
// would compute the wrong shortfall and over-provision the service.
type pendingTaskEvents struct {
	emits []func(context.Context)
}

// addLaunch buffers the launch events of a task snapshot.
func (p *pendingTaskEvents) addLaunch(m *Mock, t *driver.Task) {
	p.emits = append(p.emits, func(ctx context.Context) { m.emitTaskLaunch(ctx, t) })
}

// addStop buffers the stop events of a task snapshot.
func (p *pendingTaskEvents) addStop(m *Mock, t *driver.Task) {
	p.emits = append(p.emits, func(ctx context.Context) { m.emitTaskStop(ctx, t) })
}

// addFunc buffers an arbitrary emit.
func (p *pendingTaskEvents) addFunc(fn func(context.Context)) {
	p.emits = append(p.emits, fn)
}

// publish emits the buffered events in the order they happened.
func (*Mock) publish(ctx context.Context, p *pendingTaskEvents) {
	for _, emit := range p.emits {
		emit(ctx)
	}
}

// SetEventPublisher wires the EventBridge default bus that task, container
// instance, deployment and service action events are published to. Safe to
// leave unset: no events are emitted then.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// emitTaskLaunch publishes the task state changes of a task's launch, one event
// per lifecycle state, plus the change in its container instance's resources.
// It must be called without any emulator lock held.
func (m *Mock) emitTaskLaunch(ctx context.Context, t *driver.Task) {
	m.emitTaskSteps(ctx, t, launchSteps(t))
	m.emitInstanceChangeOfTask(ctx, t)
}

// emitTaskStop publishes the task state changes of a task's stop. It must be
// called without any emulator lock held.
func (m *Mock) emitTaskStop(ctx context.Context, t *driver.Task) {
	m.emitTaskSteps(ctx, t, stopSteps(t))
	m.emitInstanceChangeOfTask(ctx, t)
}

// emitTaskSteps publishes one "ECS Task State Change" event per step. The task
// snapshot's EventVersion is the version of its last step, so the steps carry
// consecutive versions ending there.
func (m *Mock) emitTaskSteps(ctx context.Context, t *driver.Task, steps []string) {
	first := t.EventVersion - len(steps) + 1

	for i, step := range steps {
		m.emitTaskStep(ctx, t, step, first+i)
	}
}

// emitTaskStep publishes the "ECS Task State Change" event of one lifecycle
// state, masking the snapshot's fields to what is true in that state.
func (m *Mock) emitTaskStep(ctx context.Context, t *driver.Task, step string, version int) {
	d := taskStateChangeDetail{
		Attachments: eventAttachments(t), AvailabilityZone: t.AvailabilityZone, ClusterArn: t.ClusterARN,
		ContainerInstanceArn: t.ContainerInstanceARN, Containers: eventContainers(t, step),
		CPU: t.CPU, CreatedAt: t.CreatedAt, DesiredStatus: statusRunning, Group: t.Group,
		LastStatus: step, LaunchType: t.LaunchType, Memory: t.Memory, PlatformVersion: t.PlatformVersion,
		StartedBy: t.StartedBy, TaskArn: t.ARN, TaskDefinitionArn: t.TaskDefinitionARN, UpdatedAt: m.now(), Version: version,
	}

	maskTaskDetail(&d, t, step)

	m.events.Emit(ctx, eventSource, eventTaskStateChange, d, t.ARN)
}

// maskTaskDetail sets the fields of a task event that are only true from a given
// lifecycle state on: start data once running, stop data once stopping.
func maskTaskDetail(d *taskStateChangeDetail, t *driver.Task, step string) {
	if runningOrLater(step) {
		d.StartedAt, d.Connectivity = t.StartedAt, t.Connectivity
	}

	if stoppingPhase(step) {
		d.DesiredStatus = statusStopped
		d.StopCode, d.StoppedReason = t.StopCode, t.StoppedReason
	}

	if step == taskStatusStopping || step == taskStatusDeprovisioning || step == statusStopped {
		d.StoppingAt = t.StoppingAt
	}

	if step == statusStopped {
		d.StoppedAt = t.StoppedAt
	}
}

// eventContainers lists the task's containers as they appear in an event at step.
func eventContainers(t *driver.Task, step string) []taskEventContainer {
	containers := make([]taskEventContainer, 0, len(t.Containers))

	for i := range t.Containers {
		c := &t.Containers[i]
		ec := taskEventContainer{
			ContainerArn: c.ARN, Image: c.Image, LastStatus: containerStatusAt(step), Name: c.Name, Reason: c.Reason, TaskArn: t.ARN,
		}

		if step == statusStopped {
			code := c.ExitCode
			ec.ExitCode = &code
		}

		containers = append(containers, ec)
	}

	return containers
}

// eventAttachments lists the task's attachments as they appear in an event.
func eventAttachments(t *driver.Task) []taskEventAttachment {
	attachments := make([]taskEventAttachment, 0, len(t.Attachments))

	for _, a := range t.Attachments {
		details := make([]taskEventKeyValue, 0, len(a.Details))
		for _, kv := range a.Details {
			details = append(details, taskEventKeyValue{Name: kv.Name, Value: kv.Value})
		}

		attachments = append(attachments, taskEventAttachment{ID: a.ID, Details: details, Status: a.Status, Type: a.Type})
	}

	return attachments
}

// emitServiceSteadyState publishes the SERVICE_STEADY_STATE service action once
// a service's running count has converged to its desired count, as real ECS
// does when a deployment settles.
func (m *Mock) emitServiceSteadyState(ctx context.Context, svc *driver.Service) {
	if svc.RunningCount != svc.DesiredCount || svc.PendingCount != 0 {
		return
	}

	m.emitServiceAction(ctx, svc.ARN, svc.ClusterARN, serviceSteadyStateName, serviceEventTypeInfo, "")
}

// emitServiceAction publishes an "ECS Service Action" event.
func (m *Mock) emitServiceAction(ctx context.Context, serviceARN, clusterARN, name, eventType, reason string) {
	m.events.Emit(ctx, eventSource, eventServiceAction, serviceActionDetail{
		EventType: eventType, EventName: name, ClusterArn: clusterARN, CreatedAt: m.now(), Reason: reason,
	}, serviceARN)
}

// emitDeploymentEvents publishes a started deployment: it is in progress, and
// completed once the service runs its desired count (a service that cannot
// place its tasks stays in progress). Each is both an "ECS Deployment State
// Change" and an "ECS Service Action" event. It must be called without any
// emulator lock held.
func (m *Mock) emitDeploymentEvents(ctx context.Context, svc *driver.Service) {
	id := primaryDeploymentID(svc.Deployments)
	if id == "" {
		return
	}

	m.emitDeploymentChange(ctx, svc, id, serviceDeploymentInProgress, "ECS deployment "+id+" in progress.")

	if svc.RunningCount >= svc.DesiredCount {
		m.emitDeploymentChange(ctx, svc, id, serviceDeploymentCompleted, "ECS deployment "+id+" completed.")
	}
}

func (m *Mock) emitDeploymentChange(ctx context.Context, svc *driver.Service, id, name, reason string) {
	m.emitServiceAction(ctx, svc.ARN, svc.ClusterARN, name, serviceEventTypeInfo, "")
	m.events.Emit(ctx, eventSource, eventDeploymentStateChange, deploymentStateChangeDetail{
		EventType: serviceEventTypeInfo, EventName: name, DeploymentID: id, UpdatedAt: m.now(), Reason: reason,
	}, svc.ARN)
}

// emitInstanceChangeOfTask publishes the container-instance state change a task
// placement or stop caused. Tasks with no instance (Fargate, unplaced) cause
// none.
func (m *Mock) emitInstanceChangeOfTask(ctx context.Context, t *driver.Task) {
	if t.ContainerInstanceARN == "" {
		return
	}

	m.placeMu.Lock()

	var bumped *driver.ContainerInstance

	m.instances.Update(t.ContainerInstanceARN, func(ci *driver.ContainerInstance) *driver.ContainerInstance {
		updated := *ci
		updated.Version++
		bumped = &updated

		return &updated
	})
	m.placeMu.Unlock()

	if bumped != nil {
		m.emitContainerInstanceDetail(ctx, bumped)
	}
}

// emitContainerInstanceDetail publishes an "ECS Container Instance State Change"
// event for the instance as given (its Version already counts this change).
func (m *Mock) emitContainerInstanceDetail(ctx context.Context, ci *driver.ContainerInstance) {
	cluster := instanceClusterName(ci.ARN)

	var attrs []containerInstanceAttr

	for _, a := range m.attributes.SortedValues() {
		if a.TargetID == ci.ARN {
			attrs = append(attrs, containerInstanceAttr{Name: a.Name})
		}
	}

	m.events.Emit(ctx, eventSource, eventContainerInstanceStateChange, containerInstanceDetail{
		AgentConnected: ci.AgentConnected, Attributes: attrs,
		ClusterArn:           m.arnIn(arnRegion(ci.ARN, m.opts.Region), "cluster/"+cluster),
		ContainerInstanceArn: ci.ARN, EC2InstanceID: ci.EC2InstanceID,
		RegisteredResources: []containerInstanceResource{
			{Name: "CPU", Type: containerInstanceResourceType, IntegerValue: ci.RegisteredCPU},
			{Name: "MEMORY", Type: containerInstanceResourceType, IntegerValue: ci.RegisteredMemory},
		},
		RemainingResources: []containerInstanceResource{
			{Name: "CPU", Type: containerInstanceResourceType, IntegerValue: ci.RemainingCPU},
			{Name: "MEMORY", Type: containerInstanceResourceType, IntegerValue: ci.RemainingMemory},
		},
		Status: ci.Status, Version: ci.Version, UpdatedAt: m.now(),
	}, ci.ARN)
}
