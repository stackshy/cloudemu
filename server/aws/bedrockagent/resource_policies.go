package bedrockagent

import (
	"net/http"
	"strings"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// prefixPolicy roots the resource-policy API: /resourcepolicy/{resourceArn}.
// VPC Lattice shares the path, so it is claimed only for bedrock
// knowledge-base ARNs.
const prefixPolicy = "/resourcepolicy/"

// ownsPolicyPath reports whether p is /resourcepolicy/{arn} for a bedrock
// knowledge-base ARN.
func ownsPolicyPath(p string) bool {
	arn, ok := strings.CutPrefix(p, prefixPolicy)
	if !ok {
		return false
	}

	parts := strings.SplitN(arn, ":", arnFields)

	return len(parts) == arnFields && parts[0] == "arn" && parts[2] == arnServiceBedrock &&
		strings.HasPrefix(parts[arnFields-1], "knowledge-base/")
}

type putPolicyRequest struct {
	Policy             string `json:"policy"`
	ExpectedRevisionID string `json:"expectedRevisionId"`
}

type policyResponse struct {
	ResourceArn string `json:"resourceArn"`
	Policy      string `json:"policy,omitempty"`
	RevisionID  string `json:"revisionId"`
}

// serveResourcePolicy handles PutResourcePolicy (PUT), GetResourcePolicy (GET)
// and DeleteResourcePolicy (DELETE ?expectedRevisionId=).
func (h *Handler) serveResourcePolicy(w http.ResponseWriter, r *http.Request) {
	c, ok := capabilityOf[badriver.ResourcePolicies](h.agent, w, "ResourcePolicy")
	if !ok {
		return
	}

	arn := strings.TrimPrefix(r.URL.Path, prefixPolicy)

	var (
		policy *badriver.ResourcePolicy
		err    error
	)

	switch r.Method {
	case http.MethodPut:
		var in putPolicyRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		policy, err = c.PutResourcePolicy(r.Context(), arn, in.Policy, in.ExpectedRevisionID)
	case http.MethodGet:
		policy, err = c.GetResourcePolicy(r.Context(), arn)
	case http.MethodDelete:
		policy, err = c.DeleteResourcePolicy(r.Context(), arn, r.URL.Query().Get("expectedRevisionId"))
		if err == nil {
			policy.Policy = ""
		}
	default:
		methodNotAllowed(w)

		return
	}

	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, policyResponse{ResourceArn: policy.ResourceARN, Policy: policy.Policy, RevisionID: policy.RevisionID})
}
