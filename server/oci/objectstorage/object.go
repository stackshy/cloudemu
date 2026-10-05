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
const headerStorageTier = "Storage-Tier"

// defaultContentType is what OCI reports for an object stored without one.
const defaultContentType = "application/octet-stream"

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
		IfMatch:     r.Header.Get("If-Match"),
		IfNoneMatch: r.Header.Get("If-None-Match"),
	})
	if err != nil {
		writeDriverError(w, r, err)
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
		writeDriverError(w, r, err)
		return
	}

	etag := obj.Info.ETag

	details, detailsErr := h.extras.ObjectDetailsOf(r.Context(), bucket, object)
	if detailsErr == nil && versionID == "" {
		stampObjectHeaders(w, details)

		etag = details.ETag
	} else {
		stampInfoHeaders(w, &obj.Info)
	}

	if !readPreconditionsHold(w, r, etag) {
		return
	}

	w.Header().Set("Accept-Ranges", "bytes")

	if spec := r.Header.Get("Range"); spec != "" {
		writeRange(w, r, spec, obj.Info.ContentType, obj.Data)
		return
	}

	writeRaw(w, r, obj.Info.ContentType, obj.Data)
}

// readPreconditionsHold applies if-match and if-none-match to a read. A
// failed if-match is 412 IfMatchFailed; a matching if-none-match is 304, as
// HTTP and OCI's GetObject and HeadObject define it.
func readPreconditionsHold(w http.ResponseWriter, r *http.Request, etag string) bool {
	if ifMatch := r.Header.Get("If-Match"); ifMatch != "" && ifMatch != "*" && ifMatch != etag {
		ocirest.WriteError(w, r, http.StatusPreconditionFailed, osprovider.CodeIfMatchFailed,
			"the if-match ETag "+strconv.Quote(ifMatch)+" does not match the current ETag")

		return false
	}

	if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch != "" && (ifNoneMatch == "*" || ifNoneMatch == etag) {
		stampRequestID(w, r)
		w.WriteHeader(http.StatusNotModified)

		return false
	}

	return true
}

// writeRange answers a single byte range with 206 and Content-Range. OCI serves
// one range per request, so a multi-range header is refused rather than
// answered with the whole object.
func writeRange(w http.ResponseWriter, r *http.Request, spec, contentType string, data []byte) {
	size := int64(len(data))

	start, end, ok, multi := parseRange(spec, size)
	if multi {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"only a single byte range is supported, got "+strconv.Quote(spec))

		return
	}

	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		ocirest.WriteError(w, r, http.StatusRequestedRangeNotSatisfiable, codeInvalidRange,
			"range "+strconv.Quote(spec)+" is not satisfiable for an object of "+strconv.FormatInt(size, 10)+" bytes")

		return
	}

	if contentType == "" {
		contentType = defaultContentType
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+
		"/"+strconv.FormatInt(size, 10))
	stampRequestID(w, r)
	w.WriteHeader(http.StatusPartialContent)
	w.Write(data[start : end+1]) //nolint:errcheck // best-effort response
}

// parseRange reads a bytes=start-end, bytes=start- or bytes=-suffix header
// against an object of size bytes, clamping end to the last byte.
func parseRange(spec string, size int64) (start, end int64, ok, multi bool) {
	unit, set, found := strings.Cut(spec, "=")
	if !found || strings.TrimSpace(unit) != "bytes" {
		return 0, 0, false, false
	}

	if strings.Contains(set, ",") {
		return 0, 0, false, true
	}

	first, last, found := strings.Cut(strings.TrimSpace(set), "-")
	if !found || size == 0 {
		return 0, 0, false, false
	}

	if first == "" {
		start, end, ok = suffixRange(last, size)
		return start, end, ok, false
	}

	start, end, ok = boundedRange(first, last, size)

	return start, end, ok, false
}

// suffixRange reads bytes=-n: the last n bytes.
func suffixRange(last string, size int64) (start, end int64, ok bool) {
	n, err := strconv.ParseInt(last, 10, 64)
	if err != nil || n <= 0 {
		return 0, 0, false
	}

	return max(size-n, 0), size - 1, true
}

// boundedRange reads bytes=start- and bytes=start-end, clamping end.
func boundedRange(first, last string, size int64) (start, end int64, ok bool) {
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}

	if last == "" {
		return start, size - 1, true
	}

	end, err = strconv.ParseInt(last, 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}

	return start, min(end, size-1), true
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
			writeDriverError(w, r, errVersioningUnsupported())
			return
		}

		info, err := h.versioned.HeadObjectVersion(r.Context(), bucket, object, versionID)
		if err != nil {
			writeDriverError(w, r, err)
			return
		}

		stampInfoHeaders(w, info)

		if readPreconditionsHold(w, r, info.ETag) {
			writeHead(w, r, info.Size, info.ContentType)
		}

		return
	}

	details, err := h.extras.ObjectDetailsOf(r.Context(), bucket, object)
	if err != nil {
		writeDriverError(w, r, err)
		return
	}

	stampObjectHeaders(w, details)

	if readPreconditionsHold(w, r, details.ETag) {
		writeHead(w, r, details.Size, details.ContentType)
	}
}

// writeHead answers a HeadObject. The response carries no body, so the object's
// own size and type have to be reported in headers rather than inferred — which
// is why this does not go through ocirest.WriteJSON, whose application/json
// would overwrite the object's content type.
func writeHead(w http.ResponseWriter, r *http.Request, size int64, contentType string) {
	if contentType == "" {
		contentType = defaultContentType
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Accept-Ranges", "bytes")
	stampRequestID(w, r)
	w.WriteHeader(http.StatusOK)
}

// deleteObject deletes the current object, or one version, honoring
// if-match and reporting the delete marker OCI stamps when the bucket keeps
// history.
func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	versionID := r.URL.Query().Get("versionId")

	if versionID != "" && h.versioned == nil {
		writeDriverError(w, r, errVersioningUnsupported())
		return
	}

	deleted, marker, err := h.extras.DeleteObjectIf(r.Context(), bucket, object, versionID, r.Header.Get("If-Match"))
	if err != nil {
		writeDriverError(w, r, err)
		return
	}

	if deleted != "" {
		w.Header().Set("Version-Id", deleted)
	}

	if marker {
		w.Header().Set("Is-Delete-Marker", "true")
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) listObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	opts := listOptions(r)

	objects, prefixes, next, err := h.extras.ListObjectDetails(r.Context(), bucket, opts)
	if err != nil {
		writeDriverError(w, r, err)
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
		MaxKeys:   listLimit(r),
		PageToken: q.Get("start"),
	}
}

// listLimit is the requested page size, or zero when the caller named none.
// Object Storage's own default is 1000, not the 100 shared by the other OCI
// services, so an absent limit is left for the provider to fill in.
func listLimit(r *http.Request) int {
	if r.URL.Query().Get("limit") == "" {
		return 0
	}

	return ocirest.Limit(r)
}

func (h *Handler) listObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r)
		return
	}

	if h.versioned == nil {
		writeDriverError(w, r, errVersioningUnsupported())
		return
	}

	result, err := h.versioned.ListObjectVersions(r.Context(), bucket, listOptions(r))
	if err != nil {
		writeDriverError(w, r, err)
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

	writePageAs(w, r, out.Items, func(page []objectVersionBody) any {
		return listObjectVersionsBody{Items: page, Prefixes: out.Prefixes}
	})
}

func (h *Handler) renameObject(w http.ResponseWriter, r *http.Request, bucket string) {
	var req renameObjectBody

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	details, err := h.extras.RenameObject(r.Context(), bucket, req.SourceName, req.NewName)
	if err != nil {
		writeDriverError(w, r, err)
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

	if msg := h.copyDestinationProblem(&req); msg != "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, msg)
		return
	}

	err := h.extras.CopyObjectWith(r.Context(), osprovider.CopySpec{
		SourceBucket:           bucket,
		SourceObject:           req.SourceObjectName,
		SourceVersionID:        req.SourceVersionID,
		SourceIfMatch:          req.SourceObjectIfMatchETag,
		DestinationBucket:      req.DestinationBucket,
		DestinationObject:      req.DestinationObjectName,
		DestinationIfMatch:     req.DestinationObjectIfMatchETag,
		DestinationIfNoneMatch: req.DestinationObjectIfNoneMatchETag,
		Metadata:               req.DestinationObjectMetadata,
		StorageTier:            req.DestinationObjectStorageTier,
	})
	if err != nil {
		writeDriverError(w, r, err)
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

// copyDestinationProblem names what is wrong with a copy's required fields or
// its destination, or returns "" when the copy can proceed. A destination in
// another region or namespace is refused, not copied locally.
func (h *Handler) copyDestinationProblem(req *copyObjectBody) string {
	switch {
	case req.SourceObjectName == "" || req.DestinationRegion == "" ||
		req.DestinationBucket == "" || req.DestinationObjectName == "":
		return "sourceObjectName, destinationRegion, destinationBucket and destinationObjectName are required"
	case req.DestinationRegion != h.extras.Region():
		return "cross-region copy is not emulated; destinationRegion must be " + h.extras.Region()
	case req.DestinationNamespace != "" && req.DestinationNamespace != h.extras.Namespace():
		return "cross-namespace copy is not emulated; destinationNamespace must be " + h.extras.Namespace()
	default:
		return ""
	}
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
		writeDriverError(w, r, err)
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
	w.Header().Set("Last-Modified", d.TimeModified)
	w.Header().Set(headerStorageTier, d.StorageTier)

	if d.MD5 != "" {
		w.Header().Set("Opc-Content-Md5", d.MD5)
	}

	if d.VersionID != "" {
		w.Header().Set("Version-Id", d.VersionID)
	}

	for k, v := range d.Metadata {
		w.Header().Set(metaPrefix+k, v)
	}
}

func stampInfoHeaders(w http.ResponseWriter, info *driver.ObjectInfo) {
	w.Header().Set("ETag", info.ETag)
	w.Header().Set("Last-Modified", info.LastModified)

	if info.VersionID != "" {
		w.Header().Set("Version-Id", info.VersionID)
	}

	for k, v := range info.Metadata {
		w.Header().Set(metaPrefix+k, v)
	}
}

// writeRaw writes an object body, echoing the caller's opc-request-id the way
// ocirest's JSON helpers do.
func writeRaw(w http.ResponseWriter, r *http.Request, contentType string, data []byte) {
	if contentType == "" {
		contentType = defaultContentType
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
