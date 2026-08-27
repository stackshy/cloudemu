package cloudrun

import (
	"encoding/base64"
	"net/http"
	"strconv"
)

// iamPolicy is the GCP IAM Policy resource returned by getIamPolicy /
// setIamPolicy. Bindings are stored verbatim so a set/get round-trips.
type iamPolicy struct {
	Version  int             `json:"version,omitempty"`
	Bindings []policyBinding `json:"bindings,omitempty"`
	Etag     string          `json:"etag,omitempty"`
}

type policyBinding struct {
	Role    string   `json:"role"`
	Members []string `json:"members,omitempty"`
}

// setIamPolicyRequest is the body of {resource}:setIamPolicy.
type setIamPolicyRequest struct {
	Policy iamPolicy `json:"policy"`
}

// testIamPermissionsRequest is the body of {resource}:testIamPermissions.
type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

// existsFn reports whether the IAM target resource exists (returning the
// driver's NotFound error otherwise), so IAM verbs 404 like real GCP.
type existsFn func(r *http.Request, name string) error

// serveIam routes the IAM verbs (getIamPolicy / setIamPolicy /
// testIamPermissions) for a job or service. CloudEmu does not enforce IAM; the
// policy is stored so a set/get (and Terraform's *_iam_member read-back)
// round-trips. key is the resource's canonical name used both to 404 and to
// index the stored policy.
func (h *Handler) serveIam(w http.ResponseWriter, r *http.Request, p *crPath, key string, exists existsFn) {
	if err := exists(r, p.name); err != nil {
		writeErr(w, err)
		return
	}

	switch p.action {
	case actionGetIamPolicy:
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "getIamPolicy requires GET or POST")
			return
		}

		h.getIamPolicy(w, key)
	case actionSetIamPolicy:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "setIamPolicy requires POST")
			return
		}

		h.setIamPolicy(w, r, key)
	case actionTestIamPerms:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "testIamPermissions requires POST")
			return
		}

		testIamPermissions(w, r)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
	}
}

func (h *Handler) getIamPolicy(w http.ResponseWriter, key string) {
	h.mu.RLock()
	pol := h.policies[key]
	h.mu.RUnlock()

	if pol == nil {
		// An existing resource with no policy yet returns an empty, versioned
		// policy (real GCP never 404s getIamPolicy on an existing resource).
		pol = &iamPolicy{Version: 1, Etag: policyEtag(key, 0)}
	}

	writeJSON(w, http.StatusOK, pol)
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, key string) {
	var body setIamPolicyRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	pol := body.Policy
	if pol.Version == 0 {
		pol.Version = 1
	}

	pol.Etag = policyEtag(key, len(pol.Bindings))

	h.mu.Lock()
	h.policies[key] = &pol
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, &pol)
}

// testIamPermissions echoes back the requested permissions as held. CloudEmu
// does not enforce IAM, so the caller is treated as fully authorized — the
// subset returned is the full set requested, matching what a permitted
// principal sees on real GCP.
func testIamPermissions(w http.ResponseWriter, r *http.Request) {
	var body testIamPermissionsRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"permissions": body.Permissions})
}

// policyEtag returns a stable etag for a policy state.
func policyEtag(resource string, n int) string {
	return base64.StdEncoding.EncodeToString([]byte(resource + ":" + strconv.Itoa(n)))
}
