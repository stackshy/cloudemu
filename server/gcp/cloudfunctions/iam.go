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
	h.writeIamPolicy(w, p.fullName())
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, p functionPath) {
	h.storeIamPolicy(w, r, p.fullName())
}

// writeIamPolicy returns the policy stored under key, or an empty versioned
// policy when none is set (real GCP never 404s getIamPolicy on an existing
// resource). Shared by the v1 and v2 handlers.
func (h *Handler) writeIamPolicy(w http.ResponseWriter, key string) {
	h.mu.RLock()
	pol := h.policies[key]
	h.mu.RUnlock()

	if pol == nil {
		pol = &iamPolicy{Version: 1, Etag: policyEtag(key, 0)}
	}

	writeJSON(w, http.StatusOK, pol)
}

// storeIamPolicy persists the policy from the request body under key and echoes
// it back. Shared by the v1 and v2 handlers.
func (h *Handler) storeIamPolicy(w http.ResponseWriter, r *http.Request, key string) {
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

// policyEtag returns a stable etag for a policy state.
func policyEtag(resource string, n int) string {
	return base64.StdEncoding.EncodeToString([]byte(resource + ":" + strconv.Itoa(n)))
}

// serveTestIamPermissions answers functions/{name}:testIamPermissions (v1). Real
// GCP returns the subset of the requested permissions the caller holds; CloudEmu
// does not enforce IAM (any credential is an owner), so it echoes back the full
// requested set — the answer callers use to gate optional UI, and which
// Terraform's data.google_iam_policy tooling round-trips.
func (h *Handler) serveTestIamPermissions(w http.ResponseWriter, r *http.Request, p functionPath) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "testIamPermissions requires POST")
		return
	}

	if _, err := h.fn.GetFunction(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	var body testIamPermissionsRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	writeJSON(w, http.StatusOK, testIamPermissionsResponse(body))
}

// gen2Exists reports whether a gen2 function is stored under key.
func (h *Handler) gen2Exists(key string) bool {
	h.mu.RLock()
	_, ok := h.gen2[key]
	h.mu.RUnlock()

	return ok
}

// serveV2IamPolicy handles :getIamPolicy / :setIamPolicy on a gen2 function,
// sharing the same verbatim-policy store as v1 (CloudEmu does not enforce IAM;
// the policy round-trips so terraform google_cloudfunctions2_function_iam_member
// works).
func (h *Handler) serveV2IamPolicy(w http.ResponseWriter, r *http.Request, p v2Path) {
	key := p.fullName()
	if !h.gen2Exists(key) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	switch p.action {
	case actionGetIamPolicy:
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "getIamPolicy requires GET")
			return
		}

		h.writeIamPolicy(w, key)
	case actionSetIamPolicy:
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "setIamPolicy requires POST")
			return
		}

		h.storeIamPolicy(w, r, key)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown method: "+p.action)
	}
}

// serveV2TestIamPermissions echoes the requested permissions for a gen2 function.
func (h *Handler) serveV2TestIamPermissions(w http.ResponseWriter, r *http.Request, p v2Path) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "testIamPermissions requires POST")
		return
	}

	if !h.gen2Exists(p.fullName()) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "function "+p.name+" not found")
		return
	}

	var body testIamPermissionsRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	writeJSON(w, http.StatusOK, testIamPermissionsResponse(body))
}
