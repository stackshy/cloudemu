package batch

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func (h *Handler) submitJob(w http.ResponseWriter, r *http.Request) {
	var req submitJobRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := driver.SubmitJobInput{
		JobName:                    req.JobName,
		JobQueue:                   req.JobQueue,
		JobDefinition:              req.JobDefinition,
		DependsOn:                  depsToDriver(req.DependsOn),
		Parameters:                 req.Parameters,
		ContainerOverrides:         req.ContainerOverrides,
		NodeOverrides:              req.NodeOverrides,
		EcsPropertiesOverride:      req.EcsPropertiesOverride,
		EksPropertiesOverride:      req.EksPropertiesOverride,
		RetryStrategy:              req.RetryStrategy,
		Timeout:                    req.Timeout,
		PropagateTags:              req.PropagateTags,
		SchedulingPriorityOverride: req.SchedulingPriorityOverride,
		ShareIdentifier:            req.ShareIdentifier,
		Tags:                       req.Tags,
	}

	if req.ArrayProperties != nil && req.ArrayProperties.Size != nil {
		in.ArraySize = req.ArrayProperties.Size
	}

	job, err := h.batch.SubmitJob(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, submitJobResponse{JobArn: job.JobARN, JobID: job.JobID, JobName: job.JobName})
}

func (h *Handler) describeJobs(w http.ResponseWriter, r *http.Request) {
	var req describeJobsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	jobs, err := h.batch.DescribeJobs(r.Context(), req.Jobs)
	if err != nil {
		writeErr(w, err)

		return
	}

	details := make([]jobDetail, 0, len(jobs))
	for i := range jobs {
		details = append(details, toJobDetail(&jobs[i]))
	}

	writeJSON(w, describeJobsResponse{Jobs: details})
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	var req listJobsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	page, err := h.batch.ListJobs(r.Context(), driver.ListJobsInput{
		JobQueue:       req.JobQueue,
		ArrayJobID:     req.ArrayJobID,
		MultiNodeJobID: req.MultiNodeJobID,
		JobStatus:      req.JobStatus,
		Filters:        filtersToDriver(req.Filters),
		MaxResults:     req.MaxResults,
		NextToken:      req.NextToken,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]jobSummary, 0, len(page.Jobs))
	for i := range page.Jobs {
		out = append(out, toJobSummary(&page.Jobs[i]))
	}

	writeJSON(w, listJobsResponse{JobSummaryList: out, NextToken: page.NextToken})
}

func (h *Handler) cancelJob(w http.ResponseWriter, r *http.Request) {
	h.stopJob(w, r, h.batch.CancelJob)
}

func (h *Handler) terminateJob(w http.ResponseWriter, r *http.Request) {
	h.stopJob(w, r, h.batch.TerminateJob)
}

func (*Handler) stopJob(w http.ResponseWriter, r *http.Request, stop func(ctx context.Context, id, reason string) error) {
	var req stopJobRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := stop(r.Context(), req.JobID, req.Reason); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}
