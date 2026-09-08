package binaryauthorization

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

func (h *Handler) getIamPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	pol, err := h.ba.GetIamPolicy(r.Context(), rt.attestorName())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyIAMJSON(pol))
}

func (h *Handler) setIamPolicy(w http.ResponseWriter, r *http.Request, rt route) {
	var req setIamPolicyRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	pol, err := h.ba.SetIamPolicy(r.Context(), rt.attestorName(), fromPolicyIAMJSON(req.Policy))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toPolicyIAMJSON(pol))
}

func (h *Handler) testIamPermissions(w http.ResponseWriter, r *http.Request, rt route) {
	var req testIamPermissionsRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	granted, err := h.ba.TestIamPermissions(r.Context(), rt.attestorName(), req.Permissions)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, testIamPermissionsResponse{Permissions: granted})
}
