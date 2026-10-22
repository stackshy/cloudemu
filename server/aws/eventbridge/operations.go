package eventbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/eventmatch"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// entryPublisher is the AWS-specific PutEvents surface that reports each
// entry's outcome by index, asserted against the provider (the portable
// EventBus driver only returns counts).
type entryPublisher interface {
	PutEventEntries(ctx context.Context, events []ebdriver.Event) ([]ebdriver.PutEventsEntryResult, error)
}

// endpointPublisher is the AWS-specific PutEvents surface for a request sent
// through a global endpoint (EndpointId set).
type endpointPublisher interface {
	PutEndpointEventEntries(ctx context.Context, endpointID string, events []ebdriver.Event) ([]ebdriver.PutEventsEntryResult, error)
}

// --- event buses ---

func (h *Handler) createEventBus(w http.ResponseWriter, r *http.Request) {
	var req createEventBusRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.bus.CreateEventBus(r.Context(), ebdriver.EventBusConfig{
		Name:        req.Name,
		Tags:        tagsToMap(req.Tags),
		Description: req.Description,
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
		Description:  info.Description,
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
			Description:  infos[i].Description,
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
		EventBus:           busNameOrDefault(req.EventBusName),
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
	publisher, ok := h.bus.(entryPublisher)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "PutEvents not supported"))
		return
	}

	var req putEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	results, err := h.publishEntries(r.Context(), publisher, &req)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := putEventsResponse{Entries: make([]putEventsResultEntry, len(results))}
	for i, res := range results {
		resp.Entries[i] = putEventsResultEntry{EventID: res.EventID, ErrorCode: res.ErrorCode, ErrorMessage: res.ErrorMessage}
		if res.ErrorCode != "" {
			resp.FailedEntryCount++
		}
	}

	wire.WriteJSON(w, resp)
}

// publishEntries hands the request's entries to the provider. Through a global
// endpoint the entries keep their EventBusName as sent, since the endpoint
// decides where an entry without one goes.
func (h *Handler) publishEntries(
	ctx context.Context, publisher entryPublisher, req *putEventsRequest,
) ([]ebdriver.PutEventsEntryResult, error) {
	viaEndpoint := req.EndpointID != ""
	events := make([]ebdriver.Event, len(req.Entries))

	for i := range req.Entries {
		e := &req.Entries[i]

		bus := e.EventBusName
		if !viaEndpoint {
			bus = busNameOrDefault(bus)
		}

		events[i] = ebdriver.Event{
			Source: e.Source, DetailType: e.DetailType, Detail: e.Detail,
			EventBus: bus, Resources: e.Resources, Time: entryTime(e.Time),
		}
	}

	if !viaEndpoint {
		return publisher.PutEventEntries(ctx, events)
	}

	ep, ok := h.bus.(endpointPublisher)
	if !ok {
		return nil, cerrors.New(cerrors.Unimplemented, "global endpoints not supported")
	}

	return ep.PutEndpointEventEntries(ctx, req.EndpointID, events)
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
