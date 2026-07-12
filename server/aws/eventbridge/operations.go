package eventbridge

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// --- event buses ---

func (h *Handler) createEventBus(w http.ResponseWriter, r *http.Request) {
	var req createEventBusRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.bus.CreateEventBus(r.Context(), ebdriver.EventBusConfig{
		Name: req.Name,
		Tags: tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, createEventBusResponse{
		EventBusArn: info.ARN,
		Description: req.Description,
	})
}

func (h *Handler) describeEventBus(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.bus.GetEventBus(r.Context(), busNameOrDefault(req.Name))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, describeEventBusResponse{
		Arn:          info.ARN,
		Name:         info.Name,
		CreationTime: epochSeconds(info.CreatedAt),
	})
}

func (h *Handler) listEventBuses(w http.ResponseWriter, r *http.Request) {
	infos, err := h.bus.ListEventBuses(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]eventBusEntry, 0, len(infos))
	for i := range infos {
		entries = append(entries, eventBusEntry{
			Arn:          infos[i].ARN,
			Name:         infos[i].Name,
			CreationTime: epochSeconds(infos[i].CreatedAt),
		})
	}

	wire.WriteJSON(w, listEventBusesResponse{EventBuses: entries})
}

func (h *Handler) deleteEventBus(w http.ResponseWriter, r *http.Request) {
	var req nameRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.bus.DeleteEventBus(r.Context(), req.Name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

// --- rules ---

func (h *Handler) putRule(w http.ResponseWriter, r *http.Request) {
	var req putRuleRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := h.bus.PutRule(r.Context(), &ebdriver.RuleConfig{
		Name:         req.Name,
		EventBus:     req.EventBusName,
		Description:  req.Description,
		EventPattern: req.EventPattern,
		State:        req.State,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, putRuleResponse{RuleArn: ruleARN(rule.EventBus, rule.Name)})
}

func (h *Handler) describeRule(w http.ResponseWriter, r *http.Request) {
	var req ruleRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rule, err := h.bus.GetRule(r.Context(), busNameOrDefault(req.EventBusName), req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, describeRuleResponse{
		Arn:          ruleARN(rule.EventBus, rule.Name),
		Name:         rule.Name,
		EventBusName: rule.EventBus,
		Description:  rule.Description,
		EventPattern: rule.EventPattern,
		State:        rule.State,
	})
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	var req ruleRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rules, err := h.bus.ListRules(r.Context(), busNameOrDefault(req.EventBusName))
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]ruleEntry, 0, len(rules))
	for i := range rules {
		entries = append(entries, ruleEntry{
			Arn:          ruleARN(rules[i].EventBus, rules[i].Name),
			Name:         rules[i].Name,
			EventBusName: rules[i].EventBus,
			Description:  rules[i].Description,
			EventPattern: rules[i].EventPattern,
			State:        rules[i].State,
		})
	}

	wire.WriteJSON(w, listRulesResponse{Rules: entries})
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	var req ruleRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.bus.DeleteRule(r.Context(), busNameOrDefault(req.EventBusName), req.Name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) enableRule(w http.ResponseWriter, r *http.Request) {
	var req ruleRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.bus.EnableRule(r.Context(), busNameOrDefault(req.EventBusName), req.Name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) disableRule(w http.ResponseWriter, r *http.Request) {
	var req ruleRefRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.bus.DisableRule(r.Context(), busNameOrDefault(req.EventBusName), req.Name); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

// --- targets ---

func (h *Handler) putTargets(w http.ResponseWriter, r *http.Request) {
	var req putTargetsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	targets := make([]ebdriver.Target, 0, len(req.Targets))
	for i := range req.Targets {
		targets = append(targets, fromTargetJSON(&req.Targets[i]))
	}

	err := h.bus.PutTargets(r.Context(), busNameOrDefault(req.EventBusName), req.Rule, targets)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, putTargetsResponse{FailedEntries: []any{}})
}

func (h *Handler) removeTargets(w http.ResponseWriter, r *http.Request) {
	var req removeTargetsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	err := h.bus.RemoveTargets(r.Context(), busNameOrDefault(req.EventBusName), req.Rule, req.Ids)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, removeTargetsResponse{FailedEntries: []any{}})
}

func (h *Handler) listTargetsByRule(w http.ResponseWriter, r *http.Request) {
	var req listTargetsByRuleRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	targets, err := h.bus.ListTargets(r.Context(), busNameOrDefault(req.EventBusName), req.Rule)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]targetJSON, 0, len(targets))
	for i := range targets {
		out = append(out, toTargetJSON(&targets[i]))
	}

	wire.WriteJSON(w, listTargetsByRuleResponse{Targets: out})
}

// --- events ---

func (h *Handler) putEvents(w http.ResponseWriter, r *http.Request) {
	var req putEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	events := make([]ebdriver.Event, 0, len(req.Entries))
	for i := range req.Entries {
		e := &req.Entries[i]
		events = append(events, ebdriver.Event{
			Source:     e.Source,
			DetailType: e.DetailType,
			Detail:     e.Detail,
			EventBus:   busNameOrDefault(e.EventBusName),
			Resources:  e.Resources,
		})
	}

	res, err := h.bus.PutEvents(r.Context(), events)
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]putEventsResultEntry, 0, len(res.EventIDs))
	for _, id := range res.EventIDs {
		entries = append(entries, putEventsResultEntry{EventID: id})
	}

	wire.WriteJSON(w, putEventsResponse{
		FailedEntryCount: res.FailCount,
		Entries:          entries,
	})
}
