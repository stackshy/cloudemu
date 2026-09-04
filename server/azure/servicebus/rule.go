package servicebus

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveRuleCollection(w http.ResponseWriter, r *http.Request, sp sbPath, topic, sub string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	s, ok := h.lookupSub(sp, topic, sub)
	if !ok {
		h.mu.RUnlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found: "+sub)

		return
	}

	resources := make([]any, 0, len(s.Rules))
	for _, n := range sortedKeys(s.Rules) {
		resources = append(resources, toRuleResource(sp, topic, sub, s.Rules[n]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, resources))
}

func (h *Handler) serveRule(w http.ResponseWriter, r *http.Request, sp sbPath, topic, sub, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createRule(w, r, sp, topic, sub, name)
	case http.MethodGet:
		h.getRule(w, sp, topic, sub, name)
	case http.MethodDelete:
		h.deleteRule(w, sp, topic, sub, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request, sp sbPath, topic, sub, name string) {
	var req createRuleRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	s, ok := h.lookupSub(sp, topic, sub)
	if !ok {
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found: "+sub)

		return
	}

	rec := &ruleRecord{Name: name, Props: normalizeRuleProps(req.Properties)}
	s.Rules[name] = rec

	resource := toRuleResource(sp, topic, sub, rec)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getRule(w http.ResponseWriter, sp sbPath, topic, sub, name string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	s, ok := h.lookupSub(sp, topic, sub)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found: "+sub)
		return
	}

	rec, ok := s.Rules[name]
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "rule not found: "+name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRuleResource(sp, topic, sub, rec))
}

func (h *Handler) deleteRule(w http.ResponseWriter, sp sbPath, topic, sub, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.lookupSub(sp, topic, sub)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found: "+sub)
		return
	}

	if _, ok := s.Rules[name]; !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	delete(s.Rules, name)
	w.WriteHeader(http.StatusOK)
}

// defaultRule is the $Default SqlFilter (1=1) rule created with a subscription.
func defaultRule() *ruleRecord {
	return &ruleRecord{
		Name: defaultRuleName,
		Props: ruleProperties{
			Action:     &ruleAction{},
			FilterType: filterTypeSQL,
			SQLFilter:  &sqlFilter{SQLExpression: "1=1", CompatibilityLevel: sqlCompatibilityLevel},
		},
	}
}

// normalizeRuleProps fills the filter defaults a real Service Bus rule reports.
func normalizeRuleProps(in ruleProperties) ruleProperties {
	out := in
	if out.Action == nil {
		out.Action = &ruleAction{}
	}

	switch {
	case out.FilterType == "" && out.CorrelationFilter != nil:
		out.FilterType = filterTypeCorrelation
	case out.FilterType == "":
		out.FilterType = filterTypeSQL
	}

	if out.FilterType == filterTypeSQL {
		if out.SQLFilter == nil {
			out.SQLFilter = &sqlFilter{SQLExpression: "1=1"}
		}

		out.SQLFilter.CompatibilityLevel = sqlCompatibilityLevel
		out.CorrelationFilter = nil
	}

	return out
}

func toRuleResource(sp sbPath, topic, sub string, rec *ruleRecord) ruleResource {
	return ruleResource{
		ID: azurearm.BuildResourceID(sp.sub, sp.rg, providerName, resourceType, sp.namespace) +
			"/topics/" + topic + "/subscriptions/" + sub + "/rules/" + rec.Name,
		Name:       rec.Name,
		Type:       providerName + "/Namespaces/Topics/Subscriptions/Rules",
		Properties: rec.Props,
	}
}
