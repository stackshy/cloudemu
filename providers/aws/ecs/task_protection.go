package ecs

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Task scale-in protection limits and failure reasons, from the ECS
// GetTaskProtection / UpdateTaskProtection API reference.
const (
	protectionDefaultMinutes = 120
	protectionMinMinutes     = 1
	protectionMaxMinutes     = 2880

	maxUpdateProtectionTasks = 10
	maxGetProtectionTasks    = 100

	failureTaskNotValid      = "TASK_NOT_VALID"
	failureDeploymentBlocked = "DEPLOYMENT_BLOCKED"
)

// Compile-time check that Mock implements the TaskProtection capability.
var _ driver.TaskProtection = (*Mock)(nil)

// taskProtected reports whether t's scale-in protection is active at now:
// enabled and not past its expiry. A stopped task is never protected, whatever
// protection it carried when it stopped.
func taskProtected(t *driver.Task, now time.Time) bool {
	if !t.ProtectionEnabled || t.LastStatus == statusStopped {
		return false
	}

	if t.ProtectionExpiresAt == "" {
		return true
	}

	expires, err := time.Parse(time.RFC3339, t.ProtectionExpiresAt)

	return err != nil || now.Before(expires)
}

func protectedView(t *driver.Task, now time.Time) driver.ProtectedTask {
	if !taskProtected(t, now) {
		return driver.ProtectedTask{TaskARN: t.ARN}
	}

	return driver.ProtectedTask{TaskARN: t.ARN, ProtectionEnabled: true, ExpirationDate: t.ProtectionExpiresAt}
}

// GetTaskProtection reports the scale-in protection of up to 100 service
// tasks. A task that does not exist in the cluster is a MISSING failure; a task
// that is not part of a service is TASK_NOT_VALID.
func (m *Mock) GetTaskProtection(_ context.Context, cluster string, tasks []string) ([]driver.ProtectedTask, []driver.Failure, error) {
	want, err := m.protectionCluster(cluster, tasks, maxGetProtectionTasks)
	if err != nil {
		return nil, nil, err
	}

	now := m.opts.Clock.Now()
	out := make([]driver.ProtectedTask, 0, len(tasks))
	failures := make([]driver.Failure, 0, len(tasks))

	for _, id := range tasks {
		t, failure := m.protectableTask(want, id)
		if failure != nil {
			failures = append(failures, *failure)
			continue
		}

		out = append(out, protectedView(t, now))
	}

	return out, failures, nil
}

// UpdateTaskProtection enables or disables scale-in protection on up to 10
// service tasks. Enabling lasts ExpiresInMinutes (1-2880, default 120) from the
// clock and a repeat call resets the expiry. Enabling is refused with
// DEPLOYMENT_BLOCKED when it would leave more protected tasks than the service's
// desired count. The whole call runs under protectMu so the count check and the
// writes cannot interleave with another protection change.
func (m *Mock) UpdateTaskProtection(
	_ context.Context, in driver.UpdateTaskProtectionInput,
) ([]driver.ProtectedTask, []driver.Failure, error) {
	want, err := m.protectionCluster(in.Cluster, in.Tasks, maxUpdateProtectionTasks)
	if err != nil {
		return nil, nil, err
	}

	minutes := protectionDefaultMinutes

	if in.ProtectionEnabled && in.ExpiresInMinutes != nil {
		minutes = *in.ExpiresInMinutes
		if minutes < protectionMinMinutes || minutes > protectionMaxMinutes {
			return nil, nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
				"expiresInMinutes must be between %d and %d.", protectionMinMinutes, protectionMaxMinutes)
		}
	}

	m.protectMu.Lock()
	defer m.protectMu.Unlock()

	now := m.opts.Clock.Now()
	expires := ""

	if in.ProtectionEnabled {
		expires = now.Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339)
	}

	out := make([]driver.ProtectedTask, 0, len(in.Tasks))
	failures := make([]driver.Failure, 0, len(in.Tasks))

	for _, id := range in.Tasks {
		view, failure := m.protectOne(want, id, in.ProtectionEnabled, expires, now)
		if failure != nil {
			failures = append(failures, *failure)
			continue
		}

		out = append(out, *view)
	}

	return out, failures, nil
}

// protectOne applies a protection change to one task under protectMu and
// returns its new state, or the failure that kept it from applying. It also
// holds placeMu (the lock StopTask writes STOPPED under) so the read, check and
// write are one step: a stop cannot land between them and be overwritten with
// the stale RUNNING copy. Lock order is protectMu, then placeMu.
func (m *Mock) protectOne(
	cluster, id string, enable bool, expires string, now time.Time,
) (*driver.ProtectedTask, *driver.Failure) {
	m.placeMu.Lock()
	defer m.placeMu.Unlock()

	t, failure := m.protectableTask(cluster, id)
	if failure != nil {
		return nil, failure
	}

	if enable && t.LastStatus == statusStopped {
		return nil, &driver.Failure{
			ARN: t.ARN, Reason: failureTaskNotValid,
			Detail: "The task is stopped; only running service tasks can be protected.",
		}
	}

	if enable && m.protectionBlocked(t, now) {
		return nil, &driver.Failure{
			ARN: id, Reason: failureDeploymentBlocked,
			Detail: "Protecting this task would leave more protected tasks than the service's desired count.",
		}
	}

	updated := cloneTask(t)
	updated.ProtectionEnabled = enable
	updated.ProtectionExpiresAt = expires
	m.tasks.Set(updated.ARN, &updated)

	view := protectedView(&updated, now)

	return &view, nil
}

// protectionCluster validates the cluster and task-id list shared by both
// protection operations and returns the bare cluster name.
func (m *Mock) protectionCluster(cluster string, tasks []string, maxTasks int) (string, error) {
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return "", apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	if len(tasks) == 0 || len(tasks) > maxTasks {
		return "", apiErrf(errors.InvalidArgument, excInvalidParameter,
			"tasks must contain between 1 and %d task ids or ARNs.", maxTasks)
	}

	return want, nil
}

// protectableTask resolves a task id inside cluster want. It returns a MISSING
// failure for an unknown task (or one in another cluster) and TASK_NOT_VALID for
// a task that no service manages, since only service tasks can be protected.
func (m *Mock) protectableTask(want, id string) (*driver.Task, *driver.Failure) {
	t, ok := m.resolveTask(id)
	if !ok || clusterNameFromARN(t.ClusterARN) != want {
		return nil, &driver.Failure{ARN: id, Reason: failureMissing}
	}

	if _, managed := serviceNameFromGroup(t.Group); !managed {
		return nil, &driver.Failure{
			ARN: t.ARN, Reason: failureTaskNotValid,
			Detail: "Only tasks started by a service can be protected from scale-in.",
		}
	}

	return t, nil
}

// protectionBlocked reports whether protecting t would leave its service with
// more protected tasks than its desired count.
func (m *Mock) protectionBlocked(t *driver.Task, now time.Time) bool {
	name, _ := serviceNameFromGroup(t.Group)

	svc, ok := m.services.Get(serviceKey(clusterNameFromARN(t.ClusterARN), name))
	if !ok {
		return false
	}

	protected := 1

	for _, other := range m.tasks.All() {
		if other.ARN == t.ARN || other.Group != t.Group || other.ClusterARN != t.ClusterARN || other.LastStatus == statusStopped {
			continue
		}

		if taskProtected(other, now) {
			protected++
		}
	}

	return protected > svc.DesiredCount
}

// keptProtectedTasks returns the service's non-stopped tasks whose protection is
// active; a scale-in or redeployment must leave these running.
func (m *Mock) keptProtectedTasks(svc *driver.Service) []*driver.Task {
	group := serviceGroup(svc.Name)
	cluster := clusterNameFromARN(svc.ClusterARN)
	now := m.opts.Clock.Now()

	var kept []*driver.Task

	for _, t := range m.tasks.SortedValues() {
		if t.Group == group && clusterNameFromARN(t.ClusterARN) == cluster && t.LastStatus != statusStopped && taskProtected(t, now) {
			kept = append(kept, t)
		}
	}

	return kept
}
