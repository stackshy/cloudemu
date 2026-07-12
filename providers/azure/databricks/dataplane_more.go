package databricks

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// --- job runs ---

// GetRun returns a run by ID.
func (m *Mock) GetRun(_ context.Context, runID int64) (*driver.Run, error) {
	run, ok := m.runs.Get(jobKey(runID))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "run %d not found", runID)
	}

	result := *run

	return &result, nil
}

// ListRuns lists runs, optionally filtered by job ID (0 = all).
func (m *Mock) ListRuns(_ context.Context, jobID int64) ([]driver.Run, error) {
	all := m.runs.All()
	out := make([]driver.Run, 0, len(all))

	for _, run := range all {
		if jobID == 0 || run.JobID == jobID {
			out = append(out, *run)
		}
	}

	return out, nil
}

// CancelRun marks a run canceled.
func (m *Mock) CancelRun(_ context.Context, runID int64) error {
	run, ok := m.runs.Get(jobKey(runID))
	if !ok {
		return errors.Newf(errors.NotFound, "run %d not found", runID)
	}

	updated := *run
	updated.LifeCycleState = driver.RunTerminated
	updated.ResultState = driver.ResultCanceled
	updated.StateMessage = "Run canceled"
	m.runs.Set(jobKey(runID), &updated)

	return nil
}

// GetRunOutput returns a completed run's output.
func (m *Mock) GetRunOutput(_ context.Context, runID int64) (*driver.RunOutput, error) {
	run, ok := m.runs.Get(jobKey(runID))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "run %d not found", runID)
	}

	return &driver.RunOutput{Run: *run, NotebookResult: "ok"}, nil
}

// SubmitRun creates a one-time run (not tied to a stored job) and returns its
// run ID. The run completes synchronously.
func (m *Mock) SubmitRun(_ context.Context, runName string) (int64, error) {
	runID := m.runSeq.Add(1)
	now := m.opts.Clock.Now().UTC().UnixMilli()
	m.runs.Set(jobKey(runID), &driver.Run{
		RunID:          runID,
		RunName:        runName,
		LifeCycleState: driver.RunTerminated,
		ResultState:    driver.ResultSuccess,
		StateMessage:   "Run completed",
		StartTime:      now,
		EndTime:        now,
	})

	return runID, nil
}

// CancelAllRuns cancels every run belonging to a job.
func (m *Mock) CancelAllRuns(_ context.Context, jobID int64) error {
	for key, run := range m.runs.All() {
		if run.JobID != jobID {
			continue
		}

		updated := *run
		updated.LifeCycleState = driver.RunTerminated
		updated.ResultState = driver.ResultCanceled
		m.runs.Set(key, &updated)
	}

	return nil
}

// DeleteRun removes a run by ID.
func (m *Mock) DeleteRun(_ context.Context, runID int64) error {
	if !m.runs.Delete(jobKey(runID)) {
		return errors.Newf(errors.NotFound, "run %d not found", runID)
	}

	return nil
}

// RepairRun triggers a repair of a run and returns the new repair ID.
func (m *Mock) RepairRun(_ context.Context, runID int64) (int64, error) {
	if !m.runs.Has(jobKey(runID)) {
		return 0, errors.Newf(errors.NotFound, "run %d not found", runID)
	}

	return m.runSeq.Add(1), nil
}

// --- cluster policies ---

// CreateClusterPolicy creates a cluster policy.
func (m *Mock) CreateClusterPolicy(_ context.Context, cfg driver.ClusterPolicyConfig) (*driver.ClusterPolicy, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "policy name is required")
	}

	id := idgen.GenerateID("policy-")
	policy := &driver.ClusterPolicy{
		PolicyID:           id,
		Name:               cfg.Name,
		Definition:         cfg.Definition,
		Description:        cfg.Description,
		MaxClustersPerUser: cfg.MaxClustersPerUser,
		CreatorUserName:    "emulator@cloudemu.dev",
		CreatedAt:          m.opts.Clock.Now().UTC().UnixMilli(),
	}
	m.policies.Set(id, policy)

	result := *policy

	return &result, nil
}

// GetClusterPolicy returns a cluster policy by ID.
func (m *Mock) GetClusterPolicy(_ context.Context, policyID string) (*driver.ClusterPolicy, error) {
	policy, ok := m.policies.Get(policyID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "cluster policy %q not found", policyID)
	}

	result := *policy

	return &result, nil
}

// EditClusterPolicy updates a cluster policy's mutable fields.
func (m *Mock) EditClusterPolicy(_ context.Context, policyID string, cfg driver.ClusterPolicyConfig) error {
	policy, ok := m.policies.Get(policyID)
	if !ok {
		return errors.Newf(errors.NotFound, "cluster policy %q not found", policyID)
	}

	updated := *policy
	updated.Name = cfg.Name
	updated.Definition = cfg.Definition
	updated.Description = cfg.Description
	updated.MaxClustersPerUser = cfg.MaxClustersPerUser
	m.policies.Set(policyID, &updated)

	return nil
}

// DeleteClusterPolicy deletes a cluster policy by ID.
func (m *Mock) DeleteClusterPolicy(_ context.Context, policyID string) error {
	if !m.policies.Delete(policyID) {
		return errors.Newf(errors.NotFound, "cluster policy %q not found", policyID)
	}

	return nil
}

// ListClusterPolicies lists all cluster policies.
func (m *Mock) ListClusterPolicies(_ context.Context) ([]driver.ClusterPolicy, error) {
	all := m.policies.All()
	out := make([]driver.ClusterPolicy, 0, len(all))

	for _, p := range all {
		out = append(out, *p)
	}

	return out, nil
}

// --- libraries ---

// InstallLibraries marks libraries installed on a cluster.
func (m *Mock) InstallLibraries(_ context.Context, clusterID string, libs []driver.LibrarySpec) error {
	if !m.clusters.Has(clusterID) {
		return errors.Newf(errors.NotFound, "cluster %q not found", clusterID)
	}

	statuses, _ := m.libraries.Get(clusterID)
	current := cloneStatuses(statuses)

	for i := range libs {
		current = upsertLibrary(current, &libs[i], driver.LibraryInstalled)
	}

	m.libraries.Set(clusterID, current)

	return nil
}

// UninstallLibraries marks libraries for removal on the next restart.
func (m *Mock) UninstallLibraries(_ context.Context, clusterID string, libs []driver.LibrarySpec) error {
	if !m.clusters.Has(clusterID) {
		return errors.Newf(errors.NotFound, "cluster %q not found", clusterID)
	}

	statuses, _ := m.libraries.Get(clusterID)
	current := cloneStatuses(statuses)

	for i := range libs {
		current = upsertLibrary(current, &libs[i], driver.LibraryUninstallOnRestart)
	}

	m.libraries.Set(clusterID, current)

	return nil
}

// ClusterLibraryStatuses returns the library statuses for one cluster.
func (m *Mock) ClusterLibraryStatuses(_ context.Context, clusterID string) ([]driver.LibraryStatus, error) {
	if !m.clusters.Has(clusterID) {
		return nil, errors.Newf(errors.NotFound, "cluster %q not found", clusterID)
	}

	statuses, _ := m.libraries.Get(clusterID)

	return cloneStatuses(statuses), nil
}

// AllClusterLibraryStatuses returns library statuses across all clusters.
func (m *Mock) AllClusterLibraryStatuses(_ context.Context) ([]driver.ClusterLibraryStatuses, error) {
	all := m.libraries.All()
	out := make([]driver.ClusterLibraryStatuses, 0, len(all))

	for clusterID, statuses := range all {
		out = append(out, driver.ClusterLibraryStatuses{ClusterID: clusterID, Statuses: cloneStatuses(statuses)})
	}

	return out, nil
}

// libraryKey returns a stable identity for a library spec.
func libraryKey(l *driver.LibrarySpec) string {
	return l.Jar + "|" + l.Egg + "|" + l.Whl + "|" + l.PypiPackage + "|" + l.MavenCoordinates + "|" + l.Cran
}

func upsertLibrary(list []driver.LibraryStatus, lib *driver.LibrarySpec, status string) []driver.LibraryStatus {
	for i := range list {
		if libraryKey(&list[i].Library) == libraryKey(lib) {
			list[i].Status = status

			return list
		}
	}

	return append(list, driver.LibraryStatus{Library: *lib, Status: status})
}

func cloneStatuses(in []driver.LibraryStatus) []driver.LibraryStatus {
	if in == nil {
		return nil
	}

	out := make([]driver.LibraryStatus, len(in))
	copy(out, in)

	return out
}
