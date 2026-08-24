package ecs

import (
	"context"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
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

	td, err := m.resolveLaunchableTaskDef(in.TaskDefinition)
	if err != nil {
		return nil, err
	}

	launchType := effectiveServiceLaunchType(in.LaunchType)
	if err := validateLaunch(td, launchType, in.NetworkConfiguration); err != nil {
		return nil, err
	}

	// A DAEMON service runs exactly one task per container instance, so AWS
	// rejects a caller-supplied desiredCount rather than overriding it.
	if in.SchedulingStrategy == schedDaemon && in.DesiredCount > 0 {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"desiredCount must not be specified for a DAEMON service")
	}

	svc := serviceFromInput(&in, m.arn, cluster, m.now(), m.rootPrincipalARN())

	if err := m.reserveServiceName(serviceKey(cluster, in.ServiceName), svc); err != nil {
		return nil, err
	}

	m.convergeNewService(ctx, svc, td)
	m.services.Set(serviceKey(cluster, svc.Name), svc)
	m.recordTags(svc.ARN, in.Tags)

	out := cloneService(svc)

	return &out, nil
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
		CreatedAt:                     now,
		// Clone reference-typed fields so the stored record never aliases the
		// caller's input slices/pointers (a caller mutating what it passed must
		// not corrupt the store).
		DeploymentConfiguration:  cloneDeploymentConfig(in.DeploymentConfiguration),
		NetworkConfiguration:     cloneNetworkConfig(in.NetworkConfiguration),
		CapacityProviderStrategy: append([]driver.CapacityProviderStrategyItem(nil), in.CapacityProviderStrategy...),
		LoadBalancers:            append([]driver.LoadBalancer(nil), in.LoadBalancers...),
		ServiceRegistries:        append([]driver.ServiceRegistry(nil), in.ServiceRegistries...),
		Tags:                     copyTags(in.Tags),
	}
}

// reserveServiceName claims the service name in the store before convergence.
// An ACTIVE service blocks the name; a lingering INACTIVE (deleted) tombstone is
// replaced. SetIfAbsent closes the check-then-set race on concurrent new creates.
func (m *Mock) reserveServiceName(key string, svc *driver.Service) error {
	existing, exists := m.services.Get(key)
	if exists && existing.Status == statusActive {
		return errors.Newf(errors.AlreadyExists, "service %q already exists", svc.Name)
	}

	if exists {
		m.services.Set(key, svc)

		return nil
	}

	if !m.services.SetIfAbsent(key, svc) {
		return errors.Newf(errors.AlreadyExists, "service %q already exists", svc.Name)
	}

	return nil
}

// convergeNewService resolves the target count (DAEMON implies one task per
// container instance), launches the tasks, and records the PRIMARY deployment
// and the start event on the service.
func (m *Mock) convergeNewService(ctx context.Context, svc *driver.Service, td *driver.TaskDefinition) {
	cluster := clusterNameFromARN(svc.ClusterARN)
	target := m.desiredForStrategy(cluster, svc.SchedulingStrategy, svc.DesiredCount)
	svc.DesiredCount = target

	id := m.deploymentID()
	running, pending := m.converge(ctx, svc, td, id, target)
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
// in the cluster — the implied DAEMON task target.
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
// stored PENDING rather than failing (pendingOnShortfall=true).
func (m *Mock) converge(
	ctx context.Context, svc *driver.Service, td *driver.TaskDefinition, deploymentID string, target int,
) (running, pending int) {
	spec := m.serviceTaskSpec(svc, td, deploymentID)

	for range target {
		task, _ := m.launchTask(ctx, &spec, true)
		if task == nil {
			continue
		}

		if task.LastStatus == statusRunning {
			running++
		} else {
			pending++
		}
	}

	return running, pending
}

// serviceTaskSpec builds the placement spec for a service's tasks: group links
// the task to the service and startedBy carries the deployment id.
func (*Mock) serviceTaskSpec(svc *driver.Service, td *driver.TaskDefinition, deploymentID string) taskSpec {
	return taskSpec{
		cluster:         clusterNameFromARN(svc.ClusterARN),
		clusterARN:      svc.ClusterARN,
		td:              td,
		launchType:      effectiveServiceLaunchType(svc.LaunchType),
		group:           serviceGroup(svc.Name),
		startedBy:       deploymentID,
		platformVersion: svc.PlatformVersion,
		netCfg:          svc.NetworkConfiguration,
		tags:            svc.Tags,
	}
}

// drainService stops every RUNNING or PENDING task linked to the service in its
// cluster, releasing any reserved container-instance capacity. It is used to
// drain a superseded deployment before relaunching and to tear down tasks on
// delete.
func (m *Mock) drainService(ctx context.Context, svc *driver.Service) {
	group := serviceGroup(svc.Name)
	cluster := clusterNameFromARN(svc.ClusterARN)

	for _, t := range m.tasks.SortedValues() {
		if t.Group != group || clusterNameFromARN(t.ClusterARN) != cluster || t.LastStatus == statusStopped {
			continue
		}

		_, _ = m.StopTask(ctx, cluster, t.ARN, serviceStoppedReason)
	}
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
	}
}

// rolloutState reports COMPLETED once the running count reaches the desired
// count, else IN_PROGRESS. Circuit-breaker FAILED transitions are accepted but
// not simulated.
func rolloutState(running, desired int) string {
	if running >= desired {
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

	// A DAEMON service runs one task per container instance, so ECS rejects a
	// caller-supplied desiredCount on update just as it does on create.
	if svc.SchedulingStrategy == schedDaemon && in.DesiredCount != nil {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"desiredCount must not be specified for a DAEMON service")
	}

	updated := cloneService(svc)
	applyServiceScalars(&updated, &in)
	applyServiceRefs(&updated, &in)

	tdChanged, err := m.applyTaskDefChange(&updated, svc, &in)
	if err != nil {
		return nil, err
	}

	countChanged := in.DesiredCount != nil && *in.DesiredCount != svc.DesiredCount
	if in.ForceNewDeployment || tdChanged || countChanged {
		m.redeployService(ctx, &updated, &in)
	}

	m.services.Set(serviceKey(cluster, updated.Name), &updated)

	out := cloneService(&updated)

	return &out, nil
}

// applyTaskDefChange applies a task-definition change to the pending service
// update, reporting whether the definition actually changed. An unchanged (or
// unset) task definition is a no-op. A deregistered (INACTIVE) definition can't
// back a new deployment, same as CreateService/RunTask, so it is rejected.
func (m *Mock) applyTaskDefChange(updated, svc *driver.Service, in *driver.UpdateServiceInput) (bool, error) {
	if in.TaskDefinition == "" || in.TaskDefinition == svc.TaskDefinition {
		return false, nil
	}

	if _, err := m.resolveLaunchableTaskDef(in.TaskDefinition); err != nil {
		return false, err
	}

	updated.TaskDefinition = in.TaskDefinition

	return true, nil
}

// redeployService reconciles the service to a new PRIMARY deployment: it resolves
// the new target count, drains the existing tasks, relaunches against the current
// task definition, demotes prior deployments to ACTIVE, and appends an event.
func (m *Mock) redeployService(ctx context.Context, svc *driver.Service, in *driver.UpdateServiceInput) {
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

	m.drainService(ctx, svc)

	id := m.deploymentID()
	running, pending := m.converge(ctx, svc, td, id, target)
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
}

// ListServices returns services in a cluster in deterministic order.
func (m *Mock) ListServices(_ context.Context, cluster string) ([]driver.Service, error) {
	want := resolveClusterName(cluster)

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
func (m *Mock) DescribeServices(_ context.Context, cluster string, ids []string) ([]driver.Service, []driver.Failure, error) {
	want := resolveClusterName(cluster)

	found := make([]driver.Service, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		if s, ok := m.resolveService(want, id); ok {
			found = append(found, cloneService(s))
			continue
		}

		failures = append(failures, driver.Failure{ARN: m.arn("service/" + want + "/" + serviceNameOf(id)), Reason: "MISSING"})
	}

	return found, failures, nil
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

	updated := cloneService(svc)
	m.drainService(ctx, &updated)
	m.markServiceDeleted(&updated)
	m.services.Set(serviceKey(want, updated.Name), &updated)

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
