package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type firewallArnRequest struct {
	FirewallArn string `json:"FirewallArn"`
}

func (h *Handler) describeFirewallMetadata(w http.ResponseWriter, r *http.Request) {
	var req firewallArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	meta, ok := capability[nfdriver.Metadata](h, w)
	if !ok {
		return
	}

	m, err := meta.DescribeFirewallMetadata(r.Context(), req.FirewallArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	zones := make(map[string]map[string]string, len(m.AvailabilityZones))
	for _, z := range m.AvailabilityZones {
		zones[z] = map[string]string{}
	}

	resp := map[string]any{
		"FirewallArn": m.FirewallARN, "FirewallPolicyArn": m.PolicyARN,
		"Status": m.Status, "SupportedAvailabilityZones": zones,
	}
	withDescription(resp, m.Description)

	wire.WriteJSON(w, resp)
}

func (h *Handler) describeRuleGroupMetadata(w http.ResponseWriter, r *http.Request) {
	var req ruleGroupNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	meta, ok := capability[nfdriver.Metadata](h, w)
	if !ok {
		return
	}

	rg, err := meta.DescribeRuleGroupMetadata(r.Context(), req.RuleGroupName, req.RuleGroupArn, req.Type)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := map[string]any{
		"RuleGroupArn": rg.ARN, "RuleGroupName": rg.Name, "Capacity": rg.Capacity,
		"LastModifiedTime": epochSeconds(rg.LastModified), "Type": rg.Type,
	}
	withDescription(resp, rg.Description)

	wire.WriteJSON(w, resp)
}

type ruleSummaryJSON struct {
	Msg      string `json:"Msg,omitempty"`
	SID      string `json:"SID,omitempty"`
	Metadata string `json:"Metadata,omitempty"`
}

func (h *Handler) describeRuleGroupSummary(w http.ResponseWriter, r *http.Request) {
	var req ruleGroupNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	meta, ok := capability[nfdriver.Metadata](h, w)
	if !ok {
		return
	}

	rg, summaries, err := meta.DescribeRuleGroupSummary(r.Context(), req.RuleGroupName, req.RuleGroupArn, req.Type)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]ruleSummaryJSON, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, ruleSummaryJSON{Msg: s.Msg, SID: s.SID, Metadata: s.Metadata})
	}

	resp := map[string]any{"RuleGroupName": rg.Name, "Summary": map[string]any{"RuleSummaries": out}}
	withDescription(resp, rg.Description)

	wire.WriteJSON(w, resp)
}

// withDescription adds Description to a response only when one is set, as AWS
// omits an absent description rather than returning an empty string.
func withDescription(resp map[string]any, description string) {
	if description != "" {
		resp["Description"] = description
	}
}
