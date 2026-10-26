package networkfirewall

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type summaryConfigurationJSON struct {
	RuleOptions []string `json:"RuleOptions"`
}

type ruleGroupResponseJSON struct {
	RuleGroupName        string                    `json:"RuleGroupName"`
	RuleGroupArn         string                    `json:"RuleGroupArn"`
	RuleGroupID          string                    `json:"RuleGroupId"`
	Type                 string                    `json:"Type"`
	Capacity             int                       `json:"Capacity,omitempty"`
	ConsumedCapacity     int                       `json:"ConsumedCapacity"`
	NumberOfAssociations int                       `json:"NumberOfAssociations"`
	Description          string                    `json:"Description,omitempty"`
	RuleGroupStatus      string                    `json:"RuleGroupStatus,omitempty"`
	LastModifiedTime     *float64                  `json:"LastModifiedTime,omitempty"`
	SummaryConfiguration *summaryConfigurationJSON `json:"SummaryConfiguration,omitempty"`
	Tags                 []tag                     `json:"Tags,omitempty"`
}

type createRuleGroupRequest struct {
	RuleGroupName        string                    `json:"RuleGroupName"`
	RuleGroup            json.RawMessage           `json:"RuleGroup"`
	Rules                string                    `json:"Rules"`
	Type                 string                    `json:"Type"`
	Capacity             int                       `json:"Capacity"`
	Description          string                    `json:"Description"`
	SummaryConfiguration *summaryConfigurationJSON `json:"SummaryConfiguration"`
	Tags                 []tag                     `json:"Tags"`
}

type ruleGroupNameArnRequest struct {
	RuleGroupName string `json:"RuleGroupName"`
	RuleGroupArn  string `json:"RuleGroupArn"`
	Type          string `json:"Type"`
}

// ruleGroupBody resolves the rules of a create/update request. RuleGroup and
// Rules are alternatives (AWS: "either this or Rules, but not both"); giving
// neither stores an empty rule group, as the emulator always has.
func ruleGroupBody(ruleGroup json.RawMessage, rules string) (json.RawMessage, error) {
	hasGroup := len(ruleGroup) > 0 && string(ruleGroup) != "null"

	if hasGroup && rules != "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "provide either RuleGroup or Rules, not both")
	}

	if !hasGroup && rules == "" {
		return nil, nil
	}

	if !hasGroup {
		ruleGroup = nil
	}

	return ruleGroupPayload(ruleGroup, rules), nil
}

func summaryOptions(c *summaryConfigurationJSON) []string {
	if c == nil {
		return nil
	}

	return append([]string{}, c.RuleOptions...)
}

func (h *Handler) createRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req createRuleGroupRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	body, err := ruleGroupBody(req.RuleGroup, req.Rules)
	if err != nil {
		writeErr(w, err)
		return
	}

	rg, err := h.db.CreateRuleGroup(r.Context(), nfdriver.CreateRuleGroupConfig{
		Name: req.RuleGroupName, Type: req.Type, Capacity: req.Capacity, Description: req.Description,
		Rules: body, SummaryRuleOptions: summaryOptions(req.SummaryConfiguration), Tags: tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":       rg.UpdateToken,
		"RuleGroupResponse": toRuleGroupResponseJSON(rg),
	})
}

func (h *Handler) describeRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req ruleGroupNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rg, err := h.db.DescribeRuleGroup(r.Context(), req.RuleGroupName, req.RuleGroupArn, req.Type)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":       rg.UpdateToken,
		"RuleGroupResponse": toRuleGroupResponseJSON(rg),
		"RuleGroup":         rg.Rules,
	})
}

type updateRuleGroupRequest struct {
	UpdateToken          string                    `json:"UpdateToken"`
	RuleGroupName        string                    `json:"RuleGroupName"`
	RuleGroupArn         string                    `json:"RuleGroupArn"`
	RuleGroup            json.RawMessage           `json:"RuleGroup"`
	Rules                string                    `json:"Rules"`
	Type                 string                    `json:"Type"`
	Description          string                    `json:"Description"`
	SummaryConfiguration *summaryConfigurationJSON `json:"SummaryConfiguration"`
}

func (h *Handler) updateRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req updateRuleGroupRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	body, err := ruleGroupBody(req.RuleGroup, req.Rules)
	if err != nil {
		writeErr(w, err)
		return
	}

	rg, err := h.db.UpdateRuleGroup(r.Context(), req.RuleGroupName, req.RuleGroupArn, req.Type,
		nfdriver.UpdateRuleGroupConfig{
			UpdateToken: req.UpdateToken, Description: req.Description, Rules: body,
			SummaryRuleOptions: summaryOptions(req.SummaryConfiguration),
		})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":       rg.UpdateToken,
		"RuleGroupResponse": toRuleGroupResponseJSON(rg),
	})
}

func (h *Handler) deleteRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req ruleGroupNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rg, err := h.db.DeleteRuleGroup(r.Context(), req.RuleGroupName, req.RuleGroupArn, req.Type)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"RuleGroupResponse": toRuleGroupResponseJSON(rg)})
}

type listRuleGroupsRequest struct {
	listRequest
	Type        string `json:"Type"`
	Scope       string `json:"Scope"`
	ManagedType string `json:"ManagedType"`
}

func (h *Handler) listRuleGroups(w http.ResponseWriter, r *http.Request) {
	var req listRuleGroupsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	filter := nfdriver.RuleGroupFilter{Type: req.Type, Scope: req.Scope, ManagedType: req.ManagedType}

	items, next, err := h.db.ListRuleGroups(r.Context(), filter, req.options())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"Name": items[i].Name, "Arn": items[i].ARN})
	}

	wire.WriteJSON(w, withNextToken(map[string]any{"RuleGroups": metas}, next))
}

func toRuleGroupResponseJSON(rg *nfdriver.RuleGroup) ruleGroupResponseJSON {
	modified := epochSeconds(rg.LastModified)

	out := ruleGroupResponseJSON{
		RuleGroupName: rg.Name, RuleGroupArn: rg.ARN, RuleGroupID: rg.ID, Type: rg.Type,
		Capacity: rg.Capacity, ConsumedCapacity: rg.ConsumedCapacity, NumberOfAssociations: rg.NumberOfAssociations,
		Description: rg.Description, RuleGroupStatus: rg.Status, LastModifiedTime: &modified, Tags: mapToTags(rg.Tags),
	}
	if len(rg.SummaryRuleOptions) > 0 {
		out.SummaryConfiguration = &summaryConfigurationJSON{RuleOptions: rg.SummaryRuleOptions}
	}

	return out
}
