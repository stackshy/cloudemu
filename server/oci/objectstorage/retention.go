package objectstorage

import (
	"net/http"
	"time"

	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveRetentionRules routes /retentionRules and /retentionRules/{ruleId}.
func (h *Handler) serveRetentionRules(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.Rest == "" {
		switch r.Method {
		case http.MethodPost:
			h.createRetentionRule(w, r, rt.Bucket)
		case http.MethodGet:
			h.listRetentionRules(w, r, rt.Bucket)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getRetentionRule(w, r, rt.Bucket, rt.Rest)
	case http.MethodPost:
		h.updateRetentionRule(w, r, rt.Bucket, rt.Rest)
	case http.MethodDelete:
		h.deleteRetentionRule(w, r, rt.Bucket, rt.Rest)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) createRetentionRule(w http.ResponseWriter, r *http.Request, bucket string) {
	spec, ok := decodeRule(w, r)
	if !ok {
		return
	}

	rule, err := h.extras.CreateRetentionRule(r.Context(), bucket, spec)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	w.Header().Set("ETag", rule.ETag)
	ocirest.WriteJSON(w, r, http.StatusOK, toRuleBody(rule))
}

func (h *Handler) updateRetentionRule(w http.ResponseWriter, r *http.Request, bucket, ruleID string) {
	spec, ok := decodeRule(w, r)
	if !ok {
		return
	}

	rule, err := h.extras.UpdateRetentionRule(r.Context(), bucket, ruleID, spec)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	w.Header().Set("ETag", rule.ETag)
	ocirest.WriteJSON(w, r, http.StatusOK, toRuleBody(rule))
}

func (h *Handler) getRetentionRule(w http.ResponseWriter, r *http.Request, bucket, ruleID string) {
	rule, err := h.extras.GetRetentionRule(r.Context(), bucket, ruleID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toRuleBody(rule))
}

func (h *Handler) listRetentionRules(w http.ResponseWriter, r *http.Request, bucket string) {
	rules, err := h.extras.ListRetentionRules(r.Context(), bucket)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := retentionRuleListBody{Items: make([]retentionRuleBody, 0, len(rules))}
	for i := range rules {
		out.Items = append(out.Items, toRuleBody(&rules[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) deleteRetentionRule(w http.ResponseWriter, r *http.Request, bucket, ruleID string) {
	if err := h.extras.DeleteRetentionRule(r.Context(), bucket, ruleID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func decodeRule(w http.ResponseWriter, r *http.Request) (osprovider.RetentionRuleSpec, bool) {
	var req retentionRuleRequestBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return osprovider.RetentionRuleSpec{}, false
	}

	spec := osprovider.RetentionRuleSpec{DisplayName: req.DisplayName}

	if req.Duration != nil {
		spec.Duration = &osprovider.RetentionDuration{
			TimeAmount: req.Duration.TimeAmount,
			TimeUnit:   req.Duration.TimeUnit,
		}
	}

	if req.TimeRuleLocked != "" {
		locked, err := time.Parse(time.RFC3339, req.TimeRuleLocked)
		if err != nil {
			ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
				"timeRuleLocked must be an RFC3339 timestamp: "+err.Error())

			return osprovider.RetentionRuleSpec{}, false
		}

		spec.TimeRuleLocked = &locked
	}

	return spec, true
}

func toRuleBody(rule *osprovider.RetentionRule) retentionRuleBody {
	out := retentionRuleBody{
		ID:             rule.ID,
		DisplayName:    rule.DisplayName,
		TimeRuleLocked: rule.TimeRuleLocked,
		TimeCreated:    rule.TimeCreated,
		TimeModified:   rule.TimeModified,
		ETag:           rule.ETag,
	}

	if rule.Duration != nil {
		out.Duration = &retentionDurationBody{
			TimeAmount: rule.Duration.TimeAmount,
			TimeUnit:   rule.Duration.TimeUnit,
		}
	}

	return out
}
