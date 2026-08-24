// Package s3 implements the S3 REST+XML protocol as a server.Handler.
// Point the real aws-sdk-go-v2 S3 client at a Server registered with this
// handler and operations work against an in-memory storage driver.
package s3

import (
	"context"
	"crypto/md5" //nolint:gosec // S3 object ETags are defined as MD5 digests, not a security primitive
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// minBucketNameLen / maxBucketNameLen bound a general-purpose bucket name.
	minBucketNameLen = 3
	maxBucketNameLen = 63
)

// Handler serves S3 REST requests against a storage.Bucket driver.
type Handler struct {
	bucket driver.Bucket
	// versioned is set when the driver retains per-object version history; the
	// handler then honors versioning status, ?versionId, and ListObjectVersions.
	versioned driver.VersionedBucket
	// rawConfig is set when the driver can persist opaque bucket-configuration
	// sub-resource documents (policy, cors, encryption, lifecycle, website, …);
	// the handler then round-trips PUT/GET/DELETE on them instead of treating a
	// write as a no-op.
	rawConfig driver.RawBucketConfig
	// copier is set when the driver supports a full-fidelity server-side copy
	// (versioned source, metadata directive, copy-source preconditions); the
	// handler routes CopyObject/UploadPartCopy through it when present.
	copier driver.ObjectCopier
	// regional is set when the driver can create a bucket in a caller-specified
	// region (CreateBucketConfiguration.LocationConstraint).
	regional driver.RegionalBucket
}

// New returns an S3 handler backed by b.
func New(b driver.Bucket) *Handler {
	h := &Handler{bucket: b}
	if vb, ok := b.(driver.VersionedBucket); ok {
		h.versioned = vb
	}

	if rc, ok := b.(driver.RawBucketConfig); ok {
		h.rawConfig = rc
	}

	if oc, ok := b.(driver.ObjectCopier); ok {
		h.copier = oc
	}

	if rb, ok := b.(driver.RegionalBucket); ok {
		h.regional = rb
	}

	return h
}

// md5Sum returns the raw MD5 digest of data. S3 object ETags are MD5 digests.
func md5Sum(data []byte) []byte {
	sum := md5.Sum(data) //nolint:gosec // S3 ETag is MD5 by spec, not a security control
	return sum[:]
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

	result := listAllMyBucketsResult{
		Xmlns: xmlns,
		Owner: aclOwnerXML{ID: cannedOwnerID, DisplayName: "cloudemu"},
	}
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
	case q.Has("notification"):
		h.bucketNotificationOp(w, r, bucket)
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
	case q.Has("delete"):
		// POST /{bucket}?delete => DeleteObjects (batch delete). Without this the
		// request falls through to the method switch and 405s, breaking
		// `aws s3 rm --recursive`, SDK batch delete, and Terraform force_destroy.
		h.deleteObjects(w, r, bucket)
		return
	case q.Has("acl"):
		// GET returns a canned ACL; a PUT is a no-op so it does NOT fall through
		// to createBucket (which 409s) — see aclOp.
		h.aclOp(w, r)
		return
	}

	// Read-only bucket configuration sub-resources (policy, cors, encryption,
	// location, …) that IaC clients read after create; without these the request
	// would fall through to ListObjects and the client fails to parse it.
	if sub := configSubresourceKey(q); sub != "" {
		h.bucketConfigOp(w, r, bucket, sub)
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

// usEast1 is the region where CreateBucket is idempotent for the owner.
const usEast1 = "us-east-1"

// validBucketName reports whether name satisfies S3's general-purpose bucket
// naming rules: 3-63 chars; lowercase letters, digits, hyphens and dots only;
// begins and ends with a letter or digit; no adjacent periods; not formatted as
// an IP address. See
// https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucketnamingrules.html
func validBucketName(name string) bool {
	if len(name) < minBucketNameLen || len(name) > maxBucketNameLen {
		return false
	}

	if net.ParseIP(name) != nil {
		return false
	}

	for i := 0; i < len(name); i++ {
		if !validBucketNameChar(name, i) {
			return false
		}
	}

	return true
}

// validBucketNameChar reports whether name[i] is legal at its position: an
// interior-only hyphen/dot (no leading/trailing, no adjacent periods) or a
// lowercase alphanumeric.
func validBucketNameChar(name string, i int) bool {
	c := name[i]

	if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
		return true
	}

	if c != '-' && c != '.' {
		return false
	}

	if i == 0 || i == len(name)-1 {
		return false
	}

	return c != '.' || name[i-1] != '.'
}

// maxCreateBucketBody caps the CreateBucketConfiguration document.
const maxCreateBucketBody = 1 << 16

func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if !validBucketName(bucket) {
		writeError(w, http.StatusBadRequest, "InvalidBucketName", "The specified bucket is not valid.")
		return
	}

	region := parseLocationConstraint(r)

	if err := h.createBucketInRegion(r.Context(), bucket, region); err != nil {
		// In us-east-1 (the global endpoint) re-creating a bucket you already own
		// is idempotent and returns 200; every other region returns 409
		// BucketAlreadyOwnedByYou. cloudemu models a single account, so an existing
		// bucket is always same-owner — the region alone decides.
		if cerrors.IsAlreadyExists(err) && h.bucketRegion(r.Context(), bucket) == usEast1 {
			w.Header().Set("Location", "/"+bucket)
			w.WriteHeader(http.StatusOK)
			return
		}
		writeErr(w, err)
		return
	}

	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

// createBucketInRegion honors a CreateBucketConfiguration.LocationConstraint
// when the driver supports RegionalBucket, so GetBucketLocation reports it back.
func (h *Handler) createBucketInRegion(ctx context.Context, bucket, region string) error {
	if region != "" && h.regional != nil {
		return h.regional.CreateBucketInRegion(ctx, bucket, region)
	}

	return h.bucket.CreateBucket(ctx, bucket)
}

// parseLocationConstraint extracts CreateBucketConfiguration.LocationConstraint
// from a CreateBucket body, or "" when absent/unparseable (which denotes
// us-east-1).
func parseLocationConstraint(r *http.Request) string {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCreateBucketBody))
	if err != nil || len(body) == 0 {
		return ""
	}

	var cfg createBucketConfiguration
	if err := xml.Unmarshal(body, &cfg); err != nil {
		return ""
	}

	return cfg.LocationConstraint
}

// bucketRegion returns the region of an existing bucket, or "" if it can't be
// determined. An empty LocationConstraint is equivalent to us-east-1.
func (h *Handler) bucketRegion(ctx context.Context, bucket string) string {
	buckets, err := h.bucket.ListBuckets(ctx)
	if err != nil {
		return ""
	}

	for _, b := range buckets {
		if b.Name == bucket {
			if b.Region == "" {
				return usEast1
			}
			return b.Region
		}
	}

	return ""
}

// bucketExists reports whether a bucket with the given name exists. Used by
// bucket-scoped sub-resources that must 404 NoSuchBucket for an absent bucket.
func (h *Handler) bucketExists(ctx context.Context, bucket string) bool {
	buckets, err := h.bucket.ListBuckets(ctx)
	if err != nil {
		return false
	}

	for _, b := range buckets {
		if b.Name == bucket {
			return true
		}
	}

	return false
}

func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := h.bucket.DeleteBucket(r.Context(), bucket); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// wantsOwner reports whether an <Owner> element belongs on each listed object.
// ListObjects v1 always includes it; only ListObjectsV2 (list-type=2) gates it
// behind fetch-owner=true, omitting it by default.
func wantsOwner(q url.Values) bool {
	return q.Get("list-type") != "2" || q.Get("fetch-owner") == "true"
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
		// start-after (ListObjectsV2) begins the listing strictly after this key.
		StartAfter: q.Get("start-after"),
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
		// KeyCount counts returned keys AND rolled-up common prefixes, matching
		// real S3 (a delimited listing reports both under KeyCount).
		KeyCount: len(result.Objects) + len(result.CommonPrefixes),
		// S3 echoes StartAfter in the response when it was sent with the request.
		StartAfter: opts.StartAfter,
	}

	if result.NextPageToken != "" {
		resp.NextContinuationToken = result.NextPageToken
	}

	var owner *aclOwnerXML
	if wantsOwner(q) {
		owner = &aclOwnerXML{ID: cannedOwnerID, DisplayName: "cloudemu"}
	}

	for i := range result.Objects {
		obj := &result.Objects[i]
		resp.Contents = append(resp.Contents, objectXML{
			Key:          obj.Key,
			LastModified: obj.LastModified,
			ETag:         fmt.Sprintf("%q", obj.ETag),
			Size:         int(obj.Size),
			StorageClass: "STANDARD",
			Owner:        owner,
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
	case q.Has("acl"):
		// GET returns a canned ACL; a PUT/DELETE is a no-op so it does NOT fall
		// through to putObject and overwrite the object with the ACL body.
		h.aclOp(w, r)
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

	// x-amz-tagging sets the object's tag set at upload time. Apply it to the
	// just-written object so GetObjectTagging returns it (real S3 stores the tag
	// set atomically with the object).
	if tags := parseTaggingHeader(r.Header.Get("X-Amz-Tagging")); len(tags) > 0 {
		if err := h.bucket.PutObjectTagging(r.Context(), bucket, key, tags); err != nil {
			writeErr(w, err)
			return
		}
	}

	// Real S3 always returns the object's ETag on PutObject. Read it back
	// from the driver so there is a single source of truth for the ETag
	// algorithm; if a concurrent delete races the read-back, fall back to
	// computing it from the body we just stored — a successful PUT must
	// never answer 404.
	etag := hex.EncodeToString(md5Sum(data))

	var versionID string
	if info, err := h.bucket.HeadObject(r.Context(), bucket, key); err == nil {
		etag = info.ETag
		versionID = info.VersionID
	}
	w.Header().Set("ETag", fmt.Sprintf("%q", etag))
	if versionID != "" {
		w.Header().Set("X-Amz-Version-Id", versionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	var (
		obj *driver.Object
		err error
	)

	versionID := r.URL.Query().Get("versionId")

	if versionID != "" && h.versioned != nil {
		obj, err = h.versioned.GetObjectVersion(r.Context(), bucket, key, versionID)
	} else {
		obj, err = h.bucket.GetObject(r.Context(), bucket, key)
	}

	if err != nil {
		if writeDeleteMarker(w, err, versionID) {
			return
		}
		writeErr(w, err)
		return
	}

	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		h.writeRangedObject(w, obj, rangeHeader)
		return
	}

	writeObjectHeaders(w, &obj.Info, int64(len(obj.Data)))
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // writing raw object bytes, not HTML
}

// rangeOutcome classifies a Range header against an object.
type rangeOutcome int

const (
	rangeIgnore        rangeOutcome = iota // unparseable header → serve full body (200)
	rangeUnsatisfiable                     // valid syntax but out of bounds → 416
	rangeOK                                // satisfiable → 206
)

// writeRangedObject serves a GetObject carrying a Range header: 206 with a
// Content-Range slice when satisfiable, 416 when the range is out of bounds, and
// a full 200 body when the header is unparseable (matching real S3).
func (*Handler) writeRangedObject(w http.ResponseWriter, obj *driver.Object, header string) {
	total := int64(len(obj.Data))
	start, end, outcome := parseByteRange(header, total)

	switch outcome {
	case rangeUnsatisfiable:
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "InvalidRange",
			"The requested range is not satisfiable")
	case rangeOK:
		writeObjectHeaders(w, &obj.Info, end-start+1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(obj.Data[start : end+1]) //nolint:gosec // writing raw object bytes, not HTML
	case rangeIgnore: // unparseable header → serve the full body
		writeObjectHeaders(w, &obj.Info, total)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.Data) //nolint:gosec // writing raw object bytes, not HTML
	}
}

// parseByteRange parses a single HTTP byte range ("bytes=0-4", "bytes=5-",
// "bytes=-5") against an object of size total, returning the inclusive
// [start,end] offsets and an outcome. Only the first range of a multi-range
// header is honored (the common single-range download path).
func parseByteRange(header string, total int64) (start, end int64, outcome rangeOutcome) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, rangeIgnore
	}

	spec := strings.TrimPrefix(header, prefix)
	if idx := strings.IndexByte(spec, ','); idx >= 0 {
		spec = spec[:idx]
	}

	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, rangeIgnore
	}

	startStr, endStr := spec[:dash], spec[dash+1:]

	if startStr == "" {
		return suffixRange(endStr, total)
	}

	return boundedRange(startStr, endStr, total)
}

// suffixRange handles "bytes=-N": the last N bytes of the object.
func suffixRange(endStr string, total int64) (start, end int64, outcome rangeOutcome) {
	n, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil {
		return 0, 0, rangeIgnore
	}

	if n <= 0 || total == 0 {
		return 0, 0, rangeUnsatisfiable
	}

	if n > total {
		n = total
	}

	return total - n, total - 1, rangeOK
}

// boundedRange handles "bytes=start-" and "bytes=start-end".
func boundedRange(startStr, endStr string, total int64) (start, end int64, outcome rangeOutcome) {
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		return 0, 0, rangeIgnore
	}

	if start >= total {
		return 0, 0, rangeUnsatisfiable
	}

	end = total - 1
	if endStr != "" {
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil || start > end {
			return 0, 0, rangeIgnore
		}

		if end >= total {
			end = total - 1
		}
	}

	return start, end, rangeOK
}

func (h *Handler) headObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	var (
		info *driver.ObjectInfo
		err  error
	)

	versionID := r.URL.Query().Get("versionId")

	if versionID != "" && h.versioned != nil {
		info, err = h.versioned.HeadObjectVersion(r.Context(), bucket, key, versionID)
	} else {
		info, err = h.bucket.HeadObject(r.Context(), bucket, key)
	}

	if err != nil {
		if writeDeleteMarker(w, err, versionID) {
			return
		}
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
		w.Header().Set("X-Amz-Version-Id", info.VersionID)
	}

	if info.DeleteMarker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
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
			w.Header().Set("X-Amz-Version-Id", vid)
		}
		if marker {
			w.Header().Set("X-Amz-Delete-Marker", "true")
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

// maxDeleteBody caps a DeleteObjects request body. Real S3 allows up to 1000
// keys per call; this is a generous ceiling on the XML that carries them.
const maxDeleteBody = 2 << 20

type deleteObjectsRequest struct {
	XMLName xml.Name `xml:"Delete"`
	Quiet   bool     `xml:"Quiet"`
	Objects []struct {
		Key       string `xml:"Key"`
		VersionID string `xml:"VersionId"`
	} `xml:"Object"`
}

type deletedObjectXML struct {
	Key          string `xml:"Key"`
	VersionID    string `xml:"VersionId,omitempty"`
	DeleteMarker bool   `xml:"DeleteMarker,omitempty"`
}

type deleteErrorXML struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

type deleteResultXML struct {
	XMLName xml.Name           `xml:"DeleteResult"`
	Xmlns   string             `xml:"xmlns,attr"`
	Deleted []deletedObjectXML `xml:"Deleted"`
	Errors  []deleteErrorXML   `xml:"Error"`
}

// deleteObjects implements POST /{bucket}?delete (DeleteObjects). Per-key delete
// is idempotent (a missing key still reports Deleted, matching real S3); a
// missing bucket fails the whole request with NoSuchBucket.
func (h *Handler) deleteObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDeleteBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not read request body")
		return
	}

	var req deleteObjectsRequest
	if err := xml.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "malformed Delete request")
		return
	}

	result := deleteResultXML{Xmlns: xmlns}

	for _, obj := range req.Objects {
		vid, marker, derr := h.deleteOneObject(r.Context(), bucket, obj.Key, obj.VersionID)
		switch {
		case derr != nil && bucketMissing(derr):
			// A missing bucket aborts the whole batch, as in real S3.
			writeErr(w, derr)
			return
		case derr != nil:
			result.Errors = append(result.Errors, deleteErrorXML{
				Key: obj.Key, Code: "InternalError", Message: derr.Error(),
			})
		case !req.Quiet:
			result.Deleted = append(result.Deleted, deletedObjectXML{
				Key: obj.Key, VersionID: vid, DeleteMarker: marker,
			})
		}
	}

	wire.WriteXML(w, http.StatusOK, result)
}

// cannedOwnerID is the fixed S3 canonical user id cloudemu reports as the owner
// of every bucket/object (no per-account ACLs are modeled).
const cannedOwnerID = "cloudemu00000000000000000000000000000000000000000000000000000000"

type aclOwnerXML struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type aclGranteeXML struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type aclGrantXML struct {
	Grantee    aclGranteeXML `xml:"Grantee"`
	Permission string        `xml:"Permission"`
}

type accessControlPolicyXML struct {
	XMLName xml.Name      `xml:"AccessControlPolicy"`
	Xmlns   string        `xml:"xmlns,attr"`
	Owner   aclOwnerXML   `xml:"Owner"`
	Grants  []aclGrantXML `xml:"AccessControlList>Grant"`
}

// aclOp answers ?acl on a bucket or object. GET returns a canned
// full-control-to-owner ACL; any write is accepted as a no-op. The point is to
// stop a PUT ?acl from falling through and overwriting the object/bucket, and a
// GET ?acl from returning object bytes instead of an ACL document.
func (h *Handler) aclOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusOK)
		return
	}

	owner := aclOwnerXML{ID: cannedOwnerID, DisplayName: "cloudemu"}
	wire.WriteXML(w, http.StatusOK, accessControlPolicyXML{
		Xmlns: xmlns,
		Owner: owner,
		Grants: []aclGrantXML{{
			Grantee:    aclGranteeXML{ID: cannedOwnerID, DisplayName: "cloudemu"},
			Permission: "FULL_CONTROL",
		}},
	})
}

// deleteOneObject deletes a single key for the batch path, honoring versioning
// and treating a missing key as success (idempotent).
func (h *Handler) deleteOneObject(ctx context.Context, bucket, key, versionID string) (vid string, marker bool, err error) {
	if h.versioned != nil {
		vid, marker, err = h.versioned.DeleteObjectVersion(ctx, bucket, key, versionID)
	} else {
		err = h.bucket.DeleteObject(ctx, bucket, key)
	}

	if err != nil && cerrors.IsNotFound(err) && !bucketMissing(err) {
		err = nil // missing key is a successful (idempotent) delete
	}

	return vid, marker, err
}

func (h *Handler) copyObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	srcBucket, srcKey, srcVersionID := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if srcBucket == "" || srcKey == "" {
		writeError(w, http.StatusBadRequest, "InvalidArgument", "invalid copy source")
		return
	}

	replace := strings.EqualFold(r.Header.Get("X-Amz-Metadata-Directive"), "REPLACE")

	// Copying an object onto itself with the default COPY directive, the current
	// version, and no metadata change is illegal — S3 answers 400 InvalidRequest.
	if isIllegalSelfCopy(replace, srcVersionID, srcBucket, srcKey, bucket, key) {
		writeError(w, http.StatusBadRequest, "InvalidRequest",
			"This copy request is illegal because it is trying to copy an object to itself without "+
				"changing the object's metadata, storage class, website redirect location or encryption attributes.")

		return
	}

	if h.copier == nil {
		h.copyObjectFallback(w, r, bucket, key, srcBucket, srcKey)
		return
	}

	req := buildCopyRequest(r, bucket, key, driver.CopySource{Bucket: srcBucket, Key: srcKey}, srcVersionID, replace)

	res, err := h.copier.CopyObjectV2(r.Context(), req)
	if err != nil {
		writeCopyErr(w, err)
		return
	}

	writeCopyResult(w, res)
}

// isIllegalSelfCopy reports whether a copy is a no-op self-copy: same key, the
// default COPY directive, and the current source version — which S3 rejects.
func isIllegalSelfCopy(replace bool, srcVersionID, srcBucket, srcKey, dstBucket, dstKey string) bool {
	return !replace && srcVersionID == "" && srcBucket == dstBucket && srcKey == dstKey
}

// buildCopyRequest assembles a CopyObjectRequest from the copy headers: the
// metadata directive and (for REPLACE) the replacement metadata/content-type,
// plus the copy-source preconditions.
func buildCopyRequest(
	r *http.Request, dstBucket, dstKey string, src driver.CopySource, srcVersionID string, replace bool,
) *driver.CopyObjectRequest {
	req := &driver.CopyObjectRequest{
		DstBucket: dstBucket, DstKey: dstKey, Src: src,
		SrcVersionID: srcVersionID, ReplaceMetadata: replace,
	}

	if replace {
		req.Metadata = extractMetadata(r.Header)
		req.ContentType = r.Header.Get("Content-Type")
	}

	// x-amz-tagging-directive: REPLACE takes the destination tag set from the
	// x-amz-tagging header; the default COPY inherits the source object's tags.
	if strings.EqualFold(r.Header.Get("X-Amz-Tagging-Directive"), "REPLACE") {
		req.ReplaceTags = true
		req.Tags = parseTaggingHeader(r.Header.Get("X-Amz-Tagging"))
	}

	applyCopyConditions(req, r.Header)

	return req
}

// writeCopyResult writes a CopyObject success: the version-id headers plus the
// <CopyObjectResult> body.
func writeCopyResult(w http.ResponseWriter, res *driver.CopyObjectResult) {
	if res.SourceVersionID != "" {
		w.Header().Set("X-Amz-Copy-Source-Version-Id", res.SourceVersionID)
	}

	if res.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", res.VersionID)
	}

	wire.WriteXML(w, http.StatusOK, copyObjectResult{
		Xmlns: xmlns, ETag: fmt.Sprintf("%q", res.ETag), LastModified: res.LastModified,
	})
}

// copyObjectFallback runs the basic copy (current version, COPY directive, no
// preconditions) for drivers that don't implement ObjectCopier.
func (h *Handler) copyObjectFallback(w http.ResponseWriter, r *http.Request, bucket, key, srcBucket, srcKey string) {
	if err := h.bucket.CopyObject(r.Context(), bucket, key, driver.CopySource{Bucket: srcBucket, Key: srcKey}); err != nil {
		writeErr(w, err)
		return
	}

	obj, err := h.bucket.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, copyObjectResult{
		Xmlns: xmlns, ETag: fmt.Sprintf("%q", obj.ETag), LastModified: obj.LastModified,
	})
}

// parseCopySource splits an x-amz-copy-source header into its source bucket,
// key, and optional versionId. A leading slash is tolerated and the path is
// URL-decoded, per the S3 contract; a "?versionId=<id>" suffix selects a
// specific source version.
func parseCopySource(header string) (bucket, key, versionID string) {
	header = strings.TrimPrefix(header, "/")

	if i := strings.IndexByte(header, '?'); i >= 0 {
		if q, err := url.ParseQuery(header[i+1:]); err == nil {
			versionID = q.Get("versionId")
		}

		header = header[:i]
	}

	if decoded, err := url.PathUnescape(header); err == nil {
		header = decoded
	}

	bucket, key = parsePath(header)

	return bucket, key, versionID
}

// applyCopyConditions maps the x-amz-copy-source-if-* request headers onto the
// copy request's preconditions.
func applyCopyConditions(req *driver.CopyObjectRequest, hdr http.Header) {
	req.IfMatch = hdr.Get("X-Amz-Copy-Source-If-Match")
	req.IfNoneMatch = hdr.Get("X-Amz-Copy-Source-If-None-Match")

	if v := hdr.Get("X-Amz-Copy-Source-If-Modified-Since"); v != "" {
		if t, err := http.ParseTime(v); err == nil {
			req.IfModifiedSince = t
		}
	}

	if v := hdr.Get("X-Amz-Copy-Source-If-Unmodified-Since"); v != "" {
		if t, err := http.ParseTime(v); err == nil {
			req.IfUnmodifiedSince = t
		}
	}
}

// evalCopySourceConditions evaluates the x-amz-copy-source-if-* request headers
// against a resolved source object, returning a FailedPrecondition error (mapped
// to 412 by writeCopyErr) when a precondition is not satisfied. UploadPartCopy
// honors the same four copy-source conditions — and the same documented
// combined-precedence override, where a true if-match overrides a false
// if-unmodified-since — as CopyObject.
func evalCopySourceConditions(hdr http.Header, etag, lastModified string) error {
	etag = strings.Trim(etag, `"`)
	ifMatch := hdr.Get("X-Amz-Copy-Source-If-Match")
	ifNoneMatch := hdr.Get("X-Amz-Copy-Source-If-None-Match")

	if ifMatch != "" && !copySourceETagMatches(ifMatch, etag) {
		return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-match precondition failed")
	}

	if ifNoneMatch != "" && copySourceETagMatches(ifNoneMatch, etag) {
		return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-none-match precondition failed")
	}

	skipUnmodified := ifMatch != "" && copySourceETagMatches(ifMatch, etag)

	return evalCopySourceTimeConditions(hdr, lastModified, skipUnmodified)
}

// evalCopySourceTimeConditions evaluates the two time-based copy-source headers.
func evalCopySourceTimeConditions(hdr http.Header, lastModified string, skipUnmodified bool) error {
	mod, err := time.Parse(time.RFC3339, lastModified)
	if err != nil {
		return nil // an unparseable timestamp can't be evaluated; don't block the copy
	}

	if v := hdr.Get("X-Amz-Copy-Source-If-Unmodified-Since"); v != "" && !skipUnmodified {
		if t, perr := http.ParseTime(v); perr == nil && mod.After(t) {
			return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-unmodified-since precondition failed")
		}
	}

	if v := hdr.Get("X-Amz-Copy-Source-If-Modified-Since"); v != "" {
		if t, perr := http.ParseTime(v); perr == nil && !mod.After(t) {
			return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-modified-since precondition failed")
		}
	}

	return nil
}

// copySourceETagMatches reports whether a copy-source-if-[none-]match header
// value matches the source ETag: "*" matches any object; otherwise a
// quote-insensitive, case-insensitive comparison.
func copySourceETagMatches(header, etag string) bool {
	header = strings.Trim(header, `"`)

	return header == "*" || strings.EqualFold(header, etag)
}

// writeCopyErr maps a copy driver error: a failed copy-source precondition is
// 412 PreconditionFailed; everything else follows the standard mapping.
func writeCopyErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) {
		writeError(w, http.StatusPreconditionFailed, "PreconditionFailed",
			"At least one of the preconditions you specified did not hold.")
		return
	}

	writeErr(w, err)
}

// uploadPartCopy implements UploadPartCopy: PUT a part whose bytes are copied
// from an existing object (optionally a byte range of it), returning a
// <CopyPartResult> with the new part's ETag.
func (h *Handler) uploadPartCopy(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	partNumber, ok := parsePartNumber(r.URL.Query())
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidArgument",
			"Part number must be an integer between 1 and 10000, inclusive")
		return
	}

	srcBucket, srcKey, srcVersionID := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if srcBucket == "" || srcKey == "" {
		writeError(w, http.StatusBadRequest, "InvalidArgument", "invalid copy source")
		return
	}

	obj, err := h.copySourceObject(r.Context(), srcBucket, srcKey, srcVersionID)
	if err != nil {
		writeCopyErr(w, err)
		return
	}

	if condErr := evalCopySourceConditions(r.Header, obj.Info.ETag, obj.Info.LastModified); condErr != nil {
		writeCopyErr(w, condErr)
		return
	}

	data, ok := sliceCopySourceRange(r.Header.Get("X-Amz-Copy-Source-Range"), obj.Data)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidArgument",
			"The x-amz-copy-source-range value must be of the form bytes=first-last")
		return
	}

	part, err := h.bucket.UploadPart(r.Context(), bucket, key, uploadID, partNumber, data)
	if err != nil {
		writeMultipartErr(w, err)
		return
	}

	if obj.Info.VersionID != "" {
		w.Header().Set("X-Amz-Copy-Source-Version-Id", obj.Info.VersionID)
	}

	wire.WriteXML(w, http.StatusOK, copyPartResult{
		Xmlns: xmlns, ETag: fmt.Sprintf("%q", part.ETag), LastModified: obj.Info.LastModified,
	})
}

// parsePartNumber reads and validates the partNumber query parameter (1–10000).
func parsePartNumber(q url.Values) (int, bool) {
	n, err := strconv.Atoi(q.Get("partNumber"))
	if err != nil || n < 1 || n > maxUploadPartNumber {
		return 0, false
	}

	return n, true
}

// sliceCopySourceRange returns the bytes an UploadPartCopy should copy: the full
// object when no range header is present, otherwise the requested inclusive
// byte range. ok is false when the range header is malformed or out of bounds.
func sliceCopySourceRange(header string, data []byte) ([]byte, bool) {
	if header == "" {
		return data, true
	}

	start, end, ok := copySourceRange(header, int64(len(data)))
	if !ok {
		return nil, false
	}

	return data[start : end+1], true
}

// copySourceObject fetches the source object for a part copy: a specific
// version when requested (a delete-marker version is an InvalidArgument), else
// the current object.
func (h *Handler) copySourceObject(ctx context.Context, srcBucket, srcKey, srcVersionID string) (*driver.Object, error) {
	if srcVersionID != "" && h.versioned != nil {
		obj, err := h.versioned.GetObjectVersion(ctx, srcBucket, srcKey, srcVersionID)
		if errors.Is(err, driver.ErrDeleteMarker) {
			return nil, cerrors.New(cerrors.InvalidArgument, "cannot specify a delete marker as a copy source version")
		}

		return obj, err
	}

	return h.bucket.GetObject(ctx, srcBucket, srcKey)
}

// copySourceRange parses an x-amz-copy-source-range header ("bytes=first-last",
// zero-based inclusive). Both offsets are required and must fall within the
// source; a malformed or out-of-bounds range returns ok=false.
func copySourceRange(header string, total int64) (start, end int64, ok bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}

	spec := strings.TrimPrefix(header, prefix)

	dash := strings.IndexByte(spec, '-')
	if dash <= 0 || dash == len(spec)-1 {
		return 0, 0, false
	}

	start, err1 := strconv.ParseInt(spec[:dash], 10, 64)
	end, err2 := strconv.ParseInt(spec[dash+1:], 10, 64)

	if err1 != nil || err2 != nil || start < 0 || end < start || end >= total {
		return 0, 0, false
	}

	return start, end, true
}

// multipartUploadOp dispatches operations on an in-progress multipart upload
// (those carrying an ?uploadId=... sub-resource).
func (h *Handler) multipartUploadOp(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	switch r.Method {
	case http.MethodPut:
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			h.uploadPartCopy(w, r, bucket, key, uploadID)
		} else {
			h.uploadPart(w, r, bucket, key, uploadID)
		}
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

// multipartTagger is the AWS-specific capability to carry the create-time
// x-amz-tagging tag set on a multipart upload (applied to the object on
// completion). Type-asserted like bucketNotifier; drivers without it ignore
// create-time multipart tags.
type multipartTagger interface {
	CreateMultipartUploadWithTagging(
		ctx context.Context, bucket, key, contentType string, tags map[string]string,
	) (*driver.MultipartUpload, error)
}

func (h *Handler) createMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	mp, err := h.beginMultipartUpload(r, bucket, key, contentType)
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

// beginMultipartUpload starts an upload, threading the x-amz-tagging tag set
// through to a driver that supports create-time multipart tags and falling back
// to the plain CreateMultipartUpload otherwise.
func (h *Handler) beginMultipartUpload(r *http.Request, bucket, key, contentType string) (*driver.MultipartUpload, error) {
	tags := parseTaggingHeader(r.Header.Get("X-Amz-Tagging"))
	if len(tags) > 0 {
		if tagger, ok := h.bucket.(multipartTagger); ok {
			return tagger.CreateMultipartUploadWithTagging(r.Context(), bucket, key, contentType, tags)
		}
	}

	return h.bucket.CreateMultipartUpload(r.Context(), bucket, key, contentType)
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
		writeCompleteMultipartErr(w, err)
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

	for i := range result.Objects {
		obj := &result.Objects[i]
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

// parseTaggingHeader decodes an x-amz-tagging header ("k1=v1&k2=v2",
// URL-query-encoded) into a tag map. An empty or unparseable header yields nil.
func parseTaggingHeader(header string) map[string]string {
	if header == "" {
		return nil
	}

	values, err := url.ParseQuery(header)
	if err != nil {
		return nil
	}

	tags := make(map[string]string, len(values))

	for k, v := range values {
		if len(v) > 0 {
			tags[k] = v[0]
		}
	}

	if len(tags) == 0 {
		return nil
	}

	return tags
}

// writeDeleteMarker answers a version-addressed GET/HEAD of a delete marker
// with 405 MethodNotAllowed and x-amz-delete-marker: true, as S3 does — a
// delete marker has no retrievable content. It returns false when err is not a
// delete marker, leaving the caller to handle it normally.
func writeDeleteMarker(w http.ResponseWriter, err error, versionID string) bool {
	if !errors.Is(err, driver.ErrDeleteMarker) {
		return false
	}

	w.Header().Set("X-Amz-Delete-Marker", "true")
	w.Header().Set("Allow", "DELETE")

	// S3 returns the delete marker's Last-Modified timestamp in the 405 response.
	var dm *driver.DeleteMarkerError
	if errors.As(err, &dm) && dm.LastModified != "" {
		w.Header().Set("Last-Modified", wire.ToHTTPDate(dm.LastModified))
	}

	if versionID != "" {
		w.Header().Set("X-Amz-Version-Id", versionID)
	}

	writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
		"The specified method is not allowed against this resource.")

	return true
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

// writeCompleteMultipartErr maps a CompleteMultipartUpload driver error. A
// missing upload is NoSuchUpload; a bad part reference (unknown part number or
// an ETag that does not match the stored part) is InvalidPart in real S3.
func writeCompleteMultipartErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NoSuchUpload", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidPart", err.Error())
	default:
		writeErr(w, err)
	}
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
