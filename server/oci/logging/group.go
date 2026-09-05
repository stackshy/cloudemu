package logging

import (
	"net/http"

	logprovider "github.com/stackshy/cloudemu/v2/providers/oci/logging"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveGroupCRUD maps method and path shape onto the log group operations.
func (h *Handler) serveGroupCRUD(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.ID == "" {
		switch r.Method {
		case http.MethodPost:
			h.createGroup(w, r)
		case http.MethodGet:
			h.listGroups(w, r)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getGroup(w, r, rt.ID)
	case http.MethodPut:
		h.updateGroup(w, r, rt.ID)
	case http.MethodDelete:
		h.deleteGroup(w, r, rt.ID)
	default:
		methodNotAllowed(w, r)
	}
}

// serveGroupAction serves the one action OCI defines on a log group.
func (h *Handler) serveGroupAction(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.SubID != actionChangeComp {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown action "+rt.SubID)
		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	h.moveGroup(w, r, rt.ID)
}

// createGroup creates a log group. Real OCI runs the mutation asynchronously,
// so it answers 202 with the work request a waiter polls.
func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	if !h.requireWork(w, r) {
		return
	}

	var req createLogGroupRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	g, err := h.extras.CreateGroup(r.Context(), logprovider.LogGroupSpec{
		CompartmentID: req.CompartmentID,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		FreeformTags:  req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, r, operationCreateGroup, g.CompartmentID, entityTypeGroup, workrequest.ActionCreated, g.ID)
}

// listGroups lists the log groups in a compartment.
func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	compartmentID, ok := ocirest.RequireCompartmentID(w, r)
	if !ok {
		return
	}

	groups, err := h.extras.ListGroups(r.Context(), compartmentID, r.URL.Query().Get("displayName"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]logGroupResponse, 0, len(groups))
	for i := range groups {
		out = append(out, toLogGroupResponse(&groups[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, out))
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request, id string) {
	g, err := h.extras.GetGroup(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toLogGroupResponse(g))
}

func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request, id string) {
	if !h.requireWork(w, r) {
		return
	}

	var req updateLogGroupRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	g, err := h.extras.UpdateGroup(r.Context(), id, logprovider.LogGroupUpdate{
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		FreeformTags: req.FreeformTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, r, operationUpdateGroup, g.CompartmentID, entityTypeGroup, workrequest.ActionUpdated, g.ID)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request, id string) {
	if !h.requireWork(w, r) {
		return
	}

	g, err := h.extras.GetGroup(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if err := h.extras.DeleteGroup(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, r, operationDeleteGroup, g.CompartmentID, entityTypeGroup, workrequest.ActionDeleted, id)
}

// moveGroup moves a log group between compartments.
func (h *Handler) moveGroup(w http.ResponseWriter, r *http.Request, id string) {
	if !h.requireWork(w, r) {
		return
	}

	var req changeCompartmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.TargetCompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "targetCompartmentId is required")
		return
	}

	if err := h.extras.MoveGroup(r.Context(), id, req.TargetCompartmentID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, r, operationMoveGroup, req.TargetCompartmentID, entityTypeGroup, workrequest.ActionUpdated, id)
}

func toLogGroupResponse(g *logprovider.LogGroup) logGroupResponse {
	return logGroupResponse{
		ID:               g.ID,
		CompartmentID:    g.CompartmentID,
		DisplayName:      g.DisplayName,
		Description:      g.Description,
		LifecycleState:   g.LifecycleState,
		TimeCreated:      g.TimeCreated,
		TimeLastModified: g.TimeLastModified,
		FreeformTags:     orEmptyTags(g.FreeformTags),
		DefinedTags:      definedTags{},
	}
}

// orEmptyTags keeps a tag map from serializing as null, which no OCI response
// does.
func orEmptyTags(tags map[string]string) map[string]string {
	if tags == nil {
		return map[string]string{}
	}

	return tags
}
