package batch

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func (h *Handler) createJobQueue(w http.ResponseWriter, r *http.Request) {
	var req createJobQueueRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Priority == nil {
		writeErr(w, cerrors.New(cerrors.InvalidArgument, "priority is required"))

		return
	}

	q, err := h.batch.CreateJobQueue(r.Context(), driver.CreateJobQueueInput{
		Name:                    req.JobQueueName,
		State:                   req.State,
		Priority:                *req.Priority,
		ComputeEnvironmentOrder: ordersToDriver(req.ComputeEnvironmentOrder),
		SchedulingPolicyARN:     req.SchedulingPolicyArn,
		Tags:                    req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, jobQueueNameArnResponse{JobQueueName: q.Name, JobQueueArn: q.ARN})
}

func (h *Handler) describeJobQueues(w http.ResponseWriter, r *http.Request) {
	var req describeJobQueuesRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	queues, err := h.batch.DescribeJobQueues(r.Context(), req.JobQueues)
	if err != nil {
		writeErr(w, err)

		return
	}

	details := make([]jobQueueDetail, 0, len(queues))
	for i := range queues {
		details = append(details, toJQDetail(&queues[i]))
	}

	writeJSON(w, describeJobQueuesResponse{JobQueues: details})
}

func (h *Handler) updateJobQueue(w http.ResponseWriter, r *http.Request) {
	var req updateJobQueueRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	q, err := h.batch.UpdateJobQueue(r.Context(), driver.UpdateJobQueueInput{
		Name:                    req.JobQueue,
		State:                   req.State,
		Priority:                req.Priority,
		ComputeEnvironmentOrder: ordersToDriver(req.ComputeEnvironmentOrder),
		SchedulingPolicyARN:     req.SchedulingPolicyArn,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, jobQueueNameArnResponse{JobQueueName: q.Name, JobQueueArn: q.ARN})
}

func (h *Handler) deleteJobQueue(w http.ResponseWriter, r *http.Request) {
	var req deleteJobQueueRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.batch.DeleteJobQueue(r.Context(), req.JobQueue); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}
