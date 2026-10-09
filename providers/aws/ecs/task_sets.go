package ecs

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Task-set statuses and stability values, and the bounds ECS documents for
// CreateTaskSet.
const (
	deployControllerExternal = "EXTERNAL"

	taskSetPrimary  = "PRIMARY"
	taskSetActive   = "ACTIVE"
	taskSetDraining = "DRAINING"

	stabilitySteadyState  = "STEADY_STATE"
	stabilityStabilizing  = "STABILIZING"
	maxTaskSetClientToken = 36
	maxScalePercent       = 100
	taskSetIDDigits       = 15
	taskSetIDBase         = 16

	// scaleRoundingEpsilon keeps float noise (3 x 100%% / 100 = 3.0000000000000004)
	// from rounding a whole count up.
	scaleRoundingEpsilon = 1e-9
)

// Compile-time check that Mock implements the TaskSets capability.
var _ driver.TaskSets = (*Mock)(nil)

func taskSetKey(cluster, service, id string) string {
	return cluster + "/" + service + "/" + id
}

// computedDesiredCount is the service desired count scaled by percent and
// rounded up, ECS's computedDesiredCount.
func computedDesiredCount(desired int, percent float64) int {
	return int(math.Ceil(float64(desired)*percent/maxScalePercent - scaleRoundingEpsilon))
}

func validateScale(s driver.Scale) error {
	if s.Unit != driver.ScaleUnitPercent {
		return apiErrf(errors.InvalidArgument, excInvalidParameter, "scale unit must be %s.", driver.ScaleUnitPercent)
	}

	if s.Value < 0 || s.Value > maxScalePercent {
		return apiErrf(errors.InvalidArgument, excInvalidParameter, "scale value must be between 0 and %d.", maxScalePercent)
	}

	return nil
}

// externalService resolves the ACTIVE service a task-set call targets: the
// cluster must exist, the service must exist, use the EXTERNAL deployment
// controller and be ACTIVE.
func (m *Mock) externalService(cluster, name string) (*driver.Service, error) {
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	svc, ok := m.resolveService(want, name)
	if !ok {
		return nil, apiErrf(errors.NotFound, excServiceNotFound, "service %q not found", name)
	}

	if svc.DeploymentController != deployControllerExternal {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"Task sets can only be used with services that use the EXTERNAL deployment controller.")
	}

	if svc.Status != statusActive {
		return nil, apiErrf(errors.FailedPrecondition, excServiceNotActive,
			"The specified service is not active. You can't update a service that is inactive.")
	}

	return svc, nil
}

// CreateTaskSet creates a task set in an EXTERNAL-controller service and
// launches computedDesiredCount tasks for it. The set starts ACTIVE (use
// UpdateServicePrimaryTaskSet to promote it). A repeated clientToken returns the
// task set the token already created.
//
//nolint:gocritic // in is passed by value to satisfy the driver.TaskSets interface.
func (m *Mock) CreateTaskSet(ctx context.Context, in driver.CreateTaskSetInput) (*driver.TaskSet, error) {
	scale := driver.Scale{Unit: driver.ScaleUnitPercent, Value: maxScalePercent}
	if in.Scale != nil {
		scale = *in.Scale
	}

	if err := validateScale(scale); err != nil {
		return nil, err
	}

	if len(in.ClientToken) > maxTaskSetClientToken {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"clientToken must be at most %d characters.", maxTaskSetClientToken)
	}

	if _, err := m.externalService(in.Cluster, in.Service); err != nil {
		return nil, err
	}

	td, err := m.resolveLaunchableTaskDef(in.TaskDefinition)
	if err != nil {
		return nil, err
	}

	if err = validateLaunch(td, effectiveServiceLaunchType(in.LaunchType), in.NetworkConfiguration); err != nil {
		return nil, err
	}

	var events pendingTaskEvents

	out, err := m.createTaskSetLocked(ctx, &in, td, scale, &events)
	if err != nil {
		return nil, err
	}

	m.publish(ctx, &events)
	m.publishClusterMetrics(resolveClusterName(in.Cluster))

	return out, nil
}

// createTaskSetLocked runs under the service's task-set lock: it re-resolves the
// service (a concurrent DeleteService may have removed it), honors an existing
// clientToken, claims the task-set id and launches the set's tasks.
func (m *Mock) createTaskSetLocked(
	ctx context.Context, in *driver.CreateTaskSetInput, td *driver.TaskDefinition, scale driver.Scale, events *pendingTaskEvents,
) (*driver.TaskSet, error) {
	want := resolveClusterName(in.Cluster)
	svcKey := serviceKey(want, serviceNameOf(in.Service))

	unlock := m.taskSetLock.lock(svcKey)
	defer unlock()

	svc, err := m.externalService(in.Cluster, in.Service)
	if err != nil {
		return nil, err
	}

	if in.ClientToken != "" {
		for _, existing := range m.taskSetsOf(svc) {
			if existing.ClientToken == in.ClientToken {
				out := cloneTaskSet(existing)

				return &out, nil
			}
		}
	}

	now := m.now()
	id := m.newTaskSetID(svcKey)
	region := arnRegion(svc.ARN, m.opts.Region)

	ts := &driver.TaskSet{
		ID:                       id,
		ARN:                      m.arnIn(region, "task-set/"+want+"/"+svc.Name+"/"+id),
		ServiceARN:               svc.ARN,
		ClusterARN:               svc.ClusterARN,
		ExternalID:               in.ExternalID,
		TaskDefinition:           td.ARN,
		Status:                   taskSetActive,
		LaunchType:               in.LaunchType,
		PlatformVersion:          in.PlatformVersion,
		CapacityProviderStrategy: append([]driver.CapacityProviderStrategyItem(nil), in.CapacityProviderStrategy...),
		NetworkConfiguration:     withDefaultAssignPublicIP(in.NetworkConfiguration),
		LoadBalancers:            append([]driver.LoadBalancer(nil), in.LoadBalancers...),
		ServiceRegistries:        append([]driver.ServiceRegistry(nil), in.ServiceRegistries...),
		Scale:                    scale,
		StartedBy:                id,
		ClientToken:              in.ClientToken,
		CreatedAt:                now,
		UpdatedAt:                now,
		Tags:                     copyTags(in.Tags),
	}

	ts.ComputedDesiredCount = computedDesiredCount(svc.DesiredCount, scale.Value)
	m.launchTaskSetTasks(ctx, svc, ts, td, ts.ComputedDesiredCount, events)
	m.refreshTaskSet(ts, svc.DesiredCount)
	m.taskSets.Set(taskSetKey(want, svc.Name, id), ts)
	m.recordTags(ts.ARN, in.Tags)

	out := cloneTaskSet(ts)

	return &out, nil
}

// newTaskSetID mints an "ecs-svc/<digits>" task-set id unique within the
// service.
func (m *Mock) newTaskSetID(svcKey string) string {
	for {
		n, err := strconv.ParseUint(m.hexID()[:taskSetIDDigits], taskSetIDBase, 64)
		if err != nil {
			continue
		}

		id := "ecs-svc/" + strconv.FormatUint(n, 10)
		if !m.taskSets.Has(svcKey + "/" + id) {
			return id
		}
	}
}

// taskSetsOf returns the stored task sets of svc, oldest first.
func (m *Mock) taskSetsOf(svc *driver.Service) []*driver.TaskSet {
	var out []*driver.TaskSet

	for _, ts := range m.taskSets.All() {
		if ts.ServiceARN == svc.ARN {
			out = append(out, ts)
		}
	}

	slices.SortFunc(out, func(a, b *driver.TaskSet) int {
		return strings.Compare(a.CreatedAt+a.ID, b.CreatedAt+b.ID)
	})

	return out
}

// findTaskSet resolves a task set of svc by id or ARN.
func (m *Mock) findTaskSet(svc *driver.Service, id string) (*driver.TaskSet, bool) {
	for _, ts := range m.taskSetsOf(svc) {
		if ts.ID == id || ts.ARN == id {
			return ts, true
		}
	}

	return nil, false
}

// taskSetTasks returns the non-stopped tasks a task set owns, oldest first.
func (m *Mock) taskSetTasks(svc *driver.Service, ts *driver.TaskSet) []*driver.Task {
	group := serviceGroup(svc.Name)
	cluster := clusterNameFromARN(svc.ClusterARN)

	var out []*driver.Task

	for _, t := range m.tasks.SortedValues() {
		if t.StartedBy == ts.ID && t.Group == group && clusterNameFromARN(t.ClusterARN) == cluster && t.LastStatus != statusStopped {
			out = append(out, t)
		}
	}

	return out
}

// launchTaskSetTasks launches n tasks for the set, buffering their launch
// events for the caller to publish after it releases the task-set lock.
func (m *Mock) launchTaskSetTasks(
	ctx context.Context, svc *driver.Service, ts *driver.TaskSet, td *driver.TaskDefinition, n int, events *pendingTaskEvents,
) {
	spec := taskSpec{
		cluster: clusterNameFromARN(svc.ClusterARN), clusterARN: svc.ClusterARN, td: td,
		launchType: effectiveServiceLaunchType(ts.LaunchType), group: serviceGroup(svc.Name), startedBy: ts.ID,
		platformVersion: ts.PlatformVersion, netCfg: ts.NetworkConfiguration, enableExec: svc.EnableExecuteCommand,
	}

	for range n {
		if task, _ := m.launchTask(ctx, &spec, true); task != nil {
			events.addLaunch(m, task)
		}
	}
}

// refreshTaskSet recomputes a task set's live counts and stability from the
// task store, stamping stabilityStatusAt when the stability changes.
func (m *Mock) refreshTaskSet(ts *driver.TaskSet, serviceDesired int) {
	ts.ComputedDesiredCount = computedDesiredCount(serviceDesired, ts.Scale.Value)
	ts.RunningCount, ts.PendingCount = 0, 0

	cluster := clusterNameFromARN(ts.ClusterARN)

	for _, t := range m.tasks.All() {
		if t.StartedBy != ts.ID || clusterNameFromARN(t.ClusterARN) != cluster {
			continue
		}

		switch t.LastStatus {
		case statusRunning:
			ts.RunningCount++
		case statusPending:
			ts.PendingCount++
		}
	}

	stability := stabilityStabilizing
	if ts.RunningCount == ts.ComputedDesiredCount && ts.PendingCount == 0 {
		stability = stabilitySteadyState
	}

	if stability != ts.StabilityStatus {
		ts.StabilityStatus = stability
		ts.StabilityStatusAt = m.now()
	}
}

// scaleTaskSet brings the set's task count to its computed desired count:
// launches the shortfall, or stops surplus tasks newest first, never the ones
// protected from scale-in.
func (m *Mock) scaleTaskSet(ctx context.Context, svc *driver.Service, ts *driver.TaskSet, events *pendingTaskEvents) {
	live := m.taskSetTasks(svc, ts)

	switch {
	case len(live) < ts.ComputedDesiredCount:
		if td, ok := m.resolveTaskDef(ts.TaskDefinition); ok {
			m.launchTaskSetTasks(ctx, svc, ts, td, ts.ComputedDesiredCount-len(live), events)
		}
	case len(live) > ts.ComputedDesiredCount:
		m.stopSurplus(ctx, svc, live, len(live)-ts.ComputedDesiredCount, events)
	}
}

// stopSurplus stops up to n of the given tasks, newest first, skipping tasks
// with active scale-in protection.
func (m *Mock) stopSurplus(ctx context.Context, svc *driver.Service, live []*driver.Task, n int, events *pendingTaskEvents) {
	now := m.opts.Clock.Now()
	cluster := clusterNameFromARN(svc.ClusterARN)

	for i := len(live) - 1; i >= 0 && n > 0; i-- {
		if taskProtected(live[i], now) {
			continue
		}

		if stopped, stoppedNow, err := m.stopTaskQuiet(ctx, cluster, live[i].ARN, serviceStoppedReason); err == nil && stoppedNow {
			events.addStop(m, stopped)

			n--
		}
	}
}

// UpdateTaskSet changes a task set's scale and rescales its tasks.
func (m *Mock) UpdateTaskSet(ctx context.Context, in driver.UpdateTaskSetInput) (*driver.TaskSet, error) {
	if err := validateScale(in.Scale); err != nil {
		return nil, err
	}

	var events pendingTaskEvents

	out, err := m.mutateTaskSet(ctx, in.Cluster, in.Service, in.TaskSet, &events, func(svc *driver.Service, ts *driver.TaskSet) {
		ts.Scale = in.Scale
		ts.UpdatedAt = m.now()
		m.refreshTaskSet(ts, svc.DesiredCount)
		m.scaleTaskSet(ctx, svc, ts, &events)
		m.refreshTaskSet(ts, svc.DesiredCount)
	})
	if err != nil {
		return nil, err
	}

	m.publish(ctx, &events)
	m.publishClusterMetrics(resolveClusterName(in.Cluster))

	return out, nil
}

// mutateTaskSet resolves a task set under the service's task-set lock, applies
// mutate to a copy and stores it, returning a clone.
func (m *Mock) mutateTaskSet(
	_ context.Context, cluster, service, id string, _ *pendingTaskEvents, mutate func(*driver.Service, *driver.TaskSet),
) (*driver.TaskSet, error) {
	want := resolveClusterName(cluster)
	unlock := m.taskSetLock.lock(serviceKey(want, serviceNameOf(service)))

	defer unlock()

	svc, err := m.externalService(cluster, service)
	if err != nil {
		return nil, err
	}

	stored, ok := m.findTaskSet(svc, id)
	if !ok {
		return nil, apiErrf(errors.NotFound, excTaskSetNotFound, "The specified task set %q was not found.", id)
	}

	ts := cloneTaskSet(stored)
	mutate(svc, &ts)
	m.taskSets.Set(taskSetKey(want, svc.Name, ts.ID), &ts)

	out := cloneTaskSet(&ts)

	return &out, nil
}

// DeleteTaskSet deletes a task set and stops its tasks. Unless force is set the
// set must already be scaled down to zero. The returned copy is DRAINING.
func (m *Mock) DeleteTaskSet(ctx context.Context, in driver.DeleteTaskSetInput) (*driver.TaskSet, error) {
	want := resolveClusterName(in.Cluster)

	var events pendingTaskEvents

	out, err := func() (*driver.TaskSet, error) {
		unlock := m.taskSetLock.lock(serviceKey(want, serviceNameOf(in.Service)))
		defer unlock()

		svc, err := m.externalService(in.Cluster, in.Service)
		if err != nil {
			return nil, err
		}

		stored, ok := m.findTaskSet(svc, in.TaskSet)
		if !ok {
			return nil, apiErrf(errors.NotFound, excTaskSetNotFound, "The specified task set %q was not found.", in.TaskSet)
		}

		if !in.Force && stored.Scale.Value > 0 {
			return nil, apiErrf(errors.FailedPrecondition, excInvalidParameter,
				"The task set must be scaled down to 0 before it is deleted. Scale it down or set force.")
		}

		m.stopTaskSetTasks(ctx, svc, stored, &events)
		m.taskSets.Delete(taskSetKey(want, svc.Name, stored.ID))
		m.tags.Delete(stored.ARN)

		out := cloneTaskSet(stored)
		out.Status = taskSetDraining
		out.RunningCount, out.PendingCount, out.ComputedDesiredCount = 0, 0, 0

		return &out, nil
	}()
	if err != nil {
		return nil, err
	}

	m.publish(ctx, &events)
	m.publishClusterMetrics(want)

	return out, nil
}

// stopTaskSetTasks stops every task a set owns, buffering the stop events.
func (m *Mock) stopTaskSetTasks(ctx context.Context, svc *driver.Service, ts *driver.TaskSet, events *pendingTaskEvents) {
	cluster := clusterNameFromARN(svc.ClusterARN)

	for _, t := range m.taskSetTasks(svc, ts) {
		if stopped, stoppedNow, err := m.stopTaskQuiet(ctx, cluster, t.ARN, serviceStoppedReason); err == nil && stoppedNow {
			events.addStop(m, stopped)
		}
	}
}

// DescribeTaskSets returns the service's task sets (all of them when ids is
// empty). An id that matches no task set of the service is a MISSING failure.
func (m *Mock) DescribeTaskSets(_ context.Context, cluster, service string, ids []string) ([]driver.TaskSet, []driver.Failure, error) {
	svc, err := m.externalService(cluster, service)
	if err != nil {
		return nil, nil, err
	}

	unlock := m.taskSetLock.lock(serviceKey(resolveClusterName(cluster), svc.Name))
	defer unlock()

	if len(ids) == 0 {
		return m.taskSetViews(svc), nil, nil
	}

	out := make([]driver.TaskSet, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		stored, ok := m.findTaskSet(svc, id)
		if !ok {
			failures = append(failures, driver.Failure{ARN: id, Reason: failureMissing})
			continue
		}

		out = append(out, m.viewTaskSet(svc, stored))
	}

	return out, failures, nil
}

// taskSetViews returns refreshed copies of every task set of svc.
func (m *Mock) taskSetViews(svc *driver.Service) []driver.TaskSet {
	sets := m.taskSetsOf(svc)
	out := make([]driver.TaskSet, 0, len(sets))

	for _, ts := range sets {
		out = append(out, m.viewTaskSet(svc, ts))
	}

	return out
}

// viewTaskSet returns a copy of ts with live counts and stability, persisting
// the refresh so stabilityStatusAt is stable across reads.
func (m *Mock) viewTaskSet(svc *driver.Service, stored *driver.TaskSet) driver.TaskSet {
	ts := cloneTaskSet(stored)
	m.refreshTaskSet(&ts, svc.DesiredCount)

	if ts.RunningCount != stored.RunningCount || ts.PendingCount != stored.PendingCount ||
		ts.ComputedDesiredCount != stored.ComputedDesiredCount || ts.StabilityStatus != stored.StabilityStatus {
		persisted := cloneTaskSet(&ts)
		m.taskSets.Set(taskSetKey(clusterNameFromARN(svc.ClusterARN), svc.Name, ts.ID), &persisted)
	}

	return ts
}

// UpdateServicePrimaryTaskSet promotes a task set to PRIMARY (the previous
// PRIMARY becomes ACTIVE) and has the service adopt its task definition and
// network configuration.
func (m *Mock) UpdateServicePrimaryTaskSet(_ context.Context, cluster, service, primary string) (*driver.TaskSet, error) {
	want := resolveClusterName(cluster)
	unlock := m.taskSetLock.lock(serviceKey(want, serviceNameOf(service)))

	defer unlock()

	svc, err := m.externalService(cluster, service)
	if err != nil {
		return nil, err
	}

	target, ok := m.findTaskSet(svc, primary)
	if !ok {
		return nil, apiErrf(errors.NotFound, excTaskSetNotFound, "The specified task set %q was not found.", primary)
	}

	now := m.now()

	for _, ts := range m.taskSetsOf(svc) {
		if ts.Status != taskSetPrimary || ts.ID == target.ID {
			continue
		}

		demoted := cloneTaskSet(ts)
		demoted.Status, demoted.UpdatedAt = taskSetActive, now
		m.taskSets.Set(taskSetKey(want, svc.Name, demoted.ID), &demoted)
	}

	promoted := cloneTaskSet(target)
	promoted.Status, promoted.UpdatedAt = taskSetPrimary, now
	m.taskSets.Set(taskSetKey(want, svc.Name, promoted.ID), &promoted)

	m.services.Update(serviceKey(want, svc.Name), func(s *driver.Service) *driver.Service {
		updated := cloneService(s)
		updated.TaskDefinition = promoted.TaskDefinition
		updated.NetworkConfiguration = cloneNetworkConfig(promoted.NetworkConfiguration)

		return &updated
	})

	out := m.viewTaskSet(svc, &promoted)

	return &out, nil
}

// rescaleTaskSets recomputes and rescales every task set of svc after the
// service's desired count changed. It returns the buffered task events.
func (m *Mock) rescaleTaskSets(ctx context.Context, svc *driver.Service, events *pendingTaskEvents) {
	for _, stored := range m.taskSetsOf(svc) {
		ts := cloneTaskSet(stored)
		m.refreshTaskSet(&ts, svc.DesiredCount)
		m.scaleTaskSet(ctx, svc, &ts, events)
		m.refreshTaskSet(&ts, svc.DesiredCount)
		ts.UpdatedAt = m.now()
		m.taskSets.Set(taskSetKey(clusterNameFromARN(svc.ClusterARN), svc.Name, ts.ID), &ts)
	}
}

// deleteTaskSetsOf removes every task set of svc with its tasks and tag entries
// (DeleteService's cascade). It runs under the service's task-set lock.
func (m *Mock) deleteTaskSetsOf(ctx context.Context, svc *driver.Service, events *pendingTaskEvents) {
	for _, ts := range m.taskSetsOf(svc) {
		m.stopTaskSetTasks(ctx, svc, ts, events)
		m.taskSets.Delete(taskSetKey(clusterNameFromARN(svc.ClusterARN), svc.Name, ts.ID))
		m.tags.Delete(ts.ARN)
	}
}

// reconcileTaskSetAfterStop relaunches a replacement for a stopped task-set
// task so the set converges back to its computed desired count, as the ECS
// scheduler does. It reports false when the task's service does not use the
// EXTERNAL controller.
func (m *Mock) reconcileTaskSetAfterStop(ctx context.Context, task *driver.Task, name string) bool {
	cluster := clusterNameFromARN(task.ClusterARN)
	key := serviceKey(cluster, name)

	svc, ok := m.services.Get(key)
	if !ok || svc.DeploymentController != deployControllerExternal {
		return false
	}

	var events pendingTaskEvents

	func() {
		unlock := m.taskSetLock.lock(key)
		defer unlock()

		current, ok := m.services.Get(key)
		if !ok || current.Status != statusActive {
			return
		}

		stored, ok := m.findTaskSet(current, task.StartedBy)
		if !ok {
			return
		}

		ts := cloneTaskSet(stored)
		m.refreshTaskSet(&ts, current.DesiredCount)
		m.scaleTaskSet(ctx, current, &ts, &events)
		m.refreshTaskSet(&ts, current.DesiredCount)
		m.taskSets.Set(taskSetKey(cluster, name, ts.ID), &ts)
	}()

	m.publish(ctx, &events)
	m.publishClusterMetrics(cluster)

	return true
}
