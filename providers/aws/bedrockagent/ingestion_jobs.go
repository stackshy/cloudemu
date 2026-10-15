package bedrockagent

import (
	"cmp"
	"context"
	"slices"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// ListIngestionJobs filter and sort enums from the service model.
const (
	jobFilterStatus  = "STATUS"
	jobFilterEQ      = "EQ"
	jobSortStatus    = "STATUS"
	jobSortStartedAt = "STARTED_AT"
	sortAscending    = "ASCENDING"
	sortDescending   = "DESCENDING"
	maxJobFilters    = 1
)

// ingestionJobStatuses is the IngestionJobStatus enum, for filter values.
//
//nolint:gochecknoglobals // fixed enum table from the service model.
var ingestionJobStatuses = []string{"STARTING", "IN_PROGRESS", "COMPLETE", "FAILED", "STOPPING", "STOPPED"}

// StartIngestionJob starts an ingestion job. Over an S3 data source linked to
// cloudemu S3 it syncs the bucket into the knowledge base. It completes
// immediately, or after a short IN_PROGRESS window when async settling is
// enabled.
func (m *Mock) StartIngestionJob(ctx context.Context, kbID, dsID, description string) (*driver.IngestionJob, error) {
	var v violations

	v.description(description)

	if err := v.err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ds := m.findDataSource(kbID, dsID)
	if ds == nil {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	if running := m.runningJob(kbID, dsID); running != "" {
		return nil, errors.Newf(errors.FailedPrecondition,
			"Ingestion job %s is already running on data source %s. Wait for it to finish or stop it first.", running, dsID)
	}

	id, err := newID(m.jobs)
	if err != nil {
		return nil, err
	}

	now := m.now()
	job := &driver.IngestionJob{
		ID:              id,
		KnowledgeBaseID: kbID,
		DataSourceID:    dsID,
		Description:     description,
		Status:          driver.IngestionJobComplete,
		StartedAt:       now,
		UpdatedAt:       now,
	}

	stats, synced, serr := m.syncS3(ctx, kbID, ds)

	switch {
	case serr != nil:
		job.Status, job.FailureReasons = driver.IngestionJobFailed, []string{errors.Message(serr)}
	case synced:
		job.Statistics = stats
	default:
		job.Statistics = m.ingestionStatistics(kbID, dsID)
	}

	m.jobs.Set(id, job)
	m.jobSettle.Begin(id, driver.IngestionJobInProgress, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultBedrockIngestionSettle))

	result := m.observeJob(job)

	return &result, nil
}

// runningJob returns the id of the data source's ingestion job that is still
// IN_PROGRESS, or "": Bedrock runs one ingestion job per data source at a
// time. Callers hold mu.
func (m *Mock) runningJob(kbID, dsID string) string {
	for _, job := range m.jobs.SortedValues() {
		if job.KnowledgeBaseID == kbID && job.DataSourceID == dsID &&
			m.observeJob(job).Status == driver.IngestionJobInProgress {
			return job.ID
		}
	}

	return ""
}

// GetIngestionJob returns an ingestion job of a data source.
func (m *Mock) GetIngestionJob(_ context.Context, kbID, dsID, jobID string) (*driver.IngestionJob, error) {
	job, err := m.findJob(kbID, dsID, jobID)
	if err != nil {
		return nil, err
	}

	result := m.observeJob(job)

	return &result, nil
}

// ListIngestionJobs lists one page of a data source's ingestion jobs,
// filtered by status and sorted as asked (by start time, oldest first, by
// default).
//
//nolint:gocritic // in matches the driver interface signature; copied once on entry.
func (m *Mock) ListIngestionJobs(_ context.Context, in driver.ListIngestionJobsInput) ([]driver.IngestionJob, string, error) {
	if err := validateJobQuery(&in); err != nil {
		return nil, "", err
	}

	if m.findDataSource(in.KnowledgeBaseID, in.DataSourceID) == nil {
		return nil, "", errors.Newf(errors.NotFound, "data source %q not found", in.DataSourceID)
	}

	var out []driver.IngestionJob

	for _, job := range m.jobs.SortedValues() {
		if job.KnowledgeBaseID != in.KnowledgeBaseID || job.DataSourceID != in.DataSourceID {
			continue
		}

		observed := m.observeJob(job)
		if matchesJobFilters(&observed, in.Filters) {
			out = append(out, observed)
		}
	}

	sortJobs(out, in.SortBy)

	return paginate(out, in.Page)
}

// StopIngestionJob stops a running ingestion job. A job that already finished
// can't be stopped (ConflictException).
func (m *Mock) StopIngestionJob(_ context.Context, kbID, dsID, jobID string) (*driver.IngestionJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, err := m.findJob(kbID, dsID, jobID)
	if err != nil {
		return nil, err
	}

	observed := m.observeJob(job)
	if observed.Status != driver.IngestionJobInProgress {
		return nil, errors.Newf(errors.FailedPrecondition,
			"Ingestion job %s is in status %s and can't be stopped.", jobID, observed.Status)
	}

	stopped := *job
	stopped.Status = driver.IngestionJobStopped
	stopped.UpdatedAt = m.now()
	m.jobs.Set(jobID, &stopped)
	m.jobSettle.Clear(jobID)

	result := stopped

	return &result, nil
}

// findJob resolves a job scoped to its knowledge base and data source.
func (m *Mock) findJob(kbID, dsID, jobID string) (*driver.IngestionJob, error) {
	if m.findDataSource(kbID, dsID) == nil {
		return nil, errors.Newf(errors.NotFound, "data source %q not found", dsID)
	}

	job, ok := m.jobs.Get(jobID)
	if !ok || job.KnowledgeBaseID != kbID || job.DataSourceID != dsID {
		return nil, errors.Newf(errors.NotFound, "ingestion job %q not found", jobID)
	}

	return job, nil
}

// observeJob returns a copy of job with the status a reader sees now.
func (m *Mock) observeJob(job *driver.IngestionJob) driver.IngestionJob {
	out := *job
	out.FailureReasons = slices.Clone(job.FailureReasons)
	out.Status = m.jobSettle.State(job.ID, m.opts.Clock.Now(), job.Status)

	return out
}

// ingestionStatistics counts the documents a job over the data source scans
// and indexes: the documents ingested into it directly.
func (m *Mock) ingestionStatistics(kbID, dsID string) driver.IngestionJobStatistics {
	n := int64(len(childrenOf(m.documents, versionKey(kbID, dsID), func(*storedDocument) struct{} { return struct{}{} })))

	return driver.IngestionJobStatistics{NumberOfDocumentsScanned: n}
}

func validateJobQuery(in *driver.ListIngestionJobsInput) error {
	var v violations

	if len(in.Filters) > maxJobFilters {
		v.addf("Value at 'filters' failed to satisfy constraint: Member must have length less than or equal to %d", maxJobFilters)
	}

	for _, f := range in.Filters {
		v.enum("filters.member.attribute", f.Attribute, jobFilterStatus)
		v.enum("filters.member.operator", f.Operator, jobFilterEQ)

		for _, val := range f.Values {
			v.enum("filters.member.values.member", val, ingestionJobStatuses...)
		}
	}

	if in.SortBy != nil {
		v.enum("sortBy.attribute", in.SortBy.Attribute, jobSortStatus, jobSortStartedAt)
		v.enum("sortBy.order", in.SortBy.Order, sortAscending, sortDescending)
	}

	return v.err()
}

func matchesJobFilters(job *driver.IngestionJob, filters []driver.IngestionJobFilter) bool {
	for _, f := range filters {
		if !slices.Contains(f.Values, job.Status) {
			return false
		}
	}

	return true
}

// sortJobs orders jobs by start time (then id) unless sortBy asks otherwise.
func sortJobs(jobs []driver.IngestionJob, sortBy *driver.IngestionJobSort) {
	key := func(j driver.IngestionJob) string { return j.StartedAt }
	desc := false

	if sortBy != nil {
		if sortBy.Attribute == jobSortStatus {
			key = func(j driver.IngestionJob) string { return j.Status }
		}

		desc = sortBy.Order == sortDescending
	}

	slices.SortStableFunc(jobs, func(a, b driver.IngestionJob) int {
		c := cmp.Or(cmp.Compare(key(a), key(b)), cmp.Compare(a.ID, b.ID))
		if desc {
			return -c
		}

		return c
	})
}
