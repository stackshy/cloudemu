package secretmanager

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// getIamPolicy serves secrets/{id}:getIamPolicy. CloudEmu does not enforce IAM;
// it stores the policy so Terraform's google_secret_manager_secret_iam_* round-
// trips (setIamPolicy followed by a getIamPolicy read).
func (h *Handler) getIamPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	if h.gcp == nil {
		writeUnsupported(w)
		return
	}

	pol, err := h.gcp.GetSecretIAMPolicy(r.Context(), rt.secret)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyJSON(pol))
}

// setIamPolicy serves secrets/{id}:setIamPolicy.
func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	if h.gcp == nil {
		writeUnsupported(w)
		return
	}

	var req setIamPolicyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	pol, err := h.gcp.SetSecretIAMPolicy(r.Context(), rt.secret, fromPolicyJSON(req.Policy))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyJSON(pol))
}

// testIamPermissions serves secrets/{id}:testIamPermissions.
func (h *Handler) testIamPermissions(w http.ResponseWriter, r *http.Request, rt route) {
	if h.gcp == nil {
		writeUnsupported(w)
		return
	}

	var req testIamPermissionsRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	granted, err := h.gcp.TestSecretIAMPermissions(r.Context(), rt.secret, req.Permissions)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, testIamPermissionsResponse{Permissions: granted})
}
