package codeartifact

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// putDomainPolicyRequest is the PutDomainPermissionsPolicy body: unlike the other
// domain operations it carries the domain in the body.
type putDomainPolicyRequest struct {
	Domain         string `json:"domain"`
	DomainOwner    string `json:"domainOwner"`
	PolicyRevision string `json:"policyRevision"`
	PolicyDocument string `json:"policyDocument"`
}

// putRepositoryPolicyRequest is the PutRepositoryPermissionsPolicy body.
type putRepositoryPolicyRequest struct {
	PolicyRevision string `json:"policyRevision"`
	PolicyDocument string `json:"policyDocument"`
}

func policyToWire(p *driver.ResourcePolicy) map[string]any {
	return map[string]any{"policy": map[string]any{
		"resourceArn": p.ResourceArn, "revision": p.Revision, "document": p.Document,
	}}
}

func (h *Handler) putDomainPolicy(w http.ResponseWriter, r *http.Request) {
	var req putDomainPolicyRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.policies.PutDomainPermissionsPolicy(r.Context(), driver.DomainRef{Domain: req.Domain, Owner: req.DomainOwner},
		req.PolicyRevision, req.PolicyDocument)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, policyToWire(out))
}

func (h *Handler) getDomainPolicy(w http.ResponseWriter, r *http.Request) {
	out, err := h.policies.GetDomainPermissionsPolicy(r.Context(), domainRefFromQuery(r.URL.Query()))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, policyToWire(out))
}

func (h *Handler) deleteDomainPolicy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.policies.DeleteDomainPermissionsPolicy(r.Context(), domainRefFromQuery(q), q.Get("policy-revision"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, policyToWire(out))
}

func (h *Handler) putRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	var req putRepositoryPolicyRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.policies.PutRepositoryPermissionsPolicy(r.Context(), repoRefFromQuery(r.URL.Query()), req.PolicyRevision, req.PolicyDocument)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, policyToWire(out))
}

func (h *Handler) getRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	out, err := h.policies.GetRepositoryPermissionsPolicy(r.Context(), repoRefFromQuery(r.URL.Query()))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, policyToWire(out))
}

func (h *Handler) deleteRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	out, err := h.policies.DeleteRepositoryPermissionsPolicy(r.Context(), repoRefFromQuery(q), q.Get("policy-revision"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, policyToWire(out))
}
