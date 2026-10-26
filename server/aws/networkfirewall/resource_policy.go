package networkfirewall

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type putResourcePolicyRequest struct {
	ResourceArn string `json:"ResourceArn"`
	Policy      string `json:"Policy"`
}

type resourceArnRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

func (h *Handler) putResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var req putResourcePolicyRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policies, ok := capability[nfdriver.ResourcePolicies](h, w)
	if !ok {
		return
	}

	if err := policies.PutResourcePolicy(r.Context(), req.ResourceArn, req.Policy); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) describeResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var req resourceArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policies, ok := capability[nfdriver.ResourcePolicies](h, w)
	if !ok {
		return
	}

	policy, err := policies.DescribeResourcePolicy(r.Context(), req.ResourceArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"Policy": policy})
}

func (h *Handler) deleteResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var req resourceArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	policies, ok := capability[nfdriver.ResourcePolicies](h, w)
	if !ok {
		return
	}

	if err := policies.DeleteResourcePolicy(r.Context(), req.ResourceArn); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}
