package objectstorage

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	osprovider "github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// metaPrefix is the header prefix carrying an object's user metadata.
const metaPrefix = "opc-meta-"

// headerStorageTier is the per-object storage tier header.
const headerStorageTier = "storage-tier"

// maxObjectSize bounds a single PutObject body, so a runaway upload cannot
// exhaust the emulator's memory.
const maxObjectSize = 512 << 20

// serveObjects routes /o and /o/{object}.
func (h *Handler) serveObjects(w http.ResponseWriter, r *http.Request, rt *route) {
	if rt.Rest == "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, r)
			return
		}

		h.listObjects(w, r, rt.Bucket)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putObject(w, r, rt.Bucket, rt.Rest)
	case http.MethodGet:
		h.getObject(w, r, rt.Bucket, rt.Rest)
	case http.MethodHead:
		h.headObject(w, r, rt.Bucket, rt.Rest)
	case http.MethodDelete:
		h.deleteObject(w, r, rt.Bucket, rt.Rest)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) putObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	data, ok := readBody(w, r)
	if !ok {
		return
	}

	details, err := h.extras.PutObjectWith(r.Context(), bucket, object, data, osprovider.PutOptions{
		ContentType: r.Header.Get("Content-Type"),
		StorageTier: r.Header.Get(headerStorageTier),
		Metadata:    metadataFrom(r.Header),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	stampObjectHeaders(w, details)
	ocirest.WriteJSON(w, r, http.StatusOK, nil)
}

// readBody reads a request body, refusing one larger than maxObjectSize.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxObjectSize+1))
	if err != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "cannot read request body: "+err.Error())
		return nil, false
	}

	if len(data) > maxObjectSize {
		ocirest.WriteError(w, r, http.StatusRequestEntityTooLarge, codeInvalidParameter,
			"object exceeds the emulator's "+strconv.Itoa(maxObjectSize)+" byte limit")

		return nil, false
	}

	return data, true
}

func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	versionID := r.URL.Query().Get("versionId")

	obj, err := h.fetchObject(r, bucket, object, versionID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	details, detailsErr := h.extras.ObjectDetailsOf(r.Context(), bucket, object)
	if detailsErr == nil && versionID == "" {
		stampObjectHeaders(w, details)
	} else {
		stampInfoHeaders(w, &obj.Info)
	}

	writeRaw(w, r, obj.Info.ContentType, obj.Data)
}

// fetchObject reads the current object, or a specific version when the caller
// names one and the driver keeps history.
func (h *Handler) fetchObject(r *http.Request, bucket, object, versionID string) (*driver.Object, error) {
	if versionID == "" {
		return h.store.GetObject(r.Context(), bucket, object)
	}

	if h.versioned == nil {
		return nil, errVersioningUnsupported()
	}

	return h.versioned.GetObjectVersion(r.Context(), bucket, object, versionID)
}

func (h *Handler) headObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	versionID := r.URL.Query().Get("versionId")

	if versionID != "" {
		if h.versioned == nil {
			ocirest.WriteDriverError(w, r, errVersioningUnsupported())
			return
		}

		info, err := h.versioned.HeadObjectVersion(r.Context(), bucket, object, versionID)
		if err != nil {
			ocirest.WriteDriverError(w, r, err)
			return
		}

		stampInfoHeaders(w, info)
		ocirest.WriteJSON(w, r, http.StatusOK, nil)

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

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	versionID := r.URL.Query().Get("versionId")

	if versionID != "" || h.versioned != nil {
		h.deleteObjectVersion(w, r, bucket, object, versionID)
		return
	}

	if err := h.store.DeleteObject(r.Context(), bucket, object); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// deleteObjectVersion deletes through the versioned capability, reporting the
// delete marker OCI stamps when the bucket keeps history.
func (h *Handler) deleteObjectVersion(w http.ResponseWriter, r *http.Request, bucket, object, versionID string) {
	if h.versioned == nil {
		ocirest.WriteDriverError(w, r, errVersioningUnsupported())
		return
	}

	deleted, marker, err := h.versioned.DeleteObjectVersion(r.Context(), bucket, object, versionID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if deleted != "" {
		w.Header().Set("version-id", deleted)
	}

	if marker {
		w.Header().Set("is-delete-marker", "true")
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) listObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	opts := listOptions(r)

	objects, prefixes, next, err := h.extras.ListObjectDetails(r.Context(), bucket, opts)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := listObjectsBody{Objects: make([]objectSummaryBody, 0, len(objects)), Prefixes: prefixes}

	for i := range objects {
		o := &objects[i]
		out.Objects = append(out.Objects, objectSummaryBody{
			Name:         o.Name,
			Size:         o.Size,
			MD5:          o.MD5,
			ETag:         o.ETag,
			TimeCreated:  o.TimeCreated,
			TimeModified: o.TimeModified,
			StorageTier:  o.StorageTier,
		})
	}

	out.NextStartWith = next
	ocirest.SetNextPage(w, next)
	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

// listOptions reads OCI's list parameters. OCI names the page cursor "start"
// and the page size "limit".
func listOptions(r *http.Request) driver.ListOptions {
	q := r.URL.Query()

	return driver.ListOptions{
		Prefix:    q.Get("prefix"),
		Delimiter: q.Get("delimiter"),
		MaxKeys:   ocirest.Limit(r),
		PageToken: q.Get("start"),
	}
}

func (h *Handler) listObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	if h.versioned == nil {
		ocirest.WriteDriverError(w, r, errVersioningUnsupported())
		return
	}

	result, err := h.versioned.ListObjectVersions(r.Context(), bucket, listOptions(r))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := listObjectVersionsBody{
		Items:    make([]objectVersionBody, 0, len(result.Versions)),
		Prefixes: result.CommonPrefixes,
	}

	for i := range result.Versions {
		v := &result.Versions[i]
		out.Items = append(out.Items, objectVersionBody{
			Name:           v.Key,
			Size:           v.Size,
			ETag:           v.ETag,
			TimeModified:   v.LastModified,
			VersionID:      v.VersionID,
			IsDeleteMarker: v.DeleteMarker,
		})
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) renameObject(w http.ResponseWriter, r *http.Request, bucket string) {
	var req renameObjectBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	details, err := h.extras.RenameObject(r.Context(), bucket, req.SourceName, req.NewName)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	stampObjectHeaders(w, details)
	ocirest.WriteJSON(w, r, http.StatusOK, nil)
}

// copyObject serves the copy action. OCI runs a copy asynchronously, so the
// response is a 202 carrying the work request the caller polls.
func (h *Handler) copyObject(w http.ResponseWriter, r *http.Request, bucket string) {
	if h.work == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented, "work requests are not configured")
		return
	}

	var req copyObjectBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.SourceObjectName == "" || req.DestinationBucket == "" || req.DestinationObjectName == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"sourceObjectName, destinationBucket and destinationObjectName are required")

		return
	}

	if req.DestinationNamespace != "" && req.DestinationNamespace != h.extras.Namespace() {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"cross-namespace copy is not emulated; destinationNamespace must be "+h.extras.Namespace())

		return
	}

	err := h.store.CopyObject(r.Context(), req.DestinationBucket, req.DestinationObjectName, driver.CopySource{
		Bucket: bucket, Key: req.SourceObjectName,
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	id := h.work.Accept(operationCopy, h.extras.Scope(req.DestinationBucket).Compartment, workrequest.Resource{
		EntityType: "object",
		ActionType: workrequest.ActionCreated,
		Identifier: req.DestinationBucket + "/" + req.DestinationObjectName,
	})

	ocirest.SetWorkRequestID(w, id)
	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

func (h *Handler) updateStorageTier(w http.ResponseWriter, r *http.Request, bucket string) {
	var req updateTierBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.ObjectName == "" || req.StorageTier == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"objectName and storageTier are required")

		return
	}

	if err := h.extras.UpdateObjectStorageTier(r.Context(), bucket, req.ObjectName, req.StorageTier); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, nil)
}

// metadataFrom collects the opc-meta- headers into the object's user metadata.
func metadataFrom(header http.Header) map[string]string {
	var out map[string]string

	for name, values := range header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, metaPrefix) || len(values) == 0 {
			continue
		}

		if out == nil {
			out = make(map[string]string)
		}

		out[strings.TrimPrefix(lower, metaPrefix)] = values[0]
	}

	return out
}

func stampObjectHeaders(w http.ResponseWriter, d *osprovider.ObjectDetails) {
	w.Header().Set("ETag", d.ETag)
	w.Header().Set("last-modified", d.TimeModified)
	w.Header().Set(headerStorageTier, d.StorageTier)

	if d.MD5 != "" {
		w.Header().Set("opc-content-md5", d.MD5)
	}

	if d.VersionID != "" {
		w.Header().Set("version-id", d.VersionID)
	}

	for k, v := range d.Metadata {
		w.Header().Set(metaPrefix+k, v)
	}
}

func stampInfoHeaders(w http.ResponseWriter, info *driver.ObjectInfo) {
	w.Header().Set("ETag", info.ETag)
	w.Header().Set("last-modified", info.LastModified)

	if info.VersionID != "" {
		w.Header().Set("version-id", info.VersionID)
	}

	for k, v := range info.Metadata {
		w.Header().Set(metaPrefix+k, v)
	}
}

// writeRaw writes an object body, echoing the caller's opc-request-id the way
// ocirest's JSON helpers do.
func writeRaw(w http.ResponseWriter, r *http.Request, contentType string, data []byte) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	stampRequestID(w, r)
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck // best-effort response
}

func stampRequestID(w http.ResponseWriter, r *http.Request) {
	if w.Header().Get(ocirest.HeaderRequestID) != "" {
		return
	}

	if id := r.Header.Get(ocirest.HeaderRequestID); id != "" {
		w.Header().Set(ocirest.HeaderRequestID, id)
		return
	}

	w.Header().Set(ocirest.HeaderRequestID, idgen.GenerateID("cloudemu"))
}
