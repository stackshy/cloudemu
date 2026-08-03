package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type subnetMapping struct {
	SubnetID string `json:"SubnetId"`
}

// ---- Firewall ----

type firewallJSON struct {
	FirewallName      string          `json:"FirewallName"`
	FirewallArn       string          `json:"FirewallArn"`
	FirewallPolicyArn string          `json:"FirewallPolicyArn,omitempty"`
	VpcID             string          `json:"VpcId,omitempty"`
	SubnetMappings    []subnetMapping `json:"SubnetMappings,omitempty"`
	Description       string          `json:"Description,omitempty"`
	DeleteProtection  bool            `json:"DeleteProtection"`
	Tags              []tag           `json:"Tags,omitempty"`
}

type firewallStatusJSON struct {
	Status string `json:"Status"`
}

type createFirewallRequest struct {
	FirewallName      string          `json:"FirewallName"`
	FirewallPolicyArn string          `json:"FirewallPolicyArn"`
	VpcID             string          `json:"VpcId"`
	SubnetMappings    []subnetMapping `json:"SubnetMappings"`
	Description       string          `json:"Description"`
	DeleteProtection  bool            `json:"DeleteProtection"`
	Tags              []tag           `json:"Tags"`
}

type nameArnRequest struct {
	FirewallName string `json:"FirewallName"`
	FirewallArn  string `json:"FirewallArn"`
}

func (h *Handler) createFirewall(w http.ResponseWriter, r *http.Request) {
	var req createFirewallRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.CreateFirewall(r.Context(), nfdriver.CreateFirewallConfig{
		Name:             req.FirewallName,
		PolicyARN:        req.FirewallPolicyArn,
		VPCID:            req.VpcID,
		SubnetIDs:        subnetIDs(req.SubnetMappings),
		Description:      req.Description,
		DeleteProtection: req.DeleteProtection,
		Tags:             tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"Firewall":       toFirewallJSON(fw),
		"FirewallStatus": firewallStatusJSON{Status: fw.Status},
	})
}

func (h *Handler) describeFirewall(w http.ResponseWriter, r *http.Request) {
	var req nameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.DescribeFirewall(r.Context(), req.FirewallName, req.FirewallArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":    updateToken,
		"Firewall":       toFirewallJSON(fw),
		"FirewallStatus": firewallStatusJSON{Status: fw.Status},
	})
}

func (h *Handler) deleteFirewall(w http.ResponseWriter, r *http.Request) {
	var req nameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	fw, err := h.db.DeleteFirewall(r.Context(), req.FirewallName, req.FirewallArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"Firewall":       toFirewallJSON(fw),
		"FirewallStatus": firewallStatusJSON{Status: fw.Status},
	})
}

func (h *Handler) listFirewalls(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ListFirewalls(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"FirewallName": items[i].Name, "FirewallArn": items[i].ARN})
	}

	wire.WriteJSON(w, map[string]any{"Firewalls": metas})
}

func toFirewallJSON(f *nfdriver.Firewall) firewallJSON {
	mappings := make([]subnetMapping, 0, len(f.SubnetIDs))
	for _, s := range f.SubnetIDs {
		mappings = append(mappings, subnetMapping{SubnetID: s})
	}

	return firewallJSON{
		FirewallName: f.Name, FirewallArn: f.ARN, FirewallPolicyArn: f.PolicyARN,
		VpcID: f.VPCID, SubnetMappings: mappings, Description: f.Description,
		DeleteProtection: f.DeleteProtection, Tags: mapToTags(f.Tags),
	}
}

// ---- Firewall Policy ----

type firewallPolicyResponseJSON struct {
	FirewallPolicyName string `json:"FirewallPolicyName"`
	FirewallPolicyArn  string `json:"FirewallPolicyArn"`
	FirewallPolicyID   string `json:"FirewallPolicyId"`
	Description        string `json:"Description,omitempty"`
	Tags               []tag  `json:"Tags,omitempty"`
}

type firewallPolicyDetailJSON struct {
	StatelessDefaultActions         []string `json:"StatelessDefaultActions,omitempty"`
	StatelessFragmentDefaultActions []string `json:"StatelessFragmentDefaultActions,omitempty"`
}

type createFirewallPolicyRequest struct {
	FirewallPolicyName string                   `json:"FirewallPolicyName"`
	FirewallPolicy     firewallPolicyDetailJSON `json:"FirewallPolicy"`
	Description        string                   `json:"Description"`
	Tags               []tag                    `json:"Tags"`
}

type policyNameArnRequest struct {
	FirewallPolicyName string `json:"FirewallPolicyName"`
	FirewallPolicyArn  string `json:"FirewallPolicyArn"`
}

func (h *Handler) createFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	var req createFirewallPolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := h.db.CreateFirewallPolicy(r.Context(), nfdriver.CreateFirewallPolicyConfig{
		Name:                            req.FirewallPolicyName,
		Description:                     req.Description,
		StatelessDefaultActions:         req.FirewallPolicy.StatelessDefaultActions,
		StatelessFragmentDefaultActions: req.FirewallPolicy.StatelessFragmentDefaultActions,
		Tags:                            tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":            updateToken,
		"FirewallPolicyResponse": toPolicyResponseJSON(p),
	})
}

func (h *Handler) describeFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := h.db.DescribeFirewallPolicy(r.Context(), req.FirewallPolicyName, req.FirewallPolicyArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":            updateToken,
		"FirewallPolicyResponse": toPolicyResponseJSON(p),
		"FirewallPolicy": firewallPolicyDetailJSON{
			StatelessDefaultActions:         p.StatelessDefaultActions,
			StatelessFragmentDefaultActions: p.StatelessFragmentDefaultActions,
		},
	})
}

func (h *Handler) deleteFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := h.db.DeleteFirewallPolicy(r.Context(), req.FirewallPolicyName, req.FirewallPolicyArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"FirewallPolicyResponse": toPolicyResponseJSON(p)})
}

func (h *Handler) listFirewallPolicies(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ListFirewallPolicies(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"Name": items[i].Name, "Arn": items[i].ARN})
	}

	wire.WriteJSON(w, map[string]any{"FirewallPolicies": metas})
}

func toPolicyResponseJSON(p *nfdriver.FirewallPolicy) firewallPolicyResponseJSON {
	return firewallPolicyResponseJSON{
		FirewallPolicyName: p.Name, FirewallPolicyArn: p.ARN, FirewallPolicyID: p.ID,
		Description: p.Description, Tags: mapToTags(p.Tags),
	}
}

// ---- Rule Group ----

type ruleGroupResponseJSON struct {
	RuleGroupName string `json:"RuleGroupName"`
	RuleGroupArn  string `json:"RuleGroupArn"`
	RuleGroupID   string `json:"RuleGroupId"`
	Type          string `json:"Type"`
	Capacity      int    `json:"Capacity,omitempty"`
	Description   string `json:"Description,omitempty"`
	Tags          []tag  `json:"Tags,omitempty"`
}

type createRuleGroupRequest struct {
	RuleGroupName string `json:"RuleGroupName"`
	Type          string `json:"Type"`
	Capacity      int    `json:"Capacity"`
	Description   string `json:"Description"`
	Tags          []tag  `json:"Tags"`
}

type ruleGroupNameArnRequest struct {
	RuleGroupName string `json:"RuleGroupName"`
	RuleGroupArn  string `json:"RuleGroupArn"`
	Type          string `json:"Type"`
}

func (h *Handler) createRuleGroup(w http.ResponseWriter, r *http.Request) {
	var req createRuleGroupRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rg, err := h.db.CreateRuleGroup(r.Context(), nfdriver.CreateRuleGroupConfig{
		Name: req.RuleGroupName, Type: req.Type, Capacity: req.Capacity,
		Description: req.Description, Tags: tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":       updateToken,
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
		"UpdateToken":       updateToken,
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

func (h *Handler) listRuleGroups(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ListRuleGroups(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"Name": items[i].Name, "Arn": items[i].ARN})
	}

	wire.WriteJSON(w, map[string]any{"RuleGroups": metas})
}

func toRuleGroupResponseJSON(rg *nfdriver.RuleGroup) ruleGroupResponseJSON {
	return ruleGroupResponseJSON{
		RuleGroupName: rg.Name, RuleGroupArn: rg.ARN, RuleGroupID: rg.ID, Type: rg.Type,
		Capacity: rg.Capacity, Description: rg.Description, Tags: mapToTags(rg.Tags),
	}
}

// ---- helpers ----

func subnetIDs(mappings []subnetMapping) []string {
	if len(mappings) == 0 {
		return nil
	}

	out := make([]string, 0, len(mappings))
	for _, m := range mappings {
		out = append(out, m.SubnetID)
	}

	return out
}

func tagsToMap(tags []tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	if len(m) == 0 {
		return nil
	}

	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}
