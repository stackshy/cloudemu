package kendra

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerSyncRoutes wires the data source synchronization operations.
func (h *Handler) registerSyncRoutes(d driver.SyncJobs) {
	h.routes["StartDataSourceSyncJob"] = handle(h, func(ctx context.Context, req *syncJobTargetRequest) (executionResponse, error) {
		id, err := d.StartDataSourceSyncJob(ctx, req.IndexID, req.ID)

		return executionResponse{ExecutionID: id}, err
	})
	h.routes["StopDataSourceSyncJob"] = handle(h, func(ctx context.Context, req *syncJobTargetRequest) (struct{}, error) {
		return ack(d.StopDataSourceSyncJob(ctx, req.IndexID, req.ID))
	})
	h.routes["ListDataSourceSyncJobs"] = handle(h, func(ctx context.Context, req *listSyncJobsRequest) (listSyncJobsResponse, error) {
		return listSyncJobs(ctx, d, req)
	})
}

type syncJobTargetRequest struct {
	ID      string `json:"Id"`
	IndexID string `json:"IndexId"`
}

type executionResponse struct {
	ExecutionID string `json:"ExecutionId"`
}

type listSyncJobsRequest struct {
	ID              string `json:"Id"`
	IndexID         string `json:"IndexId"`
	MaxResults      int32  `json:"MaxResults"`
	NextToken       string `json:"NextToken"`
	StatusFilter    string `json:"StatusFilter"`
	StartTimeFilter *struct {
		StartTime *float64 `json:"StartTime"`
		EndTime   *float64 `json:"EndTime"`
	} `json:"StartTimeFilter"`
}

type syncMetricsJSON struct {
	DocumentsAdded    string `json:"DocumentsAdded"`
	DocumentsDeleted  string `json:"DocumentsDeleted"`
	DocumentsFailed   string `json:"DocumentsFailed"`
	DocumentsModified string `json:"DocumentsModified"`
	DocumentsScanned  string `json:"DocumentsScanned"`
}

type syncJobJSON struct {
	ExecutionID         string          `json:"ExecutionId"`
	StartTime           int64           `json:"StartTime"`
	EndTime             *int64          `json:"EndTime,omitempty"`
	Status              string          `json:"Status"`
	ErrorMessage        string          `json:"ErrorMessage,omitempty"`
	ErrorCode           string          `json:"ErrorCode,omitempty"`
	DataSourceErrorCode string          `json:"DataSourceErrorCode,omitempty"`
	Metrics             syncMetricsJSON `json:"Metrics"`
}

type listSyncJobsResponse struct {
	History   []syncJobJSON `json:"History"`
	NextToken string        `json:"NextToken,omitempty"`
}

// optionalEpoch is the epoch seconds of t, or nil for the zero time (a job that
// has not ended has no EndTime).
func optionalEpoch(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}

	n := epochSeconds(t)

	return &n
}

func epochToTime(f *float64) *time.Time {
	if f == nil {
		return nil
	}

	t := time.Unix(0, int64(*f*nanosPerSecond)).UTC()

	return &t
}

func listSyncJobs(ctx context.Context, d driver.SyncJobs, req *listSyncJobsRequest) (listSyncJobsResponse, error) {
	in := &driver.ListSyncJobsInput{
		IndexID: req.IndexID, DataSourceID: req.ID, StatusFilter: req.StatusFilter,
		Page: driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults},
	}

	if f := req.StartTimeFilter; f != nil {
		in.StartTime = epochToTime(f.StartTime)
		in.EndTime = epochToTime(f.EndTime)
	}

	jobs, next, err := d.ListDataSourceSyncJobs(ctx, in)
	if err != nil {
		return listSyncJobsResponse{}, err
	}

	out := listSyncJobsResponse{History: make([]syncJobJSON, len(jobs)), NextToken: next}

	for i := range jobs {
		j := &jobs[i]
		out.History[i] = syncJobJSON{
			ExecutionID: j.ExecutionID, StartTime: epochSeconds(j.StartTime), EndTime: optionalEpoch(j.EndTime),
			Status: j.Status, ErrorMessage: j.ErrorMessage, ErrorCode: j.ErrorCode, DataSourceErrorCode: j.DataSourceErrorCode,
			Metrics: syncMetricsJSON(j.Metrics),
		}
	}

	return out, nil
}
