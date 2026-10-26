package cloudwatchlogs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// dispatchPolicies handles the data protection and account policy
// operations, delegating tagging to dispatchTags.
func (h *Handler) dispatchPolicies(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "PutDataProtectionPolicy":
		h.putDataProtectionPolicy(w, r)
	case "GetDataProtectionPolicy":
		h.getDataProtectionPolicy(w, r)
	case "DeleteDataProtectionPolicy":
		h.deleteDataProtectionPolicy(w, r)
	case "PutAccountPolicy":
		h.putAccountPolicy(w, r)
	case "DescribeAccountPolicies":
		h.describeAccountPolicies(w, r)
	case "DeleteAccountPolicy":
		h.deleteAccountPolicy(w, r)
	default:
		h.dispatchTags(w, r, op)
	}
}

type dataProtectionPolicyRequest struct {
	LogGroupIdentifier string `json:"logGroupIdentifier"`
	PolicyDocument     string `json:"policyDocument"`
}

type dataProtectionPolicyResponse struct {
	LogGroupIdentifier string `json:"logGroupIdentifier,omitempty"`
	PolicyDocument     string `json:"policyDocument,omitempty"`
	LastUpdatedTime    int64  `json:"lastUpdatedTime,omitempty"`
}

func toDataProtectionResponse(p *logdriver.DataProtectionPolicy) dataProtectionPolicyResponse {
	return dataProtectionPolicyResponse{
		LogGroupIdentifier: p.LogGroupIdentifier,
		PolicyDocument:     p.Document,
		LastUpdatedTime:    epochMillis(p.LastUpdatedTime),
	}
}

func (h *Handler) putDataProtectionPolicy(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "PutDataProtectionPolicy")
	if !ok {
		return
	}

	var req dataProtectionPolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := ext.PutDataProtectionPolicy(r.Context(), req.LogGroupIdentifier, req.PolicyDocument)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toDataProtectionResponse(p))
}

func (h *Handler) getDataProtectionPolicy(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "GetDataProtectionPolicy")
	if !ok {
		return
	}

	var req dataProtectionPolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := ext.GetDataProtectionPolicy(r.Context(), req.LogGroupIdentifier)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, toDataProtectionResponse(p))
}

func (h *Handler) deleteDataProtectionPolicy(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DeleteDataProtectionPolicy")
	if !ok {
		return
	}

	var req dataProtectionPolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.DeleteDataProtectionPolicy(r.Context(), req.LogGroupIdentifier); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

type accountPolicyJSON struct {
	AccountID         string `json:"accountId"`
	LastUpdatedTime   int64  `json:"lastUpdatedTime"`
	PolicyDocument    string `json:"policyDocument"`
	PolicyName        string `json:"policyName"`
	PolicyType        string `json:"policyType"`
	Scope             string `json:"scope"`
	SelectionCriteria string `json:"selectionCriteria,omitempty"`
}

func toAccountPolicyJSON(p *logdriver.AccountPolicy) accountPolicyJSON {
	return accountPolicyJSON{
		AccountID:         p.AccountID,
		LastUpdatedTime:   epochMillis(p.LastUpdatedTime),
		PolicyDocument:    p.PolicyDocument,
		PolicyName:        p.PolicyName,
		PolicyType:        p.PolicyType,
		Scope:             p.Scope,
		SelectionCriteria: p.SelectionCriteria,
	}
}

func (h *Handler) putAccountPolicy(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "PutAccountPolicy")
	if !ok {
		return
	}

	var req struct {
		PolicyName        string `json:"policyName"`
		PolicyType        string `json:"policyType"`
		PolicyDocument    string `json:"policyDocument"`
		Scope             string `json:"scope"`
		SelectionCriteria string `json:"selectionCriteria"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := ext.PutAccountPolicy(r.Context(), &logdriver.PutAccountPolicyInput{
		PolicyName: req.PolicyName, PolicyType: req.PolicyType, PolicyDocument: req.PolicyDocument,
		Scope: req.Scope, SelectionCriteria: req.SelectionCriteria,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]accountPolicyJSON{"accountPolicy": toAccountPolicyJSON(p)})
}

// describeAccountPolicies returns every match on one page; an account holds
// a few dozen policies at most.
func (h *Handler) describeAccountPolicies(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DescribeAccountPolicies")
	if !ok {
		return
	}

	var req struct {
		PolicyType         string   `json:"policyType"`
		PolicyName         string   `json:"policyName"`
		AccountIdentifiers []string `json:"accountIdentifiers"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policies, err := ext.DescribeAccountPolicies(r.Context(), logdriver.DescribeAccountPoliciesInput{
		PolicyType: req.PolicyType, PolicyName: req.PolicyName, AccountIdentifiers: req.AccountIdentifiers,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]accountPolicyJSON, 0, len(policies))
	for i := range policies {
		out = append(out, toAccountPolicyJSON(&policies[i]))
	}

	wire.WriteJSON(w, map[string][]accountPolicyJSON{"accountPolicies": out})
}

func (h *Handler) deleteAccountPolicy(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DeleteAccountPolicy")
	if !ok {
		return
	}

	var req struct {
		PolicyName string `json:"policyName"`
		PolicyType string `json:"policyType"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.DeleteAccountPolicy(r.Context(), req.PolicyName, req.PolicyType); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}
