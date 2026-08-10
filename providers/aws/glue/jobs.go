package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// jobData is a job definition plus its own lock.
type jobData struct {
	job driver.Job
	mu  sync.RWMutex
}

// jobRunData is a single job run plus its own lock. Runs are keyed
// "<jobName>/<runID>" so a job's runs share a stable prefix.
type jobRunData struct {
	run driver.JobRun
	mu  sync.RWMutex
}

// CreateJob creates an ETL job definition, atomically, returning its name.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateJob(_ context.Context, j driver.Job) (string, error) {
	if !validName(j.Name) {
		return "", invalidInput("job name %q is invalid", j.Name)
	}

	now := m.now()
	j.CreatedOn = now
	j.LastModifiedOn = now
	stored := copyJob(j)

	if !m.jobs.SetIfAbsent(j.Name, &jobData{job: stored}) {
		return "", alreadyExists("Job already exists: %s", j.Name)
	}

	return j.Name, nil
}

func (m *Mock) getJobData(name string) (*jobData, error) {
	if !validName(name) {
		return nil, invalidInput("job name %q is invalid", name)
	}

	jd, ok := m.jobs.Get(name)
	if !ok {
		return nil, entityNotFound("Job not found: %s", name)
	}

	return jd, nil
}

// GetJob returns a deep copy of a job definition.
func (m *Mock) GetJob(_ context.Context, name string) (*driver.Job, error) {
	jd, err := m.getJobData(name)
	if err != nil {
		return nil, err
	}

	jd.mu.RLock()
	defer jd.mu.RUnlock()

	out := copyJob(jd.job)

	return &out, nil
}

// UpdateJob replaces a job's mutable fields, returning its name.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) UpdateJob(_ context.Context, name string, j driver.Job) (string, error) {
	jd, err := m.getJobData(name)
	if err != nil {
		return "", err
	}

	jd.mu.Lock()
	defer jd.mu.Unlock()

	created := jd.job.CreatedOn
	jd.job = copyJob(j)
	jd.job.Name = name
	jd.job.CreatedOn = created
	jd.job.LastModifiedOn = m.now()

	return name, nil
}

// DeleteJob removes a job and its runs, returning its name.
func (m *Mock) DeleteJob(_ context.Context, name string) (string, error) {
	if _, err := m.getJobData(name); err != nil {
		return "", err
	}

	m.jobs.Delete(name)

	prefix := name + keySep
	for _, key := range m.jobRuns.Keys() {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			m.jobRuns.Delete(key)
		}
	}

	return name, nil
}

// GetJobs lists job definitions with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetJobs(_ context.Context, page driver.TablePagination) ([]driver.Job, string, error) {
	keys := sortedKeys(m.jobs.Keys())
	all := make([]driver.Job, 0, len(keys))

	for _, key := range keys {
		jd, ok := m.jobs.Get(key)
		if !ok {
			continue
		}

		jd.mu.RLock()
		all = append(all, copyJob(jd.job))
		jd.mu.RUnlock()
	}

	return paginate(all, page)
}

// ListJobs returns job names with pagination.
//
//nolint:gocritic // unnamedResult: thin pass-through to paginate; names add no clarity
func (m *Mock) ListJobs(_ context.Context, page driver.TablePagination) ([]string, string, error) {
	return paginate(sortedKeys(m.jobs.Keys()), page)
}

// BatchGetJobs returns the found jobs and the names that did not exist.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) BatchGetJobs(_ context.Context, names []string) ([]driver.Job, []string, error) {
	if len(names) > maxBatchGet {
		return nil, nil, invalidInput("cannot request more than %d jobs", maxBatchGet)
	}

	found := make([]driver.Job, 0, len(names))

	var notFound []string

	for _, n := range names {
		j, err := m.GetJob(context.Background(), n)
		if err != nil {
			notFound = append(notFound, n)

			continue
		}

		found = append(found, *j)
	}

	return found, notFound, nil
}

// StartJobRun starts a run of a job. There is no real Spark compute plane, so
// the run completes SUCCEEDED synchronously and its ID is returned immediately.
// This is a deliberate simplification documented in docs/services.md.
func (m *Mock) StartJobRun(_ context.Context, jobName string, args map[string]string) (string, error) {
	jd, err := m.getJobData(jobName)
	if err != nil {
		return "", err
	}

	jd.mu.RLock()
	timeout := jd.job.Timeout
	workerType := jd.job.WorkerType
	numWorkers := jd.job.NumberOfWorkers
	jd.mu.RUnlock()

	now := m.now()
	runID := idgen.GenerateID("jr_")
	run := driver.JobRun{
		ID:              runID,
		JobName:         jobName,
		Attempt:         0,
		StartedOn:       now,
		CompletedOn:     now,
		JobRunState:     driver.JobRunSucceeded,
		ExecutionTime:   1, // synthesized: runs settle immediately, report a minimal 1s
		Arguments:       copyTags(args),
		Timeout:         timeout,
		WorkerType:      workerType,
		NumberOfWorkers: numWorkers,
	}

	m.jobRuns.Set(nameKey(jobName, runID), &jobRunData{run: run})

	return runID, nil
}

// GetJobRun returns a deep copy of a job run.
func (m *Mock) GetJobRun(_ context.Context, jobName, runID string) (*driver.JobRun, error) {
	rd, ok := m.jobRuns.Get(nameKey(jobName, runID))
	if !ok {
		return nil, entityNotFound("JobRun not found: %s", runID)
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	out := copyJobRun(rd.run)

	return &out, nil
}

// GetJobRuns lists a job's runs with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetJobRuns(
	_ context.Context, jobName string, page driver.TablePagination,
) ([]driver.JobRun, string, error) {
	if _, err := m.getJobData(jobName); err != nil {
		return nil, "", err
	}

	prefix := jobName + keySep
	keys := sortedKeys(m.jobRuns.Keys())
	all := make([]driver.JobRun, 0, len(keys))

	for _, key := range keys {
		if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
			continue
		}

		rd, ok := m.jobRuns.Get(key)
		if !ok {
			continue
		}

		rd.mu.RLock()
		all = append(all, copyJobRun(rd.run))
		rd.mu.RUnlock()
	}

	return paginate(all, page)
}

// isTerminalJobRun reports whether a run has reached a terminal state and so
// cannot be stopped.
func isTerminalJobRun(state string) bool {
	switch state {
	case driver.JobRunSucceeded, driver.JobRunFailed, driver.JobRunStopped:
		return true
	default:
		return false
	}
}

// BatchStopJobRun attempts to stop runs. Runs complete synchronously, so an
// existing run is already terminal and cannot be stopped: it belongs under
// Errors (not stoppable), not SuccessfulSubmissions. Unknown IDs likewise error.
// Returns the successful run IDs and the per-run errors.
func (m *Mock) BatchStopJobRun(
	_ context.Context, jobName string, runIDs []string,
) (successful []string, errored []driver.BatchError) {
	for _, id := range runIDs {
		rd, ok := m.jobRuns.Get(nameKey(jobName, id))
		if !ok {
			errored = append(errored, driver.BatchError{
				Name: id, ErrorCode: driver.ExEntityNotFound, ErrorMessage: "JobRun not found",
			})

			continue
		}

		rd.mu.RLock()
		terminal := isTerminalJobRun(rd.run.JobRunState)
		rd.mu.RUnlock()

		if terminal {
			errored = append(errored, driver.BatchError{
				Name:         id,
				ErrorCode:    driver.ExInvalidInput,
				ErrorMessage: "JobRun is not in a stoppable state",
			})

			continue
		}

		rd.mu.Lock()
		rd.run.JobRunState = driver.JobRunStopped
		rd.mu.Unlock()

		successful = append(successful, id)
	}

	return successful, errored
}
