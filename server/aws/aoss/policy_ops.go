package aoss

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// policyOps binds the driver methods and wire-wrapper builders for one policy
// surface (security or access). Security and access policies share identical
// request/response shapes and logic, differing only in which driver methods run
// and the wrapper key ("securityPolicyDetail" vs "accessPolicyDetail"); binding
// those differences here lets a single set of handlers serve both.
type policyOps struct {
	create     func(context.Context, *driver.CreatePolicyInput) (*driver.Policy, error)
	get        func(context.Context, string, string) (*driver.Policy, error)
	list       func(context.Context, string, driver.Page) ([]driver.Policy, string, error)
	update     func(context.Context, *driver.UpdatePolicyInput) (*driver.Policy, error)
	del        func(context.Context, string, string) error
	wrapDetail func(policyDetailJSON) any
	wrapList   func([]policySummaryJSON, string) any
}

func (h *Handler) securityOps() policyOps {
	return policyOps{
		create: h.aoss.CreateSecurityPolicy,
		get:    h.aoss.GetSecurityPolicy,
		list:   h.aoss.ListSecurityPolicies,
		update: h.aoss.UpdateSecurityPolicy,
		del:    h.aoss.DeleteSecurityPolicy,
		wrapDetail: func(d policyDetailJSON) any {
			return securityPolicyDetailResponse{SecurityPolicyDetail: d}
		},
		wrapList: func(s []policySummaryJSON, next string) any {
			return listSecurityPoliciesResponse{SecurityPolicySummaries: s, NextToken: next}
		},
	}
}

func (h *Handler) accessOps() policyOps {
	return policyOps{
		create: h.aoss.CreateAccessPolicy,
		get:    h.aoss.GetAccessPolicy,
		list:   h.aoss.ListAccessPolicies,
		update: h.aoss.UpdateAccessPolicy,
		del:    h.aoss.DeleteAccessPolicy,
		wrapDetail: func(d policyDetailJSON) any {
			return accessPolicyDetailResponse{AccessPolicyDetail: d}
		},
		wrapList: func(s []policySummaryJSON, next string) any {
			return listAccessPoliciesResponse{AccessPolicySummaries: s, NextToken: next}
		},
	}
}

// registerPolicyRoutes wires the security-policy and access-policy operations.
func (h *Handler) registerPolicyRoutes() {
	h.routes["CreateSecurityPolicy"] = h.opCreate(h.securityOps)
	h.routes["GetSecurityPolicy"] = h.opGet(h.securityOps)
	h.routes["ListSecurityPolicies"] = h.opList(h.securityOps)
	h.routes["UpdateSecurityPolicy"] = h.opUpdate(h.securityOps)
	h.routes["DeleteSecurityPolicy"] = h.opDelete(h.securityOps)

	h.routes["CreateAccessPolicy"] = h.opCreate(h.accessOps)
	h.routes["GetAccessPolicy"] = h.opGet(h.accessOps)
	h.routes["ListAccessPolicies"] = h.opList(h.accessOps)
	h.routes["UpdateAccessPolicy"] = h.opUpdate(h.accessOps)
	h.routes["DeleteAccessPolicy"] = h.opDelete(h.accessOps)
}

type createPolicyRequest struct {
	ClientToken string `json:"clientToken"`
	Description string `json:"description"`
	Name        string `json:"name"`
	Policy      string `json:"policy"`
	Type        string `json:"type"`
}

type getPolicyRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type listPoliciesRequest struct {
	MaxResults int32    `json:"maxResults"`
	NextToken  string   `json:"nextToken"`
	Resource   []string `json:"resource"`
	Type       string   `json:"type"`
}

type updatePolicyRequest struct {
	ClientToken   string  `json:"clientToken"`
	Description   *string `json:"description"`
	Name          string  `json:"name"`
	Policy        *string `json:"policy"`
	PolicyVersion string  `json:"policyVersion"`
	Type          string  `json:"type"`
}

type deletePolicyRequest struct {
	ClientToken string `json:"clientToken"`
	Name        string `json:"name"`
	Type        string `json:"type"`
}

type securityPolicyDetailResponse struct {
	SecurityPolicyDetail policyDetailJSON `json:"securityPolicyDetail"`
}

type accessPolicyDetailResponse struct {
	AccessPolicyDetail policyDetailJSON `json:"accessPolicyDetail"`
}

type listSecurityPoliciesResponse struct {
	SecurityPolicySummaries []policySummaryJSON `json:"securityPolicySummaries"`
	NextToken               string              `json:"nextToken,omitempty"`
}

type listAccessPoliciesResponse struct {
	AccessPolicySummaries []policySummaryJSON `json:"accessPolicySummaries"`
	NextToken             string              `json:"nextToken,omitempty"`
}

// opCreate builds the create handler for a policy surface.
func (h *Handler) opCreate(ops func() policyOps) http.HandlerFunc {
	o := ops()

	return func(w http.ResponseWriter, r *http.Request) {
		dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *createPolicyRequest) (any, error) {
			doc, err := parsePolicyDocument(req.Policy)
			if err != nil {
				return nil, err
			}

			p, err := o.create(ctx, &driver.CreatePolicyInput{
				Type: req.Type, Name: req.Name, Description: req.Description, Policy: doc,
			})
			if err != nil {
				return nil, err
			}

			return o.wrapDetail(toPolicyDetail(p)), nil
		})
	}
}

// opGet builds the get handler for a policy surface.
func (h *Handler) opGet(ops func() policyOps) http.HandlerFunc {
	o := ops()

	return func(w http.ResponseWriter, r *http.Request) {
		dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *getPolicyRequest) (any, error) {
			p, err := o.get(ctx, req.Type, req.Name)
			if err != nil {
				return nil, err
			}

			return o.wrapDetail(toPolicyDetail(p)), nil
		})
	}
}

// opList builds the list handler for a policy surface.
func (h *Handler) opList(ops func() policyOps) http.HandlerFunc {
	o := ops()

	return func(w http.ResponseWriter, r *http.Request) {
		dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *listPoliciesRequest) (any, error) {
			policies, next, err := o.list(ctx, req.Type, driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
			if err != nil {
				return nil, err
			}

			summaries := make([]policySummaryJSON, 0, len(policies))
			for i := range policies {
				summaries = append(summaries, toPolicySummary(&policies[i]))
			}

			return o.wrapList(summaries, next), nil
		})
	}
}

// opUpdate builds the update handler for a policy surface.
func (h *Handler) opUpdate(ops func() policyOps) http.HandlerFunc {
	o := ops()

	return func(w http.ResponseWriter, r *http.Request) {
		dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *updatePolicyRequest) (any, error) {
			in := &driver.UpdatePolicyInput{
				Type: req.Type, Name: req.Name, Description: req.Description, PolicyVersion: req.PolicyVersion,
			}

			if req.Policy != nil {
				doc, err := parsePolicyDocument(*req.Policy)
				if err != nil {
					return nil, err
				}

				in.Policy = doc
			}

			p, err := o.update(ctx, in)
			if err != nil {
				return nil, err
			}

			return o.wrapDetail(toPolicyDetail(p)), nil
		})
	}
}

// opDelete builds the delete handler for a policy surface.
func (h *Handler) opDelete(ops func() policyOps) http.HandlerFunc {
	o := ops()

	return func(w http.ResponseWriter, r *http.Request) {
		dispatch(h, w, r, func(_ *Handler, ctx context.Context, req *deletePolicyRequest) (any, error) {
			if err := o.del(ctx, req.Type, req.Name); err != nil {
				return nil, err
			}

			return struct{}{}, nil
		})
	}
}

// parsePolicyDocument validates that a policy string is a JSON document and
// returns it compacted for stable storage. The request carries the policy as a
// string; the response returns it as a parsed JSON value.
func parsePolicyDocument(s string) (json.RawMessage, error) {
	if s == "" {
		return nil, &driver.APIError{
			Exception: driver.ExValidation,
			Err:       cerrors.New(cerrors.InvalidArgument, "policy is required"),
		}
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(s)); err != nil {
		return nil, &driver.APIError{
			Exception: driver.ExValidation,
			Err:       cerrors.New(cerrors.InvalidArgument, "policy is not valid JSON"),
		}
	}

	return json.RawMessage(compact.Bytes()), nil
}
