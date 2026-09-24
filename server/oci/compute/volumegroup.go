package compute

import (
	"net/http"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// Volume group source types OCI defines.
const (
	groupSourceVolumeIDs = "volumeIds"
	groupSourceGroup     = "volumeGroupId"
)

func (h *Handler) volumeGroupOps() crud {
	return crud{
		create: h.createVolumeGroup,
		list:   h.listVolumeGroups,
		get:    h.getVolumeGroup,
		update: h.updateVolumeGroup,
		remove: h.deleteVolumeGroup,
	}
}

func (h *Handler) createVolumeGroup(w http.ResponseWriter, r *http.Request) {
	var req volumeGroupRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	spec, ok := groupSpec(w, r, &req)
	if !ok {
		return
	}

	g, err := h.extras.CreateVolumeGroup(r.Context(), spec)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(g.ID, req.CompartmentID)
	h.accept(w, "CREATE_VOLUME_GROUP", req.CompartmentID, "volumegroup", workrequest.ActionCreated, g.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeGroupResponse(g))
}

// groupSpec reads a volume group's source, rejecting a type CloudEmu does not
// model rather than silently creating an empty group.
func groupSpec(w http.ResponseWriter, r *http.Request, req *volumeGroupRequest) (ocicompute.VolumeGroup, bool) {
	spec := ocicompute.VolumeGroup{
		AvailabilityDomain: req.AvailabilityDomain,
		DisplayName:        req.DisplayName,
		VolumeIDs:          req.VolumeIDs,
		Tags:               freeformOf(req.FreeformTags),
	}

	if req.SourceDetails == nil {
		return spec, true
	}

	switch req.SourceDetails.Type {
	case groupSourceVolumeIDs, "":
		spec.VolumeIDs = req.SourceDetails.VolumeIDs
	case groupSourceGroup:
		spec.SourceType = "volumeGroup"
		spec.SourceID = req.SourceDetails.VolumeGroupID
	default:
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"volume group source type "+req.SourceDetails.Type+" is not emulated; "+
				"use volumeIds or volumeGroupId")

		return spec, false
	}

	return spec, true
}

func (h *Handler) listVolumeGroups(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	groups, err := h.extras.ListVolumeGroups(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, groups, h.toVolumeGroupResponse)
}

func (h *Handler) getVolumeGroup(w http.ResponseWriter, r *http.Request, id string) {
	g, err := h.extras.GetVolumeGroup(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeGroupResponse(g))
}

func (h *Handler) updateVolumeGroup(w http.ResponseWriter, r *http.Request, id string) {
	var req volumeGroupRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	g, err := h.extras.UpdateVolumeGroup(r.Context(), id,
		displayNameUpdate(req.DisplayName, req.FreeformTags), req.VolumeIDs)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeGroupResponse(g))
}

func (h *Handler) deleteVolumeGroup(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.extras.DeleteVolumeGroup(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DELETE_VOLUME_GROUP", compartmentID, "volumegroup", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) toVolumeGroupResponse(g *ocicompute.VolumeGroup) volumeGroupResponse {
	ids := g.VolumeIDs
	if ids == nil {
		ids = []string{}
	}

	return volumeGroupResponse{
		ID:                 g.ID,
		CompartmentID:      h.compartmentOf(g.ID),
		AvailabilityDomain: g.AvailabilityDomain,
		DisplayName:        g.DisplayName,
		SizeInGBs:          g.SizeInGBs,
		SizeInMBs:          gbsToMBs(g.SizeInGBs),
		VolumeIDs:          ids,
		LifecycleState:     g.LifecycleState,
		TimeCreated:        g.TimeCreated,
		FreeformTags:       freeformOf(g.Tags),
		DefinedTags:        definedTags{},
	}
}
