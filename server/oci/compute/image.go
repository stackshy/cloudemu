package compute

import (
	"net/http"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

func (h *Handler) imageOps() crud {
	return crud{
		create: h.createImage,
		list:   h.listImages,
		get:    h.getImage,
		update: h.updateImage,
		remove: h.deleteImage,
	}
}

func (h *Handler) createImage(w http.ResponseWriter, r *http.Request) {
	var req imageRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.InstanceID == "" {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"only instanceId-sourced images are emulated; imageSourceDetails imports from Object Storage")

		return
	}

	info, err := h.compute.CreateImage(r.Context(), computedriver.ImageConfig{
		InstanceID:  req.InstanceID,
		Name:        req.DisplayName,
		Description: req.DisplayName,
		Tags:        freeformOf(req.FreeformTags),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(info.ID, req.CompartmentID)

	img, err := h.extras.GetImage(r.Context(), info.ID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "CREATE_IMAGE", req.CompartmentID, "image", workrequest.ActionCreated, info.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, toImageResponse(img))
}

func (h *Handler) listImages(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	q := r.URL.Query()

	images, err := h.extras.ListImages(r.Context(), compartmentID,
		q.Get("operatingSystem"), q.Get("operatingSystemVersion"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, images, toImageResponse)
}

func (h *Handler) getImage(w http.ResponseWriter, r *http.Request, id string) {
	img, err := h.extras.GetImage(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toImageResponse(img))
}

func (h *Handler) updateImage(w http.ResponseWriter, r *http.Request, id string) {
	var req imageRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	img, err := h.extras.UpdateImage(r.Context(), id, displayNameUpdate(req.DisplayName, req.FreeformTags))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toImageResponse(img))
}

func (h *Handler) deleteImage(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.compute.DeregisterImage(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DELETE_IMAGE", compartmentID, "image", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func toImageResponse(img *ocicompute.Image) imageResponse {
	return imageResponse{
		ID:                     img.ID,
		CompartmentID:          img.CompartmentID,
		DisplayName:            img.DisplayName,
		OperatingSystem:        img.OperatingSystem,
		OperatingSystemVersion: img.OperatingSystemVersion,
		LaunchMode:             img.LaunchMode,
		SizeInMBs:              img.SizeInMBs,
		BaseImageID:            img.BaseImageID,
		CreateImageAllowed:     true,
		LifecycleState:         storageLifecycle(img.LifecycleState),
		TimeCreated:            img.TimeCreated,
		FreeformTags:           freeformOf(img.Tags),
		DefinedTags:            definedTags{},
	}
}
