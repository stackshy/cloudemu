package objectstorage

import (
	"net/http"

	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// getNamespace serves GET /n. Real OCI returns the namespace as a bare JSON
// string.
func (h *Handler) getNamespace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.extras.Namespace())
}

// namespaceMetadata serves GET /n/{ns}.
func (h *Handler) namespaceMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	md := h.extras.Metadata(r.Context())
	ocirest.WriteJSON(w, r, http.StatusOK, namespaceMetadataBody{
		Namespace:                 md.Namespace,
		DefaultS3CompartmentID:    md.DefaultS3CompartmentID,
		DefaultSwiftCompartmentID: md.DefaultSwiftCompartmentID,
	})
}

func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request) {
	var req createBucketBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "name is required")
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	bkt, err := h.extras.CreateBucketWith(r.Context(), osprovider.BucketSpec{
		Name:                req.Name,
		CompartmentID:       req.CompartmentID,
		PublicAccessType:    req.PublicAccessType,
		StorageTier:         req.StorageTier,
		Versioning:          req.Versioning,
		KMSKeyID:            req.KMSKeyID,
		AutoTiering:         req.AutoTiering,
		ObjectEventsEnabled: req.ObjectEventsEnabled,
		Metadata:            req.Metadata,
		FreeformTags:        req.FreeformTags,
		DefinedTags:         req.DefinedTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	w.Header().Set("ETag", bkt.ETag)
	ocirest.WriteJSON(w, r, http.StatusOK, toBucketBody(bkt))
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	compartmentID, ok := ocirest.RequireCompartmentID(w, r)
	if !ok {
		return
	}

	buckets, err := h.extras.ListBucketsIn(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := make([]bucketSummaryBody, 0, len(buckets))

	for i := range buckets {
		b := &buckets[i]
		out = append(out, bucketSummaryBody{
			Namespace:     b.Namespace,
			Name:          b.Name,
			CompartmentID: b.CompartmentID,
			CreatedBy:     b.CreatedBy,
			TimeCreated:   b.TimeCreated,
			ETag:          b.ETag,
			FreeformTags:  b.FreeformTags,
			DefinedTags:   b.DefinedTags,
		})
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

// serveBucketItem serves GET/HEAD/POST/DELETE on one bucket.
func (h *Handler) serveBucketItem(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodGet:
		h.getBucket(w, r, bucket)
	case http.MethodHead:
		h.headBucket(w, r, bucket)
	case http.MethodPost:
		h.updateBucket(w, r, bucket)
	case http.MethodDelete:
		h.deleteBucket(w, r, bucket)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) getBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	bkt, err := h.extras.BucketDetails(r.Context(), bucket)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	w.Header().Set("ETag", bkt.ETag)
	ocirest.WriteJSON(w, r, http.StatusOK, toBucketBody(bkt))
}

func (h *Handler) headBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	bkt, err := h.extras.BucketDetails(r.Context(), bucket)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	w.Header().Set("ETag", bkt.ETag)
	ocirest.WriteJSON(w, r, http.StatusOK, nil)
}

func (h *Handler) updateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	var req updateBucketBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	bkt, err := h.extras.UpdateBucket(r.Context(), bucket, osprovider.BucketUpdate{
		CompartmentID:       req.CompartmentID,
		PublicAccessType:    req.PublicAccessType,
		Versioning:          req.Versioning,
		KMSKeyID:            req.KMSKeyID,
		AutoTiering:         req.AutoTiering,
		ObjectEventsEnabled: req.ObjectEventsEnabled,
		Metadata:            req.Metadata,
		FreeformTags:        req.FreeformTags,
		DefinedTags:         req.DefinedTags,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	w.Header().Set("ETag", bkt.ETag)
	ocirest.WriteJSON(w, r, http.StatusOK, toBucketBody(bkt))
}

func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := h.store.DeleteBucket(r.Context(), bucket); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func toBucketBody(b *osprovider.Bucket) bucketBody {
	return bucketBody{
		ID:                  b.ID,
		Namespace:           b.Namespace,
		Name:                b.Name,
		CompartmentID:       b.CompartmentID,
		CreatedBy:           b.CreatedBy,
		TimeCreated:         b.TimeCreated,
		ETag:                b.ETag,
		PublicAccessType:    b.PublicAccessType,
		StorageTier:         b.StorageTier,
		Versioning:          b.Versioning,
		KMSKeyID:            b.KMSKeyID,
		AutoTiering:         b.AutoTiering,
		ObjectEventsEnabled: b.ObjectEventsEnabled,
		ReplicationEnabled:  b.ReplicationEnabled,
		IsReadOnly:          b.IsReadOnly,
		Metadata:            b.Metadata,
		FreeformTags:        b.FreeformTags,
		DefinedTags:         b.DefinedTags,
		ApproximateCount:    b.ApproximateCount,
		ApproximateSize:     b.ApproximateSize,
	}
}
