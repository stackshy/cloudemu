package compute

import (
	"net/http"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

func (h *Handler) bootVolumeOps() crud {
	return crud{
		create: h.createBootVolume,
		list:   h.listBootVolumes,
		get:    h.getBootVolume,
		update: h.updateBootVolume,
		remove: h.deleteBootVolume,
	}
}

func (h *Handler) createBootVolume(w http.ResponseWriter, r *http.Request) {
	var req bootVolumeRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	source := toSourceDetails(req.SourceDetails)
	if source.SourceType == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"sourceDetails is required: a boot volume is cloned from one or restored from a backup")

		return
	}

	bv, err := h.extras.CreateBootVolume(r.Context(), ocicompute.BootVolume{
		AvailabilityDomain: req.AvailabilityDomain,
		DisplayName:        req.DisplayName,
		SizeInGBs:          req.SizeInGBs,
		VpusPerGB:          req.VpusPerGB,
		SourceDetails:      source,
		Tags:               freeformOf(req.FreeformTags),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(bv.ID, req.CompartmentID)
	h.accept(w, "CREATE_BOOT_VOLUME", req.CompartmentID, "bootvolume", workrequest.ActionCreated, bv.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toBootVolumeResponse(bv))
}

func (h *Handler) listBootVolumes(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	vols, err := h.extras.ListBootVolumes(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, vols, h.toBootVolumeResponse)
}

func (h *Handler) getBootVolume(w http.ResponseWriter, r *http.Request, id string) {
	bv, err := h.extras.GetBootVolume(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toBootVolumeResponse(bv))
}

func (h *Handler) updateBootVolume(w http.ResponseWriter, r *http.Request, id string) {
	var req bootVolumeRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	bv, err := h.extras.UpdateBootVolume(r.Context(), id,
		displayNameUpdate(req.DisplayName, req.FreeformTags), req.SizeInGBs, req.VpusPerGB)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toBootVolumeResponse(bv))
}

func (h *Handler) deleteBootVolume(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.extras.DeleteBootVolume(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DELETE_BOOT_VOLUME", compartmentID, "bootvolume", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) toBootVolumeResponse(bv *ocicompute.BootVolume) bootVolumeResponse {
	return bootVolumeResponse{
		ID:                 bv.ID,
		CompartmentID:      h.compartmentOf(bv.ID),
		AvailabilityDomain: bv.AvailabilityDomain,
		DisplayName:        bv.DisplayName,
		SizeInGBs:          bv.SizeInGBs,
		VpusPerGB:          bv.VpusPerGB,
		ImageID:            bv.ImageID,
		SourceDetails:      toSourceDetailsWire(bv.SourceDetails),
		VolumeGroupID:      bv.VolumeGroupID,
		IsHydrated:         bv.IsHydrated,
		LifecycleState:     bv.LifecycleState,
		TimeCreated:        bv.TimeCreated,
		FreeformTags:       freeformOf(bv.Tags),
		DefinedTags:        definedTags{},
	}
}

func (h *Handler) bootVolumeAttachmentOps() crud {
	return crud{
		create: h.attachBootVolume,
		list:   h.listBootVolumeAttachments,
		get:    h.getBootVolumeAttachment,
		remove: h.detachBootVolume,
	}
}

func (h *Handler) attachBootVolume(w http.ResponseWriter, r *http.Request) {
	var req bootVolumeAttachmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.InstanceID == "" || req.BootVolumeID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"instanceId and bootVolumeId are required")

		return
	}

	att, err := h.extras.AttachBootVolume(r.Context(), req.InstanceID, req.BootVolumeID, req.DisplayName)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	compartmentID := h.compartmentOf(req.InstanceID)
	h.place(att.ID, compartmentID)
	h.accept(w, "ATTACH_BOOT_VOLUME", compartmentID, "bootvolumeattachment", workrequest.ActionCreated, att.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toBootVolumeAttachmentResponse(att))
}

func (h *Handler) listBootVolumeAttachments(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	q := r.URL.Query()

	atts, err := h.extras.ListBootVolumeAttachments(r.Context(), compartmentID,
		q.Get("instanceId"), q.Get("bootVolumeId"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, atts, h.toBootVolumeAttachmentResponse)
}

func (h *Handler) getBootVolumeAttachment(w http.ResponseWriter, r *http.Request, id string) {
	att, err := h.extras.GetBootVolumeAttachment(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toBootVolumeAttachmentResponse(att))
}

func (h *Handler) detachBootVolume(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.extras.DetachBootVolume(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DETACH_BOOT_VOLUME", compartmentID, "bootvolumeattachment", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) toBootVolumeAttachmentResponse(a *ocicompute.BootVolumeAttachment) bootVolumeAttachmentResponse {
	return bootVolumeAttachmentResponse{
		ID:                 a.ID,
		CompartmentID:      h.compartmentOf(a.ID),
		AvailabilityDomain: a.AvailabilityDomain,
		InstanceID:         a.InstanceID,
		BootVolumeID:       a.BootVolumeID,
		DisplayName:        a.DisplayName,
		LifecycleState:     a.LifecycleState,
		TimeCreated:        a.TimeCreated,
	}
}
