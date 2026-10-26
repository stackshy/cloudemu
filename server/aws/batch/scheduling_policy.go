package batch

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func (h *Handler) createSchedulingPolicy(w http.ResponseWriter, r *http.Request) {
	var req createSchedulingPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	p, err := h.batch.CreateSchedulingPolicy(r.Context(), req.toInput())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, schedulingPolicyNameArnResponse{Name: p.Name, Arn: p.ARN})
}

func (h *Handler) describeSchedulingPolicies(w http.ResponseWriter, r *http.Request) {
	var req describeSchedulingPoliciesRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	policies, err := h.batch.DescribeSchedulingPolicies(r.Context(), req.Arns)
	if err != nil {
		writeErr(w, err)

		return
	}

	details := make([]schedulingPolicyDetail, 0, len(policies))
	for i := range policies {
		details = append(details, toSPDetail(&policies[i]))
	}

	writeJSON(w, describeSchedulingPoliciesResponse{SchedulingPolicies: details})
}

func (h *Handler) listSchedulingPolicies(w http.ResponseWriter, r *http.Request) {
	var req listSchedulingPoliciesRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	policies, err := h.batch.ListSchedulingPolicies(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	page, next, ok := paginate(w, policies, req.MaxResults, req.NextToken)
	if !ok {
		return
	}

	out := make([]schedulingPolicyListing, 0, len(page))
	for i := range page {
		out = append(out, schedulingPolicyListing{Arn: page[i].ARN})
	}

	writeJSON(w, listSchedulingPoliciesResponse{SchedulingPolicies: out, NextToken: next})
}

func (h *Handler) updateSchedulingPolicy(w http.ResponseWriter, r *http.Request) {
	var req updateSchedulingPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.batch.UpdateSchedulingPolicy(r.Context(), driver.UpdateSchedulingPolicyInput{
		ARN:              req.Arn,
		FairsharePolicy:  req.FairsharePolicy,
		QuotaSharePolicy: req.QuotaSharePolicy,
	}); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}

func (h *Handler) deleteSchedulingPolicy(w http.ResponseWriter, r *http.Request) {
	var req deleteSchedulingPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.batch.DeleteSchedulingPolicy(r.Context(), req.Arn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, emptyResponse{})
}
