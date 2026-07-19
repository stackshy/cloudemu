// Package s3 implements the S3 REST+XML protocol as a server.Handler.
// Point the real aws-sdk-go-v2 S3 client at a Server registered with this
// handler and operations work against an in-memory storage driver.
package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	defaultMaxKeys = 1000
	xmlns          = "http://s3.amazonaws.com/doc/2006-03-01/"
	// maxPutObjectSize caps PutObject bodies at 5 GiB (S3 single-PUT limit).
	maxPutObjectSize = 5 << 30
)

// Handler serves S3 REST requests against a storage.Bucket driver.
type Handler struct {
	bucket driver.Bucket
}

// New returns an S3 handler backed by b.
func New(b driver.Bucket) *Handler {
	return &Handler{bucket: b}
}

// Matches returns true for requests that look like S3 REST calls: no
// X-Amz-Target header (that's JSON-RPC services like DynamoDB), no Action= in
// the URL, and no form-encoded body (that's query-protocol services like EC2).
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.URL.Query().Get("Action") != "" {
		return false
	}

	if r.Method == http.MethodPost &&
		strings.HasPrefix(r.Header.Get("Content-Type"),
			"application/x-www-form-urlencoded") {
		return false
	}

	return true
}

// ServeHTTP dispatches S3 REST requests based on method and path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key := parsePath(r.URL.Path)

	switch {
	case bucket == "":
		h.listBuckets(w, r)
	case key == "":
		h.bucketOp(w, r, bucket)
	default:
		h.objectOp(w, r, bucket, key)
	}
}

// parsePath extracts bucket and key from a path-style URL.
// "/bucket/key/with/slashes" returns ("bucket", "key/with/slashes").
func parsePath(path string) (bucket, key string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", ""
	}

	const bucketAndKey = 2
	parts := strings.SplitN(path, "/", bucketAndKey)
	bucket = parts[0]

	if len(parts) > 1 {
		key = parts[1]
	}

	return bucket, key
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	result := listAllMyBucketsResult{Xmlns: xmlns}
	for _, b := range buckets {
		result.Buckets = append(result.Buckets, bucketXML{
			Name: b.Name, CreationDate: b.CreatedAt,
		})
	}

	wire.WriteXML(w, http.StatusOK, result)
}

func (h *Handler) bucketOp(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()

	switch {
	case q.Has("versioning"):
		h.bucketVersioningOp(w, r, bucket)
		return
	case q.Has("uploads"):
		// GET /{bucket}?uploads => ListMultipartUploads.
		if r.Method == http.MethodGet {
			h.listMultipartUploads(w, r, bucket)
			return
		}
	case q.Has("versions"):
		// GET /{bucket}?versions => ListObjectVersions.
		if r.Method == http.MethodGet {
			h.listObjectVersions(w, r, bucket)
			return
		}
	}

	switch r.Method {
	case http.MethodPut:
		h.createBucket(w, r, bucket)
	case http.MethodDelete:
		h.deleteBucket(w, r, bucket)
	case http.MethodGet:
		h.listObjects(w, r, bucket)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := h.bucket.CreateBucket(r.Context(), bucket); err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := h.bucket.DeleteBucket(r.Context(), bucket); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	opts := driver.ListOptions{
		Prefix:    r.URL.Query().Get("prefix"),
		Delimiter: r.URL.Query().Get("delimiter"),
		PageToken: r.URL.Query().Get("continuation-token"),
	}

	result, err := h.bucket.ListObjects(r.Context(), bucket, opts)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := listBucketResult{
		Xmlns:       xmlns,
		Name:        bucket,
		Prefix:      opts.Prefix,
		Delimiter:   opts.Delimiter,
		MaxKeys:     defaultMaxKeys,
		IsTruncated: result.IsTruncated,
		KeyCount:    len(result.Objects),
	}

	if result.NextPageToken != "" {
		resp.NextContinuationToken = result.NextPageToken
	}

	for _, obj := range result.Objects {
		resp.Contents = append(resp.Contents, objectXML{
			Key:          obj.Key,
			LastModified: obj.LastModified,
			ETag:         fmt.Sprintf("%q", obj.ETag),
			Size:         int(obj.Size),
			StorageClass: "STANDARD",
		})
	}

	for _, p := range result.CommonPrefixes {
		resp.CommonPrefixes = append(resp.CommonPrefixes, prefixXML{Prefix: p})
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

func (h *Handler) objectOp(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()

	switch {
	case q.Has("tagging"):
		h.objectTaggingOp(w, r, bucket, key)
		return
	case q.Has("uploads"):
		// POST /{bucket}/{key}?uploads => CreateMultipartUpload.
		if r.Method == http.MethodPost {
			h.createMultipartUpload(w, r, bucket, key)
			return
		}
	case q.Has("uploadId"):
		h.multipartUploadOp(w, r, bucket, key, q.Get("uploadId"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			h.copyObject(w, r, bucket, key)
		} else {
			h.putObject(w, r, bucket, key)
		}
	case http.MethodGet:
		h.getObject(w, r, bucket, key)
	case http.MethodHead:
		h.headObject(w, r, bucket, key)
	case http.MethodDelete:
		h.deleteObject(w, r, bucket, key)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	limited := http.MaxBytesReader(w, r.Body, maxPutObjectSize)

	data, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "IncompleteBody", "could not read body")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	metadata := extractMetadata(r.Header)

	if err := h.bucket.PutObject(r.Context(), bucket, key, data, contentType, metadata); err != nil {
		writeErr(w, err)
		return
	}

	// Real S3 always returns the object's ETag on PutObject. Read it back
	// from the driver so there is a single source of truth for the ETag
	// algorithm.
	info, err := h.bucket.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("%q", info.ETag))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	obj, err := h.bucket.GetObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeObjectHeaders(w, &obj.Info, int64(len(obj.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // writing raw object bytes, not HTML
}

func (h *Handler) headObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	info, err := h.bucket.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeObjectHeaders(w, info, info.Size)
	w.WriteHeader(http.StatusOK)
}

func writeObjectHeaders(w http.ResponseWriter, info *driver.ObjectInfo, size int64) {
	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("ETag", fmt.Sprintf("%q", info.ETag))
	w.Header().Set("Last-Modified", wire.ToHTTPDate(info.LastModified))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))

	for k, v := range info.Metadata {
		w.Header().Set("X-Amz-Meta-"+k, v)
	}
}

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	err := h.bucket.DeleteObject(r.Context(), bucket, key)
	// Real S3 DeleteObject is idempotent: deleting a missing KEY succeeds
	// with 204. A missing BUCKET is still NoSuchBucket.
	if err != nil && (!cerrors.IsNotFound(err) || bucketMissing(err)) {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) copyObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	src := r.Header.Get("X-Amz-Copy-Source")
	src = strings.TrimPrefix(src, "/")

	srcBucket, srcKey := parsePath(src)
	if srcBucket == "" || srcKey == "" {
		writeError(w, http.StatusBadRequest, "InvalidArgument", "invalid copy source")
		return
	}

	if err := h.bucket.CopyObject(r.Context(), bucket, key, driver.CopySource{
		Bucket: srcBucket, Key: srcKey,
	}); err != nil {
		writeErr(w, err)
		return
	}

	obj, err := h.bucket.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, copyObjectResult{
		Xmlns:        xmlns,
		ETag:         fmt.Sprintf("%q", obj.ETag),
		LastModified: obj.LastModified,
	})
}

// multipartUploadOp dispatches operations on an in-progress multipart upload
// (those carrying an ?uploadId=... sub-resource).
func (h *Handler) multipartUploadOp(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	switch r.Method {
	case http.MethodPut:
		h.uploadPart(w, r, bucket, key, uploadID)
	case http.MethodPost:
		h.completeMultipartUpload(w, r, bucket, key, uploadID)
	case http.MethodDelete:
		h.abortMultipartUpload(w, r, bucket, key, uploadID)
	case http.MethodGet:
		h.listParts(w, r, bucket, key, uploadID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	mp, err := h.bucket.CreateMultipartUpload(r.Context(), bucket, key, contentType)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, initiateMultipartUploadResult{
		Xmlns:    xmlns,
		Bucket:   bucket,
		Key:      key,
		UploadID: mp.UploadID,
	})
}

func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	partNumber, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
	if err != nil || partNumber < 1 {
		writeError(w, http.StatusBadRequest, "InvalidArgument", "invalid partNumber")
		return
	}

	limited := http.MaxBytesReader(w, r.Body, maxPutObjectSize)

	data, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "IncompleteBody", "could not read body")
		return
	}

	part, err := h.bucket.UploadPart(r.Context(), bucket, key, uploadID, partNumber, data)
	if err != nil {
		writeMultipartErr(w, err)
		return
	}

	w.Header().Set("ETag", fmt.Sprintf("%q", part.ETag))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) completeMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	var req completeMultipartUpload
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not parse request body")
		return
	}

	if len(req.Parts) == 0 {
		writeError(w, http.StatusBadRequest, "MalformedXML", "the CompleteMultipartUpload request must contain at least one part")
		return
	}

	parts := make([]driver.UploadPart, 0, len(req.Parts))
	for _, p := range req.Parts {
		parts = append(parts, driver.UploadPart{
			PartNumber: p.PartNumber,
			ETag:       strings.Trim(p.ETag, `"`),
		})
	}

	if err := h.bucket.CompleteMultipartUpload(r.Context(), bucket, key, uploadID, parts); err != nil {
		writeMultipartErr(w, err)
		return
	}

	info, err := h.bucket.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, completeMultipartUploadResult{
		Xmlns:    xmlns,
		Location: "/" + bucket + "/" + key,
		Bucket:   bucket,
		Key:      key,
		ETag:     fmt.Sprintf("%q", info.ETag),
	})
}

func (h *Handler) abortMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	if err := h.bucket.AbortMultipartUpload(r.Context(), bucket, key, uploadID); err != nil {
		writeMultipartErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// listParts lists the parts uploaded so far for a multipart upload. The driver
// exposes uploads but not their individual parts, so this returns the upload's
// existence with an empty part list rather than fabricating part data.
func (h *Handler) listParts(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	uploads, err := h.bucket.ListMultipartUploads(r.Context(), bucket)
	if err != nil {
		writeErr(w, err)
		return
	}

	found := false
	for _, u := range uploads {
		if u.UploadID == uploadID {
			found = true
			break
		}
	}

	if !found {
		writeError(w, http.StatusNotFound, "NoSuchUpload", "the specified upload does not exist")
		return
	}

	wire.WriteXML(w, http.StatusOK, listPartsResult{
		Xmlns:       xmlns,
		Bucket:      bucket,
		Key:         key,
		UploadID:    uploadID,
		IsTruncated: false,
	})
}

func (h *Handler) listMultipartUploads(w http.ResponseWriter, r *http.Request, bucket string) {
	uploads, err := h.bucket.ListMultipartUploads(r.Context(), bucket)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := listMultipartUploadsResult{Xmlns: xmlns, Bucket: bucket}
	for _, u := range uploads {
		resp.Uploads = append(resp.Uploads, multipartUploadXML{
			Key:       u.Key,
			UploadID:  u.UploadID,
			Initiated: u.CreatedAt,
		})
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// objectTaggingOp dispatches PUT/GET/DELETE for the ?tagging sub-resource.
func (h *Handler) objectTaggingOp(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		h.putObjectTagging(w, r, bucket, key)
	case http.MethodGet:
		h.getObjectTagging(w, r, bucket, key)
	case http.MethodDelete:
		h.deleteObjectTagging(w, r, bucket, key)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	var body tagging
	if err := xml.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not parse request body")
		return
	}

	tags := make(map[string]string, len(body.TagSet))
	for _, t := range body.TagSet {
		tags[t.Key] = t.Value
	}

	if err := h.bucket.PutObjectTagging(r.Context(), bucket, key, tags); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	tags, err := h.bucket.GetObjectTagging(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := tagging{Xmlns: xmlns}
	// Sort keys for a deterministic response ordering.
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		resp.TagSet = append(resp.TagSet, tagXML{Key: k, Value: tags[k]})
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

func (h *Handler) deleteObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if err := h.bucket.DeleteObjectTagging(r.Context(), bucket, key); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// bucketVersioningOp dispatches PUT/GET for the ?versioning sub-resource.
func (h *Handler) bucketVersioningOp(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		h.putBucketVersioning(w, r, bucket)
	case http.MethodGet:
		h.getBucketVersioning(w, r, bucket)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	var body versioningConfiguration
	if err := xml.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not parse request body")
		return
	}

	enabled := body.Status == "Enabled"

	if err := h.bucket.SetBucketVersioning(r.Context(), bucket, enabled); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	enabled, err := h.bucket.GetBucketVersioning(r.Context(), bucket)
	if err != nil {
		writeErr(w, err)
		return
	}

	// The driver tracks versioning as a boolean only. Enabled reports "Enabled";
	// a disabled bucket returns an empty <VersioningConfiguration/> (matching a
	// never-versioned bucket) since "Suspended" vs. never-set isn't tracked.
	resp := versioningConfiguration{Xmlns: xmlns}
	if enabled {
		resp.Status = "Enabled"
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// listObjectVersions handles GET /{bucket}?versions. The storage driver tracks
// bucket-level versioning as a boolean flag only and does NOT retain per-object
// version history, so no versionId-addressable versions exist. We return the
// current objects as the sole (null) version rather than fabricating history.
func (h *Handler) listObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	opts := driver.ListOptions{
		Prefix:    r.URL.Query().Get("prefix"),
		Delimiter: r.URL.Query().Get("delimiter"),
	}

	result, err := h.bucket.ListObjects(r.Context(), bucket, opts)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := listVersionsResult{
		Xmlns:     xmlns,
		Name:      bucket,
		Prefix:    opts.Prefix,
		Delimiter: opts.Delimiter,
		MaxKeys:   defaultMaxKeys,
	}

	for _, obj := range result.Objects {
		resp.Versions = append(resp.Versions, objectVersionXML{
			Key:          obj.Key,
			VersionID:    "null",
			IsLatest:     true,
			LastModified: obj.LastModified,
			ETag:         fmt.Sprintf("%q", obj.ETag),
			Size:         obj.Size,
			StorageClass: "STANDARD",
		})
	}

	for _, p := range result.CommonPrefixes {
		resp.CommonPrefixes = append(resp.CommonPrefixes, prefixXML{Prefix: p})
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// extractMetadata pulls x-amz-meta-* headers into a map.
func extractMetadata(h http.Header) map[string]string {
	meta := make(map[string]string)

	for key, vals := range h {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-amz-meta-") && len(vals) > 0 {
			name := strings.TrimPrefix(lower, "x-amz-meta-")
			meta[name] = vals[0]
		}
	}

	if len(meta) == 0 {
		return nil
	}

	return meta
}

// writeError writes an S3-format XML error response.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	wire.WriteXML(w, status, errorXML{Code: code, Message: msg})
}

// writeErr maps CloudEmu errors to S3 HTTP error responses.
// writeMultipartErr maps a driver error from a multipart operation, where a
// missing resource is the upload (NoSuchUpload), not the object key.
func writeMultipartErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "NoSuchUpload", err.Error())
		return
	}
	writeErr(w, err)
}

// bucketMissing reports whether a NotFound error names the bucket itself
// (the driver formats bucket misses as `bucket ... not found`).
func bucketMissing(err error) bool {
	var ce *cerrors.Error
	return errors.As(err, &ce) && strings.HasPrefix(ce.Message, "bucket ")
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		// Real S3 distinguishes NoSuchBucket from NoSuchKey.
		if bucketMissing(err) {
			writeError(w, http.StatusNotFound, "NoSuchBucket", err.Error())
			return
		}
		writeError(w, http.StatusNotFound, "NoSuchKey", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "BucketAlreadyOwnedByYou", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidArgument", err.Error())
	case cerrors.IsFailedPrecondition(err):
		// Deleting a non-empty bucket is a client error in real S3, not a
		// server fault — and a 5xx would trigger SDK retry backoff.
		writeError(w, http.StatusConflict, "BucketNotEmpty", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
