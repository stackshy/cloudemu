package identity

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// routePolicy dispatches the /policies surface.
func (h *Handler) routePolicy(w http.ResponseWriter, r *http.Request, id string) {
	if h.policies == nil {
		capabilityMissing(w, r, kindPolicy)
		return
	}

	ops := collectionOps{
		kind:   kindPolicy,
		create: h.createPolicy,
		list:   h.listPolicies,
		get:    h.getPolicy,
		update: h.updatePolicy,
		remove: h.deletePolicy,
	}

	ops.route(w, r, id)
}

func (h *Handler) createPolicy(w http.ResponseWriter, r *http.Request) {
	var body createPolicyBody
	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	if !requireBodyCompartment(w, r, body.CompartmentID) {
		return
	}

	info, err := h.policies.CreateStatementPolicy(r.Context(), &iamdriver.PolicySpec{
		CompartmentID: body.CompartmentID,
		Name:          body.Name,
		Description:   body.Description,
		Statements:    body.Statements,
		FreeformTags:  body.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toPolicyResource(info))
}

func (h *Handler) listPolicies(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.policies.ListStatementPolicies(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]policyResource, 0, len(infos))
	for i := range infos {
		out = append(out, toPolicyResource(&infos[i]))
	}

	writeList(w, r, out)
}

func (h *Handler) getPolicy(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.policies.GetStatementPolicy(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toPolicyResource(info))
}

func (h *Handler) updatePolicy(w http.ResponseWriter, r *http.Request, id string) {
	var body updatePolicyBody
	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	info, err := h.policies.UpdateStatementPolicy(r.Context(), id, body.policyUpdate())
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toPolicyResource(info))
}

func (h *Handler) deletePolicy(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.policies.DeleteStatementPolicy(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}
