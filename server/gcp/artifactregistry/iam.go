package artifactregistry

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const (
	verbGetIamPolicy       = "getIamPolicy"
	verbSetIamPolicy       = "setIamPolicy"
	verbTestIamPermissions = "testIamPermissions"
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

type setIamPolicyRequest struct {
	Policy iamPolicy `json:"policy"`
}

type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

type testIamPermissionsResponse struct {
	Permissions []string `json:"permissions,omitempty"`
}

// serveIAM routes the repository IAM colon-verbs. CloudEmu does not enforce
// IAM; it stores the policy so setIamPolicy → getIamPolicy round-trips (the
// google_artifact_registry_repository_iam_* Terraform flow).
func (h *Handler) serveIAM(w http.ResponseWriter, r *http.Request, rt *route) {
	// All verbs act on an existing repository (real AR never serves IAM for a
	// repository that does not exist).
	if _, err := h.registry.GetRepository(r.Context(), rt.repository); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	switch rt.verb {
	case verbGetIamPolicy:
		h.getIamPolicy(w, r, rt)
	case verbSetIamPolicy:
		h.setIamPolicy(w, r, rt)
	case verbTestIamPermissions:
		h.testIamPermissions(w, r)
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unsupported verb "+rt.verb)
	}
}

func (h *Handler) getIamPolicy(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodGet {
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "getIamPolicy requires GET")
		return
	}

	key := repositoryResourceName(rt.project, rt.location, rt.repository)

	h.mu.RLock()
	pol := h.policies[key]
	h.mu.RUnlock()

	if pol == nil {
		// An existing repo with no policy yet returns an empty versioned policy
		// (real AR never 404s getIamPolicy on an existing resource).
		pol = &iamPolicy{Version: 1, Etag: policyEtag(key, 0)}
	}

	gcprest.WriteJSON(w, http.StatusOK, pol)
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, rt *route) {
	if r.Method != http.MethodPost {
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "setIamPolicy requires POST")
		return
	}

	var body setIamPolicyRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	pol := body.Policy
	if pol.Version == 0 {
		pol.Version = 1
	}

	key := repositoryResourceName(rt.project, rt.location, rt.repository)
	pol.Etag = policyEtag(key, len(pol.Bindings))

	h.mu.Lock()
	h.policies[key] = &pol
	h.mu.Unlock()

	gcprest.WriteJSON(w, http.StatusOK, &pol)
}

func (*Handler) testIamPermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "testIamPermissions requires POST")
		return
	}

	var body testIamPermissionsRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	// CloudEmu does not enforce IAM, so every requested permission is held: echo
	// the request set, matching real AR's "subset held" contract for an owner.
	gcprest.WriteJSON(w, http.StatusOK, testIamPermissionsResponse(body))
}

// policyEtag returns a stable etag for a policy state.
func policyEtag(resource string, n int) string {
	return base64.StdEncoding.EncodeToString([]byte(resource + ":" + strconv.Itoa(n)))
}
