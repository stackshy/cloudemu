package eventbridge

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/eventmatch"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// maxPutEventsEntries is the maximum number of entries EventBridge accepts in a
// single PutEvents request.
const maxPutEventsEntries = 10

// putEventsEntryCountMessage is the ValidationException detail EventBridge
// returns when a PutEvents request carries fewer than 1 or more than 10 entries.
const putEventsEntryCountMessage = "1 validation error detected: Value at 'entries' failed to satisfy " +
	"constraint: Member must have length less than or equal to 10 and greater than or equal to 1."

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
		Policy:       info.Policy,
	})
}

func (h *Handler) listEventBuses(w http.ResponseWriter, r *http.Request) {
	var req listEventBusesRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	infos, err := h.bus.ListEventBuses(r.Context(), scope.Scope{})
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := make([]eventBusEntry, 0, len(infos))
	for i := range infos {
		if req.NamePrefix != "" && !strings.HasPrefix(infos[i].Name, req.NamePrefix) {
			continue
		}

		entries = append(entries, eventBusEntry{
			Arn:          infos[i].ARN,
			Name:         infos[i].Name,
			CreationTime: epochSeconds(infos[i].CreatedAt),
		})
	}

	entries, nextToken := paginateByCursor(entries, req.NextToken, req.Limit, func(e eventBusEntry) string { return e.Name })

	wire.WriteJSON(w, listEventBusesResponse{EventBuses: entries, NextToken: nextToken})
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

	// Real EventBridge requires a rule to carry either an EventPattern or a
	// ScheduleExpression; a rule with neither is rejected.
	if req.EventPattern == "" && req.ScheduleExpression == "" {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException",
			"Parameter(s) EventPattern or ScheduleExpression must be specified.")
		return
	}

	// A structurally invalid event pattern is rejected up front with
	// InvalidEventPatternException, the exact exception real EventBridge returns.
	if req.EventPattern != "" {
		if err := eventmatch.ValidatePattern(req.EventPattern); err != nil {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidEventPatternException", err.Error())
			return
		}
	}

	rule, err := h.bus.PutRule(r.Context(), &ebdriver.RuleConfig{
		Name:               req.Name,
		EventBus:           req.EventBusName,
		Description:        req.Description,
		EventPattern:       req.EventPattern,
		ScheduleExpression: req.ScheduleExpression,
		RoleARN:            req.RoleArn,
		State:              req.State,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, putRuleResponse{RuleArn: h.ruleARN(rule.EventBus, rule.Name)})
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
		Arn:                h.ruleARN(rule.EventBus, rule.Name),
		Name:               rule.Name,
		EventBusName:       rule.EventBus,
		Description:        rule.Description,
		EventPattern:       rule.EventPattern,
		ScheduleExpression: rule.ScheduleExpression,
		RoleArn:            rule.RoleARN,
		State:              rule.State,
	})
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	var req listRulesRequest
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
		if req.NamePrefix != "" && !strings.HasPrefix(rules[i].Name, req.NamePrefix) {
			continue
		}

		entries = append(entries, ruleEntry{
			Arn:                h.ruleARN(rules[i].EventBus, rules[i].Name),
			Name:               rules[i].Name,
			EventBusName:       rules[i].EventBus,
			Description:        rules[i].Description,
			EventPattern:       rules[i].EventPattern,
			ScheduleExpression: rules[i].ScheduleExpression,
			RoleArn:            rules[i].RoleARN,
			State:              rules[i].State,
		})
	}

	entries, nextToken := paginateRules(entries, req.NextToken, req.Limit)

	wire.WriteJSON(w, listRulesResponse{Rules: entries, NextToken: nextToken})
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

	out, nextToken := paginateByCursor(out, req.NextToken, req.Limit, func(t targetJSON) string { return t.ID })

	wire.WriteJSON(w, listTargetsByRuleResponse{Targets: out, NextToken: nextToken})
}

// --- events ---

func (h *Handler) putEvents(w http.ResponseWriter, r *http.Request) {
	var req putEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// EventBridge bounds a PutEvents request to between 1 and 10 entries; an
	// out-of-range batch is rejected wholesale with a ValidationException.
	if n := len(req.Entries); n < 1 || n > maxPutEventsEntries {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", putEventsEntryCountMessage)
		return
	}

	// Each entry is aligned to a result slot; entries with a malformed Detail
	// fail up front (ErrorCode=MalformedDetail) and never reach the bus, while
	// well-formed entries are published and back-filled with their EventId.
	results := make([]putEventsResultEntry, len(req.Entries))

	events := make([]ebdriver.Event, 0, len(req.Entries))
	slots := make([]int, 0, len(req.Entries))
	failed := 0

	for i := range req.Entries {
		e := &req.Entries[i]
		if !isValidDetail(e.Detail) {
			results[i] = putEventsResultEntry{ErrorCode: "MalformedDetail", ErrorMessage: "Detail is malformed."}
			failed++

			continue
		}

		slots = append(slots, i)
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

	for i, id := range res.EventIDs {
		if i < len(slots) {
			results[slots[i]] = putEventsResultEntry{EventID: id}
		}
	}

	wire.WriteJSON(w, putEventsResponse{
		FailedEntryCount: failed + res.FailCount,
		Entries:          results,
	})
}

// testEventPattern evaluates a sample event against an event pattern using the
// same matcher PutEvents delivery uses, so "would this rule fire?" answered here
// is consistent with what actually gets delivered. Real users / the console call
// it to debug a pattern before deploying a rule.
func (*Handler) testEventPattern(w http.ResponseWriter, r *http.Request) {
	var req testEventPatternRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// The pattern is validated with the same rule PutRule enforces, so a pattern
	// that would be rejected at deploy time is rejected here too.
	if err := eventmatch.ValidatePattern(req.EventPattern); err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidEventPatternException", err.Error())
		return
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(req.Event), &event); err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException",
			"Event is not a valid JSON object.")
		return
	}

	pattern, _ := eventmatch.ParsePattern(req.EventPattern)

	wire.WriteJSON(w, testEventPatternResponse{Result: eventmatch.MatchEvent(pattern, event)})
}
