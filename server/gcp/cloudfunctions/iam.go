package cloudfunctions

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

// setIamPolicyRequest is the body of functions/{name}:setIamPolicy.
type setIamPolicyRequest struct {
	Policy iamPolicy `json:"policy"`
}

// serveIamPolicy routes the :getIamPolicy (GET) and :setIamPolicy (POST) verbs
// on a function. CloudEmu does not enforce IAM; it stores the policy so that
// Terraform's google_cloudfunctions_function_iam_member (setIamPolicy) and its
// following read (getIamPolicy) round-trip.
func (h *Handler) serveIamPolicy(w http.ResponseWriter, r *http.Request, p functionPath) {
	// Both verbs act on an existing function (matching real GCP, which never
	// serves an IAM policy for a function that does not exist).
	if _, err := h.fn.GetFunction(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	switch p.action {
	case actionGetIamPolicy:
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "getIamPolicy requires GET")
			return
		}

		h.getIamPolicy(w, p)
	case actionSetIamPolicy:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "setIamPolicy requires POST")
			return
		}

		h.setIamPolicy(w, r, p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
	}
}

func (h *Handler) getIamPolicy(w http.ResponseWriter, p functionPath) {
	key := p.fullName()

	h.mu.RLock()
	pol := h.policies[key]
	h.mu.RUnlock()

	if pol == nil {
		// An existing function with no policy yet returns an empty, versioned
		// policy (real GCP never 404s getIamPolicy on an existing resource).
		pol = &iamPolicy{Version: 1, Etag: policyEtag(key, 0)}
	}

	writeJSON(w, http.StatusOK, pol)
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, p functionPath) {
	var body setIamPolicyRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	pol := body.Policy
	if pol.Version == 0 {
		pol.Version = 1
	}

	key := p.fullName()
	pol.Etag = policyEtag(key, len(pol.Bindings))

	h.mu.Lock()
	h.policies[key] = &pol
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, &pol)
}

// policyEtag returns a stable etag for a policy state.
func policyEtag(resource string, n int) string {
	return base64.StdEncoding.EncodeToString([]byte(resource + ":" + strconv.Itoa(n)))
}
