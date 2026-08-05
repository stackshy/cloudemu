package vpclattice

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

type wireRule struct {
	Arn           string          `json:"arn,omitempty"`
	ID            string          `json:"id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Priority      int32           `json:"priority,omitempty"`
	IsDefault     bool            `json:"isDefault"`
	Match         json.RawMessage `json:"match,omitempty"`
	Action        json.RawMessage `json:"action,omitempty"`
	CreatedAt     string          `json:"createdAt,omitempty"`
	LastUpdatedAt string          `json:"lastUpdatedAt,omitempty"`
}

func ruleToWire(r *driver.Rule) wireRule {
	w := wireRule{
		Arn: r.ARN, ID: r.ID, Name: r.Name, Priority: r.Priority, IsDefault: r.IsDefault,
		CreatedAt: r.CreatedAt, LastUpdatedAt: r.LastUpdatedAt,
	}
	if len(r.Match) > 0 {
		w.Match = json.RawMessage(r.Match)
	}

	if len(r.Action) > 0 {
		w.Action = json.RawMessage(r.Action)
	}

	return w
}

// serveRules routes /services/{sid}/listeners/{lid}/rules[/{id}]. A PATCH on
// the bare collection path is BatchUpdateRule.
func (h *Handler) serveRules(w http.ResponseWriter, r *http.Request, serviceID, listenerID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createRule(w, r, serviceID, listenerID)
		case http.MethodGet:
			h.listRules(w, r, serviceID, listenerID)
		case http.MethodPatch:
			h.batchUpdateRule(w, r, serviceID, listenerID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	routeByID(w, r, rest[0],
		func(w http.ResponseWriter, r *http.Request, id string) { h.getRule(w, r, serviceID, listenerID, id) },
		func(w http.ResponseWriter, r *http.Request, id string) { h.updateRule(w, r, serviceID, listenerID, id) },
		func(w http.ResponseWriter, r *http.Request, id string) { h.deleteRule(w, r, serviceID, listenerID, id) })
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request, serviceID, listenerID string) {
	var req struct {
		Name     string            `json:"name"`
		Priority int32             `json:"priority"`
		Match    json.RawMessage   `json:"match"`
		Action   json.RawMessage   `json:"action"`
		Tags     map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	rule, err := h.lattice.CreateRule(r.Context(), &driver.CreateRuleInput{
		ServiceID: serviceID, ListenerID: listenerID, Name: req.Name, Priority: req.Priority,
		Match: req.Match, Action: req.Action, Tags: req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, ruleToWire(rule))
}

func (h *Handler) getRule(w http.ResponseWriter, r *http.Request, serviceID, listenerID, id string) {
	rule, err := h.lattice.GetRule(r.Context(), serviceID, listenerID, id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, ruleToWire(rule))
}

func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request, serviceID, listenerID, id string) {
	var req struct {
		Priority int32           `json:"priority"`
		Match    json.RawMessage `json:"match"`
		Action   json.RawMessage `json:"action"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	rule, err := h.lattice.UpdateRule(r.Context(), serviceID, listenerID, id, req.Priority, req.Match, req.Action)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, ruleToWire(rule))
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request, serviceID, listenerID, id string) {
	if err := h.lattice.DeleteRule(r.Context(), serviceID, listenerID, id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request, serviceID, listenerID string) {
	rules, err := h.lattice.ListRules(r.Context(), serviceID, listenerID)
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireRule, 0, len(rules))
	for i := range rules {
		items = append(items, ruleToWire(&rules[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}

func (h *Handler) batchUpdateRule(w http.ResponseWriter, r *http.Request, serviceID, listenerID string) {
	var req struct {
		Rules []struct {
			RuleIdentifier string          `json:"ruleIdentifier"`
			Priority       int32           `json:"priority"`
			Match          json.RawMessage `json:"match"`
			Action         json.RawMessage `json:"action"`
		} `json:"rules"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	updates := make([]driver.RuleUpdate, 0, len(req.Rules))
	for _, u := range req.Rules {
		updates = append(updates, driver.RuleUpdate{
			RuleID: u.RuleIdentifier, Priority: u.Priority, Match: u.Match, Action: u.Action,
		})
	}

	ok, fail, err := h.lattice.BatchUpdateRules(r.Context(), serviceID, listenerID, updates)
	if err != nil {
		writeErr(w, err)

		return
	}

	successful := make([]wireRule, 0, len(ok))
	for i := range ok {
		successful = append(successful, ruleToWire(&ok[i]))
	}

	unsuccessful := make([]map[string]any, 0, len(fail))
	for i := range fail {
		unsuccessful = append(unsuccessful, map[string]any{
			"ruleIdentifier": fail[i].RuleID,
			"failureCode":    fail[i].FailureCode,
			"failureMessage": fail[i].FailureMessage,
		})
	}

	writeJSON(w, map[string]any{"successful": successful, "unsuccessful": unsuccessful})
}
