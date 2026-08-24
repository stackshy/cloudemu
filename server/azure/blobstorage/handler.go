// Package blobstorage implements the Azure Blob Storage REST+XML wire protocol as a
// server.Handler. Real azure-sdk-for-go azblob clients configured with a
// custom service URL hit this handler the same way they hit
// {account}.blob.core.windows.net.
//
// Supported operations (parity with AWS S3):
//
//	GET    /?comp=list                                  — list containers
//	PUT    /{container}?restype=container               — create container
//	DELETE /{container}?restype=container               — delete container
//	GET    /{container}?restype=container&comp=list     — list blobs
//	PUT    /{container}/{blob}                          — put blob (BlockBlob)
//	PUT    /{container}/{blob} (x-ms-copy-source)       — copy blob
//	GET    /{container}/{blob}                          — get blob
//	HEAD   /{container}/{blob}                          — head blob
//	DELETE /{container}/{blob}                          — delete blob
//
// Less-used surfaces (lifecycle, encryption, tags, access policies, leases,
// snapshots, versioning) are not yet wired and return 501.
package blobstorage

import (
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	contentTypeXML = "application/xml"

	// maxPutBodyBytes caps single-PUT blob bodies. Real Azure limits BlockBlob
	// PUT to 5000 MiB; we use a 5 GiB cap to match S3.
	maxPutBodyBytes = 5 << 30

	// xmsVersion is the Azure Blob Storage service version we report.
	xmsVersion = "2023-11-03"

	// compList is the value of the ?comp= parameter for list operations.
	compList = "list"

	// blobTypeBlockBlob is the default Azure blob type.
	blobTypeBlockBlob = "BlockBlob"

	// comp* are the ?comp= sub-operation selectors on a blob PUT.
	compBlock       = "block"
	compBlockList   = "blocklist"
	compMetadata    = "metadata"
	compProperties  = "properties"
	compTier        = "tier"
	compSnapshot    = "snapshot"
	compAppendBlock = "appendblock"
)

// Handler serves Azure Blob Storage REST requests against a storage.Bucket
// driver.
type Handler struct {
	bucket storagedriver.Bucket
}

// New returns a Blob handler backed by b.
func New(b storagedriver.Bucket) *Handler {
	return &Handler{bucket: b}
}

// Matches returns true for requests that look like Azure Blob calls. The
// shape: path doesn't start with /subscriptions/ (that's ARM management
// plane), and the request isn't a known REST style we delegate elsewhere.
//
// In practice this handler is registered as the data-plane fallback for the
// Azure server, so Matches() is permissive.
func (*Handler) Matches(r *http.Request) bool {
	return !strings.HasPrefix(r.URL.Path, "/subscriptions/")
}

// ServeHTTP routes the request based on path shape and query params.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	container, blob := parseBlobPath(r.URL.Path)
	q := r.URL.Query()

	w.Header().Set("X-Ms-Version", xmsVersion)

	switch {
	case container == "" && q.Get("comp") == compList:
		h.listContainers(w, r)
	case container == "":
		writeError(w, http.StatusNotImplemented, "NotImplemented", "operation not supported on root")
	case blob == "" && q.Get("restype") == "container":
		h.containerOp(w, r, container, q)
	case blob == "":
		writeError(w, http.StatusBadRequest, "InvalidUri",
			"missing restype=container query for container-level op")
	default:
		h.blobOp(w, r, container, blob)
	}
}

// parseBlobPath splits "/container/key/with/slashes" into ("container",
// "key/with/slashes"). Empty strings when parts are absent.
func parseBlobPath(path string) (container, blob string) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", ""
	}

	const containerAndBlob = 2
	parts := strings.SplitN(path, "/", containerAndBlob)
	container = parts[0]

	if len(parts) > 1 {
		blob = parts[1]
	}

	return container, blob
}

// containerOp handles operations targeting the container (?restype=container).
func (h *Handler) containerOp(w http.ResponseWriter, r *http.Request, container string, q url.Values) {
	switch r.Method {
	case http.MethodPut:
		if q.Get("comp") == compMetadata {
			h.setContainerMetadata(w, r, container)
			return
		}

		h.createContainer(w, r, container)
	case http.MethodDelete:
		h.deleteContainer(w, r, container)
	case http.MethodGet, http.MethodHead:
		if q.Get("comp") == compList {
			h.listBlobs(w, r, container, q)
			return
		}

		h.getContainerProperties(w, r, container)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) blobOp(w http.ResponseWriter, r *http.Request, container, blob string) {
	switch r.Method {
	case http.MethodPut:
		h.putBlobOp(w, r, container, blob)
	case http.MethodGet:
		h.getBlob(w, r, container, blob)
	case http.MethodHead:
		h.headBlob(w, r, container, blob)
	case http.MethodDelete:
		h.deleteBlob(w, r, container, blob)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// putBlobOp dispatches a PUT on a blob by its ?comp= sub-operation. Azure
// overloads PUT: plain Put Blob writes content, but comp=block/blocklist/
// metadata/properties/tier/snapshot/appendblock each mean something different
// and must NOT be treated as a content write (doing so corrupts the blob).
func (h *Handler) putBlobOp(w http.ResponseWriter, r *http.Request, container, blob string) {
	comp := r.URL.Query().Get("comp")

	ext, hasExt := h.bucket.(storagedriver.AzureBlobExtensions)
	if comp != "" && hasExt && h.dispatchBlobComp(w, r, ext, container, blob, comp) {
		return
	}

	if r.Header.Get("X-Ms-Copy-Source") != "" {
		h.copyBlob(w, r, container, blob)
		return
	}

	if strings.EqualFold(r.Header.Get("x-ms-blob-type"), "AppendBlob") && hasExt {
		h.createAppendBlob(w, r, ext, container, blob)
		return
	}

	h.putBlob(w, r, container, blob)
}

// dispatchBlobComp routes a PUT comp sub-operation to its handler, returning
// true when it handled the request and false for an unrecognized comp value.
func (h *Handler) dispatchBlobComp(
	w http.ResponseWriter, r *http.Request, ext storagedriver.AzureBlobExtensions, container, blob, comp string,
) bool {
	switch comp {
	case compBlock:
		h.stageBlock(w, r, ext, container, blob)
	case compBlockList:
		h.commitBlockList(w, r, ext, container, blob)
	case compMetadata:
		h.setBlobMetadata(w, r, ext, container, blob)
	case compProperties:
		h.setBlobProperties(w, r, ext, container, blob)
	case compTier:
		h.setBlobTier(w, r, ext, container, blob)
	case compSnapshot:
		h.snapshotBlob(w, r, ext, container, blob)
	case compAppendBlock:
		h.appendBlock(w, r, ext, container, blob)
	default:
		return false
	}

	return true
}

func (h *Handler) listContainers(w http.ResponseWriter, r *http.Request) {
	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listContainersResult{}
	for _, b := range buckets {
		out.Containers.Containers = append(out.Containers.Containers, containerXML{
			Name: b.Name,
			Properties: containerPropsXML{
				LastModified: httpDate(b.CreatedAt),
				ETag:         containerETag(b.Name, b.CreatedAt),
			},
		})
	}

	writeXML(w, http.StatusOK, out)
}

func (h *Handler) createContainer(w http.ResponseWriter, r *http.Request, container string) {
	if err := h.bucket.CreateBucket(r.Context(), container); err != nil {
		writeErr(w, err)
		return
	}

	created, _ := h.containerCreatedAt(r, container)
	w.Header().Set("ETag", containerETag(container, created))
	w.Header().Set("Last-Modified", httpDate(created))
	w.WriteHeader(http.StatusCreated)
}

// containerCreatedAt looks up a container's creation time from the driver.
func (h *Handler) containerCreatedAt(r *http.Request, container string) (string, bool) {
	buckets, err := h.bucket.ListBuckets(r.Context())
	if err != nil {
		return "", false
	}
	for _, b := range buckets {
		if b.Name == container {
			return b.CreatedAt, true
		}
	}
	return "", false
}

// containerETag derives a stable per-container ETag from the container's
// name and creation time — unique even for containers created within the
// same clock second (which is every container under a pinned fake clock).
func containerETag(name, createdAt string) string {
	sum := crc32.ChecksumIEEE([]byte(name + "|" + createdAt))
	return fmt.Sprintf("%q", "0x"+strings.ToUpper(fmt.Sprintf("%x", sum)))
}

func (h *Handler) deleteContainer(w http.ResponseWriter, r *http.Request, container string) {
	err := h.bucket.DeleteBucket(r.Context(), container)

	// Real Azure deletes a container together with its blobs; the portable
	// driver refuses non-empty deletes, so empty it here and retry.
	if err != nil && cerrors.IsFailedPrecondition(err) {
		if derr := h.emptyContainer(r, container); derr != nil {
			writeErr(w, derr)
			return
		}
		err = h.bucket.DeleteBucket(r.Context(), container)
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// emptyContainer deletes every blob in the container. A blob that vanishes
// between list and delete (concurrent deletes) is fine; the pass budget
// bounds the loop so a racing writer cannot spin it forever.
func (h *Handler) emptyContainer(r *http.Request, container string) error {
	const maxPasses = 1000
	for range maxPasses {
		list, err := h.bucket.ListObjects(r.Context(), container, storagedriver.ListOptions{})
		if err != nil {
			return err
		}
		if len(list.Objects) == 0 {
			return nil
		}
		for i := range list.Objects {
			err := h.bucket.DeleteObject(r.Context(), container, list.Objects[i].Key)
			if err != nil && !cerrors.IsNotFound(err) {
				return err
			}
		}
	}
	return cerrors.Newf(cerrors.FailedPrecondition,
		"container %q kept receiving blobs during delete", container)
}

func (h *Handler) getContainerProperties(w http.ResponseWriter, r *http.Request, container string) {
	created, ok := h.containerCreatedAt(r, container)
	if !ok {
		writeError(w, http.StatusNotFound, "ContainerNotFound",
			"container "+container+" not found")
		return
	}
	w.Header().Set("ETag", containerETag(container, created))
	w.Header().Set("Last-Modified", httpDate(created))

	if ext, ok := h.bucket.(storagedriver.AzureBlobExtensions); ok {
		if meta, err := ext.ContainerMetadata(r.Context(), container); err == nil {
			for k, v := range meta {
				w.Header().Set("x-ms-meta-"+k, v)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listBlobs(w http.ResponseWriter, r *http.Request, container string, q url.Values) {
	maxResults := defaultMaxResults

	if v := q.Get("maxresults"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxResults = n
		}
	}

	opts := storagedriver.ListOptions{
		Prefix:    q.Get("prefix"),
		Delimiter: q.Get("delimiter"),
		MaxKeys:   maxResults,
		PageToken: q.Get("marker"),
	}

	result, err := h.bucket.ListObjects(r.Context(), container, opts)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listBlobsResult{
		ContainerName: container,
		Prefix:        opts.Prefix,
		Marker:        opts.PageToken,
		Delimiter:     opts.Delimiter,
		NextMarker:    result.NextPageToken,
	}

	for i := range result.Objects {
		obj := &result.Objects[i]

		blobType := obj.BlobType
		if blobType == "" {
			blobType = blobTypeBlockBlob
		}

		out.Blobs.Blobs = append(out.Blobs.Blobs, blobXML{
			Name: obj.Key,
			Properties: blobPropsXML{
				LastModified:  httpDate(obj.LastModified),
				ETag:          fmt.Sprintf("%q", obj.ETag),
				ContentLength: obj.Size,
				ContentType:   obj.ContentType,
				BlobType:      blobType,
			},
		})
	}

	for _, p := range result.CommonPrefixes {
		out.Blobs.BlobPrefixes = append(out.Blobs.BlobPrefixes, blobPrefixXML{Name: p})
	}

	writeXML(w, http.StatusOK, out)
}

const defaultMaxResults = 5000

func (h *Handler) putBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	if h.conditionFailed(w, r, container, blob) {
		return
	}

	limited := http.MaxBytesReader(w, r.Body, maxPutBodyBytes)

	data, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "could not read body")
		return
	}

	// Per the Azure Put Blob spec, x-ms-blob-content-type sets the blob's
	// content type (the azblob SDK sends HTTPHeaders.BlobContentType there);
	// the request's own Content-Type header is only a fallback.
	contentType := r.Header.Get("x-ms-blob-content-type")
	if contentType == "" {
		contentType = r.Header.Get("Content-Type")
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	metadata := extractMetadata(r.Header)

	if err := h.bucket.PutObject(r.Context(), container, blob, data, contentType, metadata); err != nil {
		writeErr(w, err)
		return
	}

	// The driver's ETag is the hex sha256 of the body; if a concurrent
	// delete races the read-back, fall back to computing it — a successful
	// PUT must never answer 404.
	etag := fmt.Sprintf("%x", sha256.Sum256(data))
	lastModified := ""
	if info, err := h.bucket.HeadObject(r.Context(), container, blob); err == nil {
		etag = info.ETag
		lastModified = info.LastModified
	}
	w.Header().Set("ETag", fmt.Sprintf("%q", etag))
	w.Header().Set("Last-Modified", httpDate(lastModified))
	w.Header().Set("X-Ms-Request-Server-Encrypted", "true")
	w.WriteHeader(http.StatusCreated)
}

// conditionFailed evaluates the If-None-Match / If-Match conditional write
// headers against the current blob and, if a condition is not met, writes the
// Azure error and returns true. Real Azure Put Blob returns 409 when
// If-None-Match:* hits an existing blob (create-if-absent conflict) and 412
// when If-Match does not match the current ETag.
func (h *Handler) conditionFailed(w http.ResponseWriter, r *http.Request, container, blob string) bool {
	ifNoneMatch := r.Header.Get("If-None-Match")
	ifMatch := r.Header.Get("If-Match")

	if ifNoneMatch == "" && ifMatch == "" {
		return false
	}

	var curETag string

	exists := false
	if info, err := h.bucket.HeadObject(r.Context(), container, blob); err == nil {
		exists = true
		curETag = info.ETag
	}

	status, code := evalWriteConditions(ifNoneMatch, ifMatch, curETag, exists)
	if status == 0 {
		return false
	}

	writeError(w, status, code, conditionMessage(status))

	return true
}

// evalWriteConditions evaluates the If-None-Match / If-Match write conditions
// against the current blob state. It returns the HTTP status and Azure error
// code to fail with, or (0, "") when the write may proceed.
func evalWriteConditions(ifNoneMatch, ifMatch, curETag string, exists bool) (status int, code string) {
	const conditionNotMet = "ConditionNotMet"

	if ifNoneMatch == "*" && exists {
		return http.StatusConflict, "BlobAlreadyExists"
	}

	if ifMatch != "" && (!exists || !etagMatches(ifMatch, curETag)) {
		return http.StatusPreconditionFailed, conditionNotMet
	}

	if ifNoneMatch != "" && exists && etagMatches(ifNoneMatch, curETag) {
		return http.StatusPreconditionFailed, conditionNotMet
	}

	return 0, ""
}

// conditionMessage maps a conditional-failure status to its Azure message.
func conditionMessage(status int) string {
	if status == http.StatusConflict {
		return "the specified blob already exists"
	}

	return "the condition specified using HTTP conditional header(s) is not met"
}

// etagMatches compares a conditional-header ETag (which may be quoted) with a
// stored raw ETag.
func etagMatches(header, stored string) bool {
	return strings.Trim(header, `"`) == strings.Trim(stored, `"`)
}

func (h *Handler) getBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	if snapshot := r.URL.Query().Get("snapshot"); snapshot != "" {
		h.getBlobSnapshot(w, r, container, blob, snapshot)
		return
	}

	obj, err := h.bucket.GetObject(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeBlobHeaders(w, &obj.Info, int64(len(obj.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // raw object bytes
}

// getBlobSnapshot serves GET /{container}/{blob}?snapshot=… reading an
// immutable snapshot captured by Snapshot Blob.
func (h *Handler) getBlobSnapshot(w http.ResponseWriter, r *http.Request, container, blob, snapshot string) {
	ext, ok := h.bucket.(storagedriver.AzureBlobExtensions)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "snapshots not supported")
		return
	}

	obj, err := ext.GetBlobSnapshot(r.Context(), container, blob, snapshot)
	if err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Snapshot", snapshot)
	writeBlobHeaders(w, &obj.Info, int64(len(obj.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // raw snapshot bytes
}

func (h *Handler) headBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	info, err := h.bucket.HeadObject(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeBlobHeaders(w, info, info.Size)
	w.WriteHeader(http.StatusOK)
}

func writeBlobHeaders(w http.ResponseWriter, info *storagedriver.ObjectInfo, size int64) {
	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("ETag", fmt.Sprintf("%q", info.ETag))
	w.Header().Set("Last-Modified", httpDate(info.LastModified))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))

	blobType := info.BlobType
	if blobType == "" {
		blobType = blobTypeBlockBlob
	}

	w.Header().Set("X-Ms-Blob-Type", blobType)

	if info.AccessTier != "" {
		w.Header().Set("X-Ms-Access-Tier", info.AccessTier)
	}

	for k, v := range info.Metadata {
		w.Header().Set("x-ms-meta-"+k, v)
	}
}

func (h *Handler) deleteBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	if err := h.bucket.DeleteObject(r.Context(), container, blob); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) copyBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	src := r.Header.Get("X-Ms-Copy-Source")
	srcBucket, srcKey := extractCopySource(src)

	if srcBucket == "" || srcKey == "" {
		writeError(w, http.StatusBadRequest, "InvalidInput", "invalid x-ms-copy-source")
		return
	}

	if err := h.bucket.CopyObject(r.Context(), container, blob, storagedriver.CopySource{
		Bucket: srcBucket, Key: srcKey,
	}); err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("X-Ms-Copy-Id", "00000000-0000-0000-0000-000000000001")
	w.Header().Set("X-Ms-Copy-Status", "success")
	if info, err := h.bucket.HeadObject(r.Context(), container, blob); err == nil {
		w.Header().Set("ETag", fmt.Sprintf("%q", info.ETag))
		w.Header().Set("Last-Modified", httpDate(info.LastModified))
	}
	w.WriteHeader(http.StatusAccepted)
}

// extractCopySource parses x-ms-copy-source which is a full URL like
// "https://account.blob.core.windows.net/{container}/{blob}".
func extractCopySource(src string) (container, blob string) {
	u, err := url.Parse(src)
	if err != nil {
		return "", ""
	}

	return parseBlobPath(u.Path)
}

func extractMetadata(h http.Header) map[string]string {
	meta := make(map[string]string)

	for k, vals := range h {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-ms-meta-") && len(vals) > 0 {
			meta[strings.TrimPrefix(lower, "x-ms-meta-")] = vals[0]
		}
	}

	if len(meta) == 0 {
		return nil
	}

	return meta
}

func httpDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(http.TimeFormat)
	}

	return time.Now().UTC().Format(http.TimeFormat)
}

func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeXML)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", contentTypeXML)
	w.Header().Set("X-Ms-Error-Code", code)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(errorXML{Code: code, Message: msg})
}

// writeErr maps CloudEmu canonical errors to Azure Blob HTTP errors.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "BlobNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ContainerAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusConflict, "Conflict", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
