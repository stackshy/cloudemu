// Package gcs implements the Google Cloud Storage JSON REST API as a
// server.Handler. Real cloud.google.com/go/storage clients configured with a
// custom endpoint hit this handler the same way they hit
// storage.googleapis.com.
//
// Supported operations (parity with AWS S3):
//
//	POST   /storage/v1/b?project={p}                    — create bucket
//	GET    /storage/v1/b?project={p}                    — list buckets
//	GET    /storage/v1/b/{bucket}                       — get bucket
//	DELETE /storage/v1/b/{bucket}                       — delete bucket
//	POST   /upload/storage/v1/b/{bucket}/o?uploadType=media&name={obj}  — upload object
//	GET    /storage/v1/b/{bucket}/o                     — list objects
//	GET    /storage/v1/b/{bucket}/o/{obj}               — get object metadata
//	GET    /storage/v1/b/{bucket}/o/{obj}?alt=media     — download object
//	DELETE /storage/v1/b/{bucket}/o/{obj}               — delete object
//	POST   /storage/v1/b/{bucket}/o/{obj}/rewriteTo/b/{dst}/o/{dstObj}  — copy
//	POST   /storage/v1/b/{srcBucket}/o/{srcObj}/copyTo/b/{dst}/o/{dstObj}  — legacy copy
package gcs

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

const (
	contentTypeJSON   = "application/json"
	contentTypeBinary = "application/octet-stream"
	jsonAPIPrefix     = "/storage/v1/"
	uploadAPIPrefix   = "/upload/storage/v1/"

	// uploadTypeMedia is single-part raw-body upload.
	uploadTypeMedia = "media"
	// uploadTypeMultipart is JSON metadata + payload.
	uploadTypeMultipart = "multipart"

	// maxPutBodyBytes caps single-request uploads. Real GCS supports up to
	// 5 TiB but we use 5 GiB to mirror our S3 cap.
	maxPutBodyBytes = 5 << 30

	// pathBucketAndKey is the number of path segments in a /{bucket}/{key} URL.
	pathBucketAndKey = 2
	// pathBOObj is /b/{bucket}/o/{obj} = 4 segments under jsonAPIPrefix.
	pathBOObj = 4
	// pathBucket is /b/{bucket} = 2 segments.
	pathBucket = 2
	// pathBO is /b/{bucket}/o = 3 segments.
	pathBO = 3
)

// Handler serves GCS JSON REST requests against a storage.Bucket driver.
type Handler struct {
	bucket storagedriver.Bucket
}

// New returns a GCS handler backed by b.
func New(b storagedriver.Bucket) *Handler {
	return &Handler{bucket: b}
}

// Matches returns true for /storage/v1/, /upload/storage/v1/, and direct
// /{bucket}/{object} media URLs (used by Reader.NewRangeReader).
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	if strings.HasPrefix(p, jsonAPIPrefix) || strings.HasPrefix(p, uploadAPIPrefix) {
		return true
	}

	// Direct media URLs are /{bucket}/{object}. Two or more path segments
	// suffices.
	trimmed := strings.TrimPrefix(p, "/")
	parts := strings.SplitN(trimmed, "/", pathBucketAndKey)

	return len(parts) == pathBucketAndKey && parts[0] != "" && parts[1] != ""
}

// ServeHTTP routes the request based on URL path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, uploadAPIPrefix) {
		h.upload(w, r)
		return
	}

	// Direct media URLs (/{bucket}/{object}) — used by NewRangeReader for
	// downloads bypassing the JSON API.
	if !strings.HasPrefix(r.URL.Path, jsonAPIPrefix) {
		h.directMedia(w, r)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, jsonAPIPrefix)
	parts := strings.Split(rest, "/")

	if len(parts) == 0 || parts[0] != "b" {
		writeError(w, http.StatusNotFound, "notFound", "unknown collection")
		return
	}

	switch len(parts) {
	case 1:
		h.bucketCollection(w, r)
	case pathBucket:
		h.bucketResource(w, r, parts[1])
	case pathBO:
		// /b/{bucket}/o — list objects
		if parts[2] != "o" {
			writeError(w, http.StatusNotFound, "notFound", "unknown sub-collection")
			return
		}

		h.listObjects(w, r, parts[1])
	default:
		// /b/{bucket}/o/{obj}[/...]
		if parts[2] != "o" {
			writeError(w, http.StatusNotFound, "notFound", "unknown sub-collection")
			return
		}

		objAndRest := strings.Join(parts[3:], "/")
		h.objectOp(w, r, parts[1], objAndRest)
	}
}

func (h *Handler) bucketCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createBucket(w, r)
	case http.MethodGet:
		h.listBuckets(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

func (h *Handler) bucketResource(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.getBucket(w, r, name)
	case http.MethodDelete:
		h.deleteBucket(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}

	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid", "bucket name required")
		return
	}

	if err := h.bucket.CreateBucket(r.Context(), body.Name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, bucketResource{
		Kind:        "storage#bucket",
		ID:          body.Name,
		Name:        body.Name,
		SelfLink:    selfLink(r, "/storage/v1/b/"+body.Name),
		Location:    "US",
		TimeCreated: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := bucketsListResponse{Kind: "storage#buckets"}
	for _, b := range buckets {
		out.Items = append(out.Items, bucketResource{
			Kind:        "storage#bucket",
			ID:          b.Name,
			Name:        b.Name,
			SelfLink:    selfLink(r, "/storage/v1/b/"+b.Name),
			Location:    "US",
			TimeCreated: b.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getBucket(w http.ResponseWriter, r *http.Request, name string) {
	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	for _, b := range buckets {
		if b.Name == name {
			writeJSON(w, http.StatusOK, bucketResource{
				Kind:        "storage#bucket",
				ID:          b.Name,
				Name:        b.Name,
				SelfLink:    selfLink(r, "/storage/v1/b/"+b.Name),
				Location:    "US",
				TimeCreated: b.CreatedAt,
			})

			return
		}
	}

	writeError(w, http.StatusNotFound, "notFound", "bucket "+name+" not found")
}

func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.bucket.DeleteBucket(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// upload handles POST /upload/storage/v1/b/{bucket}/o?uploadType=media&name={obj}.
// We support uploadType=media (raw body) only.
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, uploadAPIPrefix)
	parts := strings.Split(rest, "/")

	if len(parts) < pathBO || parts[0] != "b" || parts[2] != "o" {
		writeError(w, http.StatusNotFound, "notFound", "unknown upload path")
		return
	}

	bucket := parts[1]
	q := r.URL.Query()

	uploadType := q.Get("uploadType")

	switch uploadType {
	case uploadTypeMedia:
		h.uploadMedia(w, r, bucket, q.Get("name"))
	case uploadTypeMultipart:
		h.uploadMultipart(w, r, bucket)
	default:
		writeError(w, http.StatusBadRequest, "invalid",
			"only uploadType=media or multipart supported (got "+uploadType+")")
	}
}

func (h *Handler) uploadMedia(w http.ResponseWriter, r *http.Request, bucket, name string) {
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid", "name query parameter required")
		return
	}

	limited := http.MaxBytesReader(w, r.Body, maxPutBodyBytes)

	data, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not read body")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = contentTypeBinary
	}

	if putErr := h.bucket.PutObject(r.Context(), bucket, name, data, contentType, nil); putErr != nil {
		writeErr(w, putErr)
		return
	}

	info, err := h.bucket.HeadObject(r.Context(), bucket, name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toObjectResource(info, bucket, r))
}

// uploadMultipart parses a multipart/related body where the first part is a
// JSON metadata object and the second part is the binary payload.
func (h *Handler) uploadMultipart(w http.ResponseWriter, r *http.Request, bucket string) {
	contentType := r.Header.Get("Content-Type")

	boundary := extractBoundary(contentType)
	if boundary == "" {
		writeError(w, http.StatusBadRequest, "invalid", "missing multipart boundary")
		return
	}

	limited := http.MaxBytesReader(w, r.Body, maxPutBodyBytes)

	raw, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not read body")
		return
	}

	meta, payload, payloadCT, ok := parseMultipart(raw, boundary)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "malformed multipart body")
		return
	}

	if meta.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid", "metadata.name required")
		return
	}

	if payloadCT == "" {
		payloadCT = contentTypeBinary
	}

	if putErr := h.bucket.PutObject(r.Context(), bucket, meta.Name, payload, payloadCT, meta.Metadata); putErr != nil {
		writeErr(w, putErr)
		return
	}

	info, err := h.bucket.HeadObject(r.Context(), bucket, meta.Name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toObjectResource(info, bucket, r))
}

func (h *Handler) listObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		return
	}

	q := r.URL.Query()

	maxResults := 1000

	if v := q.Get("maxResults"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxResults = n
		}
	}

	opts := storagedriver.ListOptions{
		Prefix:    q.Get("prefix"),
		Delimiter: q.Get("delimiter"),
		MaxKeys:   maxResults,
		PageToken: q.Get("pageToken"),
	}

	result, err := h.bucket.ListObjects(r.Context(), bucket, opts)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := objectsListResponse{
		Kind:          "storage#objects",
		Prefixes:      result.CommonPrefixes,
		NextPageToken: result.NextPageToken,
	}

	for _, obj := range result.Objects {
		out.Items = append(out.Items, toObjectResourceFromInfo(&obj, bucket, r))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) objectOp(w http.ResponseWriter, r *http.Request, bucket, objAndRest string) {
	// Detect rewriteTo / copyTo sub-resources, e.g. "k1/rewriteTo/b/dstb/o/dstk"
	parts := strings.Split(objAndRest, "/")

	for i, p := range parts {
		if p == "rewriteTo" || p == "copyTo" {
			obj := strings.Join(parts[:i], "/")
			h.copyObject(w, r, bucket, obj, parts[i+1:])

			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("alt") == "media" {
			h.downloadObject(w, r, bucket, objAndRest)
			return
		}

		h.getObjectMetadata(w, r, bucket, objAndRest)
	case http.MethodDelete:
		h.deleteObject(w, r, bucket, objAndRest)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

func (h *Handler) getObjectMetadata(w http.ResponseWriter, r *http.Request, bucket, key string) {
	info, err := h.bucket.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toObjectResource(info, bucket, r))
}

func (h *Handler) downloadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	obj, err := h.bucket.GetObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("Content-Type", obj.Info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(obj.Data)), 10))
	w.Header().Set("ETag", obj.Info.ETag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // raw object bytes
}

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if err := h.bucket.DeleteObject(r.Context(), bucket, key); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// directMedia handles direct /{bucket}/{object} URLs for media download.
func (h *Handler) directMedia(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/")

	parts := strings.SplitN(rest, "/", pathBucketAndKey)

	if len(parts) != pathBucketAndKey {
		writeError(w, http.StatusNotFound, "notFound", "malformed media path")
		return
	}

	bucket, key := parts[0], parts[1]

	switch r.Method {
	case http.MethodGet:
		h.downloadObject(w, r, bucket, key)
	case http.MethodHead:
		info, err := h.bucket.HeadObject(r.Context(), bucket, key)
		if err != nil {
			writeErr(w, err)
			return
		}

		w.Header().Set("Content-Type", info.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// copyObject handles /b/{src}/o/{srcObj}/{rewriteTo|copyTo}/b/{dst}/o/{dstObj}.
func (h *Handler) copyObject(w http.ResponseWriter, r *http.Request, srcBucket, srcKey string, tail []string) {
	if len(tail) < pathBOObj || tail[0] != "b" || tail[2] != "o" {
		writeError(w, http.StatusBadRequest, "invalid", "malformed copy target")
		return
	}

	dstBucket := tail[1]
	dstKey := strings.Join(tail[3:], "/")

	if err := h.bucket.CopyObject(r.Context(), dstBucket, dstKey, storagedriver.CopySource{
		Bucket: srcBucket, Key: srcKey,
	}); err != nil {
		writeErr(w, err)
		return
	}

	info, err := h.bucket.HeadObject(r.Context(), dstBucket, dstKey)
	if err != nil {
		writeErr(w, err)
		return
	}

	resource := toObjectResource(info, dstBucket, r)

	// rewriteTo response shape includes a "done" flag and the resource.
	if strings.Contains(r.URL.Path, "/rewriteTo/") {
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":                "storage#rewriteResponse",
			"totalBytesRewritten": strconv.FormatInt(info.Size, 10),
			"objectSize":          strconv.FormatInt(info.Size, 10),
			"done":                true,
			"resource":            resource,
		})

		return
	}

	writeJSON(w, http.StatusOK, resource)
}

func toObjectResource(info *storagedriver.ObjectInfo, bucket string, r *http.Request) objectResource {
	return objectResource{
		Kind:           "storage#object",
		ID:             bucket + "/" + info.Key + "/1",
		Name:           info.Key,
		Bucket:         bucket,
		Generation:     "1",
		Metageneration: "1",
		ContentType:    info.ContentType,
		Size:           strconv.FormatInt(info.Size, 10),
		ETag:           info.ETag,
		StorageClass:   "STANDARD",
		TimeCreated:    info.LastModified,
		Updated:        info.LastModified,
		Metadata:       info.Metadata,
		SelfLink:       selfLink(r, "/storage/v1/b/"+bucket+"/o/"+info.Key),
		MediaLink:      selfLink(r, "/storage/v1/b/"+bucket+"/o/"+info.Key+"?alt=media"),
	}
}

func toObjectResourceFromInfo(info *storagedriver.ObjectInfo, bucket string, r *http.Request) objectResource {
	return toObjectResource(info, bucket, r)
}

func selfLink(r *http.Request, suffix string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host + suffix
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxPutBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    status,
			Message: msg,
			Status:  reason,
			Errors: []errorDetail{{
				Domain:  "global",
				Reason:  reason,
				Message: msg,
			}},
		},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "notFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internalError", err.Error())
	}
}

// extractBoundary pulls the boundary= directive out of a Content-Type header.
func extractBoundary(ct string) string {
	for _, part := range strings.Split(ct, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "boundary=") {
			return strings.TrimPrefix(part, "boundary=")
		}
	}

	return ""
}

// uploadMetadata is the JSON metadata part of a multipart upload.
type uploadMetadata struct {
	Name        string            `json:"name"`
	ContentType string            `json:"contentType,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// parseMultipart extracts metadata, payload, and payload content-type from a
// multipart/related body. ok=false if either part is missing.
func parseMultipart(raw []byte, boundary string) (
	meta uploadMetadata, payload []byte, payloadContentType string, ok bool,
) {
	delim := []byte("--" + boundary)
	rawStr := string(raw)

	const minPartsForMetaAndPayload = 3

	parts := strings.Split(rawStr, string(delim))
	if len(parts) < minPartsForMetaAndPayload {
		return uploadMetadata{}, nil, "", false
	}

	// parts[0] is preamble (often empty), parts[1] is JSON metadata,
	// parts[2] is the payload.
	_, body := splitHeaderBody(parts[1])

	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &meta); err != nil {
		return uploadMetadata{}, nil, "", false
	}

	pHeaders, pBody := splitHeaderBody(parts[2])

	payloadContentType = lookupHeader(pHeaders, "Content-Type")
	if payloadContentType == "" {
		payloadContentType = meta.ContentType
	}

	// Strip trailing CRLF that precedes the next boundary.
	payload = []byte(strings.TrimRight(pBody, "\r\n-"))

	return meta, payload, payloadContentType, true
}

func splitHeaderBody(part string) (header, body string) {
	part = strings.TrimLeft(part, "\r\n")

	const headerBodySep = "\r\n\r\n"

	idx := strings.Index(part, headerBodySep)
	if idx < 0 {
		return "", part
	}

	return part[:idx], part[idx+len(headerBodySep):]
}

func lookupHeader(headers, key string) string {
	for _, line := range strings.Split(headers, "\r\n") {
		k, v, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		if strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v)
		}
	}

	return ""
}
