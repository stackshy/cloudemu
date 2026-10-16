package networkfirewall

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type firewallPolicyResponseJSON struct {
	FirewallPolicyName             string   `json:"FirewallPolicyName"`
	FirewallPolicyArn              string   `json:"FirewallPolicyArn"`
	FirewallPolicyID               string   `json:"FirewallPolicyId"`
	Description                    string   `json:"Description,omitempty"`
	FirewallPolicyStatus           string   `json:"FirewallPolicyStatus,omitempty"`
	NumberOfAssociations           int      `json:"NumberOfAssociations"`
	ConsumedStatelessRuleCapacity  int      `json:"ConsumedStatelessRuleCapacity"`
	ConsumedStatefulRuleCapacity   int      `json:"ConsumedStatefulRuleCapacity"`
	ConsumedStatefulDomainCapacity int      `json:"ConsumedStatefulDomainCapacity"`
	LastModifiedTime               *float64 `json:"LastModifiedTime,omitempty"`
	Tags                           []tag    `json:"Tags,omitempty"`
}

type ruleGroupReferenceJSON struct {
	ResourceArn          string          `json:"ResourceArn"`
	Priority             int             `json:"Priority,omitempty"`
	Override             json.RawMessage `json:"Override,omitempty"`
	DeepThreatInspection *bool           `json:"DeepThreatInspection,omitempty"`
}

type firewallPolicyDetailJSON struct {
	StatelessDefaultActions         []string                 `json:"StatelessDefaultActions,omitempty"`
	StatelessFragmentDefaultActions []string                 `json:"StatelessFragmentDefaultActions,omitempty"`
	StatefulDefaultActions          []string                 `json:"StatefulDefaultActions,omitempty"`
	StatefulRuleGroupReferences     []ruleGroupReferenceJSON `json:"StatefulRuleGroupReferences,omitempty"`
	StatelessRuleGroupReferences    []ruleGroupReferenceJSON `json:"StatelessRuleGroupReferences,omitempty"`
	TLSInspectionConfigurationArn   string                   `json:"TLSInspectionConfigurationArn,omitempty"`
	StatelessCustomActions          json.RawMessage          `json:"StatelessCustomActions,omitempty"`
	PolicyVariables                 json.RawMessage          `json:"PolicyVariables,omitempty"`
	StatefulEngineOptions           json.RawMessage          `json:"StatefulEngineOptions,omitempty"`
	EnableTLSSessionHolding         *bool                    `json:"EnableTLSSessionHolding,omitempty"`
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

	d := req.FirewallPolicy

	p, err := h.db.CreateFirewallPolicy(r.Context(), nfdriver.CreateFirewallPolicyConfig{
		Name:                            req.FirewallPolicyName,
		Description:                     req.Description,
		StatelessDefaultActions:         d.StatelessDefaultActions,
		StatelessFragmentDefaultActions: d.StatelessFragmentDefaultActions,
		StatefulDefaultActions:          d.StatefulDefaultActions,
		StatefulRuleGroupReferences:     toRuleGroupRefs(d.StatefulRuleGroupReferences),
		StatelessRuleGroupReferences:    toRuleGroupRefs(d.StatelessRuleGroupReferences),
		TLSInspectionConfigurationARN:   d.TLSInspectionConfigurationArn,
		Extra:                           policyExtra(&d),
		Tags:                            tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":            p.UpdateToken,
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
		"UpdateToken":            p.UpdateToken,
		"FirewallPolicyResponse": toPolicyResponseJSON(p),
		"FirewallPolicy":         toPolicyDetailJSON(p),
	})
}

type updateFirewallPolicyRequest struct {
	UpdateToken        string                   `json:"UpdateToken"`
	FirewallPolicyName string                   `json:"FirewallPolicyName"`
	FirewallPolicyArn  string                   `json:"FirewallPolicyArn"`
	FirewallPolicy     firewallPolicyDetailJSON `json:"FirewallPolicy"`
	Description        string                   `json:"Description"`
}

func (h *Handler) updateFirewallPolicy(w http.ResponseWriter, r *http.Request) {
	var req updateFirewallPolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	d := req.FirewallPolicy

	p, err := h.db.UpdateFirewallPolicy(r.Context(), req.FirewallPolicyName, req.FirewallPolicyArn,
		nfdriver.UpdateFirewallPolicyConfig{
			UpdateToken:                     req.UpdateToken,
			Description:                     req.Description,
			StatelessDefaultActions:         d.StatelessDefaultActions,
			StatelessFragmentDefaultActions: d.StatelessFragmentDefaultActions,
			StatefulDefaultActions:          d.StatefulDefaultActions,
			StatefulRuleGroupReferences:     toRuleGroupRefs(d.StatefulRuleGroupReferences),
			StatelessRuleGroupReferences:    toRuleGroupRefs(d.StatelessRuleGroupReferences),
			TLSInspectionConfigurationARN:   d.TLSInspectionConfigurationArn,
			Extra:                           policyExtra(&d),
		})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":            p.UpdateToken,
		"FirewallPolicyResponse": toPolicyResponseJSON(p),
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

type listRequest struct {
	MaxResults int    `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

func (l listRequest) options() nfdriver.ListOptions {
	return nfdriver.ListOptions{MaxResults: l.MaxResults, NextToken: l.NextToken}
}

func (h *Handler) listFirewallPolicies(w http.ResponseWriter, r *http.Request) {
	var req listRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	items, next, err := h.db.ListFirewallPolicies(r.Context(), req.options())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"Name": items[i].Name, "Arn": items[i].ARN})
	}

	wire.WriteJSON(w, withNextToken(map[string]any{"FirewallPolicies": metas}, next))
}

func toPolicyResponseJSON(p *nfdriver.FirewallPolicy) firewallPolicyResponseJSON {
	modified := epochSeconds(p.LastModified)

	return firewallPolicyResponseJSON{
		FirewallPolicyName: p.Name, FirewallPolicyArn: p.ARN, FirewallPolicyID: p.ID,
		Description: p.Description, FirewallPolicyStatus: p.Status,
		NumberOfAssociations:           p.NumberOfAssociations,
		ConsumedStatelessRuleCapacity:  p.ConsumedStatelessRuleCapacity,
		ConsumedStatefulRuleCapacity:   p.ConsumedStatefulRuleCapacity,
		ConsumedStatefulDomainCapacity: p.ConsumedStatefulDomainCapacity,
		LastModifiedTime:               &modified, Tags: mapToTags(p.Tags),
	}
}

func toPolicyDetailJSON(p *nfdriver.FirewallPolicy) firewallPolicyDetailJSON {
	return firewallPolicyDetailJSON{
		StatelessDefaultActions:         p.StatelessDefaultActions,
		StatelessFragmentDefaultActions: p.StatelessFragmentDefaultActions,
		StatefulDefaultActions:          p.StatefulDefaultActions,
		StatefulRuleGroupReferences:     fromRuleGroupRefs(p.StatefulRuleGroupReferences),
		StatelessRuleGroupReferences:    fromRuleGroupRefs(p.StatelessRuleGroupReferences),
		TLSInspectionConfigurationArn:   p.TLSInspectionConfigurationARN,
		StatelessCustomActions:          p.Extra.StatelessCustomActions,
		PolicyVariables:                 p.Extra.PolicyVariables,
		StatefulEngineOptions:           p.Extra.StatefulEngineOptions,
		EnableTLSSessionHolding:         p.Extra.EnableTLSSessionHolding,
	}
}

func policyExtra(d *firewallPolicyDetailJSON) nfdriver.PolicyExtra {
	return nfdriver.PolicyExtra{
		StatelessCustomActions:  d.StatelessCustomActions,
		PolicyVariables:         d.PolicyVariables,
		StatefulEngineOptions:   d.StatefulEngineOptions,
		EnableTLSSessionHolding: d.EnableTLSSessionHolding,
	}
}

func toRuleGroupRefs(refs []ruleGroupReferenceJSON) []nfdriver.RuleGroupReference {
	if len(refs) == 0 {
		return nil
	}

	out := make([]nfdriver.RuleGroupReference, 0, len(refs))
	for _, r := range refs {
		out = append(out, nfdriver.RuleGroupReference{
			ResourceARN: r.ResourceArn, Priority: r.Priority,
			Override: r.Override, DeepThreatInspection: r.DeepThreatInspection,
		})
	}

	return out
}

func fromRuleGroupRefs(refs []nfdriver.RuleGroupReference) []ruleGroupReferenceJSON {
	if len(refs) == 0 {
		return nil
	}

	out := make([]ruleGroupReferenceJSON, 0, len(refs))
	for _, r := range refs {
		out = append(out, ruleGroupReferenceJSON{
			ResourceArn: r.ResourceARN, Priority: r.Priority,
			Override: r.Override, DeepThreatInspection: r.DeepThreatInspection,
		})
	}

	return out
}
