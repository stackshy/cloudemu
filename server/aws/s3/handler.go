// Package s3 implements the S3 REST+XML protocol as a server.Handler.
// Point the real aws-sdk-go-v2 S3 client at a Server registered with this
// handler and operations work against an in-memory storage driver.
package s3

import (
	"crypto/sha256"
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
	// maxUploadPartNumber is S3's upper bound on multipart part numbers.
	maxUploadPartNumber = 10000
)

// Handler serves S3 REST requests against a storage.Bucket driver.
type Handler struct {
	bucket driver.Bucket
	// versioned is set when the driver retains per-object version history; the
	// handler then honors versioning status, ?versionId, and ListObjectVersions.
	versioned driver.VersionedBucket
}

// New returns an S3 handler backed by b.
func New(b driver.Bucket) *Handler {
	h := &Handler{bucket: b}
	if vb, ok := b.(driver.VersionedBucket); ok {
		h.versioned = vb
	}

	return h
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
	case q.Has("tagging"):
		h.bucketTaggingOp(w, r, bucket)
		return
	case q.Has("versioning"):
		h.bucketVersioningOp(w, r, bucket)
		return
	case q.Has("uploads"):
		// GET /{bucket}?uploads => ListMultipartUploads. Any other method on the
		// sub-resource is rejected rather than falling through to create/delete
		// the bucket (which would ignore the ?uploads sub-resource entirely).
		if r.Method == http.MethodGet {
			h.listMultipartUploads(w, r, bucket)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed on ?uploads")
		return
	case q.Has("versions"):
		// GET /{bucket}?versions => ListObjectVersions (see note above re: fallthrough).
		if r.Method == http.MethodGet {
			h.listObjectVersions(w, r, bucket)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed on ?versions")
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createBucket(w, r, bucket)
	case http.MethodDelete:
		h.deleteBucket(w, r, bucket)
	case http.MethodGet:
		h.listObjects(w, r, bucket)
	case http.MethodHead:
		h.headBucket(w, r, bucket)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// headBucket answers HEAD /{bucket}: 200 if the bucket exists, 404 otherwise.
// It backs the SDK's HeadBucket / bucket-exists waiters.
func (h *Handler) headBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	for _, b := range buckets {
		if b.Name == bucket {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// HEAD carries no body, so the SDK infers NoSuchBucket from the 404 status.
	w.WriteHeader(http.StatusNotFound)
}

// bucketTaggingOp dispatches PUT/GET/DELETE for the bucket ?tagging
// sub-resource. Without this, a PUT ?tagging fell through to CreateBucket and
// failed with BucketAlreadyOwnedByYou.
func (h *Handler) bucketTaggingOp(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		var body tagging
		if err := xml.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "MalformedXML", "could not parse request body")
			return
		}

		tags := make(map[string]string, len(body.TagSet))
		for _, t := range body.TagSet {
			tags[t.Key] = t.Value
		}

		if err := h.bucket.PutBucketTagging(r.Context(), bucket, tags); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		tags, err := h.bucket.GetBucketTagging(r.Context(), bucket)
		if err != nil {
			writeErr(w, err)
			return
		}

		resp := tagging{Xmlns: xmlns}

		keys := make([]string, 0, len(tags))
		for k := range tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			resp.TagSet = append(resp.TagSet, tagXML{Key: k, Value: tags[k]})
		}

		wire.WriteXML(w, http.StatusOK, resp)
	case http.MethodDelete:
		if err := h.bucket.DeleteBucketTagging(r.Context(), bucket); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
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
	q := r.URL.Query()

	// A client-supplied continuation-token (ListObjectsV2) or marker
	// (ListObjects v1) both resume paging; accept either.
	pageToken := q.Get("continuation-token")
	if pageToken == "" {
		pageToken = q.Get("marker")
	}

	opts := driver.ListOptions{
		Prefix:    q.Get("prefix"),
		Delimiter: q.Get("delimiter"),
		PageToken: pageToken,
	}

	// max-keys caps the page; an absent or unparseable value leaves the driver
	// default in place. Previously ignored, so large buckets never truncated.
	maxKeys := defaultMaxKeys
	if v := q.Get("max-keys"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			opts.MaxKeys = n
			maxKeys = n
		}
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
		MaxKeys:     maxKeys,
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
		// POST /{bucket}/{key}?uploads => CreateMultipartUpload. Any other method
		// is rejected rather than falling through to a plain object PUT/GET/DELETE.
		if r.Method == http.MethodPost {
			h.createMultipartUpload(w, r, bucket, key)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed on ?uploads")
		return
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
	// algorithm; if a concurrent delete races the read-back, fall back to
	// computing it from the body we just stored — a successful PUT must
	// never answer 404.
	etag := fmt.Sprintf("%x", sha256.Sum256(data))

	var versionID string
	if info, err := h.bucket.HeadObject(r.Context(), bucket, key); err == nil {
		etag = info.ETag
		versionID = info.VersionID
	}
	w.Header().Set("ETag", fmt.Sprintf("%q", etag))
	if versionID != "" {
		w.Header().Set("x-amz-version-id", versionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	var (
		obj *driver.Object
		err error
	)
	if versionID := r.URL.Query().Get("versionId"); versionID != "" && h.versioned != nil {
		obj, err = h.versioned.GetObjectVersion(r.Context(), bucket, key, versionID)
	} else {
		obj, err = h.bucket.GetObject(r.Context(), bucket, key)
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	writeObjectHeaders(w, &obj.Info, int64(len(obj.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // writing raw object bytes, not HTML
}

func (h *Handler) headObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	var (
		info *driver.ObjectInfo
		err  error
	)
	if versionID := r.URL.Query().Get("versionId"); versionID != "" && h.versioned != nil {
		info, err = h.versioned.HeadObjectVersion(r.Context(), bucket, key, versionID)
	} else {
		info, err = h.bucket.HeadObject(r.Context(), bucket, key)
	}
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

	if info.VersionID != "" {
		w.Header().Set("x-amz-version-id", info.VersionID)
	}

	if info.DeleteMarker {
		w.Header().Set("x-amz-delete-marker", "true")
	}

	for k, v := range info.Metadata {
		w.Header().Set("X-Amz-Meta-"+k, v)
	}
}

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	versionID := r.URL.Query().Get("versionId")

	if h.versioned != nil {
		// Versioned driver: a top-level delete (no versionId) appends a delete
		// marker on an Enabled bucket; ?versionId permanently removes a version.
		vid, marker, err := h.versioned.DeleteObjectVersion(r.Context(), bucket, key, versionID)
		if err != nil && (!cerrors.IsNotFound(err) || bucketMissing(err)) {
			writeErr(w, err)
			return
		}
		if vid != "" {
			w.Header().Set("x-amz-version-id", vid)
		}
		if marker {
			w.Header().Set("x-amz-delete-marker", "true")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

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
	if err != nil || partNumber < 1 || partNumber > maxUploadPartNumber {
		writeError(w, http.StatusBadRequest, "InvalidArgument",
			"Part number must be an integer between 1 and 10000, inclusive")
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

// listParts lists the parts uploaded so far for a multipart upload, so
// resumable-upload tooling can read back the parts it has already sent.
func (h *Handler) listParts(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	parts, err := h.bucket.ListParts(r.Context(), bucket, key, uploadID)
	if err != nil {
		writeMultipartErr(w, err)
		return
	}

	result := listPartsResult{
		Xmlns:       xmlns,
		Bucket:      bucket,
		Key:         key,
		UploadID:    uploadID,
		IsTruncated: false,
	}
	for _, p := range parts {
		result.Parts = append(result.Parts, partXML{
			PartNumber: p.PartNumber,
			ETag:       fmt.Sprintf("%q", p.ETag),
			Size:       p.Size,
		})
	}

	wire.WriteXML(w, http.StatusOK, result)
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

	if body.Status != "Enabled" && body.Status != "Suspended" {
		writeError(w, http.StatusBadRequest, "IllegalVersioningConfigurationException",
			"the versioning status must be Enabled or Suspended")
		return
	}

	var err error
	if h.versioned != nil {
		err = h.versioned.SetVersioningStatus(r.Context(), bucket, body.Status)
	} else {
		err = h.bucket.SetBucketVersioning(r.Context(), bucket, body.Status == "Enabled")
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	// Status is "Enabled", "Suspended", or "" (never configured → empty
	// <VersioningConfiguration/>, matching real S3).
	var status string

	if h.versioned != nil {
		s, err := h.versioned.VersioningStatus(r.Context(), bucket)
		if err != nil {
			writeErr(w, err)
			return
		}
		status = s
	} else {
		enabled, err := h.bucket.GetBucketVersioning(r.Context(), bucket)
		if err != nil {
			writeErr(w, err)
			return
		}
		if enabled {
			status = "Enabled"
		}
	}

	wire.WriteXML(w, http.StatusOK, versioningConfiguration{Xmlns: xmlns, Status: status})
}

// listObjectVersions handles GET /{bucket}?versions. When the driver retains
// version history it returns the full history (versions + delete markers);
// otherwise it falls back to listing current objects as the sole "null" version.
func (h *Handler) listObjectVersions(w http.ResponseWriter, r *http.Request, bucket string) {
	opts := driver.ListOptions{
		Prefix:    r.URL.Query().Get("prefix"),
		Delimiter: r.URL.Query().Get("delimiter"),
	}

	resp := listVersionsResult{
		Xmlns:     xmlns,
		Name:      bucket,
		Prefix:    opts.Prefix,
		Delimiter: opts.Delimiter,
		MaxKeys:   defaultMaxKeys,
	}

	if h.versioned != nil {
		result, err := h.versioned.ListObjectVersions(r.Context(), bucket, opts)
		if err != nil {
			writeErr(w, err)
			return
		}

		for _, v := range result.Versions {
			if v.DeleteMarker {
				resp.DeleteMarkers = append(resp.DeleteMarkers, deleteMarkerXML{
					Key: v.Key, VersionID: v.VersionID, IsLatest: v.IsLatest, LastModified: v.LastModified,
				})
				continue
			}

			resp.Versions = append(resp.Versions, objectVersionXML{
				Key: v.Key, VersionID: v.VersionID, IsLatest: v.IsLatest, LastModified: v.LastModified,
				ETag: fmt.Sprintf("%q", v.ETag), Size: v.Size, StorageClass: "STANDARD",
			})
		}

		for _, p := range result.CommonPrefixes {
			resp.CommonPrefixes = append(resp.CommonPrefixes, prefixXML{Prefix: p})
		}

		wire.WriteXML(w, http.StatusOK, resp)

		return
	}

	// Fallback: no version history — list current objects as the "null" version.
	result, err := h.bucket.ListObjects(r.Context(), bucket, opts)
	if err != nil {
		writeErr(w, err)
		return
	}

	for _, obj := range result.Objects {
		resp.Versions = append(resp.Versions, objectVersionXML{
			Key: obj.Key, VersionID: "null", IsLatest: true, LastModified: obj.LastModified,
			ETag: fmt.Sprintf("%q", obj.ETag), Size: obj.Size, StorageClass: "STANDARD",
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

// bucketMissing reports whether a NotFound error names a bucket rather
// than an object. The driver formats object misses as `object ... not
// found in bucket ...` and bucket misses as `[source |destination ]bucket
// ... not found`, so exclude the object form first, then require the word
// bucket — robust to the source/destination copy variants.
func bucketMissing(err error) bool {
	var ce *cerrors.Error
	if !errors.As(err, &ce) {
		return false
	}
	if strings.HasPrefix(ce.Message, "object ") {
		return false
	}
	return strings.Contains(ce.Message, "bucket ")
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
