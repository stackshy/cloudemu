package compute

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// volumeTypeBlock is the portable volume type OCI's block volumes report.
const volumeTypeBlock = "block"

func (h *Handler) volumeOps() crud {
	return crud{
		create: h.createVolume,
		list:   h.listVolumes,
		get:    h.getVolume,
		update: h.updateVolume,
		remove: h.deleteVolume,
	}
}

func (h *Handler) createVolume(w http.ResponseWriter, r *http.Request) {
	var req volumeRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	size := req.SizeInGBs
	if size == 0 && req.SizeInMBs != 0 {
		size = req.SizeInMBs / 1024
	}

	cfg := computedriver.VolumeConfig{
		Size:             size,
		VolumeType:       volumeTypeBlock,
		AvailabilityZone: req.AvailabilityDomain,
		Tags:             withInternal(req.FreeformTags, tagDisplayName, req.DisplayName),
		IOPS:             req.VpusPerGB,
	}

	info, err := h.extras.CreateVolumeFrom(r.Context(), cfg, toSourceDetails(req.SourceDetails))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)
	h.accept(w, "CREATE_VOLUME", req.CompartmentID, "volume", workrequest.ActionCreated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeResponse(info))
}

func (h *Handler) listVolumes(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	infos, err := h.compute.DescribeVolumes(r.Context(), nil)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *computedriver.VolumeInfo) string { return v.ID },
		h.toVolumeResponse)
}

func (h *Handler) getVolume(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.findVolume(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeResponse(info))
}

func (h *Handler) updateVolume(w http.ResponseWriter, r *http.Request, id string) {
	var req volumeRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	info, err := h.extras.UpdateVolume(r.Context(), id,
		displayNameUpdate(req.DisplayName, req.FreeformTags), req.SizeInGBs, req.VpusPerGB)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeResponse(info))
}

func (h *Handler) deleteVolume(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.compute.DeleteVolume(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DELETE_VOLUME", compartmentID, "volume", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// findVolume reads one volume, reporting OCI's not-found for an unknown OCID.
func (h *Handler) findVolume(ctx context.Context, id string) (*computedriver.VolumeInfo, error) {
	infos, err := h.compute.DescribeVolumes(ctx, []string{id})
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "volume %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toVolumeResponse(info *computedriver.VolumeInfo) volumeResponse {
	source, vpus := h.extras.VolumeSource(info.ID)

	return volumeResponse{
		ID:                 info.ID,
		CompartmentID:      h.compartmentOf(info.ID),
		AvailabilityDomain: info.AvailabilityZone,
		DisplayName:        tagOr(info.Tags, tagDisplayName, ""),
		SizeInGBs:          info.Size,
		SizeInMBs:          gbsToMBs(info.Size),
		VpusPerGB:          vpus,
		SourceDetails:      toSourceDetailsWire(source),
		IsHydrated:         true,
		LifecycleState:     storageLifecycle(info.State),
		TimeCreated:        info.CreatedAt,
		FreeformTags:       freeformOf(info.Tags),
		DefinedTags:        definedTags{},
	}
}

func (h *Handler) volumeAttachmentOps() crud {
	return crud{
		create: h.attachVolume,
		list:   h.listVolumeAttachments,
		get:    h.getVolumeAttachment,
		remove: h.detachVolume,
	}
}

func (h *Handler) attachVolume(w http.ResponseWriter, r *http.Request) {
	var req volumeAttachmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.InstanceID == "" || req.VolumeID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"instanceId and volumeId are required")

		return
	}

	att, err := h.extras.AttachVolumeToInstance(r.Context(), ocicompute.VolumeAttachment{
		InstanceID:                     req.InstanceID,
		VolumeID:                       req.VolumeID,
		DisplayName:                    req.DisplayName,
		Device:                         req.Device,
		AttachmentType:                 req.Type,
		IsReadOnly:                     req.IsReadOnly,
		IsShareable:                    req.IsShareable,
		IsPVEncryptionInTransitEnabled: req.IsPvEncryptionInTransitEnabled,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	compartmentID := h.compartmentOf(req.InstanceID)
	h.place(att.ID, compartmentID)
	h.accept(w, "ATTACH_VOLUME", compartmentID, "volumeattachment", workrequest.ActionCreated, att.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeAttachmentResponse(att))
}

func (h *Handler) listVolumeAttachments(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	q := r.URL.Query()

	atts, err := h.extras.ListVolumeAttachments(r.Context(), compartmentID, q.Get("instanceId"), q.Get("volumeId"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, atts, h.toVolumeAttachmentResponse)
}

func (h *Handler) getVolumeAttachment(w http.ResponseWriter, r *http.Request, id string) {
	att, err := h.extras.GetVolumeAttachment(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVolumeAttachmentResponse(att))
}

func (h *Handler) detachVolume(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.extras.DetachVolumeAttachment(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DETACH_VOLUME", compartmentID, "volumeattachment", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) toVolumeAttachmentResponse(a *ocicompute.VolumeAttachment) volumeAttachmentResponse {
	return volumeAttachmentResponse{
		ID:                             a.ID,
		CompartmentID:                  h.compartmentOf(a.ID),
		AttachmentType:                 a.AttachmentType,
		AvailabilityDomain:             a.AvailabilityDomain,
		InstanceID:                     a.InstanceID,
		VolumeID:                       a.VolumeID,
		DisplayName:                    a.DisplayName,
		Device:                         a.Device,
		IsReadOnly:                     a.IsReadOnly,
		IsShareable:                    a.IsShareable,
		IsPvEncryptionInTransitEnabled: a.IsPVEncryptionInTransitEnabled,
		LifecycleState:                 a.LifecycleState,
		TimeCreated:                    a.TimeCreated,
	}
}
