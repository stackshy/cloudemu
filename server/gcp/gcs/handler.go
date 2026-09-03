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
//	POST   /upload/storage/v1/b/{bucket}/o?uploadType=resumable         — resumable upload
//	GET    /storage/v1/b/{bucket}/o                     — list objects
//	GET    /storage/v1/b/{bucket}/o/{obj}               — get object metadata
//	GET    /storage/v1/b/{bucket}/o/{obj}?alt=media     — download object (honors Range)
//	DELETE /storage/v1/b/{bucket}/o/{obj}               — delete object
//	POST   /storage/v1/b/{bucket}/o/{obj}/rewriteTo/b/{dst}/o/{dstObj}  — copy
//	POST   /storage/v1/b/{srcBucket}/o/{srcObj}/copyTo/b/{dst}/o/{dstObj}  — legacy copy
package gcs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
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
	// uploadTypeResumable is a session-based chunked upload (Content-Range PUTs).
	uploadTypeResumable = "resumable"

	// statusResumeIncomplete (308) tells a resumable client the server has the
	// bytes so far and expects more chunks.
	statusResumeIncomplete = http.StatusPermanentRedirect

	// rangeTotalUnknown marks a Content-Range whose total ("*") isn't yet known.
	rangeTotalUnknown = -1

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

	// subresIAM is the /b/{bucket}/iam sub-collection segment.
	subresIAM = "iam"
	// subresNotificationConfigs is the /b/{bucket}/notificationConfigs segment.
	subresNotificationConfigs = "notificationConfigs"
	// defaultLocation / defaultStorageClass are the GCS defaults a bucket reads
	// back when none was set at create.
	defaultLocation     = "US"
	defaultStorageClass = "STANDARD"
)

// Handler serves GCS JSON REST requests against a storage.Bucket driver.
type Handler struct {
	bucket storagedriver.Bucket
	// ext is the optional GCS-specific capability (nil when the backing driver
	// doesn't implement it), covering preconditions, generation, compose,
	// object patch, versioned listing, and bucket location/IAM/metageneration.
	ext storagedriver.GCSExtensions

	// lifecycle is the optional capability that persists a bucket's lifecycle
	// configuration verbatim (every rule condition), nil when the backing driver
	// only supports the age-only portable driver.LifecycleConfig.
	lifecycle lifecycleRawStore

	// publisher emits object-change events to Pub/Sub for matching bucket
	// notification configs. Nil (the default) makes object events a no-op, so
	// object writes/deletes still succeed without Pub/Sub wired.
	publisher TopicPublisher

	// resumable holds in-progress resumable-upload sessions keyed by upload id.
	// A session buffers the object's bytes as Content-Range chunks arrive over
	// successive requests, committed through the normal object-write path on the
	// final chunk.
	// This is a pure wire-protocol concern (Content-Range chunking, 308 replies,
	// session URIs) with no analog in the portable driver, so it lives here.
	resumableMu sync.Mutex
	resumable   map[string]*resumableSession
}

// resumableSession is one in-progress resumable upload: the target and the
// captured insert-time metadata plus the bytes accumulated so far.
type resumableSession struct {
	bucket      string
	name        string
	contentType string
	metadata    map[string]string
	attrs       *storagedriver.GCSObjectAttrs

	mu  sync.Mutex
	buf []byte
}

// New returns a GCS handler backed by b.
func New(b storagedriver.Bucket) *Handler {
	ext, _ := b.(storagedriver.GCSExtensions)
	lifecycle, _ := b.(lifecycleRawStore)

	return &Handler{bucket: b, ext: ext, lifecycle: lifecycle, resumable: make(map[string]*resumableSession)}
}

// Matches returns true for /storage/v1/, /upload/storage/v1/, and direct
// /{bucket}/{object} media URLs (used by Reader.NewRangeReader).
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	if strings.HasPrefix(p, jsonAPIPrefix) || strings.HasPrefix(p, uploadAPIPrefix) {
		return true
	}

	// Direct media URLs are /{bucket}/{object}. Two or more path segments
	// suffices — but NOT when the first segment is a reserved API prefix
	// (v1, v2, sql, compute, …): those are other services' endpoints that no
	// earlier handler claimed, and swallowing them here yields a misleading
	// "bucket \"v1\" not found" instead of a clean not-implemented/not-found.
	trimmed := strings.TrimPrefix(p, "/")
	parts := strings.SplitN(trimmed, "/", pathBucketAndKey)

	if len(parts) != pathBucketAndKey || parts[0] == "" || parts[1] == "" {
		return false
	}

	return !isReservedAPIPrefix(parts[0])
}

// isReservedAPIPrefix reports whether a first path segment is a Google API
// version/service prefix rather than a plausible bucket name. GCS bucket names
// are lowercase and never collide with these in practice.
func isReservedAPIPrefix(seg string) bool {
	switch seg {
	case "sql", "compute", "dns", "upload", "storage", "download", "batch", "_cloudemu":
		return true
	}

	// A whole-segment API version token (v1, v3, v1beta4, v2beta) — but NOT a
	// bucket that merely starts that way (e.g. "v2-assets", "v1data").
	return isVersionToken(seg)
}

// isVersionToken reports whether seg is exactly an API version like v1, v3,
// v1beta4, v2beta — "v" + digits, optionally a beta/alpha qualifier, nothing
// else. A hyphen or other suffix (a real bucket name) is not a version.
func isVersionToken(seg string) bool {
	if len(seg) < 2 || seg[0] != 'v' || seg[1] < '0' || seg[1] > '9' {
		return false
	}

	i := 1
	for i < len(seg) && seg[i] >= '0' && seg[i] <= '9' {
		i++
	}

	rest := seg[i:]

	return rest == "" || strings.HasPrefix(rest, "beta") || strings.HasPrefix(rest, "alpha")
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
		// /b/{bucket}/o — list objects; /b/{bucket}/iam — bucket IAM policy.
		switch parts[2] {
		case "o":
			h.listObjects(w, r, parts[1])
		case subresIAM:
			h.bucketIAM(w, r, parts[1])
		case subresNotificationConfigs:
			h.notificationCollection(w, r, parts[1])
		default:
			writeError(w, http.StatusNotFound, "notFound", "unknown sub-collection")
		}
	default:
		// /b/{bucket}/o/{obj}[/...], /b/{bucket}/iam/testPermissions, or
		// /b/{bucket}/notificationConfigs/{id}.
		switch {
		case parts[2] == "o":
			h.objectOp(w, r, parts[1], strings.Join(parts[3:], "/"))
		case parts[2] == subresIAM && parts[3] == "testPermissions":
			h.testIAMPermissions(w, r, parts[1])
		case parts[2] == subresNotificationConfigs:
			h.notificationResourceOp(w, r, parts[1], parts[3])
		default:
			writeError(w, http.StatusNotFound, "notFound", "unknown sub-collection")
		}
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
	case http.MethodPatch, http.MethodPut:
		h.patchBucket(w, r, name)
	case http.MethodDelete:
		h.deleteBucket(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request) {
	var body bucketResource

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

	// Persist configuration supplied at create so it round-trips on read.
	if len(body.Labels) > 0 {
		_ = h.bucket.PutBucketTagging(r.Context(), body.Name, body.Labels)
	}

	if body.Versioning != nil && body.Versioning.Enabled {
		_ = h.bucket.SetBucketVersioning(r.Context(), body.Name, true)
	}

	if h.ext != nil {
		_ = h.ext.SetBucketAttrsGCS(r.Context(), body.Name, body.Location, body.StorageClass)
	}

	if body.Lifecycle != nil {
		_ = h.putLifecycle(r.Context(), body.Name, body.Lifecycle)
	}

	writeJSON(w, http.StatusOK, h.bucketView(r, body.Name, time.Now().UTC().Format(time.RFC3339)))
}

func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := bucketsListResponse{Kind: "storage#buckets"}
	for _, b := range buckets {
		out.Items = append(out.Items, h.bucketView(r, b.Name, b.CreatedAt))
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
			writeJSON(w, http.StatusOK, h.bucketView(r, b.Name, b.CreatedAt))
			return
		}
	}

	writeError(w, http.StatusNotFound, "notFound", "bucket "+name+" not found")
}

// bucketView builds the bucket JSON with its configured versioning, labels,
// location, storage class, lifecycle, IAM config, and metageneration/etag/
// updated reflected — real GCS returns these, and the driver stores them so a
// read must surface them instead of a hardcoded US/STANDARD default.
func (h *Handler) bucketView(r *http.Request, name, created string) bucketResource {
	location, storageClass, metageneration, updated := h.resolveBucketAttrs(r, name, created)

	res := bucketResource{
		Kind:             "storage#bucket",
		ID:               name,
		Name:             name,
		SelfLink:         selfLink(r, "/storage/v1/b/"+name),
		Location:         location,
		LocationType:     locationType(location),
		StorageClass:     storageClass,
		Metageneration:   strconv.FormatInt(metageneration, 10),
		Etag:             name + "/" + strconv.FormatInt(metageneration, 10),
		TimeCreated:      created,
		Updated:          updated,
		IamConfiguration: &iamConfiguration{PublicAccessPrevention: "inherited"},
	}

	if enabled, err := h.bucket.GetBucketVersioning(r.Context(), name); err == nil && enabled {
		res.Versioning = &bucketVersioning{Enabled: true}
	}

	if labels, err := h.bucket.GetBucketTagging(r.Context(), name); err == nil && len(labels) > 0 {
		res.Labels = labels
	}

	if lc := h.lifecycleView(r.Context(), name); lc != nil {
		res.Lifecycle = lc
	}

	return res
}

// resolveBucketAttrs returns the bucket's location, storage class,
// metageneration, and updated timestamp, falling back to the GCS defaults when
// the backing driver doesn't record them.
func (h *Handler) resolveBucketAttrs(
	r *http.Request, name, created string,
) (location, storageClass string, metageneration int64, updated string) {
	location, storageClass, metageneration, updated = defaultLocation, defaultStorageClass, 1, created

	if h.ext == nil {
		return location, storageClass, metageneration, updated
	}

	attrs, err := h.ext.BucketAttrsGCS(r.Context(), name)
	if err != nil {
		return location, storageClass, metageneration, updated
	}

	if attrs.Location != "" {
		location = attrs.Location
	}

	if attrs.StorageClass != "" {
		storageClass = attrs.StorageClass
	}

	if attrs.Metageneration > 0 {
		metageneration = attrs.Metageneration
	}

	if attrs.Updated != "" {
		updated = attrs.Updated
	}

	return location, storageClass, metageneration, updated
}

// locationType classifies a GCS location as a multi-region or a region, which
// real GCS reports in the locationType field.
func locationType(location string) string {
	switch strings.ToUpper(location) {
	case "US", "EU", "ASIA":
		return "multi-region"
	default:
		return "region"
	}
}

// patchBucket applies a bucket configuration update (versioning + labels),
// which real clients set via Buckets.Patch/Update. Without this the driver's
// versioning/label capabilities are unreachable over the wire.
func (h *Handler) patchBucket(w http.ResponseWriter, r *http.Request, name string) {
	var body bucketResource
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Versioning != nil {
		if err := h.bucket.SetBucketVersioning(r.Context(), name, body.Versioning.Enabled); err != nil {
			writeErr(w, err)
			return
		}
	}

	if body.Labels != nil {
		if err := h.bucket.PutBucketTagging(r.Context(), name, body.Labels); err != nil {
			writeErr(w, err)
			return
		}
	}

	if body.Lifecycle != nil {
		if err := h.putLifecycle(r.Context(), name, body.Lifecycle); err != nil {
			writeErr(w, err)
			return
		}
	}

	if h.ext != nil {
		if body.StorageClass != "" {
			_ = h.ext.SetBucketAttrsGCS(r.Context(), name, "", body.StorageClass)
		}

		_ = h.ext.TouchBucket(r.Context(), name)
	}

	writeJSON(w, http.StatusOK, h.bucketView(r, name, ""))
}

func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.bucket.DeleteBucket(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// bucketIAM serves /b/{bucket}/iam — GET returns the bucket's IAM policy (a
// default empty policy when none was set), PUT/POST replaces it.
func (h *Handler) bucketIAM(w http.ResponseWriter, r *http.Request, name string) {
	if !h.bucketExists(r, name) {
		writeError(w, http.StatusNotFound, "notFound", "bucket "+name+" not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBucketIAM(w, r, name)
	case http.MethodPut, http.MethodPost:
		h.setBucketIAM(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

func (h *Handler) getBucketIAM(w http.ResponseWriter, r *http.Request, name string) {
	if h.ext != nil {
		if raw, err := h.ext.BucketIAMPolicy(r.Context(), name); err == nil {
			writeRawJSON(w, raw)
			return
		}
	}

	writeJSON(w, http.StatusOK, defaultIAMPolicy(name))
}

func (h *Handler) setBucketIAM(w http.ResponseWriter, r *http.Request, name string) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPutBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not read body")
		return
	}

	// Validate it is a JSON policy before storing.
	var policy iamPolicyResource
	if uErr := json.Unmarshal(raw, &policy); uErr != nil {
		writeError(w, http.StatusBadRequest, "invalid", uErr.Error())
		return
	}

	if h.ext != nil {
		if sErr := h.ext.SetBucketIAMPolicy(r.Context(), name, raw); sErr != nil {
			writeErr(w, sErr)
			return
		}
	}

	writeRawJSON(w, raw)
}

// testIAMPermissions serves /b/{bucket}/iam/testPermissions — the mock grants
// every requested permission back.
func (h *Handler) testIAMPermissions(w http.ResponseWriter, r *http.Request, name string) {
	if !h.bucketExists(r, name) {
		writeError(w, http.StatusNotFound, "notFound", "bucket "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, testPermissionsResponse{
		Kind:        "storage#testIamPermissionsResponse",
		Permissions: r.URL.Query()["permissions"],
	})
}

func (h *Handler) bucketExists(r *http.Request, name string) bool {
	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		return false
	}

	for _, b := range buckets {
		if b.Name == name {
			return true
		}
	}

	return false
}

func defaultIAMPolicy(bucket string) iamPolicyResource {
	return iamPolicyResource{
		Kind:       "storage#policy",
		ResourceID: "projects/_/buckets/" + bucket,
		Version:    1,
		Bindings:   []iamPolicyBind{},
		Etag:       "CAE=",
	}
}

func writeRawJSON(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	// raw is an IAM policy document validated as JSON before storage and served
	// as application/json, not HTML — no XSS surface.
	_, _ = w.Write(raw) //nolint:gosec // validated JSON policy, served as application/json
}

// upload handles POST /upload/storage/v1/b/{bucket}/o, dispatching on
// uploadType: media (raw body), multipart (JSON metadata + payload), and
// resumable (a session initialized here, then chunked Content-Range uploads
// that carry the issued upload_id).
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, uploadAPIPrefix)
	parts := strings.Split(rest, "/")

	if len(parts) < pathBO || parts[0] != "b" || parts[2] != "o" {
		writeError(w, http.StatusNotFound, "notFound", "unknown upload path")
		return
	}

	bucket := parts[1]
	q := r.URL.Query()

	// A resumable session's chunk uploads come back to this upload URL carrying
	// the upload_id issued at session init (the SDK sends them as POSTs with a
	// Content-Range) — route those to the chunk handler before dispatching on
	// uploadType (which the client echoes as "resumable").
	if uploadID := q.Get("upload_id"); uploadID != "" {
		h.uploadResumableChunk(w, r, uploadID)
		return
	}

	uploadType := q.Get("uploadType")

	switch uploadType {
	case uploadTypeMedia:
		h.uploadMedia(w, r, bucket, q.Get("name"))
	case uploadTypeMultipart:
		h.uploadMultipart(w, r, bucket)
	case uploadTypeResumable:
		h.initResumable(w, r, bucket)
	default:
		writeError(w, http.StatusBadRequest, "invalid",
			"only uploadType=media, multipart or resumable supported (got "+uploadType+")")
	}
}

// initResumable starts a resumable upload session: it captures the object name
// and insert-time metadata from the POST (JSON body and/or query + upload
// headers), mints a session id, and returns 200 with a Location header giving
// the session URI the client PUTs chunks to.
func (h *Handler) initResumable(w http.ResponseWriter, r *http.Request, bucket string) {
	meta := h.readResumableInitMetadata(r)

	name := meta.Name
	if name == "" {
		name = r.URL.Query().Get("name")
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid", "name required for resumable upload")
		return
	}

	contentType := firstNonEmpty(meta.ContentType, r.Header.Get("X-Upload-Content-Type"), contentTypeBinary)

	sessionID := idgen.GenerateID("resumable-")

	h.resumableMu.Lock()
	h.resumable[sessionID] = &resumableSession{
		bucket:      bucket,
		name:        name,
		contentType: contentType,
		metadata:    meta.Metadata,
		attrs: &storagedriver.GCSObjectAttrs{
			CacheControl:       meta.CacheControl,
			ContentEncoding:    meta.ContentEncoding,
			ContentDisposition: meta.ContentDisposition,
			ContentLanguage:    meta.ContentLanguage,
			StorageClass:       meta.StorageClass,
		},
	}
	h.resumableMu.Unlock()

	location := selfLink(r, r.URL.Path) + "?uploadType=resumable&upload_id=" + url.QueryEscape(sessionID)
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusOK)
}

// readResumableInitMetadata parses the optional JSON object metadata carried in
// the resumable init POST body. A missing or unparseable body yields a zero
// value (name/content-type then come from the query and upload headers).
func (*Handler) readResumableInitMetadata(r *http.Request) uploadMetadata {
	if r.Body == nil {
		return uploadMetadata{}
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxPutBodyBytes))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return uploadMetadata{}
	}

	var meta uploadMetadata
	if uErr := json.Unmarshal(raw, &meta); uErr != nil {
		return uploadMetadata{}
	}

	return meta
}

// uploadResumableChunk appends one Content-Range chunk to its session's buffer.
// Until the final chunk it replies 308 Resume Incomplete with a Range header; on
// the final chunk it commits the assembled object through the normal write path
// and replies 200 with the object resource.
func (h *Handler) uploadResumableChunk(w http.ResponseWriter, r *http.Request, sessionID string) {
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid", "upload_id required")
		return
	}

	h.resumableMu.Lock()
	sess := h.resumable[sessionID]
	h.resumableMu.Unlock()

	if sess == nil {
		writeError(w, http.StatusNotFound, "notFound", "resumable upload session not found")
		return
	}

	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPutBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not read chunk body")
		return
	}

	// end is intentionally ignored: finality is decided by the buffered length
	// reaching the declared total, never by a chunk claiming to carry the last
	// byte (which could arrive after a gap and truncate the object).
	start, _, total, ok := parseContentRange(r.Header.Get("Content-Range"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "malformed Content-Range")
		return
	}

	buffered, contiguous := sess.appendChunk(start, data)

	// The upload commits only once the buffer holds the full declared size and no
	// gap was left behind. Gating finality on a chunk claiming the last byte would
	// wrongly commit a short object when that chunk arrives after a gap.
	if !contiguous || total == rangeTotalUnknown || buffered < total {
		writeResumeIncomplete(w, r, buffered)
		return
	}

	h.commitResumable(w, r, sessionID, sess)
}

// appendChunk appends a contiguous chunk's bytes and reports the resulting
// buffered length. contiguous is false when a bytes-carrying chunk begins past
// the current buffer length (a gap) — nothing is appended in that case. A
// status-probe (no bytes) or an already-buffered replay no-ops but stays
// contiguous.
func (s *resumableSession) appendChunk(start int64, data []byte) (buffered int64, contiguous bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(data) > 0 && start > int64(len(s.buf)) {
		return int64(len(s.buf)), false
	}

	if len(data) > 0 && start == int64(len(s.buf)) {
		s.buf = append(s.buf, data...)
	}

	return int64(len(s.buf)), true
}

// writeResumeIncomplete signals the client that the bytes so far are stored and
// more chunks are expected. The Go SDK sets "X-GUploader-No-308: yes" asking the
// server to convey this as a 200 carrying an "X-Http-Status-Code-Override: 308"
// header (some HTTP stacks mishandle a bare 308); otherwise a real 308 is sent.
func writeResumeIncomplete(w http.ResponseWriter, r *http.Request, buffered int64) {
	w.Header().Set("Range", "bytes=0-"+strconv.FormatInt(maxInt64(buffered-1, 0), 10))

	if strings.EqualFold(r.Header.Get("X-Guploader-No-308"), "yes") {
		w.Header().Set("X-Http-Status-Code-Override", "308")
		w.WriteHeader(http.StatusOK)

		return
	}

	w.WriteHeader(statusResumeIncomplete)
}

// commitResumable writes the fully-assembled session buffer through the normal
// object-write path and drops the session.
func (h *Handler) commitResumable(w http.ResponseWriter, r *http.Request, sessionID string, sess *resumableSession) {
	h.resumableMu.Lock()
	delete(h.resumable, sessionID)
	h.resumableMu.Unlock()

	sess.mu.Lock()
	data := sess.buf
	sess.mu.Unlock()

	h.storeObject(w, r, sess.bucket, sess.name, sess.contentType, data, sess.metadata, sess.attrs)
}

// parseContentRange parses a resumable-upload Content-Range: "bytes start-end/
// total", "bytes start-end/*" (total unknown), or "bytes */total" (a size probe
// with no bytes). total is rangeTotalUnknown for "*"; end is -1 when the range
// numerator is "*". ok is false on a malformed header.
func parseContentRange(header string) (start, end, total int64, ok bool) {
	const prefix = "bytes "

	if !strings.HasPrefix(header, prefix) {
		return 0, 0, 0, false
	}

	spec := strings.TrimPrefix(header, prefix)

	slash := strings.IndexByte(spec, '/')
	if slash < 0 {
		return 0, 0, 0, false
	}

	rangePart, totalPart := spec[:slash], spec[slash+1:]

	total = rangeTotalUnknown
	if totalPart != "*" {
		total, ok = parseNonNegInt64(totalPart)
		if !ok {
			return 0, 0, 0, false
		}
	}

	if rangePart == "*" {
		return 0, rangeTotalUnknown, total, true
	}

	dash := strings.IndexByte(rangePart, '-')
	if dash < 0 {
		return 0, 0, 0, false
	}

	start, ok = parseNonNegInt64(rangePart[:dash])
	if !ok {
		return 0, 0, 0, false
	}

	end, ok = parseNonNegInt64(rangePart[dash+1:])
	if !ok || end < start {
		return 0, 0, 0, false
	}

	return start, end, total, true
}

func parseNonNegInt64(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}

	return n, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}

	return b
}

// parsePrecondition extracts the GCS write preconditions from the request query
// (ifGenerationMatch/ifGenerationNotMatch/ifMetagenerationMatch/
// ifMetagenerationNotMatch). Each is a nil pointer when absent.
func parsePrecondition(q url.Values) storagedriver.GCSPrecondition {
	return storagedriver.GCSPrecondition{
		IfGenerationMatch:        parseInt64Ptr(q.Get("ifGenerationMatch")),
		IfGenerationNotMatch:     parseInt64Ptr(q.Get("ifGenerationNotMatch")),
		IfMetagenerationMatch:    parseInt64Ptr(q.Get("ifMetagenerationMatch")),
		IfMetagenerationNotMatch: parseInt64Ptr(q.Get("ifMetagenerationNotMatch")),
	}
}

func parseInt64Ptr(s string) *int64 {
	if s == "" {
		return nil
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}

	return &n
}

// readPrecondition extracts the conditional-read preconditions. The Go client
// sends them on the JSON metadata request as query params but on the media
// download as x-goog-if-* headers, so both are consulted (query wins).
func readPrecondition(r *http.Request) storagedriver.GCSPrecondition {
	q := r.URL.Query()
	h := r.Header

	return storagedriver.GCSPrecondition{
		IfGenerationMatch:        firstInt64Ptr(q.Get("ifGenerationMatch"), h.Get("X-Goog-If-Generation-Match")),
		IfGenerationNotMatch:     firstInt64Ptr(q.Get("ifGenerationNotMatch"), h.Get("X-Goog-If-Generation-Not-Match")),
		IfMetagenerationMatch:    firstInt64Ptr(q.Get("ifMetagenerationMatch"), h.Get("X-Goog-If-Metageneration-Match")),
		IfMetagenerationNotMatch: firstInt64Ptr(q.Get("ifMetagenerationNotMatch"), h.Get("X-Goog-If-Metageneration-Not-Match")),
	}
}

// firstInt64Ptr returns the first parseable int64 pointer among its args.
func firstInt64Ptr(vals ...string) *int64 {
	for _, v := range vals {
		if p := parseInt64Ptr(v); p != nil {
			return p
		}
	}

	return nil
}

// parseGeneration extracts the ?generation object-revision selector; nil (and a
// meaningless generation=0) select the live object.
func parseGeneration(q url.Values) *int64 {
	g := parseInt64Ptr(q.Get("generation"))
	if g != nil && *g == 0 {
		return nil
	}

	return g
}

// evalReadPrecondition evaluates the conditional-read preconditions against the
// object being returned. It reports the HTTP status the response must be
// short-circuited with — 412 for a failed Match, 304 for a satisfied NotMatch —
// and false when the read should proceed. This mirrors real GCS's conditional
// GET semantics.
func evalReadPrecondition(pre storagedriver.GCSPrecondition, gen, metagen int64) (int, bool) {
	if pre.IfGenerationMatch != nil && gen != *pre.IfGenerationMatch {
		return http.StatusPreconditionFailed, true
	}

	if pre.IfMetagenerationMatch != nil && metagen != *pre.IfMetagenerationMatch {
		return http.StatusPreconditionFailed, true
	}

	if pre.IfGenerationNotMatch != nil && gen == *pre.IfGenerationNotMatch {
		return http.StatusNotModified, true
	}

	if pre.IfMetagenerationNotMatch != nil && metagen == *pre.IfMetagenerationNotMatch {
		return http.StatusNotModified, true
	}

	return 0, false
}

// writeConditionalRead writes the short-circuit response for a conditional read:
// a 304 carries no body, a 412 the conditionNotMet error envelope.
func writeConditionalRead(w http.ResponseWriter, status int) {
	if status == http.StatusNotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	writeError(w, http.StatusPreconditionFailed, "conditionNotMet", "conditionNotMet")
}

// writePreconditionOrErr maps a *GCSPreconditionError to a 412 conditionNotMet
// response and any other error through writeErr.
func writePreconditionOrErr(w http.ResponseWriter, err error) {
	var preErr *storagedriver.GCSPreconditionError
	if errors.As(err, &preErr) {
		writeError(w, http.StatusPreconditionFailed, "conditionNotMet", preErr.Error())
		return
	}

	writeErr(w, err)
}

// storeObject writes an object through the preconditioned GCS path when the
// backing driver supports it (returning a 412 on a failed precondition), else
// falls back to the plain Bucket.PutObject.
func (h *Handler) storeObject(
	w http.ResponseWriter, r *http.Request, bucket, name, contentType string,
	data []byte, metadata map[string]string, attrs *storagedriver.GCSObjectAttrs,
) {
	var (
		info *storagedriver.ObjectInfo
		err  error
	)

	if h.ext != nil {
		info, err = h.ext.PutObjectGCS(r.Context(), bucket, name, data, contentType, metadata, attrs, parsePrecondition(r.URL.Query()))
		if err != nil {
			writePreconditionOrErr(w, err)
			return
		}
	} else {
		if putErr := h.bucket.PutObject(r.Context(), bucket, name, data, contentType, metadata); putErr != nil {
			writeErr(w, putErr)
			return
		}

		if info, err = h.bucket.HeadObject(r.Context(), bucket, name); err != nil {
			writeErr(w, err)
			return
		}
	}

	resource := toObjectResource(info, bucket, r)
	writeJSON(w, http.StatusOK, resource)

	// A successful create/overwrite is an OBJECT_FINALIZE event; emit it to any
	// matching bucket notification config (best-effort, never fails the write).
	h.emitObjectEvent(r, bucket, &resource, eventTypeObjectFinalize)
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

	h.storeObject(w, r, bucket, name, contentType, data, nil, nil)
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

	h.storeObject(w, r, bucket, meta.Name, payloadCT, payload, meta.Metadata, &storagedriver.GCSObjectAttrs{
		CacheControl:       meta.CacheControl,
		ContentEncoding:    meta.ContentEncoding,
		ContentDisposition: meta.ContentDisposition,
		ContentLanguage:    meta.ContentLanguage,
		StorageClass:       meta.StorageClass,
	})
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

	// versions=true lists every generation (current + archived), not just the
	// live objects — real GCS retains prior generations on a versioned bucket.
	listFn := h.bucket.ListObjects
	if q.Get("versions") == "true" && h.ext != nil {
		listFn = h.ext.ListObjectGenerations
	}

	result, err := listFn(r.Context(), bucket, opts)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := objectsListResponse{
		Kind:          "storage#objects",
		Prefixes:      result.CommonPrefixes,
		NextPageToken: result.NextPageToken,
	}

	for i := range result.Objects {
		out.Items = append(out.Items, toObjectResourceFromInfo(&result.Objects[i], bucket, r))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) objectOp(w http.ResponseWriter, r *http.Request, bucket, objAndRest string) {
	// Detect rewriteTo / copyTo / compose sub-resources, e.g.
	// "k1/rewriteTo/b/dstb/o/dstk" or "dst/compose".
	parts := strings.Split(objAndRest, "/")

	for i, p := range parts {
		if i == 0 {
			continue // the object key itself is never a sub-resource verb
		}

		switch p {
		case "rewriteTo", "copyTo":
			h.copyObject(w, r, bucket, strings.Join(parts[:i], "/"), parts[i+1:])
			return
		case "compose":
			h.composeObject(w, r, bucket, strings.Join(parts[:i], "/"))
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
	case http.MethodPatch, http.MethodPut:
		h.updateObject(w, r, bucket, objAndRest)
	case http.MethodDelete:
		h.deleteObject(w, r, bucket, objAndRest)
	default:
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// updateObject handles PATCH/PUT /b/{bucket}/o/{obj} — an Objects: patch/update
// that mutates system properties and/or custom metadata without touching data.
func (h *Handler) updateObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if h.ext == nil {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "object update not supported")
		return
	}

	var body objectPatchBody
	if !decodeJSON(w, r, &body) {
		return
	}

	info, err := h.ext.UpdateObjectGCS(r.Context(), bucket, key, storagedriver.GCSObjectUpdate{
		ContentType:        body.ContentType,
		CacheControl:       body.CacheControl,
		ContentEncoding:    body.ContentEncoding,
		ContentDisposition: body.ContentDisposition,
		ContentLanguage:    body.ContentLanguage,
		Metadata:           body.Metadata,
	}, parsePrecondition(r.URL.Query()))
	if err != nil {
		writePreconditionOrErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toObjectResource(info, bucket, r))
}

// composeObject handles POST /b/{bucket}/o/{dst}/compose — concatenating the
// named source objects' bytes into the destination.
func (h *Handler) composeObject(w http.ResponseWriter, r *http.Request, bucket, dstKey string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		return
	}

	if h.ext == nil {
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "compose not supported")
		return
	}

	var body composeRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	srcs := make([]storagedriver.GCSComposeSource, 0, len(body.SourceObjects))

	for _, s := range body.SourceObjects {
		var gen *int64

		if s.Generation != 0 {
			g := s.Generation
			gen = &g
		}

		srcs = append(srcs, storagedriver.GCSComposeSource{Key: s.Name, Generation: gen})
	}

	contentType := ""
	if body.Destination != nil {
		contentType = body.Destination.ContentType
	}

	info, err := h.ext.ComposeObjectGCS(
		r.Context(), bucket, dstKey, srcs, contentType, destinationMetadata(body.Destination), parsePrecondition(r.URL.Query()),
	)
	if err != nil {
		writePreconditionOrErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toObjectResource(info, bucket, r))
}

func destinationMetadata(dst *objectResource) map[string]string {
	if dst == nil {
		return nil
	}

	return dst.Metadata
}

func (h *Handler) getObjectMetadata(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()

	info, err := h.headObject(r, bucket, key, parseGeneration(q))
	if err != nil {
		writeErr(w, err)
		return
	}

	if status, short := evalReadPrecondition(readPrecondition(r), info.Generation, info.Metageneration); short {
		writeConditionalRead(w, status)
		return
	}

	writeJSON(w, http.StatusOK, toObjectResource(info, bucket, r))
}

func (h *Handler) downloadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()

	obj, err := h.fetchObject(r, bucket, key, parseGeneration(q))
	if err != nil {
		writeErr(w, err)
		return
	}

	if status, short := evalReadPrecondition(readPrecondition(r), obj.Info.Generation, obj.Info.Metageneration); short {
		writeConditionalRead(w, status)
		return
	}

	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		h.writeRangedObject(w, obj, rangeHeader)
		return
	}

	w.Header().Set("Content-Type", obj.Info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(obj.Data)), 10))
	w.Header().Set("ETag", obj.Info.ETag)
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // raw object bytes
}

// writeRangedObject serves an alt=media download carrying a Range header: 206
// with a Content-Range slice when satisfiable, 416 when out of bounds, and a
// full 200 body when the header is unparseable — matching real GCS.
func (*Handler) writeRangedObject(w http.ResponseWriter, obj *storagedriver.Object, header string) {
	total := int64(len(obj.Data))
	start, end, outcome := parseByteRange(header, total)

	switch outcome {
	case rangeUnsatisfiable:
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(total, 10))
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "requestedRangeNotSatisfiable",
			"the requested range is not satisfiable")
	case rangeOK:
		w.Header().Set("Content-Type", obj.Info.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.Header().Set("ETag", obj.Info.ETag)
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+
			strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(total, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(obj.Data[start : end+1]) //nolint:gosec // bounded slice of object bytes
	case rangeIgnore:
		w.Header().Set("Content-Type", obj.Info.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.Header().Set("ETag", obj.Info.ETag)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.Data) //nolint:gosec // raw object bytes
	}
}

// rangeOutcome classifies a Range header against an object.
type rangeOutcome int

const (
	rangeIgnore        rangeOutcome = iota // unparseable header → serve full body (200)
	rangeUnsatisfiable                     // valid syntax but out of bounds → 416
	rangeOK                                // satisfiable → 206
)

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

// headObject fetches an object's metadata, addressing a specific generation
// through the GCS extension when available.
func (h *Handler) headObject(r *http.Request, bucket, key string, gen *int64) (*storagedriver.ObjectInfo, error) {
	if h.ext != nil {
		return h.ext.HeadObjectGCS(r.Context(), bucket, key, gen)
	}

	return h.bucket.HeadObject(r.Context(), bucket, key)
}

// fetchObject fetches an object's bytes+metadata, addressing a specific
// generation through the GCS extension when available.
func (h *Handler) fetchObject(r *http.Request, bucket, key string, gen *int64) (*storagedriver.Object, error) {
	if h.ext != nil {
		return h.ext.GetObjectGCS(r.Context(), bucket, key, gen)
	}

	return h.bucket.GetObject(r.Context(), bucket, key)
}

func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	gen := parseGeneration(q)

	// Capture the object resource for the OBJECT_DELETE event before it is
	// removed (best-effort: skip the event when it can't be read).
	deletedRes, haveRes := h.objectResourceForEvent(r, bucket, key, gen)

	if h.ext != nil {
		if err := h.ext.DeleteObjectGCS(r.Context(), bucket, key, gen, parsePrecondition(q)); err != nil {
			writePreconditionOrErr(w, err)
			return
		}
	} else if err := h.bucket.DeleteObject(r.Context(), bucket, key); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)

	if haveRes {
		h.emitObjectEvent(r, bucket, &deletedRes, eventTypeObjectDelete)
	}
}

// objectResourceForEvent reads the object's current metadata as an
// objectResource for a notification event, returning ok=false when it can't be
// read (a missing object or a driver without head support).
func (h *Handler) objectResourceForEvent(r *http.Request, bucket, key string, gen *int64) (objectResource, bool) {
	info, err := h.headObject(r, bucket, key, gen)
	if err != nil || info == nil {
		return objectResource{}, false
	}

	return toObjectResource(info, bucket, r), true
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
	generation := info.Generation
	if generation == 0 {
		generation = 1
	}

	metageneration := info.Metageneration
	if metageneration == 0 {
		metageneration = 1
	}

	genStr := strconv.FormatInt(generation, 10)

	storageClass := info.StorageClass
	if storageClass == "" {
		storageClass = defaultStorageClass
	}

	// timeCreated is fixed at first write; updated advances on overwrite or a
	// metadata patch. Providers that don't track a distinct creation time leave
	// Created empty, so fall back to LastModified.
	created := info.Created
	if created == "" {
		created = info.LastModified
	}

	return objectResource{
		Kind:               "storage#object",
		ID:                 bucket + "/" + info.Key + "/" + genStr,
		Name:               info.Key,
		Bucket:             bucket,
		Generation:         genStr,
		Metageneration:     strconv.FormatInt(metageneration, 10),
		ContentType:        info.ContentType,
		Size:               strconv.FormatInt(info.Size, 10),
		MD5Hash:            info.MD5,
		CRC32C:             info.CRC32C,
		ETag:               info.ETag,
		StorageClass:       storageClass,
		CacheControl:       info.CacheControl,
		ContentEncoding:    info.ContentEncoding,
		ContentDisposition: info.ContentDisposition,
		ContentLanguage:    info.ContentLanguage,
		TimeCreated:        created,
		Updated:            info.LastModified,
		Metadata:           info.Metadata,
		SelfLink:           selfLink(r, "/storage/v1/b/"+bucket+"/o/"+info.Key),
		MediaLink:          selfLink(r, "/storage/v1/b/"+bucket+"/o/"+info.Key+"?alt=media"),
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
	case cerrors.IsFailedPrecondition(err):
		// Real GCS refuses to delete a non-empty bucket with 409 conflict,
		// not a 5xx (which would trigger client retry backoff).
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internalError", err.Error())
	}
}

// extractBoundary pulls the boundary= directive out of a Content-Type header.
func extractBoundary(ct string) string {
	for _, part := range strings.Split(ct, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "boundary=") {
			return strings.Trim(strings.TrimPrefix(part, "boundary="), `"`)
		}
	}

	return ""
}

// uploadMetadata is the JSON metadata part of a multipart upload.
type uploadMetadata struct {
	Name               string            `json:"name"`
	ContentType        string            `json:"contentType,omitempty"`
	CacheControl       string            `json:"cacheControl,omitempty"`
	ContentEncoding    string            `json:"contentEncoding,omitempty"`
	ContentDisposition string            `json:"contentDisposition,omitempty"`
	ContentLanguage    string            `json:"contentLanguage,omitempty"`
	StorageClass       string            `json:"storageClass,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// parseMultipart extracts metadata, payload, and payload content-type from a
// multipart/related body. ok=false if either part is missing. The payload is
// framed by RFC 2046 boundary delimiters ("\r\n--boundary"); using the stdlib
// reader keeps the payload byte-exact even when it ends in '-', '\r', or '\n'.
func parseMultipart(raw []byte, boundary string) (
	meta uploadMetadata, payload []byte, payloadContentType string, ok bool,
) {
	mr := multipart.NewReader(bytes.NewReader(raw), boundary)

	// First part: JSON object metadata.
	metaPart, err := mr.NextRawPart()
	if err != nil {
		return uploadMetadata{}, nil, "", false
	}

	metaBody, err := io.ReadAll(metaPart)
	if err != nil {
		return uploadMetadata{}, nil, "", false
	}

	if err := json.Unmarshal(bytes.TrimSpace(metaBody), &meta); err != nil {
		return uploadMetadata{}, nil, "", false
	}

	// Second part: raw object payload.
	payloadPart, err := mr.NextRawPart()
	if err != nil {
		return uploadMetadata{}, nil, "", false
	}

	payload, err = io.ReadAll(payloadPart)
	if err != nil {
		return uploadMetadata{}, nil, "", false
	}

	// Real GCS gives metadata.contentType precedence over the media part's
	// Content-Type header (SDKs often send application/octet-stream there).
	payloadContentType = meta.ContentType
	if payloadContentType == "" {
		payloadContentType = payloadPart.Header.Get("Content-Type")
	}

	return meta, payload, payloadContentType, true
}
