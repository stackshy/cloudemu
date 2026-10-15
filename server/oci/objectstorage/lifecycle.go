package objectstorage

import (
	"net/http"

	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveLifecycle routes the object lifecycle policy at /l.
func (h *Handler) serveLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		h.putLifecycle(w, r, bucket)
	case http.MethodGet:
		h.getLifecycle(w, r, bucket)
	case http.MethodDelete:
		h.deleteLifecycle(w, r, bucket)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) putLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	var req lifecycleBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	rules := make([]osprovider.LifecycleRule, 0, len(req.Items))
	for i := range req.Items {
		rules = append(rules, toLifecycleRule(&req.Items[i]))
	}

	policy, err := h.extras.PutLifecyclePolicy(r.Context(), bucket, rules)
	if err != nil {
		writeDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toLifecycleBody(policy))
}

func (h *Handler) getLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	policy, err := h.extras.GetLifecyclePolicy(r.Context(), bucket)
	if err != nil {
		writeDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toLifecycleBody(policy))
}

func (h *Handler) deleteLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := h.extras.DeleteLifecyclePolicy(r.Context(), bucket); err != nil {
		writeDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func toLifecycleRule(item *lifecycleRuleBody) osprovider.LifecycleRule {
	rule := osprovider.LifecycleRule{
		Name:       item.Name,
		Action:     item.Action,
		TimeAmount: item.TimeAmount,
		TimeUnit:   item.TimeUnit,
		Target:     item.Target,
		IsEnabled:  item.IsEnabled,
	}

	if f := item.ObjectNameFilter; f != nil {
		rule.InclusionPrefixes = f.InclusionPrefixes
		rule.InclusionPatterns = f.InclusionPatterns
		rule.ExclusionPatterns = f.ExclusionPatterns
	}

	return rule
}

func toLifecycleBody(policy *osprovider.LifecyclePolicy) lifecycleBody {
	out := lifecycleBody{TimeCreated: policy.TimeCreated, Items: make([]lifecycleRuleBody, 0, len(policy.Rules))}

	for i := range policy.Rules {
		rule := &policy.Rules[i]
		item := lifecycleRuleBody{
			Name:       rule.Name,
			Target:     rule.Target,
			Action:     rule.Action,
			TimeAmount: rule.TimeAmount,
			TimeUnit:   rule.TimeUnit,
			IsEnabled:  rule.IsEnabled,
		}

		if len(rule.InclusionPrefixes)+len(rule.InclusionPatterns)+len(rule.ExclusionPatterns) > 0 {
			item.ObjectNameFilter = &lifecycleFilterBody{
				InclusionPrefixes: rule.InclusionPrefixes,
				InclusionPatterns: rule.InclusionPatterns,
				ExclusionPatterns: rule.ExclusionPatterns,
			}
		}

		out.Items = append(out.Items, item)
	}

	return out
}
