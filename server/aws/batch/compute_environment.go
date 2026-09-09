package batch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func (h *Handler) createComputeEnvironment(w http.ResponseWriter, r *http.Request) {
	var req createComputeEnvironmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ce, err := h.batch.CreateComputeEnvironment(r.Context(), driver.CreateComputeEnvironmentInput{
		Name:             req.ComputeEnvironmentName,
		Type:             req.Type,
		State:            req.State,
		ComputeResources: req.ComputeResources,
		ServiceRole:      req.ServiceRole,
		UnmanagedvCpus:   req.UnmanagedvCpus,
		Tags:             req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, computeEnvironmentNameArnResponse{
		ComputeEnvironmentName: ce.Name,
		ComputeEnvironmentArn:  ce.ARN,
	})
}

func (h *Handler) describeComputeEnvironments(w http.ResponseWriter, r *http.Request) {
	var req describeComputeEnvironmentsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ces, err := h.batch.DescribeComputeEnvironments(r.Context(), req.ComputeEnvironments)
	if err != nil {
		writeErr(w, err)

		return
	}

	details := make([]computeEnvironmentDetail, 0, len(ces))
	for i := range ces {
		details = append(details, toCEDetail(&ces[i]))
	}

	writeJSON(w, describeComputeEnvironmentsResponse{ComputeEnvironments: details})
}

func (h *Handler) updateComputeEnvironment(w http.ResponseWriter, r *http.Request) {
	var req updateComputeEnvironmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	ce, err := h.batch.UpdateComputeEnvironment(r.Context(), driver.UpdateComputeEnvironmentInput{
		Name:             req.ComputeEnvironment,
		State:            req.State,
		ServiceRole:      req.ServiceRole,
		ComputeResources: req.ComputeResources,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, computeEnvironmentNameArnResponse{
		ComputeEnvironmentName: ce.Name,
		ComputeEnvironmentArn:  ce.ARN,
	})
}

func (h *Handler) deleteComputeEnvironment(w http.ResponseWriter, r *http.Request) {
	var req deleteComputeEnvironmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.batch.DeleteComputeEnvironment(r.Context(), req.ComputeEnvironment); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}
