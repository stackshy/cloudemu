package eventbridge

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// permissionManager is the AWS-specific EventBridge resource-policy surface,
// asserted against the provider (not part of the portable EventBus driver, since
// event-bus resource policies are an AWS-only concept).
type permissionManager interface {
	PutPermission(ctx context.Context, busName string, in ebdriver.PermissionInput) error
	RemovePermission(ctx context.Context, busName, statementID string, removeAll bool) error
}

func (h *Handler) permissions() (permissionManager, bool) {
	p, ok := h.bus.(permissionManager)

	return p, ok
}

type conditionJSON struct {
	Type  string `json:"Type"`
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type putPermissionRequest struct {
	EventBusName string         `json:"EventBusName"`
	Action       string         `json:"Action"`
	Principal    string         `json:"Principal"`
	StatementID  string         `json:"StatementId"`
	Policy       string         `json:"Policy"`
	Condition    *conditionJSON `json:"Condition"`
}

type removePermissionRequest struct {
	EventBusName         string `json:"EventBusName"`
	StatementID          string `json:"StatementId"`
	RemoveAllPermissions bool   `json:"RemoveAllPermissions"`
}

func (h *Handler) putPermission(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.permissions()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "resource policies not supported"))
		return
	}

	var req putPermissionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	in := ebdriver.PermissionInput{
		StatementID: req.StatementID,
		Action:      req.Action,
		Principal:   req.Principal,
		Policy:      req.Policy,
	}

	if req.Condition != nil {
		in.Condition = &ebdriver.PermissionCondition{
			Type:  req.Condition.Type,
			Key:   req.Condition.Key,
			Value: req.Condition.Value,
		}
	}

	if err := mgr.PutPermission(r.Context(), req.EventBusName, in); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) removePermission(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.permissions()
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "resource policies not supported"))
		return
	}

	var req removePermissionRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := mgr.RemovePermission(r.Context(), req.EventBusName, req.StatementID, req.RemoveAllPermissions); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

type listRuleNamesByTargetRequest struct {
	TargetArn    string `json:"TargetArn"`
	EventBusName string `json:"EventBusName"`
	Limit        int    `json:"Limit"`
	NextToken    string `json:"NextToken"`
}

type listRuleNamesByTargetResponse struct {
	RuleNames []string `json:"RuleNames"`
	NextToken string   `json:"NextToken,omitempty"`
}

// listRuleNamesByTarget returns the names of the rules on a bus that route to a
// given target ARN. It is derived from the portable ListRules/ListTargets driver
// methods, so no AWS-specific provider hook is needed.
func (h *Handler) listRuleNamesByTarget(w http.ResponseWriter, r *http.Request) {
	var req listRuleNamesByTargetRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.TargetArn == "" {
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException",
			"Parameter TargetArn must be specified.")
		return
	}

	bus := busNameOrDefault(req.EventBusName)

	rules, err := h.bus.ListRules(r.Context(), bus)
	if err != nil {
		writeErr(w, err)
		return
	}

	names := make([]string, 0, len(rules))

	for i := range rules {
		targets, tErr := h.bus.ListTargets(r.Context(), bus, rules[i].Name)
		if tErr != nil {
			continue
		}

		for j := range targets {
			if targets[j].ARN == req.TargetArn {
				names = append(names, rules[i].Name)

				break
			}
		}
	}

	names, nextToken := paginateByCursor(names, req.NextToken, req.Limit, func(s string) string { return s })

	wire.WriteJSON(w, listRuleNamesByTargetResponse{RuleNames: names, NextToken: nextToken})
}
