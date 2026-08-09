package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// Import/export jobs complete instantly in the emulator: there is no external
// data source or destination to stream from, so a created job is reported
// COMPLETED. This exercises a caller's create-then-poll flow.

// CreateImportJob creates an import job that completes immediately.
func (m *Mock) CreateImportJob(_ context.Context) (string, error) {
	job := m.newJob(driver.JobStatusCompleted, "IMPORT")
	m.importJobs.Set(job.JobID, job)

	return job.JobID, nil
}

// GetImportJob returns an import job by ID.
func (m *Mock) GetImportJob(_ context.Context, jobID string) (*driver.Job, error) {
	j, ok := m.importJobs.Get(jobID)
	if !ok {
		return nil, errJobNotFound(jobID)
	}

	out := *j

	return &out, nil
}

// ListImportJobs returns all import jobs.
func (m *Mock) ListImportJobs(_ context.Context) ([]driver.Job, error) {
	return jobsCopy(m.importJobs.SortedValues()), nil
}

// CreateExportJob creates an export job that completes immediately.
func (m *Mock) CreateExportJob(_ context.Context) (string, error) {
	job := m.newJob(driver.JobStatusCompleted, "EXPORT")
	m.exportJobs.Set(job.JobID, job)

	return job.JobID, nil
}

// GetExportJob returns an export job by ID.
func (m *Mock) GetExportJob(_ context.Context, jobID string) (*driver.Job, error) {
	j, ok := m.exportJobs.Get(jobID)
	if !ok {
		return nil, errJobNotFound(jobID)
	}

	out := *j

	return &out, nil
}

// ListExportJobs returns all export jobs.
func (m *Mock) ListExportJobs(_ context.Context) ([]driver.Job, error) {
	return jobsCopy(m.exportJobs.SortedValues()), nil
}

// CancelExportJob marks an export job canceled.
func (m *Mock) CancelExportJob(_ context.Context, jobID string) error {
	if !m.exportJobs.Update(jobID, func(j *driver.Job) *driver.Job {
		j.Status = driver.JobStatusCancelled

		return j
	}) {
		return errJobNotFound(jobID)
	}

	return nil
}

func (m *Mock) newJob(status, jobType string) *driver.Job {
	now := m.now()

	return &driver.Job{
		JobID:       idgen.GenerateID(""),
		JobType:     jobType,
		Status:      status,
		CreatedAt:   now,
		CompletedAt: now,
	}
}

func jobsCopy(in []*driver.Job) []driver.Job {
	out := make([]driver.Job, 0, len(in))
	for _, j := range in {
		out = append(out, *j)
	}

	return out
}

func errJobNotFound(jobID string) error {
	return cerrors.Newf(cerrors.NotFound, "job %q does not exist", jobID)
}
