package ecs

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Service scheduling strategies, deployment statuses, rollout states, and the
// default deployment controller. These match the aws-sdk-go-v2/service/ecs
// enum string values exactly so they round-trip on the wire.
const (
	schedReplica = "REPLICA"
	schedDaemon  = "DAEMON"

	deploymentPrimary = "PRIMARY"
	deploymentActive  = "ACTIVE"

	rolloutCompleted  = "COMPLETED"
	rolloutInProgress = "IN_PROGRESS"

	deployControllerECS = "ECS"

	// propagateTags values (NONE, the default, needs no constant: anything
	// else propagates nothing).
	propagateService        = "SERVICE"
	propagateTaskDefinition = "TASK_DEFINITION"

	// Rolling-update deployment defaults ECS applies when the caller omits a
	// deploymentConfiguration on a service using the ECS (rolling update)
	// deployment controller. A REPLICA service defaults to 200/100, a DAEMON
	// service to 100/0 (the CLI/SDK/API default). Real ECS always echoes these
	// on Create/DescribeServices, so a caller reading maximumPercent /
	// minimumHealthyPercent back never sees a missing field.
	replicaMaxPercent        = 200
	replicaMinHealthyPercent = 100
	daemonMaxPercent         = 100
	daemonMinHealthyPercent  = 0

	// azRebalancingDisabled is the default availabilityZoneRebalancing value a
	// service is created with when the caller doesn't specify one, matching
	// real ECS.
	azRebalancingDisabled = "DISABLED"

	// serviceStoppedReason is the StopTask reason used when the scheduler drains a
	// superseded deployment or a deleted service.
	serviceStoppedReason = "Service scheduler stopped task."
)

// serviceKey builds the store key scoping a service name to its cluster.
func serviceKey(cluster, name string) string {
	return cluster + "/" + name
}

// serviceGroup returns the "service:<name>" task-group string ECS uses to link a
// task to the service that launched it, or "" for an empty name.
func serviceGroup(name string) string {
	if name == "" {
		return ""
	}

	return "service:" + name
}

// effectiveServiceLaunchType resolves a service's launch type for placement,
// defaulting an empty value to EC2 (as AWS does when no capacity-provider
// strategy is supplied; capacity-provider resolution is Wave 4).
func effectiveServiceLaunchType(launchType string) string {
	if launchType == "" {
		return launchEC2
	}

	return launchType
}

// CreateService creates a service and synchronously converges it: it launches
// DesiredCount tasks through the placement engine (EC2 capacity is consumed;
// Fargate honors networkConfiguration), links each task to the service via its
// group and startedBy (the deployment id), and records one PRIMARY deployment
// plus a start event. Tasks that cannot be placed on EC2 capacity are left
// PENDING, so RunningCount can be less than DesiredCount.
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) CreateService(ctx context.Context, in driver.CreateServiceInput) (*driver.Service, error) {
	if in.ServiceName == "" {
		return nil, errors.New(errors.InvalidArgument, "serviceName is required")
	}

	cluster := resolveClusterName(in.Cluster)
	if !m.clusterActive(cluster) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", cluster)
	}

	td, err := m.prepareCreateService(&in)
	if err != nil {
		return nil, err
	}

	region := regionctx.RegionOr(ctx, m.opts.Region)
	arnIn := func(resource string) string { return m.arnIn(region, resource) }
	svc := serviceFromInput(&in, arnIn, cluster, m.now(), m.rootPrincipalARN())

	// The name is claimed with a copy: convergence below keeps mutating svc, and
	// the stored claim is read concurrently (metrics, other requests) until the
	// finished record replaces it.
	claim := cloneService(svc)
	if err := m.reserveServiceName(serviceKey(cluster, in.ServiceName), &claim); err != nil {
		return nil, err
	}

	var events pendingTaskEvents

	// Record the service's tags before its first tasks launch: propagateTags
	// SERVICE reads them from the tag store, and a re-created service reuses
	// its predecessor's ARN, whose stale entry must not leak into its tasks.
	m.recordTags(svc.ARN, in.Tags)

	if svc.DeploymentController == deployControllerExternal {
		// No tasks and no deployments: an EXTERNAL service runs tasks only
		// through its task sets.
		svc.Events = []driver.ServiceEvent{m.serviceEvent(fmt.Sprintf("(service %s) has started 0 tasks.", svc.Name))}
	} else {
		m.convergeNewService(ctx, svc, td, &events)
	}

	m.services.Set(serviceKey(cluster, svc.Name), svc)
	m.recordServiceDeployment(svc)
	m.publish(ctx, &events)

	if svc.DeploymentController != deployControllerExternal {
		m.emitDeploymentEvents(ctx, svc)
	}

	m.emitServiceSteadyState(ctx, svc)
	m.publishClusterMetrics(cluster)

	out := cloneService(svc)

	return &out, nil
}

// prepareCreateService validates and normalizes a CreateService request and
// returns the task definition its tasks run (nil for an EXTERNAL-controller
// service created without one, since its task sets supply the definition).
func (m *Mock) prepareCreateService(in *driver.CreateServiceInput) (*driver.TaskDefinition, error) {
	// A DAEMON service runs exactly one task per container instance, so AWS
	// rejects a caller-supplied desiredCount rather than overriding it.
	if in.SchedulingStrategy == schedDaemon && in.DesiredCount > 0 {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"desiredCount must not be specified for a DAEMON service")
	}

	var td *driver.TaskDefinition

	if in.DeploymentController != deployControllerExternal || in.TaskDefinition != "" {
		resolved, err := m.resolveLaunchableTaskDef(in.TaskDefinition)
		if err != nil {
			return nil, err
		}

		td = resolved

		if err := validateLaunch(td, effectiveServiceLaunchType(in.LaunchType), in.NetworkConfiguration); err != nil {
			return nil, err
		}

		// Real ECS normalizes the service's taskDefinition to the full ARN regardless
		// of how the caller referenced it (bare family or family:revision), so echo the
		// resolved ARN rather than the caller's short form. This is what lets Terraform
		// diff-suppress family:revision against the ARN AWS always returns.
		in.TaskDefinition = td.ARN
	}

	in.NetworkConfiguration = withDefaultAssignPublicIP(in.NetworkConfiguration)

	return td, nil
}

// serviceFromInput builds the service record (without convergence) from the
// create input, defaulting the scheduling strategy and deployment controller.
// createdBy is the creating principal ECS records on the service.
func serviceFromInput(
	in *driver.CreateServiceInput, arn func(string) string, cluster, now, createdBy string,
) *driver.Service {
	sched := in.SchedulingStrategy
	if sched == "" {
		sched = schedReplica
	}

	controller := in.DeploymentController
	if controller == "" {
		controller = deployControllerECS
	}

	azRebalancing := in.AvailabilityZoneRebalancing
	if azRebalancing == "" {
		azRebalancing = azRebalancingDisabled
	}

	return &driver.Service{
		ARN:                           arn("service/" + cluster + "/" + in.ServiceName),
		Name:                          in.ServiceName,
		ClusterARN:                    arn("cluster/" + cluster),
		TaskDefinition:                in.TaskDefinition,
		RoleARN:                       in.Role,
		CreatedBy:                     createdBy,
		DesiredCount:                  in.DesiredCount,
		Status:                        statusActive,
		LaunchType:                    in.LaunchType,
		SchedulingStrategy:            sched,
		DeploymentController:          controller,
		PlatformVersion:               in.PlatformVersion,
		PropagateTags:                 in.PropagateTags,
		EnableExecuteCommand:          in.EnableExecuteCommand,
		HealthCheckGracePeriodSeconds: in.HealthCheckGracePeriodSeconds,
		AvailabilityZoneRebalancing:   azRebalancing,
		CreatedAt:                     now,
		// Clone reference-typed fields so the stored record never aliases the
		// caller's input slices/pointers (a caller mutating what it passed must
		// not corrupt the store).
		DeploymentConfiguration:  defaultDeploymentConfig(sched, controller, in.DeploymentConfiguration),
		NetworkConfiguration:     cloneNetworkConfig(in.NetworkConfiguration),
		CapacityProviderStrategy: append([]driver.CapacityProviderStrategyItem(nil), in.CapacityProviderStrategy...),
		LoadBalancers:            append([]driver.LoadBalancer(nil), in.LoadBalancers...),
		ServiceRegistries:        append([]driver.ServiceRegistry(nil), in.ServiceRegistries...),
		ServiceConnect:           cloneServiceConnect(in.ServiceConnect),
		Tags:                     copyTags(in.Tags),
	}
}

// defaultDeploymentConfig returns the deployment configuration ECS reports for a
// service, filling the rolling-update defaults AWS applies when the caller omits
// them: maximumPercent/minimumHealthyPercent of 200/100 for a REPLICA service
// and 100/0 for a DAEMON service, plus a disabled circuit breaker. These
// defaults apply only to the ECS (rolling update) deployment controller;
// CODE_DEPLOY / EXTERNAL controllers carry no rolling-update defaults, so their
// configuration is echoed back unchanged.
func defaultDeploymentConfig(
	sched, controller string, in *driver.DeploymentConfiguration,
) *driver.DeploymentConfiguration {
	out := cloneDeploymentConfig(in)
	if controller != deployControllerECS {
		return out
	}

	if out == nil {
		out = &driver.DeploymentConfiguration{}
	}

	maxPct, minPct := replicaMaxPercent, replicaMinHealthyPercent
	if sched == schedDaemon {
		maxPct, minPct = daemonMaxPercent, daemonMinHealthyPercent
	}

	if out.MaximumPercent == nil {
		out.MaximumPercent = ptrInt(maxPct)
	}

	if out.MinimumHealthyPercent == nil {
		out.MinimumHealthyPercent = ptrInt(minPct)
	}

	if out.DeploymentCircuitBreaker == nil {
		out.DeploymentCircuitBreaker = &driver.DeploymentCircuitBreaker{}
	}

	return out
}

// ptrInt returns a pointer to v.
func ptrInt(v int) *int { return &v }

// reserveServiceName claims the service name in the store before convergence.
// An ACTIVE service blocks the name; a lingering INACTIVE (deleted) tombstone is
// replaced. SetIfAbsent closes the check-then-set race on concurrent new creates.
func (m *Mock) reserveServiceName(key string, svc *driver.Service) error {
	existing, exists := m.services.Get(key)
	if exists && existing.Status == statusActive {
		return errServiceNotIdempotent()
	}

	if exists {
		m.services.Set(key, svc)

		return nil
	}

	if !m.services.SetIfAbsent(key, svc) {
		return errServiceNotIdempotent()
	}

	return nil
}

// errServiceNotIdempotent is the error CreateService returns when an ACTIVE
// service of the same name already exists in the cluster.
func errServiceNotIdempotent() error {
	return apiErrf(errors.AlreadyExists, excInvalidParameter, "Creation of service was not idempotent.")
}

// convergeNewService resolves the target count (DAEMON implies one task per
// container instance), launches the tasks, and records the PRIMARY deployment
// and the start event on the service.
func (m *Mock) convergeNewService(
	ctx context.Context, svc *driver.Service, td *driver.TaskDefinition, events *pendingTaskEvents,
) {
	cluster := clusterNameFromARN(svc.ClusterARN)
	target := m.desiredForStrategy(cluster, svc.SchedulingStrategy, svc.DesiredCount)
	svc.DesiredCount = target

	id := m.deploymentID()
	running, pending := m.converge(ctx, svc, td, id, target, events)
	svc.RunningCount = running
	svc.PendingCount = pending
	svc.Deployments = []driver.Deployment{m.newDeployment(id, deploymentPrimary, svc, running, pending)}
	svc.Events = []driver.ServiceEvent{m.serviceEvent(fmt.Sprintf("(service %s) has started %d tasks.", svc.Name, running))}
}

// desiredForStrategy resolves the effective desired count: DAEMON runs one task
// per placeable container instance; REPLICA honors the requested count.
func (m *Mock) desiredForStrategy(cluster, sched string, requested int) int {
	if sched == schedDaemon {
		return m.placeableInstanceCount(cluster)
	}

	return requested
}

// placeableInstanceCount counts the ACTIVE, agent-connected container instances
// in the cluster, the implied DAEMON task target.
func (m *Mock) placeableInstanceCount(cluster string) int {
	var n int

	for _, ci := range m.instances.All() {
		if instanceClusterName(ci.ARN) == cluster && ci.Status == statusActive && ci.AgentConnected {
			n++
		}
	}

	return n
}

// converge launches target tasks for the service under the given deployment id
// and returns the running/pending split. EC2 tasks with no fitting instance are
// stored PENDING rather than failing (pendingOnShortfall=true). Each launch is
// buffered on events, to be published once the caller commits the service.
func (m *Mock) converge(
	ctx context.Context, svc *driver.Service, td *driver.TaskDefinition, deploymentID string, target int,
	events *pendingTaskEvents,
) (running, pending int) {
	spec := m.serviceTaskSpec(svc, td, deploymentID)
	placementFailure := ""

	for range target {
		task, failure := m.launchTask(ctx, &spec, true)
		if task == nil {
			continue
		}

		if failure != nil && placementFailure == "" {
			placementFailure = failure.Reason
		}

		events.addLaunch(m, task)

		if task.LastStatus == statusRunning {
			running++

			m.registerTaskTargets(ctx, svc, td, task)
		} else {
			pending++
		}
	}

	if placementFailure != "" {
		serviceARN, clusterARN := svc.ARN, svc.ClusterARN

		events.addFunc(func(ctx context.Context) {
			m.emitServiceAction(ctx, serviceARN, clusterARN, serviceTaskPlacementFailure, serviceEventTypeError, placementFailure)
		})
	}

	return running, pending
}

// serviceTaskSpec builds the placement spec for a service's tasks: group links
// the task to the service and startedBy carries the deployment id. The task's
// tags are the ones the service propagates (see propagatedTaskTags).
func (m *Mock) serviceTaskSpec(svc *driver.Service, td *driver.TaskDefinition, deploymentID string) taskSpec {
	return taskSpec{
		cluster:         clusterNameFromARN(svc.ClusterARN),
		clusterARN:      svc.ClusterARN,
		td:              td,
		launchType:      effectiveServiceLaunchType(svc.LaunchType),
		group:           serviceGroup(svc.Name),
		startedBy:       deploymentID,
		platformVersion: svc.PlatformVersion,
		netCfg:          svc.NetworkConfiguration,
		tags:            m.propagatedTaskTags(svc, td),
		enableExec:      svc.EnableExecuteCommand,
	}
}

// propagatedTaskTags returns the tags a service stamps on a task it launches,
// per its propagateTags setting, read when the task is launched: SERVICE copies
// the service's current tags, TASK_DEFINITION the task definition's current
// tags, and NONE (the default, also an empty value) copies nothing. Both reads
// go through the ARN-keyed tag store, so a TagResource/UntagResource on the
// source before a new deployment is reflected in the new tasks.
func (m *Mock) propagatedTaskTags(svc *driver.Service, td *driver.TaskDefinition) []driver.Tag {
	switch svc.PropagateTags {
	case propagateService:
		return m.liveTags(svc.ARN, svc.Tags)
	case propagateTaskDefinition:
		return m.liveTags(td.ARN, td.Tags)
	default:
		return nil
	}
}

// drainService stops every RUNNING or PENDING task linked to the service in its
// cluster, releasing any reserved container-instance capacity. It is used to
// drain a superseded deployment before relaunching (keepProtected leaves tasks
// with active scale-in protection running) and to tear down tasks on delete.
// Each stop is buffered on events, to be published once the caller commits the
// service.
func (m *Mock) drainService(ctx context.Context, svc *driver.Service, events *pendingTaskEvents, keepProtected bool) {
	group := serviceGroup(svc.Name)
	cluster := clusterNameFromARN(svc.ClusterARN)
	now := m.opts.Clock.Now()

	for _, t := range m.tasks.SortedValues() {
		if t.Group != group || clusterNameFromARN(t.ClusterARN) != cluster || t.LastStatus == statusStopped {
			continue
		}

		// A scale-in or redeployment leaves tasks with active scale-in protection
		// running; deleting the service stops everything.
		if keepProtected && taskProtected(t, now) {
			continue
		}

		m.deregisterTaskTargets(ctx, svc, t)
		// No reconciliation: this drain already owns and re-converges the whole
		// service state itself (the caller launches the replacement tasks), so
		// StopTask's own reconciliation would race it. See stopTaskQuiet.
		if stopped, stoppedNow, err := m.stopTaskQuiet(ctx, cluster, t.ARN, serviceStoppedReason); err == nil && stoppedNow {
			events.addStop(m, stopped)
		}
	}
}

// serviceNameFromGroup extracts the service name from a task's Group field
// ("service:<name>"), the inverse of serviceGroup. It reports false for a task
// with no owning service (an empty group, or one from RunTask/StartTask that
// never carries the "service:" prefix).
func serviceNameFromGroup(group string) (string, bool) {
	const prefix = "service:"

	if !strings.HasPrefix(group, prefix) {
		return "", false
	}

	return strings.TrimPrefix(group, prefix), true
}

// primaryDeploymentID returns the id of the service's PRIMARY deployment, or
// "" if none is recorded. An ACTIVE service always carries exactly one (see
// convergeNewService/redeployService), so this only misses defensively.
func primaryDeploymentID(deployments []driver.Deployment) string {
	for i := range deployments {
		if deployments[i].Status == deploymentPrimary {
			return deployments[i].ID
		}
	}

	return ""
}

// liveServiceTaskCounts recomputes a service's running/pending task counts by
// scanning the task store directly, rather than trusting bookkeeping fields
// that a concurrent StopTask/RunTask could have raced. Mirrors the same
// group+cluster scoping drainService uses to find a service's tasks.
func (m *Mock) liveServiceTaskCounts(cluster, group string) (running, pending int) {
	for _, t := range m.tasks.All() {
		if t.Group != group || clusterNameFromARN(t.ClusterARN) != cluster {
			continue
		}

		switch t.LastStatus {
		case statusRunning:
			running++
		case statusPending:
			pending++
		}
	}

	return running, pending
}

// launchServiceReplacements launches up to n replacement tasks for svc under
// its existing PRIMARY deployment (no new deployment is minted for a
// replacement, matching real ECS). A task definition that's since been
// deregistered leaves the service short rather than erroring, same as a real
// scheduler that can't resolve its target definition.
//
// It returns the launched tasks without publishing their state-change events:
// the caller holds the service's reconcileLock and publishes them only after
// releasing it.
func (m *Mock) launchServiceReplacements(ctx context.Context, svc *driver.Service, n int) []*driver.Task {
	td, ok := m.resolveTaskDef(svc.TaskDefinition)
	if !ok {
		return nil
	}

	spec := m.serviceTaskSpec(svc, td, primaryDeploymentID(svc.Deployments))
	launched := make([]*driver.Task, 0, n)

	for range n {
		t, _ := m.launchTask(ctx, &spec, true)
		if t == nil {
			continue
		}

		launched = append(launched, t)

		if t.LastStatus == statusRunning {
			m.registerTaskTargets(ctx, svc, td, t)
		}
	}

	return launched
}

// reconcileServiceAfterStop re-converges the stopped task's owning service (if
// any): it recomputes the service's live running/pending counts from the task
// store and, if short of desiredCount, launches replacement task(s) under the
// existing PRIMARY deployment, mirroring real ECS's scheduler, which notices a
// service-owned task died on its next reconciliation pass, reflects the drop
// immediately, and launches a replacement to converge back. No new deployment
// is minted; a single dead task doesn't roll a fresh one, matching real ECS.
//
// A task with no owning service (Group doesn't start with "service:") is a
// no-op, as is a service that's been deleted or is mid-delete (Status is no
// longer ACTIVE). DeleteService/drainService already own tearing that down.
//
// The whole read-decide-launch-commit sequence below runs under the service's
// reconcileLock key: two concurrent StopTask calls on different tasks of the
// same service must not both read the pre-replacement counts, both compute
// the full shortfall, and both launch replacements, which would over-provision
// the service above desiredCount with nothing to ever scale it back down. The
// lock is per-service (keyed by cluster+name), so unrelated services still
// reconcile concurrently, and it is acquired here, before stopTaskLocked's
// placeMu has any chance to be re-taken by a replacement launch, and before
// m.services's own per-call lock, making it the outermost lock in this path.
func (m *Mock) reconcileServiceAfterStop(ctx context.Context, task *driver.Task) {
	name, ok := serviceNameFromGroup(task.Group)
	if !ok {
		return
	}

	// A task-set task is replaced by its task set, not by the service scheduler.
	if m.reconcileTaskSetAfterStop(ctx, task, name) {
		return
	}

	// Replacement launches are published only after reconcileLock is released:
	// an event target (e.g. a synchronously invoked Lambda) that stops another
	// task of this same service re-enters this function for the same key, and
	// the lock is not reentrant.
	for _, t := range m.reconcileServiceLocked(ctx, task, name) {
		m.emitTaskLaunch(ctx, t)
	}
}

// reconcileServiceLocked is reconcileServiceAfterStop's reconcileLock-guarded
// core. It returns the replacement tasks it launched, unpublished.
func (m *Mock) reconcileServiceLocked(ctx context.Context, task *driver.Task, name string) []*driver.Task {
	cluster := clusterNameFromARN(task.ClusterARN)
	key := serviceKey(cluster, name)

	unlock := m.reconcileLock.lock(key)
	defer unlock()

	svc, ok := m.services.Get(key)
	if !ok || svc.Status != statusActive {
		return nil
	}

	m.deregisterTaskTargets(ctx, svc, task)

	var launched []*driver.Task

	running, pending := m.liveServiceTaskCounts(cluster, task.Group)
	if shortfall := svc.DesiredCount - (running + pending); shortfall > 0 {
		launched = m.launchServiceReplacements(ctx, svc, shortfall)
	}

	m.services.Update(key, func(s *driver.Service) *driver.Service {
		updated := cloneService(s)
		updated.RunningCount, updated.PendingCount = m.liveServiceTaskCounts(cluster, task.Group)

		for i := range updated.Deployments {
			if updated.Deployments[i].Status == deploymentPrimary {
				updated.Deployments[i].RunningCount = updated.RunningCount
				updated.Deployments[i].PendingCount = updated.PendingCount
				updated.Deployments[i].RolloutState = rolloutState(updated.RunningCount, updated.DesiredCount)
				updated.Deployments[i].UpdatedAt = m.now()
			}
		}

		return &updated
	})

	return launched
}

// deploymentID mints an ECS service deployment id ("ecs-svc/<id>"). Service
// tasks carry it as startedBy, linking a task to its deployment.
func (m *Mock) deploymentID() string {
	return "ecs-svc/" + m.hexID()
}

// newDeployment builds a deployment record for the service's current
// task-definition/desired count with the given status and observed counts.
func (m *Mock) newDeployment(id, status string, svc *driver.Service, running, pending int) driver.Deployment {
	now := m.now()

	return driver.Deployment{
		ID:             id,
		Status:         status,
		TaskDefinition: svc.TaskDefinition,
		DesiredCount:   svc.DesiredCount,
		RunningCount:   running,
		PendingCount:   pending,
		LaunchType:     effectiveServiceLaunchType(svc.LaunchType),
		RolloutState:   rolloutState(running, svc.DesiredCount),
		CreatedAt:      now,
		UpdatedAt:      now,
		ServiceConnect: cloneServiceConnect(svc.ServiceConnect),
	}
}

// rolloutState reports COMPLETED once the running count equals the desired
// count, else IN_PROGRESS (running fewer tasks, or more because scale-in
// protection kept surplus ones). Circuit-breaker FAILED transitions are accepted but
// not simulated.
func rolloutState(running, desired int) string {
	if running == desired {
		return rolloutCompleted
	}

	return rolloutInProgress
}

// serviceEvent builds a timestamped service event with a fresh id.
func (m *Mock) serviceEvent(message string) driver.ServiceEvent {
	return driver.ServiceEvent{ID: m.hexID(), CreatedAt: m.now(), Message: message}
}

// UpdateService updates a service. It stores/echoes every supplied field and,
// when the task definition or desired count changes (or forceNewDeployment is
// set), promotes a new PRIMARY deployment, drains the previous one, relaunches
// the tasks against the new target/definition, and appends an event.
//
//nolint:gocritic // in matches the driver.ECS interface signature; copied once on entry.
func (m *Mock) UpdateService(ctx context.Context, in driver.UpdateServiceInput) (*driver.Service, error) {
	cluster := resolveClusterName(in.Cluster)

	svc, ok := m.resolveService(cluster, in.Service)
	if !ok {
		return nil, apiErrf(errors.NotFound, excServiceNotFound, "service %q not found", in.Service)
	}

	if err := validateServiceUpdate(svc, &in); err != nil {
		return nil, err
	}

	in.NetworkConfiguration = withDefaultAssignPublicIP(in.NetworkConfiguration)

	if svc.DeploymentController == deployControllerExternal {
		return m.updateExternalService(ctx, svc, &in)
	}

	updated := cloneService(svc)
	applyServiceScalars(&updated, &in)
	applyServiceRefs(&updated, &in)

	tdChanged, err := m.applyTaskDefChange(&updated, svc, &in)
	if err != nil {
		return nil, err
	}

	countChanged := in.DesiredCount != nil && *in.DesiredCount != svc.DesiredCount
	redeployed := in.ForceNewDeployment || tdChanged || countChanged

	var events pendingTaskEvents

	if redeployed {
		m.redeployService(ctx, &updated, &in, &events)
	}

	m.services.Set(serviceKey(cluster, updated.Name), &updated)

	deployed := in.ForceNewDeployment || tdChanged || serviceConfigChanged(&in)
	if deployed {
		m.recordServiceDeployment(&updated)
	}

	m.publish(ctx, &events)
	m.emitServiceUpdateEvents(ctx, &updated, countChanged, deployed, redeployed)
	m.publishClusterMetrics(cluster)

	out := cloneService(&updated)

	return &out, nil
}

// validateServiceUpdate rejects the update inputs ECS refuses up front: a
// caller-supplied desiredCount on a DAEMON service (it runs one task per
// container instance, as on create) and an invalid assignPublicIp.
func validateServiceUpdate(svc *driver.Service, in *driver.UpdateServiceInput) error {
	if svc.SchedulingStrategy == schedDaemon && in.DesiredCount != nil {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"desiredCount must not be specified for a DAEMON service")
	}

	return validateAssignPublicIP(in.NetworkConfiguration)
}

// emitServiceUpdateEvents publishes the events a committed UpdateService caused:
// a desired-count change, a started deployment and the steady state it reached.
func (m *Mock) emitServiceUpdateEvents(ctx context.Context, svc *driver.Service, countChanged, deployed, redeployed bool) {
	if countChanged {
		m.emitServiceAction(ctx, svc.ARN, svc.ClusterARN, serviceDesiredCountUpdated, serviceEventTypeInfo, "")
	}

	if deployed {
		m.emitDeploymentEvents(ctx, svc)
	}

	if redeployed {
		m.emitServiceSteadyState(ctx, svc)
	}
}

// updateExternalService updates a service that uses the EXTERNAL deployment
// controller. Only the desired count and the scalar/reference settings change
// (the task definition comes from task sets), and a new desired count rescales
// every task set. It runs under the service's task-set lock.
func (m *Mock) updateExternalService(ctx context.Context, svc *driver.Service, in *driver.UpdateServiceInput) (*driver.Service, error) {
	cluster := clusterNameFromARN(svc.ClusterARN)
	key := serviceKey(cluster, svc.Name)

	var events pendingTaskEvents

	out, err := func() (*driver.Service, error) {
		unlock := m.taskSetLock.lock(key)
		defer unlock()

		current, ok := m.services.Get(key)
		if !ok || current.Status != statusActive {
			return nil, apiErrf(errors.FailedPrecondition, excServiceNotActive,
				"The specified service is not active. You can't update a service that is inactive.")
		}

		updated := cloneService(current)
		applyServiceScalars(&updated, in)
		applyServiceRefs(&updated, in)

		countChanged := in.DesiredCount != nil && *in.DesiredCount != current.DesiredCount

		if in.DesiredCount != nil {
			updated.DesiredCount = *in.DesiredCount
		}

		m.services.Set(key, &updated)

		if countChanged {
			m.rescaleTaskSets(ctx, &updated, &events)
		}

		out := cloneService(&updated)

		return &out, nil
	}()
	if err != nil {
		return nil, err
	}

	m.publish(ctx, &events)

	if in.DesiredCount != nil && *in.DesiredCount != svc.DesiredCount {
		m.emitServiceAction(ctx, out.ARN, out.ClusterARN, serviceDesiredCountUpdated, serviceEventTypeInfo, "")
	}

	m.publishClusterMetrics(cluster)

	return out, nil
}

// applyTaskDefChange applies a task-definition change to the pending service
// update, reporting whether the definition actually changed. An unchanged (or
// unset) task definition is a no-op. A deregistered (INACTIVE) definition can't
// back a new deployment, same as CreateService/RunTask, so it is rejected.
func (m *Mock) applyTaskDefChange(updated, svc *driver.Service, in *driver.UpdateServiceInput) (bool, error) {
	if in.TaskDefinition == "" {
		return false, nil
	}

	td, err := m.resolveLaunchableTaskDef(in.TaskDefinition)
	if err != nil {
		return false, err
	}

	// The stored taskDefinition is always the full ARN, so compare the resolved ARN
	// (not the caller's possibly-short reference) to detect a real change. A caller
	// re-supplying the same revision as "family:revision" must remain a no-op.
	if td.ARN == svc.TaskDefinition {
		return false, nil
	}

	// Normalize to the full task-definition ARN, matching real ECS (and CreateService).
	updated.TaskDefinition = td.ARN

	return true, nil
}

// redeployService reconciles the service to a new PRIMARY deployment: it resolves
// the new target count, drains the existing tasks, relaunches against the current
// task definition, demotes prior deployments to ACTIVE, and appends an event.
func (m *Mock) redeployService(
	ctx context.Context, svc *driver.Service, in *driver.UpdateServiceInput, events *pendingTaskEvents,
) {
	cluster := clusterNameFromARN(svc.ClusterARN)

	requested := svc.DesiredCount
	if in.DesiredCount != nil {
		requested = *in.DesiredCount
	}

	target := m.desiredForStrategy(cluster, svc.SchedulingStrategy, requested)
	svc.DesiredCount = target

	td, ok := m.resolveTaskDef(svc.TaskDefinition)
	if !ok {
		return
	}

	m.drainService(ctx, svc, events, true)

	// Tasks kept alive by scale-in protection count toward the service, so only
	// the shortfall is launched; the service reports IN_PROGRESS while it runs
	// more tasks than desired.
	id := m.deploymentID()
	m.converge(ctx, svc, td, id, max(0, target-len(m.keptProtectedTasks(svc))), events)
	running, pending := m.liveServiceTaskCounts(cluster, serviceGroup(svc.Name))
	svc.RunningCount = running
	svc.PendingCount = pending

	// The superseded deployment drained synchronously in drainService above, so
	// real ECS's "drop a deployment once drained" leaves just the new PRIMARY.
	// Replacing the slice (rather than prepending) is what stops the deployments
	// list from growing unbounded across repeated UpdateService calls.
	dep := m.newDeployment(id, deploymentPrimary, svc, running, pending)
	svc.Deployments = []driver.Deployment{dep}
	svc.Events = append(svc.Events, m.serviceEvent(fmt.Sprintf("(service %s) has started %d tasks.", svc.Name, running)))
}

// serviceConfigChanged reports whether an update supplies a setting that is
// part of a service revision (network, load balancers, registries, capacity
// providers, platform version, Service Connect), which starts a deployment.
func serviceConfigChanged(in *driver.UpdateServiceInput) bool {
	return in.NetworkConfiguration != nil || in.LoadBalancers != nil || in.ServiceRegistries != nil ||
		in.CapacityProviderStrategy != nil || in.PlatformVersion != "" || in.ServiceConnect != nil
}

// applyServiceScalars stores the supplied scalar/pointer update fields, leaving
// unset (empty or nil) fields unchanged.
func applyServiceScalars(svc *driver.Service, in *driver.UpdateServiceInput) {
	if in.PlatformVersion != "" {
		svc.PlatformVersion = in.PlatformVersion
	}

	if in.PropagateTags != "" {
		svc.PropagateTags = in.PropagateTags
	}

	if in.EnableExecuteCommand != nil {
		svc.EnableExecuteCommand = *in.EnableExecuteCommand
	}

	if in.HealthCheckGracePeriodSeconds != nil {
		svc.HealthCheckGracePeriodSeconds = in.HealthCheckGracePeriodSeconds
	}

	if in.AvailabilityZoneRebalancing != "" {
		svc.AvailabilityZoneRebalancing = in.AvailabilityZoneRebalancing
	}
}

// applyServiceRefs stores the supplied reference-typed update fields, leaving nil
// fields unchanged.
func applyServiceRefs(svc *driver.Service, in *driver.UpdateServiceInput) {
	// Clone reference-typed fields so the stored record never aliases the
	// caller's input.
	if in.DeploymentConfiguration != nil {
		svc.DeploymentConfiguration = cloneDeploymentConfig(in.DeploymentConfiguration)
	}

	if in.NetworkConfiguration != nil {
		svc.NetworkConfiguration = cloneNetworkConfig(in.NetworkConfiguration)
	}

	if in.CapacityProviderStrategy != nil {
		svc.CapacityProviderStrategy = append([]driver.CapacityProviderStrategyItem(nil), in.CapacityProviderStrategy...)
	}

	if in.LoadBalancers != nil {
		svc.LoadBalancers = append([]driver.LoadBalancer(nil), in.LoadBalancers...)
	}

	if in.ServiceRegistries != nil {
		svc.ServiceRegistries = append([]driver.ServiceRegistry(nil), in.ServiceRegistries...)
	}

	if in.ServiceConnect != nil {
		svc.ServiceConnect = cloneServiceConnect(in.ServiceConnect)
	}
}

// ListServices returns services in a cluster in deterministic order.
func (m *Mock) ListServices(_ context.Context, cluster string) ([]driver.Service, error) {
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	all := m.services.SortedValues()

	out := make([]driver.Service, 0, len(all))

	for _, s := range all {
		// Real ECS ListServices returns only live services (ACTIVE/DRAINING); a
		// deleted service is marked INACTIVE but kept for DescribeServices, so
		// filter the tombstones out here.
		if clusterNameFromARN(s.ClusterARN) == want && s.Status != statusInactive {
			out = append(out, cloneService(s))
		}
	}

	return out, nil
}

// DescribeServices resolves services by name or ARN; unresolved ids become failures.
func (m *Mock) DescribeServices(ctx context.Context, cluster string, ids []string) ([]driver.Service, []driver.Failure, error) {
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return nil, nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	found := make([]driver.Service, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		if s, ok := m.resolveService(want, id); ok {
			out := cloneService(s)
			out.Tags = m.liveTags(s.ARN, s.Tags)
			out.TaskSets = m.describedTaskSets(want, s)
			found = append(found, out)
			continue
		}

		failures = append(failures, driver.Failure{
			ARN:    m.arnIn(regionctx.RegionOr(ctx, m.opts.Region), "service/"+want+"/"+serviceNameOf(id)),
			Reason: "MISSING",
		})
	}

	return found, failures, nil
}

// describedTaskSets returns the task sets DescribeServices reports for svc: the
// refreshed sets of an ACTIVE EXTERNAL-controller service, none otherwise.
func (m *Mock) describedTaskSets(cluster string, svc *driver.Service) []driver.TaskSet {
	if svc.DeploymentController != deployControllerExternal || svc.Status != statusActive {
		return nil
	}

	unlock := m.taskSetLock.lock(serviceKey(cluster, svc.Name))
	defer unlock()

	return m.taskSetViews(svc)
}

// DeleteService marks a service INACTIVE and stops its tasks (releasing
// capacity). AWS refuses to delete a service whose desired or running count is
// non-zero unless force is set; with force the service is deleted regardless.
func (m *Mock) DeleteService(ctx context.Context, cluster, service string, force bool) (*driver.Service, error) {
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	svc, ok := m.resolveService(want, service)
	if !ok {
		return nil, apiErrf(errors.NotFound, excServiceNotFound, "service %q not found", service)
	}

	if !force && (svc.DesiredCount > 0 || svc.RunningCount > 0) {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"the service %q cannot be deleted while it has a desired count greater than 0; "+
				"update the desired count to 0 or delete with force", service)
	}

	var events pendingTaskEvents

	// The task-set lock makes the cascade atomic against a concurrent
	// CreateTaskSet: either the new set exists before the service is marked
	// deleted (and is removed here) or it is refused as ServiceNotActive.
	unlock := m.taskSetLock.lock(serviceKey(want, svc.Name))

	updated := cloneService(svc)
	m.drainService(ctx, &updated, &events, false)
	m.deleteTaskSetsOf(ctx, &updated, &events)
	m.markServiceDeleted(&updated)
	m.services.Set(serviceKey(want, updated.Name), &updated)
	m.deleteServiceDeployments(&updated)
	unlock()

	m.publish(ctx, &events)
	m.publishClusterMetrics(want)

	out := cloneService(&updated)

	return &out, nil
}

// markServiceDeleted flips a drained service to INACTIVE and zeroes its counts
// and deployment counts.
func (*Mock) markServiceDeleted(svc *driver.Service) {
	svc.Status = statusInactive
	svc.DesiredCount = 0
	svc.RunningCount = 0
	svc.PendingCount = 0

	for i := range svc.Deployments {
		svc.Deployments[i].Status = deploymentActive
		svc.Deployments[i].RunningCount = 0
		svc.Deployments[i].PendingCount = 0
	}
}

// resolveService looks up a service by name or ARN within a cluster.
func (m *Mock) resolveService(cluster, id string) (*driver.Service, bool) {
	name := serviceNameOf(id)

	return m.services.Get(serviceKey(cluster, name))
}

// serviceNameOf returns the bare service name from a name or service ARN
// (…:service/cluster/name).
func serviceNameOf(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}

	return id
}
