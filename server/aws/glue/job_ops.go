package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

type jobJSON struct {
	Name             string            `json:"Name"`
	Description      string            `json:"Description,omitempty"`
	Role             string            `json:"Role,omitempty"`
	Command          map[string]any    `json:"Command,omitempty"`
	DefaultArguments map[string]string `json:"DefaultArguments,omitempty"`
	MaxRetries       int32             `json:"MaxRetries,omitempty"`
	Timeout          int32             `json:"Timeout,omitempty"`
	GlueVersion      string            `json:"GlueVersion,omitempty"`
	WorkerType       string            `json:"WorkerType,omitempty"`
	NumberOfWorkers  int32             `json:"NumberOfWorkers,omitempty"`
	MaxCapacity      float64           `json:"MaxCapacity,omitempty"`
	CreatedOn        *float64          `json:"CreatedOn,omitempty"`
	LastModifiedOn   *float64          `json:"LastModifiedOn,omitempty"`
}

func jobToWire(j *driver.Job) jobJSON {
	return jobJSON{
		Name: j.Name, Description: j.Description, Role: j.Role, Command: j.Command,
		DefaultArguments: j.DefaultArguments, MaxRetries: j.MaxRetries, Timeout: j.Timeout,
		GlueVersion: j.GlueVersion, WorkerType: j.WorkerType, NumberOfWorkers: j.NumberOfWorkers,
		MaxCapacity: j.MaxCapacity, CreatedOn: epochOrNil(j.CreatedOn), LastModifiedOn: epochOrNil(j.LastModifiedOn),
	}
}

type jobInputJSON struct {
	Description      string            `json:"Description"`
	Role             string            `json:"Role"`
	Command          map[string]any    `json:"Command"`
	DefaultArguments map[string]string `json:"DefaultArguments"`
	MaxRetries       int32             `json:"MaxRetries"`
	Timeout          int32             `json:"Timeout"`
	GlueVersion      string            `json:"GlueVersion"`
	WorkerType       string            `json:"WorkerType"`
	NumberOfWorkers  int32             `json:"NumberOfWorkers"`
	MaxCapacity      float64           `json:"MaxCapacity"`
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func jobFromInput(name string, in jobInputJSON) driver.Job {
	return driver.Job{
		Name: name, Description: in.Description, Role: in.Role, Command: in.Command,
		DefaultArguments: in.DefaultArguments, MaxRetries: in.MaxRetries, Timeout: in.Timeout,
		GlueVersion: in.GlueVersion, WorkerType: in.WorkerType, NumberOfWorkers: in.NumberOfWorkers,
		MaxCapacity: in.MaxCapacity,
	}
}

type createJobRequest struct {
	Name             string            `json:"Name"`
	Description      string            `json:"Description"`
	Role             string            `json:"Role"`
	Command          map[string]any    `json:"Command"`
	DefaultArguments map[string]string `json:"DefaultArguments"`
	MaxRetries       int32             `json:"MaxRetries"`
	Timeout          int32             `json:"Timeout"`
	GlueVersion      string            `json:"GlueVersion"`
	WorkerType       string            `json:"WorkerType"`
	NumberOfWorkers  int32             `json:"NumberOfWorkers"`
	MaxCapacity      float64           `json:"MaxCapacity"`
}

type nameResponse struct {
	Name string `json:"Name"`
}

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createJobRequest) (any, error) {
		j := driver.Job{
			Name: req.Name, Description: req.Description, Role: req.Role, Command: req.Command,
			DefaultArguments: req.DefaultArguments, MaxRetries: req.MaxRetries, Timeout: req.Timeout,
			GlueVersion: req.GlueVersion, WorkerType: req.WorkerType, NumberOfWorkers: req.NumberOfWorkers,
			MaxCapacity: req.MaxCapacity,
		}

		name, err := h.glue.CreateJob(ctx, j)
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

type jobNameRequest struct {
	JobName string `json:"JobName"`
}

type getJobResponse struct {
	Job jobJSON `json:"Job"`
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *jobNameRequest) (any, error) {
		j, err := h.glue.GetJob(ctx, req.JobName)
		if err != nil {
			return nil, err
		}

		return getJobResponse{Job: jobToWire(j)}, nil
	})
}

type updateJobRequest struct {
	JobName   string       `json:"JobName"`
	JobUpdate jobInputJSON `json:"JobUpdate"`
}

type jobNameResponse struct {
	JobName string `json:"JobName"`
}

func (h *Handler) updateJob(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateJobRequest) (any, error) {
		name, err := h.glue.UpdateJob(ctx, req.JobName, jobFromInput(req.JobName, req.JobUpdate))
		if err != nil {
			return nil, err
		}

		return jobNameResponse{JobName: name}, nil
	})
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *jobNameRequest) (any, error) {
		name, err := h.glue.DeleteJob(ctx, req.JobName)
		if err != nil {
			return nil, err
		}

		return jobNameResponse{JobName: name}, nil
	})
}

type getJobsResponse struct {
	Jobs      []jobJSON `json:"Jobs"`
	NextToken string    `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getJobs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		js, next, err := h.glue.GetJobs(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		out := make([]jobJSON, 0, len(js))
		for i := range js {
			out = append(out, jobToWire(&js[i]))
		}

		return getJobsResponse{Jobs: out, NextToken: next}, nil
	})
}

type listJobsResponse struct {
	JobNames  []string `json:"JobNames"`
	NextToken string   `json:"NextToken,omitempty"`
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		names, next, err := h.glue.ListJobs(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		return listJobsResponse{JobNames: names, NextToken: next}, nil
	})
}

type batchGetJobsRequest struct {
	JobNames []string `json:"JobNames"`
}

type batchGetJobsResponse struct {
	Jobs         []jobJSON `json:"Jobs"`
	JobsNotFound []string  `json:"JobsNotFound,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) batchGetJobs(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetJobsRequest) (any, error) {
		found, notFound, err := h.glue.BatchGetJobs(ctx, req.JobNames)
		if err != nil {
			return nil, err
		}

		out := make([]jobJSON, 0, len(found))
		for i := range found {
			out = append(out, jobToWire(&found[i]))
		}

		return batchGetJobsResponse{Jobs: out, JobsNotFound: notFound}, nil
	})
}

type startJobRunRequest struct {
	JobName   string            `json:"JobName"`
	Arguments map[string]string `json:"Arguments"`
}

type startJobRunResponse struct {
	JobRunID string `json:"JobRunId"`
}

func (h *Handler) startJobRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startJobRunRequest) (any, error) {
		id, err := h.glue.StartJobRun(ctx, req.JobName, req.Arguments)
		if err != nil {
			return nil, err
		}

		return startJobRunResponse{JobRunID: id}, nil
	})
}

type jobRunJSON struct {
	ID              string            `json:"Id"`
	JobName         string            `json:"JobName,omitempty"`
	Attempt         int32             `json:"Attempt,omitempty"`
	StartedOn       *float64          `json:"StartedOn,omitempty"`
	CompletedOn     *float64          `json:"CompletedOn,omitempty"`
	JobRunState     string            `json:"JobRunState,omitempty"`
	Arguments       map[string]string `json:"Arguments,omitempty"`
	ErrorMessage    string            `json:"ErrorMessage,omitempty"`
	ExecutionTime   int32             `json:"ExecutionTime,omitempty"`
	Timeout         int32             `json:"Timeout,omitempty"`
	WorkerType      string            `json:"WorkerType,omitempty"`
	NumberOfWorkers int32             `json:"NumberOfWorkers,omitempty"`
}

func jobRunToWire(jr *driver.JobRun) jobRunJSON {
	return jobRunJSON{
		ID: jr.ID, JobName: jr.JobName, Attempt: jr.Attempt, StartedOn: epochOrNil(jr.StartedOn),
		CompletedOn: epochOrNil(jr.CompletedOn), JobRunState: jr.JobRunState, Arguments: jr.Arguments,
		ErrorMessage: jr.ErrorMessage, ExecutionTime: jr.ExecutionTime, Timeout: jr.Timeout,
		WorkerType: jr.WorkerType, NumberOfWorkers: jr.NumberOfWorkers,
	}
}

type getJobRunRequest struct {
	JobName string `json:"JobName"`
	RunID   string `json:"RunId"`
}

type getJobRunResponse struct {
	JobRun jobRunJSON `json:"JobRun"`
}

func (h *Handler) getJobRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getJobRunRequest) (any, error) {
		jr, err := h.glue.GetJobRun(ctx, req.JobName, req.RunID)
		if err != nil {
			return nil, err
		}

		return getJobRunResponse{JobRun: jobRunToWire(jr)}, nil
	})
}

type getJobRunsRequest struct {
	JobName    string `json:"JobName"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type getJobRunsResponse struct {
	JobRuns   []jobRunJSON `json:"JobRuns"`
	NextToken string       `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getJobRuns(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getJobRunsRequest) (any, error) {
		runs, next, err := h.glue.GetJobRuns(ctx, req.JobName,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]jobRunJSON, 0, len(runs))
		for i := range runs {
			out = append(out, jobRunToWire(&runs[i]))
		}

		return getJobRunsResponse{JobRuns: out, NextToken: next}, nil
	})
}

type batchStopJobRunRequest struct {
	JobName   string   `json:"JobName"`
	JobRunIDs []string `json:"JobRunIds"`
}

type batchStopJobRunSuccessJSON struct {
	JobName  string `json:"JobName,omitempty"`
	JobRunID string `json:"JobRunId,omitempty"`
}

type batchStopJobRunErrorJSON struct {
	JobName     string           `json:"JobName,omitempty"`
	JobRunID    string           `json:"JobRunId,omitempty"`
	ErrorDetail *errorDetailJSON `json:"ErrorDetail,omitempty"`
}

type batchStopJobRunResponse struct {
	SuccessfulSubmissions []batchStopJobRunSuccessJSON `json:"SuccessfulSubmissions,omitempty"`
	Errors                []batchStopJobRunErrorJSON   `json:"Errors,omitempty"`
}

func (h *Handler) batchStopJobRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchStopJobRunRequest) (any, error) {
		ok, failed := h.glue.BatchStopJobRun(ctx, req.JobName, req.JobRunIDs)

		succ := make([]batchStopJobRunSuccessJSON, 0, len(ok))
		for _, id := range ok {
			succ = append(succ, batchStopJobRunSuccessJSON{JobName: req.JobName, JobRunID: id})
		}

		errs := make([]batchStopJobRunErrorJSON, 0, len(failed))
		for i := range failed {
			errs = append(errs, batchStopJobRunErrorJSON{
				JobName: req.JobName, JobRunID: failed[i].Name,
				ErrorDetail: &errorDetailJSON{
					ErrorCode: failed[i].ErrorCode, ErrorMessage: failed[i].ErrorMessage,
				},
			})
		}

		return batchStopJobRunResponse{SuccessfulSubmissions: succ, Errors: errs}, nil
	})
}
