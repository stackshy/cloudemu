package objectstorage

import (
	"net/http"
	"strconv"

	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// serveUploads routes /u and /u/{object}.
func (h *Handler) serveUploads(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.Rest == "" {
		switch r.Method {
		case http.MethodPost:
			h.createUpload(w, r, rt.Bucket)
		case http.MethodGet:
			h.listUploads(w, r, rt.Bucket)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	if uploadID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "uploadId is required")
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.uploadPart(w, r, rt.Bucket, rt.Rest, uploadID)
	case http.MethodPost:
		h.commitUpload(w, r, rt.Bucket, rt.Rest, uploadID)
	case http.MethodGet:
		h.listParts(w, r, rt.Bucket, rt.Rest, uploadID)
	case http.MethodDelete:
		h.abortUpload(w, r, rt.Bucket, rt.Rest, uploadID)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) createUpload(w http.ResponseWriter, r *http.Request, bucket string) {
	var req createUploadBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Object == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "object is required")
		return
	}

	up, err := h.extras.CreateMultipartUploadWith(r.Context(), bucket, osprovider.MultipartUploadSpec{
		Object:      req.Object,
		ContentType: req.ContentType,
		StorageTier: req.StorageTier,
		Metadata:    req.Metadata,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, uploadBody{
		Namespace:   h.extras.Namespace(),
		Bucket:      bucket,
		Object:      up.Key,
		UploadID:    up.UploadID,
		TimeCreated: up.CreatedAt,
	})
}

func (h *Handler) listUploads(w http.ResponseWriter, r *http.Request, bucket string) {
	uploads, err := h.store.ListMultipartUploads(r.Context(), bucket)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]uploadBody, 0, len(uploads))
	namespace := h.extras.Namespace()

	for i := range uploads {
		up := &uploads[i]
		out = append(out, uploadBody{
			Namespace:   namespace,
			Bucket:      bucket,
			Object:      up.Key,
			UploadID:    up.UploadID,
			TimeCreated: up.CreatedAt,
		})
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request, bucket, object, uploadID string) {
	raw := r.URL.Query().Get("uploadPartNum")

	partNum, err := strconv.Atoi(raw)
	if err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"uploadPartNum must be an integer, got "+strconv.Quote(raw))

		return
	}

	data, ok := readBody(w, r)
	if !ok {
		return
	}

	part, err := h.store.UploadPart(r.Context(), bucket, object, uploadID, partNum, data)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	w.Header().Set("ETag", part.ETag)
	ocirest.WriteJSON(w, r, http.StatusOK, nil)
}

// commitUpload assembles the named parts. OCI's partsToExclude is rejected
// rather than dropped: excluding a part changes the object that results.
func (h *Handler) commitUpload(w http.ResponseWriter, r *http.Request, bucket, object, uploadID string) {
	var req commitUploadBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if len(req.PartsToExclude) > 0 {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"partsToExclude is not emulated; omit the parts from partsToCommit instead")

		return
	}

	if len(req.PartsToCommit) == 0 {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "partsToCommit is required")
		return
	}

	parts := make([]driver.UploadPart, 0, len(req.PartsToCommit))
	for _, p := range req.PartsToCommit {
		parts = append(parts, driver.UploadPart{PartNumber: p.PartNum, ETag: p.ETag})
	}

	if err := h.store.CompleteMultipartUpload(r.Context(), bucket, object, uploadID, parts); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	details, err := h.extras.ObjectDetailsOf(r.Context(), bucket, object)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	stampObjectHeaders(w, details)
	ocirest.WriteJSON(w, r, http.StatusOK, nil)
}

func (h *Handler) listParts(w http.ResponseWriter, r *http.Request, bucket, object, uploadID string) {
	parts, err := h.store.ListParts(r.Context(), bucket, object, uploadID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]partBody, 0, len(parts))
	for _, p := range parts {
		out = append(out, partBody{PartNumber: p.PartNumber, ETag: p.ETag, Size: p.Size})
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) abortUpload(w http.ResponseWriter, r *http.Request, bucket, object, uploadID string) {
	if err := h.store.AbortMultipartUpload(r.Context(), bucket, object, uploadID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}
