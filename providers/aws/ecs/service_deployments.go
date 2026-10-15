package ecs

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Limits from the ECS service-deployment API reference, and the retained
// history per service (ECS keeps 90 days; the emulator keeps the newest 100).
const (
	maxServiceDeployments        = 100
	maxDeploymentDescribeARNs    = 20
	defaultDeploymentListResults = 20
	maxDeploymentListResults     = 100
	deploymentIDDigits           = 15
	idBaseHex                    = 16

	rollbackReasonUser = "Service deployment rolled back by user."
)

// Compile-time check that Mock implements the ServiceDeployments capability.
var _ driver.ServiceDeployments = (*Mock)(nil)

// numericID mints a decimal id from random hex, the shape of ECS revision ids.
func (m *Mock) numericID() string {
	for {
		if n, err := strconv.ParseUint(m.hexID()[:deploymentIDDigits], idBaseHex, 64); err == nil {
			return strconv.FormatUint(n, 10)
		}
	}
}

// recordServiceDeployment records the service deployment (and the service
// revision it rolls out) that a committed CreateService or deploying
// UpdateService started. It is a no-op when the service was deleted meanwhile,
// so a deployment is never recorded for an inactive service.
func (m *Mock) recordServiceDeployment(svc *driver.Service) {
	if svc.DeploymentController == deployControllerExternal {
		return
	}

	cluster := clusterNameFromARN(svc.ClusterARN)
	key := serviceKey(cluster, svc.Name)

	unlock := m.deploymentLock.lock(key)
	defer unlock()

	if current, ok := m.services.Get(key); !ok || current.Status != statusActive {
		return
	}

	m.addDeploymentLocked(svc)
}

// addDeploymentLocked appends a deployment+revision for svc and trims the
// history. The caller holds the service's deploymentLock.
func (m *Mock) addDeploymentLocked(svc *driver.Service) {
	prior := m.deploymentsOf(svc)
	now := m.now()
	region := arnRegion(svc.ARN, m.opts.Region)
	cluster := clusterNameFromARN(svc.ClusterARN)

	rev := &driver.ServiceRevision{
		ARN:                      m.arnIn(region, "service-revision/"+cluster+"/"+svc.Name+"/"+m.numericID()),
		ServiceARN:               svc.ARN,
		ClusterARN:               svc.ClusterARN,
		TaskDefinition:           svc.TaskDefinition,
		LaunchType:               svc.LaunchType,
		PlatformVersion:          svc.PlatformVersion,
		CapacityProviderStrategy: append([]driver.CapacityProviderStrategyItem(nil), svc.CapacityProviderStrategy...),
		NetworkConfiguration:     cloneNetworkConfig(svc.NetworkConfiguration),
		LoadBalancers:            append([]driver.LoadBalancer(nil), svc.LoadBalancers...),
		ServiceRegistries:        append([]driver.ServiceRegistry(nil), svc.ServiceRegistries...),
		ServiceConnect:           cloneServiceConnect(svc.ServiceConnect),
		CreatedAt:                now,
	}
	m.serviceRevisions.Set(rev.ARN, rev)

	dep := &driver.ServiceDeployment{
		Sequence:   nextDeploymentSequence(prior),
		ARN:        m.arnIn(region, "service-deployment/"+cluster+"/"+svc.Name+"/"+m.hexID()[:21]),
		ServiceARN: svc.ARN,
		ClusterARN: svc.ClusterARN,
		Status:     driver.DeploymentStatusSuccessful,
		TargetServiceRevision: driver.ServiceRevisionSummary{
			ARN: rev.ARN, RequestedTaskCount: svc.DesiredCount,
			RunningTaskCount: svc.RunningCount, PendingTaskCount: svc.PendingCount,
		},
		DeploymentConfiguration: cloneDeploymentConfig(svc.DeploymentConfiguration),
		CreatedAt:               now,
		StartedAt:               now,
		FinishedAt:              now,
		UpdatedAt:               now,
	}

	if len(prior) > 0 {
		dep.SourceServiceRevisions = []driver.ServiceRevisionSummary{{ARN: prior[0].TargetServiceRevision.ARN}}
	}

	if !deploymentComplete(svc.RunningCount, svc.DesiredCount) {
		dep.Status, dep.FinishedAt = driver.DeploymentStatusInProgress, ""
	}

	m.serviceDeployments.Set(dep.ARN, dep)

	if dep.Status == driver.DeploymentStatusSuccessful {
		m.deploymentSettle.Begin(dep.ARN, driver.DeploymentStatusInProgress, m.opts.Clock.Now(),
			m.opts.SettleDuration(settle.DefaultECSDeploymentSettle))
	}

	m.trimDeploymentsLocked(svc)
}

func nextDeploymentSequence(prior []*driver.ServiceDeployment) uint64 {
	if len(prior) == 0 {
		return 1
	}

	return prior[0].Sequence + 1
}

// deploymentsOf returns the stored deployments of svc, newest first.
func (m *Mock) deploymentsOf(svc *driver.Service) []*driver.ServiceDeployment {
	var out []*driver.ServiceDeployment

	for _, d := range m.serviceDeployments.All() {
		if d.ServiceARN == svc.ARN {
			out = append(out, d)
		}
	}

	slices.SortFunc(out, func(a, b *driver.ServiceDeployment) int {
		switch {
		case a.Sequence > b.Sequence:
			return -1
		case a.Sequence < b.Sequence:
			return 1
		default:
			return 0
		}
	})

	return out
}

// trimDeploymentsLocked drops the oldest deployments (and revisions no retained
// deployment references) beyond the retained history.
func (m *Mock) trimDeploymentsLocked(svc *driver.Service) {
	deps := m.deploymentsOf(svc)
	if len(deps) <= maxServiceDeployments {
		return
	}

	for _, old := range deps[maxServiceDeployments:] {
		m.serviceDeployments.Delete(old.ARN)
		m.deploymentSettle.Clear(old.ARN)
	}

	keep := make(map[string]bool, len(deps))

	for _, d := range deps[:maxServiceDeployments] {
		keep[d.TargetServiceRevision.ARN] = true

		for _, src := range d.SourceServiceRevisions {
			keep[src.ARN] = true
		}
	}

	for arn, rev := range m.serviceRevisions.All() {
		if rev.ServiceARN == svc.ARN && !keep[arn] {
			m.serviceRevisions.Delete(arn)
		}
	}
}

// deleteServiceDeployments removes a deleted service's history. It takes the
// service's deploymentLock, so it serializes with a concurrent record.
func (m *Mock) deleteServiceDeployments(svc *driver.Service) {
	unlock := m.deploymentLock.lock(serviceKey(clusterNameFromARN(svc.ClusterARN), svc.Name))
	defer unlock()

	for _, d := range m.deploymentsOf(svc) {
		m.serviceDeployments.Delete(d.ARN)
		m.deploymentSettle.Clear(d.ARN)
	}

	for arn, rev := range m.serviceRevisions.All() {
		if rev.ServiceARN == svc.ARN {
			m.serviceRevisions.Delete(arn)
		}
	}
}

// ensureServiceDeployment gives an ECS-controller service that has no recorded
// deployment (state restored from before deployments were recorded) its current
// one, once. The caller holds the service's deploymentLock.
func (m *Mock) ensureServiceDeployment(svc *driver.Service) {
	if svc.DeploymentController == deployControllerExternal || svc.Status != statusActive || len(m.deploymentsOf(svc)) > 0 {
		return
	}

	m.addDeploymentLocked(svc)
}

// observedDeployment copies d with its wire-visible status: the settle overlay
// while a transient window is open, in which case finishedAt/stoppedAt are not
// yet set.
func (m *Mock) observedDeployment(d *driver.ServiceDeployment) driver.ServiceDeployment {
	out := cloneServiceDeployment(d)

	if observed := m.deploymentSettle.State(d.ARN, m.opts.Clock.Now(), d.Status); observed != d.Status {
		out.Status = observed
		out.FinishedAt, out.StoppedAt = "", ""
	}

	return out
}

// deploymentService resolves the cluster and service a list call names.
func (m *Mock) deploymentService(cluster, service string) (*driver.Service, error) {
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	svc, ok := m.resolveService(want, service)
	if !ok {
		return nil, apiErrf(errors.NotFound, excServiceNotFound, "service %q not found", service)
	}

	return svc, nil
}

// ListServiceDeployments lists a service's deployments newest first, filtered by
// status and creation time and paged with maxResults (1-100, default 20) and
// nextToken.
//
//nolint:gocritic // in is passed by value to satisfy the driver.ServiceDeployments interface.
func (m *Mock) ListServiceDeployments(
	_ context.Context, in driver.ListServiceDeploymentsInput,
) ([]driver.ServiceDeployment, string, error) {
	svc, err := m.deploymentService(in.Cluster, in.Service)
	if err != nil {
		return nil, "", err
	}

	if in.MaxResults < 0 || in.MaxResults > maxDeploymentListResults {
		return nil, "", apiErrf(errors.InvalidArgument, excInvalidParameter,
			"maxResults must be between 1 and %d.", maxDeploymentListResults)
	}

	before, err := parseOptionalTime(in.CreatedAtBefore)
	if err != nil {
		return nil, "", err
	}

	after, err := parseOptionalTime(in.CreatedAtAfter)
	if err != nil {
		return nil, "", err
	}

	stored := m.ensuredDeployments(svc)

	matched := make([]driver.ServiceDeployment, 0, len(stored))

	for _, d := range stored {
		view := m.observedDeployment(d)
		if deploymentMatches(&view, in.Statuses, before, after) {
			matched = append(matched, view)
		}
	}

	size := in.MaxResults
	if size == 0 {
		size = defaultDeploymentListResults
	}

	page, err := pagination.Paginate(matched, in.NextToken, size)
	if err != nil {
		return nil, "", apiErrf(errors.InvalidArgument, excInvalidParameter, "invalid nextToken: %v", err)
	}

	return page.Items, page.NextPageToken, nil
}

// ensuredDeployments returns svc's deployments newest first, synthesizing the
// current one first when state restored from before deployments were recorded
// has none.
func (m *Mock) ensuredDeployments(svc *driver.Service) []*driver.ServiceDeployment {
	unlock := m.deploymentLock.lock(serviceKey(clusterNameFromARN(svc.ClusterARN), svc.Name))
	defer unlock()

	m.ensureServiceDeployment(svc)

	return m.deploymentsOf(svc)
}

func parseOptionalTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, apiErrf(errors.InvalidArgument, excInvalidParameter, "%q is not an RFC3339 timestamp.", v)
	}

	return t, nil
}

// deploymentMatches applies the list filters: status membership and a creation
// time strictly after/before the given instants.
func deploymentMatches(d *driver.ServiceDeployment, statuses []string, before, after time.Time) bool {
	if len(statuses) > 0 && !slices.Contains(statuses, d.Status) {
		return false
	}

	created, err := time.Parse(time.RFC3339, d.CreatedAt)
	if err != nil {
		return false
	}

	if !after.IsZero() && !created.After(after) {
		return false
	}

	return before.IsZero() || created.Before(before)
}

func validateARNBatch(arns []string) error {
	if len(arns) == 0 || len(arns) > maxDeploymentDescribeARNs {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"ARNs must contain between 1 and %d entries.", maxDeploymentDescribeARNs)
	}

	return nil
}

// DescribeServiceDeployments describes up to 20 deployments by ARN; an ARN that
// matches no deployment is a MISSING failure.
func (m *Mock) DescribeServiceDeployments(_ context.Context, arns []string) ([]driver.ServiceDeployment, []driver.Failure, error) {
	if err := validateARNBatch(arns); err != nil {
		return nil, nil, err
	}

	out := make([]driver.ServiceDeployment, 0, len(arns))
	failures := make([]driver.Failure, 0, len(arns))

	for _, arn := range arns {
		d, ok := m.serviceDeployments.Get(arn)
		if !ok {
			failures = append(failures, driver.Failure{ARN: arn, Reason: failureMissing})
			continue
		}

		out = append(out, m.observedDeployment(d))
	}

	return out, failures, nil
}

// DescribeServiceRevisions describes up to 20 service revisions by ARN.
func (m *Mock) DescribeServiceRevisions(_ context.Context, arns []string) ([]driver.ServiceRevision, []driver.Failure, error) {
	if err := validateARNBatch(arns); err != nil {
		return nil, nil, err
	}

	out := make([]driver.ServiceRevision, 0, len(arns))
	failures := make([]driver.Failure, 0, len(arns))

	for _, arn := range arns {
		r, ok := m.serviceRevisions.Get(arn)
		if !ok {
			failures = append(failures, driver.Failure{ARN: arn, Reason: failureMissing})
			continue
		}

		out = append(out, cloneServiceRevision(r))
	}

	return out, failures, nil
}

// StopServiceDeployment stops a deployment that has not completed. ROLLBACK
// rolls the service back to the deployment's source revision and ends
// ROLLBACK_SUCCESSFUL; ABORT ends STOPPED and leaves the service as it is. With
// --async-settle the transient ROLLBACK_IN_PROGRESS / STOP_REQUESTED status is
// visible first and a repeated stop continues as-is; a completed deployment is a
// ConflictException. stopType is optional on the wire: an omitted stopType
// means ROLLBACK.
func (m *Mock) StopServiceDeployment(ctx context.Context, arn, stopType string) (string, error) {
	if stopType == "" {
		stopType = driver.StopTypeRollback
	}

	if stopType != driver.StopTypeAbort && stopType != driver.StopTypeRollback {
		return "", apiErrf(errors.InvalidArgument, excInvalidParameter,
			"stopType must be %s or %s.", driver.StopTypeAbort, driver.StopTypeRollback)
	}

	stored, ok := m.serviceDeployments.Get(arn)
	if !ok {
		return "", apiErrf(errors.NotFound, excServiceDeploymentNotFound, "The service deployment %q was not found.", arn)
	}

	svcKey := serviceKey(clusterNameFromARN(stored.ClusterARN), serviceNameOf(stored.ServiceARN))

	revert, err := m.stopDeploymentLocked(arn, stopType, svcKey)
	if err != nil {
		return "", err
	}

	if revert != nil {
		m.revertService(ctx, svcKey, revert)
	}

	return arn, nil
}

// stopDeploymentLocked flips the deployment's status under the service's
// deploymentLock and returns the revision to roll the service back to (nil for
// ABORT or a repeated stop).
func (m *Mock) stopDeploymentLocked(arn, stopType, svcKey string) (*driver.ServiceRevision, error) {
	unlock := m.deploymentLock.lock(svcKey)
	defer unlock()

	stored, ok := m.serviceDeployments.Get(arn)
	if !ok {
		return nil, apiErrf(errors.NotFound, excServiceDeploymentNotFound, "The service deployment %q was not found.", arn)
	}

	switch m.deploymentSettle.State(arn, m.opts.Clock.Now(), stored.Status) {
	case driver.DeploymentStatusStopRequested, driver.DeploymentStatusRollbackRequested, driver.DeploymentStatusRollbackInProgress:
		return nil, nil // already stopping: continues as-is
	case driver.DeploymentStatusPending, driver.DeploymentStatusInProgress:
	default:
		return nil, apiErrf(errors.FailedPrecondition, excConflict,
			"The service deployment has already completed and can't be stopped.")
	}

	updated := cloneServiceDeployment(stored)
	now := m.now()
	updated.UpdatedAt = now

	var (
		revert    *driver.ServiceRevision
		transient string
	)

	if stopType == driver.StopTypeRollback {
		if len(updated.SourceServiceRevisions) == 0 {
			return nil, apiErrf(errors.FailedPrecondition, excConflict,
				"The service deployment has no previous service revision to roll back to.")
		}

		source := updated.SourceServiceRevisions[0].ARN

		rev, found := m.serviceRevisions.Get(source)
		if !found {
			return nil, apiErrf(errors.FailedPrecondition, excConflict, "The previous service revision is no longer available.")
		}

		copied := cloneServiceRevision(rev)
		revert = &copied
		updated.Status, updated.FinishedAt = driver.DeploymentStatusRollbackSuccessful, now
		updated.Rollback = &driver.ServiceDeploymentRollback{Reason: rollbackReasonUser, ServiceRevisionARN: source, StartedAt: now}
		transient = driver.DeploymentStatusRollbackInProgress
	} else {
		updated.Status, updated.StoppedAt = driver.DeploymentStatusStopped, now
		transient = driver.DeploymentStatusStopRequested
	}

	m.serviceDeployments.Set(arn, &updated)
	m.deploymentSettle.Begin(arn, transient, m.opts.Clock.Now(), m.opts.SettleDuration(settle.DefaultECSDeploymentSettle))

	return revert, nil
}

// revertService puts the service back on a previous revision's task definition
// and network configuration and redeploys its tasks. It does not record a new
// deployment: the rollback belongs to the deployment that was stopped.
func (m *Mock) revertService(ctx context.Context, svcKey string, rev *driver.ServiceRevision) {
	svc, ok := m.services.Get(svcKey)
	if !ok || svc.Status != statusActive {
		return
	}

	updated := cloneService(svc)
	updated.TaskDefinition = rev.TaskDefinition
	updated.NetworkConfiguration = cloneNetworkConfig(rev.NetworkConfiguration)

	var events pendingTaskEvents

	m.redeployService(ctx, &updated, &driver.UpdateServiceInput{}, &events)
	m.services.Set(svcKey, &updated)
	m.publish(ctx, &events)
}
