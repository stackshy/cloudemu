package eks

import (
	"net/http"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// Access entry JSON shapes. Field names match the aws-sdk-go-v2 EKS client.

type createAccessEntryRequest struct {
	PrincipalArn       string            `json:"principalArn"`
	KubernetesGroups   []string          `json:"kubernetesGroups,omitempty"`
	Username           string            `json:"username,omitempty"`
	Type               string            `json:"type,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	ClientRequestToken string            `json:"clientRequestToken,omitempty"`
}

// updateAccessEntryRequest uses pointers so an omitted field keeps the
// stored value while an explicit empty list clears the groups.
type updateAccessEntryRequest struct {
	KubernetesGroups   *[]string `json:"kubernetesGroups,omitempty"`
	Username           *string   `json:"username,omitempty"`
	ClientRequestToken string    `json:"clientRequestToken,omitempty"`
}

type accessEntryJSON struct {
	AccessEntryArn   string            `json:"accessEntryArn"`
	ClusterName      string            `json:"clusterName"`
	CreatedAt        float64           `json:"createdAt"`
	KubernetesGroups []string          `json:"kubernetesGroups"`
	ModifiedAt       float64           `json:"modifiedAt"`
	PrincipalArn     string            `json:"principalArn"`
	Tags             map[string]string `json:"tags"`
	Type             string            `json:"type"`
	Username         string            `json:"username"`
}

type accessEntryEnvelope struct {
	AccessEntry accessEntryJSON `json:"accessEntry"`
}

type listAccessEntriesResponse struct {
	AccessEntries []string `json:"accessEntries"`
	NextToken     string   `json:"nextToken,omitempty"`
}

type accessScopeJSON struct {
	Type       string   `json:"type"`
	Namespaces []string `json:"namespaces"`
}

type associateAccessPolicyRequest struct {
	PolicyArn   string          `json:"policyArn"`
	AccessScope accessScopeJSON `json:"accessScope"`
}

type associatedAccessPolicyJSON struct {
	PolicyArn    string          `json:"policyArn"`
	AccessScope  accessScopeJSON `json:"accessScope"`
	AssociatedAt float64         `json:"associatedAt"`
	ModifiedAt   float64         `json:"modifiedAt"`
}

type associateAccessPolicyResponse struct {
	ClusterName            string                     `json:"clusterName"`
	PrincipalArn           string                     `json:"principalArn"`
	AssociatedAccessPolicy associatedAccessPolicyJSON `json:"associatedAccessPolicy"`
}

type listAssociatedAccessPoliciesResponse struct {
	ClusterName              string                       `json:"clusterName"`
	PrincipalArn             string                       `json:"principalArn"`
	AssociatedAccessPolicies []associatedAccessPolicyJSON `json:"associatedAccessPolicies"`
	NextToken                string                       `json:"nextToken,omitempty"`
}

type accessPolicyJSON struct {
	Name string `json:"name"`
	Arn  string `json:"arn"`
}

type listAccessPoliciesResponse struct {
	AccessPolicies []accessPolicyJSON `json:"accessPolicies"`
	NextToken      string             `json:"nextToken,omitempty"`
}

// Routing.

// serveAccessEntriesCollection handles /clusters/{name}/access-entries.
func (h *Handler) serveAccessEntriesCollection(w http.ResponseWriter, r *http.Request, clusterName string) {
	switch r.Method {
	case http.MethodPost:
		h.createAccessEntry(w, r, clusterName)
	case http.MethodGet:
		h.listAccessEntries(w, r, clusterName)
	default:
		methodNotAllowed(w)
	}
}

// serveAccessEntry handles /clusters/{name}/access-entries/{principalArn}.
func (h *Handler) serveAccessEntry(w http.ResponseWriter, r *http.Request, clusterName, principalArn string) {
	switch r.Method {
	case http.MethodGet:
		h.describeAccessEntry(w, r, clusterName, principalArn)
	case http.MethodPost:
		h.updateAccessEntry(w, r, clusterName, principalArn)
	case http.MethodDelete:
		h.deleteAccessEntry(w, r, clusterName, principalArn)
	default:
		methodNotAllowed(w)
	}
}

// serveEntryPolicies handles
// /clusters/{name}/access-entries/{principalArn}/access-policies.
func (h *Handler) serveEntryPolicies(w http.ResponseWriter, r *http.Request, clusterName, principalArn string) {
	switch r.Method {
	case http.MethodPost:
		h.associateAccessPolicy(w, r, clusterName, principalArn)
	case http.MethodGet:
		h.listAssociatedAccessPolicies(w, r, clusterName, principalArn)
	default:
		methodNotAllowed(w)
	}
}

// Operations.

func (h *Handler) createAccessEntry(w http.ResponseWriter, r *http.Request, clusterName string) {
	var body createAccessEntryRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	e, err := h.eks.CreateAccessEntry(r.Context(), eksdriver.AccessEntryConfig{
		ClusterName:        clusterName,
		PrincipalArn:       body.PrincipalArn,
		Type:               body.Type,
		Username:           body.Username,
		KubernetesGroups:   body.KubernetesGroups,
		Tags:               body.Tags,
		ClientRequestToken: body.ClientRequestToken,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, accessEntryEnvelope{AccessEntry: toAccessEntryJSON(e)})
}

func (h *Handler) describeAccessEntry(w http.ResponseWriter, r *http.Request, clusterName, principalArn string) {
	e, err := h.eks.DescribeAccessEntry(r.Context(), clusterName, principalArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, accessEntryEnvelope{AccessEntry: toAccessEntryJSON(e)})
}

func (h *Handler) listAccessEntries(w http.ResponseWriter, r *http.Request, clusterName string) {
	arns, err := h.eks.ListAccessEntries(r.Context(), clusterName, r.URL.Query().Get("associatedPolicyArn"))
	if err != nil {
		writeErr(w, err)

		return
	}

	items, next, ok := paginateNames(w, r, arns)
	if !ok {
		return
	}

	writeJSON(w, listAccessEntriesResponse{AccessEntries: items, NextToken: next})
}

func (h *Handler) updateAccessEntry(w http.ResponseWriter, r *http.Request, clusterName, principalArn string) {
	var body updateAccessEntryRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	e, err := h.eks.UpdateAccessEntry(r.Context(), eksdriver.AccessEntryUpdate{
		ClusterName:      clusterName,
		PrincipalArn:     principalArn,
		KubernetesGroups: body.KubernetesGroups,
		Username:         body.Username,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, accessEntryEnvelope{AccessEntry: toAccessEntryJSON(e)})
}

func (h *Handler) deleteAccessEntry(w http.ResponseWriter, r *http.Request, clusterName, principalArn string) {
	if err := h.eks.DeleteAccessEntry(r.Context(), clusterName, principalArn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) associateAccessPolicy(w http.ResponseWriter, r *http.Request, clusterName, principalArn string) {
	var body associateAccessPolicyRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	scope := eksdriver.AccessScope{Type: body.AccessScope.Type, Namespaces: body.AccessScope.Namespaces}

	p, err := h.eks.AssociateAccessPolicy(r.Context(), clusterName, principalArn, body.PolicyArn, scope)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, associateAccessPolicyResponse{
		ClusterName:            clusterName,
		PrincipalArn:           principalArn,
		AssociatedAccessPolicy: toAssociatedPolicyJSON(p),
	})
}

func (h *Handler) disassociateAccessPolicy(
	w http.ResponseWriter, r *http.Request, clusterName, principalArn, policyArn string,
) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)

		return
	}

	if err := h.eks.DisassociateAccessPolicy(r.Context(), clusterName, principalArn, policyArn); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listAssociatedAccessPolicies(w http.ResponseWriter, r *http.Request, clusterName, principalArn string) {
	policies, err := h.eks.ListAssociatedAccessPolicies(r.Context(), clusterName, principalArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	page, next, ok := paginateItems(w, r, policies, func(p *eksdriver.AssociatedAccessPolicy) string { return p.PolicyArn })
	if !ok {
		return
	}

	out := make([]associatedAccessPolicyJSON, 0, len(page))
	for i := range page {
		out = append(out, toAssociatedPolicyJSON(&page[i]))
	}

	writeJSON(w, listAssociatedAccessPoliciesResponse{
		ClusterName:              clusterName,
		PrincipalArn:             principalArn,
		AssociatedAccessPolicies: out,
		NextToken:                next,
	})
}

func (h *Handler) listAccessPolicies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	policies, err := h.eks.ListAccessPolicies(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	page, next, ok := paginateItems(w, r, policies, func(p *eksdriver.AccessPolicy) string { return p.Name })
	if !ok {
		return
	}

	out := make([]accessPolicyJSON, 0, len(page))
	for _, p := range page {
		out = append(out, accessPolicyJSON{Name: p.Name, Arn: p.ARN})
	}

	writeJSON(w, listAccessPoliciesResponse{AccessPolicies: out, NextToken: next})
}

// Conversion helpers.

func toAccessEntryJSON(e *eksdriver.AccessEntry) accessEntryJSON {
	groups := e.KubernetesGroups
	if groups == nil {
		groups = []string{}
	}

	tags := e.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	return accessEntryJSON{
		AccessEntryArn:   e.ARN,
		ClusterName:      e.ClusterName,
		CreatedAt:        epochSeconds(e.CreatedAt),
		KubernetesGroups: groups,
		ModifiedAt:       epochSeconds(e.ModifiedAt),
		PrincipalArn:     e.PrincipalArn,
		Tags:             tags,
		Type:             e.Type,
		Username:         e.Username,
	}
}

func toAssociatedPolicyJSON(p *eksdriver.AssociatedAccessPolicy) associatedAccessPolicyJSON {
	namespaces := p.AccessScope.Namespaces
	if namespaces == nil {
		namespaces = []string{}
	}

	return associatedAccessPolicyJSON{
		PolicyArn:    p.PolicyArn,
		AccessScope:  accessScopeJSON{Type: p.AccessScope.Type, Namespaces: namespaces},
		AssociatedAt: epochSeconds(p.AssociatedAt),
		ModifiedAt:   epochSeconds(p.ModifiedAt),
	}
}
