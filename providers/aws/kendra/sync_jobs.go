package kendra

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// syncJobRecord is a stored data source sync job. The job is keyed
// "indexId/dataSourceId/executionId".
type syncJobRecord struct {
	IndexID      string
	DataSourceID string
	Seq          uint64 // start order, so same-second jobs list deterministically
	Job          driver.SyncJob
}

func syncKey(dsKey, executionID string) string { return dsKey + "/" + executionID }

// jobStatus is a job's status as observed now: under async settling a running
// job reports SYNCING until its window elapses, then SUCCEEDED.
func (m *Mock) jobStatus(key string, j *driver.SyncJob) string {
	if j.Status == driver.SyncStatusAborted {
		return j.Status
	}

	return m.settleStatus(key, j.Status)
}

// syncRunning reports whether the data source (by store key) has a job in
// progress.
func (m *Mock) syncRunning(dsKey string) bool {
	for _, k := range m.syncJobs.Keys() {
		rec, ok := m.syncJobs.Get(k)
		if !ok || syncKeyPrefix(k) != dsKey {
			continue
		}

		if m.jobStatus(k, &rec.Job) == driver.SyncStatusSyncing {
			return true
		}
	}

	return false
}

// syncKeyPrefix strips the trailing "/executionId" from a sync job key.
func syncKeyPrefix(k string) string {
	for i := len(k) - 1; i >= 0; i-- {
		if k[i] == '/' {
			return k[:i]
		}
	}

	return k
}

// StartDataSourceSyncJob starts a synchronization run of a data source and
// returns its execution id. The run completes at once (SUCCEEDED with zero
// counters: the emulator has no external repository to crawl); under async
// settling it reports SYNCING until the settle window elapses. Starting a job
// while one is in progress is a ResourceInUseException.
func (m *Mock) StartDataSourceSyncJob(_ context.Context, indexID, dataSourceID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(indexID); err != nil {
		return "", err
	}

	if err := m.requireActiveDataSource(indexID, dataSourceID); err != nil {
		return "", err
	}

	dsKey := dataSourceKey(indexID, dataSourceID)
	if m.syncRunning(dsKey) {
		return "", resourceInUse("data source %q already has a synchronization job in progress", dataSourceID)
	}

	execID := newUUID()
	now := m.now()
	key := syncKey(dsKey, execID)

	m.syncSeq++

	m.syncJobs.Set(key, syncJobRecord{
		IndexID: indexID, DataSourceID: dataSourceID, Seq: m.syncSeq,
		Job: driver.SyncJob{
			ExecutionID: execID, StartTime: now, EndTime: now, Status: driver.SyncStatusSucceeded,
			Metrics: zeroMetrics(),
		},
	})
	m.beginSettle(key, driver.SyncStatusSyncing)

	return execID, nil
}

func zeroMetrics() driver.SyncJobMetrics {
	return driver.SyncJobMetrics{
		DocumentsAdded: "0", DocumentsDeleted: "0", DocumentsFailed: "0", DocumentsModified: "0", DocumentsScanned: "0",
	}
}

// StopDataSourceSyncJob stops the data source's running synchronization job. With
// no job running it does nothing, as in the real service.
func (m *Mock) StopDataSourceSyncJob(_ context.Context, indexID, dataSourceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.getDataSource(indexID, dataSourceID); err != nil {
		return err
	}

	dsKey := dataSourceKey(indexID, dataSourceID)

	for _, k := range m.syncJobs.Keys() {
		rec, ok := m.syncJobs.Get(k)
		if !ok || syncKeyPrefix(k) != dsKey || m.jobStatus(k, &rec.Job) != driver.SyncStatusSyncing {
			continue
		}

		rec.Job.Status = driver.SyncStatusAborted
		rec.Job.EndTime = m.now()
		m.syncJobs.Set(k, rec)
		m.settling.Clear(k)
	}

	return nil
}

// addDeletedMetric credits the documents a BatchDeleteDocument removed to the
// data source sync job it names.
func (m *Mock) addDeletedMetric(indexID string, t *driver.SyncJobMetricTarget, deleted int) {
	m.recordDeletionMetric(indexID, t.DataSourceID, deleted)

	key := syncKey(dataSourceKey(indexID, t.DataSourceID), t.DataSourceSyncJobID)

	m.syncJobs.Update(key, func(r syncJobRecord) syncJobRecord {
		n, _ := strconv.Atoi(r.Job.Metrics.DocumentsDeleted)
		r.Job.Metrics.DocumentsDeleted = strconv.Itoa(n + deleted)

		return r
	})
}

// ListDataSourceSyncJobs returns a data source's synchronization history, newest
// first, optionally narrowed by start time range and status.
func (m *Mock) ListDataSourceSyncJobs(
	_ context.Context, in *driver.ListSyncJobsInput,
) (jobs []driver.SyncJob, nextToken string, err error) {
	if _, err = m.getDataSource(in.IndexID, in.DataSourceID); err != nil {
		return nil, "", err
	}

	if f := in.StatusFilter; f != "" && !validSyncStatus[f] {
		return nil, "", validation("invalid StatusFilter: %q", f)
	}

	dsKey := dataSourceKey(in.IndexID, in.DataSourceID)
	records := []syncJobRecord{}

	for _, k := range m.syncJobs.Keys() {
		rec, ok := m.syncJobs.Get(k)
		if !ok || syncKeyPrefix(k) != dsKey {
			continue
		}

		rec.Job.Status = m.jobStatus(k, &rec.Job)

		if syncJobMatches(&rec.Job, in) {
			records = append(records, rec)
		}
	}

	sort.Slice(records, func(i, j int) bool { return records[i].Seq > records[j].Seq })

	matched := make([]driver.SyncJob, len(records))
	for i := range records {
		matched[i] = records[i].Job
	}

	start, end, next, err := m.paginate("syncjobs/"+dsKey, len(matched), in.Page, maxSyncPageSize)
	if err != nil {
		return nil, "", err
	}

	return matched[start:end], next, nil
}

// validSyncStatus is the StatusFilter value set.
//
//nolint:gochecknoglobals // static validation set
var validSyncStatus = map[string]bool{
	"FAILED": true, "SUCCEEDED": true, "SYNCING": true, "INCOMPLETE": true,
	"STOPPING": true, "ABORTED": true, "SYNCING_INDEXING": true,
}

func syncJobMatches(j *driver.SyncJob, in *driver.ListSyncJobsInput) bool {
	if in.StatusFilter != "" && j.Status != in.StatusFilter {
		return false
	}

	return inTimeRange(j.StartTime, in.StartTime, in.EndTime)
}

func inTimeRange(t time.Time, from, to *time.Time) bool {
	if from != nil && t.Before(*from) {
		return false
	}

	return to == nil || !t.After(*to)
}
