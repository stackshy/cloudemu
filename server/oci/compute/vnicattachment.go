package compute

import (
	"net/http"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

func (h *Handler) vnicAttachmentOps() crud {
	return crud{
		create: h.attachVNIC,
		list:   h.listVNICAttachments,
		get:    h.getVNICAttachment,
		remove: h.detachVNIC,
	}
}

func (h *Handler) attachVNIC(w http.ResponseWriter, r *http.Request) {
	var req vnicAttachmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.InstanceID == "" || req.CreateVnicDetails == nil || req.CreateVnicDetails.SubnetID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"instanceId and createVnicDetails.subnetId are required")

		return
	}

	vnic := req.CreateVnicDetails
	displayName := firstNonEmpty(req.DisplayName, vnic.DisplayName)

	att, err := h.extras.AttachVNIC(r.Context(), req.InstanceID, vnic.SubnetID,
		displayName, vnic.HostnameLabel, vnic.NsgIDs)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	compartmentID := h.compartmentOf(req.InstanceID)
	h.place(att.ID, compartmentID)
	h.accept(w, "ATTACH_VNIC", compartmentID, "vnicattachment", workrequest.ActionCreated, att.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toVNICAttachmentResponse(att))
}

func (h *Handler) listVNICAttachments(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	q := r.URL.Query()

	atts, err := h.extras.ListVNICAttachments(r.Context(), compartmentID, q.Get("instanceId"), q.Get("vnicId"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, atts, h.toVNICAttachmentResponse)
}

func (h *Handler) getVNICAttachment(w http.ResponseWriter, r *http.Request, id string) {
	att, err := h.extras.GetVNICAttachment(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toVNICAttachmentResponse(att))
}

func (h *Handler) detachVNIC(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.extras.DetachVNIC(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DETACH_VNIC", compartmentID, "vnicattachment", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) toVNICAttachmentResponse(a *ocicompute.VNICAttachment) vnicAttachmentResponse {
	return vnicAttachmentResponse{
		ID:                 a.ID,
		CompartmentID:      h.compartmentOf(a.ID),
		AvailabilityDomain: a.AvailabilityDomain,
		InstanceID:         a.InstanceID,
		VnicID:             a.VNICID,
		SubnetID:           a.SubnetID,
		DisplayName:        a.DisplayName,
		NicIndex:           a.NICIndex,
		LifecycleState:     a.LifecycleState,
		TimeCreated:        a.TimeCreated,
	}
}
