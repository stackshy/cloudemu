package identity

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// routeMembership dispatches the /userGroupMemberships surface.
func (h *Handler) routeMembership(w http.ResponseWriter, r *http.Request, id string) {
	if h.identity == nil {
		capabilityMissing(w, r, kindMembership)
		return
	}

	ops := collectionOps{
		kind:   kindMembership,
		create: h.createMembership,
		list:   h.listMemberships,
		get:    h.getMembership,
		remove: h.deleteMembership,
	}

	ops.route(w, r, id)
}

func (h *Handler) createMembership(w http.ResponseWriter, r *http.Request) {
	var body createMembershipBody
	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	if body.UserID == "" || body.GroupID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "userId and groupId are required")
		return
	}

	info, err := h.identity.CreateOCIGroupMembership(r.Context(), body.UserID, body.GroupID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toMembershipResource(info))
}

// listMemberships lists one compartment's memberships, narrowed by the userId
// and groupId query parameters OCI accepts.
func (h *Handler) listMemberships(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	query := r.URL.Query()

	infos, err := h.identity.ListOCIGroupMemberships(r.Context(), compartmentID,
		query.Get("userId"), query.Get("groupId"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]membershipResource, 0, len(infos))
	for i := range infos {
		out = append(out, toMembershipResource(&infos[i]))
	}

	writeList(w, r, out)
}

func (h *Handler) getMembership(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.identity.GetOCIGroupMembership(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toMembershipResource(info))
}

func (h *Handler) deleteMembership(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.identity.DeleteOCIGroupMembership(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}
