package identity

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// Compartment work request operation types.
const (
	opCreateCompartment = "CREATE_COMPARTMENT"
	opDeleteCompartment = "DELETE_COMPARTMENT"
)

// routeCompartment dispatches the /compartments surface.
func (h *Handler) routeCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if h.compartments == nil {
		capabilityMissing(w, r, kindCompartment)
		return
	}

	ops := collectionOps{
		kind:   kindCompartment,
		create: h.createCompartment,
		list:   h.listCompartments,
		get:    h.getCompartment,
		update: h.updateCompartment,
		remove: h.deleteCompartment,
	}

	ops.route(w, r, id)
}

func (h *Handler) createCompartment(w http.ResponseWriter, r *http.Request) {
	var body createCompartmentBody
	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	if !requireBodyCompartment(w, r, body.CompartmentID) {
		return
	}

	info, err := h.compartments.CreateCompartment(r.Context(), iamdriver.CompartmentSpec{
		ParentID:     body.CompartmentID,
		Name:         body.Name,
		Description:  body.Description,
		FreeformTags: body.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.recordWork(w, opCreateCompartment, info.ParentID, workrequest.ActionCreated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toCompartmentResource(info))
}

// listCompartments lists the children of the compartment named by
// compartmentId, descending the tree only when compartmentIdInSubtree is set —
// which is the one place OCI resolves ancestry rather than matching exactly.
func (h *Handler) listCompartments(w http.ResponseWriter, r *http.Request) {
	parentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	inSubtree, _ := strconv.ParseBool(r.URL.Query().Get("compartmentIdInSubtree"))

	infos, err := h.compartments.ListCompartments(r.Context(), parentID, inSubtree)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]compartmentResource, 0, len(infos))
	for i := range infos {
		out = append(out, toCompartmentResource(&infos[i]))
	}

	writeList(w, r, out)
}

func (h *Handler) getCompartment(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.compartments.GetCompartment(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toCompartmentResource(info))
}

func (h *Handler) updateCompartment(w http.ResponseWriter, r *http.Request, id string) {
	var body updateBody
	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	info, err := h.compartments.UpdateCompartment(r.Context(), id, body.identityUpdate())
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toCompartmentResource(info))
}

func (h *Handler) deleteCompartment(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.compartments.GetCompartment(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if err := h.compartments.DeleteCompartment(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.recordWork(w, opDeleteCompartment, info.ParentID, workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// recordWork stamps the work request SDK waiters poll after an asynchronous
// compartment mutation.
func (h *Handler) recordWork(w http.ResponseWriter, operation, compartmentID, action, id string) {
	if h.work == nil {
		return
	}

	ocirest.SetWorkRequestID(w, h.work.Accept(operation, compartmentID, workrequest.Resource{
		EntityType: kindCompartment,
		ActionType: action,
		Identifier: id,
	}))
}
