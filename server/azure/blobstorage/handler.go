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
// When account-level versioning is enabled, every blob write mints a new
// version (x-ms-version-id); versions are readable/deletable via ?versionid= and
// listable via include=versions. An unrecognized or unimplemented blob-PUT comp
// selector fails closed with an Azure error rather than falling through to a
// content write.
package blobstorage

import (
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
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
	compLease       = "lease"
	compTags        = "tags"
	compPage        = "page"
	compUndelete    = "undelete"

	// blobTypePageBlob is the Azure page-blob type; cloudemu does not implement
	// page-blob range semantics, so a page-blob create/write fails closed.
	blobTypePageBlob = "PageBlob"

	// compACL is the ?comp= value for Set/Get Container ACL
	// (?restype=container&comp=acl).
	compACL = "acl"
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

	if enforceSAS(w, r, blob) {
		return
	}

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
		switch q.Get("comp") {
		case compMetadata:
			h.setContainerMetadata(w, r, container)
		case compACL:
			h.setContainerACL(w, r, container)
		default:
			h.createContainer(w, r, container)
		}
	case http.MethodDelete:
		h.deleteContainer(w, r, container)
	case http.MethodGet, http.MethodHead:
		switch q.Get("comp") {
		case compList:
			h.listBlobs(w, r, container, q)
		case compACL:
			h.getContainerACL(w, r, container)
		default:
			h.getContainerProperties(w, r, container)
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) blobOp(w http.ResponseWriter, r *http.Request, container, blob string) {
	switch r.Method {
	case http.MethodPut:
		h.putBlobOp(w, r, container, blob)
	case http.MethodGet:
		if r.URL.Query().Get("comp") == compTags {
			h.getBlobTags(w, r, container, blob)
			return
		}

		if ext, ok := h.bucket.(storagedriver.AzureBlobExtensions); ok && r.URL.Query().Get("comp") == compBlockList {
			h.getBlockList(w, r, ext, container, blob)
			return
		}

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
	// A blob PUT carrying a ?comp= selector is a distinct sub-operation, never a
	// content write. An unrecognized or unimplemented comp MUST fail closed:
	// falling through to Put Blob would write the sub-operation's request body
	// over the blob's content (silent data corruption).
	if comp := r.URL.Query().Get("comp"); comp != "" {
		h.putBlobComp(w, r, container, blob, comp)
		return
	}

	if r.Header.Get("X-Ms-Copy-Source") != "" {
		h.copyBlob(w, r, container, blob)
		return
	}

	blobType := r.Header.Get("X-Ms-Blob-Type")

	if ext, ok := h.bucket.(storagedriver.AzureBlobExtensions); ok && strings.EqualFold(blobType, "AppendBlob") {
		h.createAppendBlob(w, r, ext, container, blob)
		return
	}

	// Page blobs are not implemented; fail closed rather than silently creating
	// a block blob (which would misreport page-blob support and, on an existing
	// blob, clobber it).
	if strings.EqualFold(blobType, blobTypePageBlob) {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "page blobs are not supported")
		return
	}

	h.putBlob(w, r, container, blob)
}

// putBlobComp routes a blob PUT ?comp= sub-operation. Any comp it does not
// implement fails closed with an Azure error so an unknown selector can never
// fall through to a content write and corrupt the blob body.
func (h *Handler) putBlobComp(w http.ResponseWriter, r *http.Request, container, blob, comp string) {
	if comp == compTags {
		h.setBlobTags(w, r, container, blob)
		return
	}

	if comp == compUndelete {
		h.undeleteBlob(w, r, container, blob)
		return
	}

	if ext, ok := h.bucket.(storagedriver.AzureBlobExtensions); ok && h.dispatchBlobComp(w, r, ext, container, blob, comp) {
		return
	}

	if comp == compPage {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "page blobs are not supported")
		return
	}

	writeError(w, http.StatusBadRequest, "UnsupportedQueryParameter",
		fmt.Sprintf("the query parameter comp=%s is not supported for a blob PUT", comp))
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
	case compLease:
		h.leaseBlob(w, r, ext, container, blob)
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

	q := r.URL.Query()
	maxResults := parseMaxResults(q)

	page, err := pagination.Paginate(buckets, q.Get("marker"), maxResults)
	if err != nil {
		writeError(w, http.StatusBadRequest, "OutOfRangeInput", "invalid marker")
		return
	}

	out := listContainersResult{
		Marker:     q.Get("marker"),
		MaxResults: maxResults,
		NextMarker: page.NextPageToken,
	}

	for i := range page.Items {
		b := &page.Items[i]

		out.Containers.Containers = append(out.Containers.Containers, containerXML{
			Name: b.Name,
			Properties: containerPropsXML{
				LastModified: httpDate(b.CreatedAt),
				ETag:         containerETag(b.Name, b.CreatedAt),
			},
		})
	}

	writeXML(w, out)
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
	if ext, ok := h.bucket.(storagedriver.AzureVersionedBlob); ok && includesOption(q.Get("include"), "versions") {
		h.listBlobVersions(w, r, ext, container, q)
		return
	}

	maxResults := parseMaxResults(q)

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

	includeMetadata := includesOption(q.Get("include"), "metadata")
	out.Blobs.Blobs = appendLiveBlobs(nil, result.Objects, includeMetadata)

	for _, p := range result.CommonPrefixes {
		out.Blobs.BlobPrefixes = append(out.Blobs.BlobPrefixes, blobPrefixXML{Name: p})
	}

	if ext, ok := h.bucket.(storagedriver.AzureSoftDeleteBlob); ok && includesOption(q.Get("include"), "deleted") {
		if err := h.appendDeletedBlobs(r, ext, container, opts.Prefix, &out); err != nil {
			writeErr(w, err)
			return
		}
	}

	writeXML(w, out)
}

// appendLiveBlobs renders each listed live blob as a blobXML, attaching metadata
// when the caller asked for include=metadata.
func appendLiveBlobs(dst []blobXML, objects []storagedriver.ObjectInfo, includeMetadata bool) []blobXML {
	for i := range objects {
		obj := &objects[i]

		blobType := obj.BlobType
		if blobType == "" {
			blobType = blobTypeBlockBlob
		}

		bx := blobXML{
			Name: obj.Key,
			Properties: blobPropsXML{
				LastModified:  httpDate(obj.LastModified),
				ETag:          fmt.Sprintf("%q", obj.ETag),
				ContentLength: obj.Size,
				ContentType:   obj.ContentType,
				BlobType:      blobType,
				AccessTier:    obj.AccessTier,
			},
		}

		if includeMetadata && len(obj.Metadata) > 0 {
			bx.Metadata = &metadataXML{Items: obj.Metadata}
		}

		dst = append(dst, bx)
	}

	return dst
}

// appendDeletedBlobs merges the container's soft-deleted blobs into a List Blobs
// include=deleted response, each marked Deleted=true with its DeletedTime and
// RemainingRetentionDays, and re-sorts the combined blob list by name so live
// and soft-deleted blobs interleave as real Azure returns them.
func (*Handler) appendDeletedBlobs(
	r *http.Request, ext storagedriver.AzureSoftDeleteBlob, container, prefix string, out *listBlobsResult,
) error {
	res, err := ext.ListDeletedBlobs(r.Context(), container, storagedriver.ListOptions{Prefix: prefix})
	if err != nil {
		return err
	}

	deletedTrue := true

	for i := range res.Blobs {
		d := &res.Blobs[i]
		remaining := clampRetentionDays(d.RemainingRetentionDays)

		out.Blobs.Blobs = append(out.Blobs.Blobs, blobXML{
			Name: d.Info.Key,
			Properties: blobPropsXML{
				LastModified:           httpDate(d.Info.LastModified),
				ETag:                   fmt.Sprintf("%q", d.Info.ETag),
				ContentLength:          d.Info.Size,
				ContentType:            d.Info.ContentType,
				BlobType:               blobTypeBlockBlob,
				DeletedTime:            httpDate(d.DeletedTime),
				RemainingRetentionDays: &remaining,
			},
			Deleted: &deletedTrue,
		})
	}

	sort.Slice(out.Blobs.Blobs, func(i, j int) bool {
		return out.Blobs.Blobs[i].Name < out.Blobs.Blobs[j].Name
	})

	return nil
}

// clampRetentionDays narrows a non-negative day count to the int32 the wire
// field carries, saturating at the int32 max (a remaining-retention window can
// never approach that, so this only exists to keep the conversion provably
// overflow-free).
func clampRetentionDays(days int) int32 {
	if days < 0 {
		return 0
	}

	if days > math.MaxInt32 {
		return math.MaxInt32
	}

	return int32(days)
}

// listBlobVersions serves GET /{container}?restype=container&comp=list&include=versions,
// rendering every version of the matching blobs with its VersionId and
// IsCurrentVersion marker. Versions sort by blob name then version id, so the
// current version (newest id) appears last within a name, matching Azure.
func (*Handler) listBlobVersions(
	w http.ResponseWriter, r *http.Request, ext storagedriver.AzureVersionedBlob, container string, q url.Values,
) {
	maxResults := parseMaxResults(q)

	res, err := ext.ListBlobVersions(r.Context(), container, storagedriver.ListOptions{
		Prefix: q.Get("prefix"), MaxKeys: maxResults, PageToken: q.Get("marker"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	page, err := pagination.Paginate(res.Versions, q.Get("marker"), maxResults)
	if err != nil {
		writeError(w, http.StatusBadRequest, "OutOfRangeInput", "invalid marker")
		return
	}

	out := listBlobsResult{
		ContainerName: container,
		Prefix:        q.Get("prefix"),
		Marker:        q.Get("marker"),
		NextMarker:    page.NextPageToken,
	}

	for i := range page.Items {
		v := &page.Items[i]
		isCurrent := v.IsLatest

		out.Blobs.Blobs = append(out.Blobs.Blobs, blobXML{
			Name: v.Key,
			Properties: blobPropsXML{
				LastModified:  httpDate(v.LastModified),
				ETag:          fmt.Sprintf("%q", v.ETag),
				ContentLength: v.Size,
				ContentType:   v.ContentType,
				BlobType:      blobTypeBlockBlob,
			},
			VersionID:        v.VersionID,
			IsCurrentVersion: &isCurrent,
		})
	}

	writeXML(w, out)
}

const defaultMaxResults = 5000

// parseMaxResults reads ?maxresults= off q, falling back to defaultMaxResults
// when it's absent, non-numeric, or not positive.
func parseMaxResults(q url.Values) int {
	v := q.Get("maxresults")
	if v == "" {
		return defaultMaxResults
	}

	n, convErr := strconv.Atoi(v)
	if convErr != nil || n <= 0 {
		return defaultMaxResults
	}

	return n
}

// includesOption reports whether the comma-separated ?include= value (e.g.
// "metadata,tags") contains want.
func includesOption(include, want string) bool {
	for _, part := range strings.Split(include, ",") {
		if strings.EqualFold(strings.TrimSpace(part), want) {
			return true
		}
	}

	return false
}

func (h *Handler) putBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, true) {
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

	if err := h.storePutBlob(r, container, blob, data, contentType, metadata); err != nil {
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
		setIfNonEmpty(w, "x-ms-version-id", info.VersionID)
	}
	w.Header().Set("ETag", fmt.Sprintf("%q", etag))
	w.Header().Set("Last-Modified", httpDate(lastModified))
	w.Header().Set("X-Ms-Request-Server-Encrypted", "true")
	w.WriteHeader(http.StatusCreated)
}

// storePutBlob writes a Put Blob's content. When the driver implements the
// Azure extensions it routes through PutBlockBlob so the request's system
// content properties (Content-Encoding/Cache-Control/Content-Language/
// Content-Disposition) are persisted and round-trip on a later read; otherwise
// it falls back to the plain PutObject path.
func (h *Handler) storePutBlob(r *http.Request, container, blob string, data []byte, contentType string, metadata map[string]string) error {
	if ext, ok := h.bucket.(storagedriver.AzureBlobExtensions); ok {
		props := &storagedriver.BlobProperties{ContentType: contentType}
		if cp := blobContentProps(r); cp != nil {
			props.ContentEncoding = cp.ContentEncoding
			props.CacheControl = cp.CacheControl
			props.ContentLanguage = cp.ContentLanguage
			props.ContentDisposition = cp.ContentDisposition
		}

		_, err := ext.PutBlockBlob(r.Context(), container, blob, data, props, metadata)

		return err
	}

	return h.bucket.PutObject(r.Context(), container, blob, data, contentType, metadata)
}

// blobContentProps reads the four x-ms-blob-* system content-property headers
// (Content-Encoding/Cache-Control/Content-Language/Content-Disposition) into a
// BlobProperties, or returns nil when none are set. ContentType is left to the
// caller, which has its own x-ms-blob-content-type fallback chain.
func blobContentProps(r *http.Request) *storagedriver.BlobProperties {
	enc := r.Header.Get("X-Ms-Blob-Content-Encoding")
	cache := r.Header.Get("X-Ms-Blob-Cache-Control")
	lang := r.Header.Get("X-Ms-Blob-Content-Language")
	disp := r.Header.Get("X-Ms-Blob-Content-Disposition")

	if enc == "" && cache == "" && lang == "" && disp == "" {
		return nil
	}

	return &storagedriver.BlobProperties{
		ContentEncoding: enc, CacheControl: cache,
		ContentLanguage: lang, ContentDisposition: disp,
	}
}

// conditionFailed evaluates the If-None-Match / If-Match conditional write
// headers against the current blob and, if a condition is not met, writes the
// Azure error and returns true. Every mutating data-plane op must call this so
// optimistic-concurrency writes on a stale ETag are rejected rather than
// silently losing the update. isCreate selects create semantics for a
// If-None-Match:* conflict: a create (Put Blob / create Append Blob) returns
// 409 BlobAlreadyExists, every other write returns 412 (per the write-operations
// response-code table). A mismatched If-Match always yields 412.
func (h *Handler) conditionFailed(w http.ResponseWriter, r *http.Request, container, blob string, isCreate bool) bool {
	c := writeConditions{
		ifNoneMatch:       r.Header.Get("If-None-Match"),
		ifMatch:           r.Header.Get("If-Match"),
		ifUnmodifiedSince: r.Header.Get("If-Unmodified-Since"),
		ifModifiedSince:   r.Header.Get("If-Modified-Since"),
	}

	if c.empty() {
		return false
	}

	var curETag, lastModified string

	exists := false
	if info, err := h.bucket.HeadObject(r.Context(), container, blob); err == nil {
		exists = true
		curETag = info.ETag
		lastModified = info.LastModified
	}

	status, code := evalWriteConditions(c, curETag, lastModified, exists, isCreate)
	if status == 0 {
		return false
	}

	writeError(w, status, code, conditionMessage(status))

	return true
}

// writeConditions bundles the conditional headers evaluated on a mutating
// data-plane op: the two ETag conditions plus the two time-based conditions.
type writeConditions struct {
	ifNoneMatch       string
	ifMatch           string
	ifUnmodifiedSince string
	ifModifiedSince   string
}

func (c writeConditions) empty() bool {
	return c.ifNoneMatch == "" && c.ifMatch == "" && c.ifUnmodifiedSince == "" && c.ifModifiedSince == ""
}

// evalWriteConditions evaluates the If-None-Match / If-Match / If-Unmodified-
// Since / If-Modified-Since write conditions against the current blob state. It
// returns the HTTP status and Azure error code to fail with, or (0, "") when
// the write may proceed. ETag conditions are evaluated first (they take
// precedence), then the time-based conditions.
func evalWriteConditions(c writeConditions, curETag, lastModified string, exists, isCreate bool) (status int, code string) {
	if status, code := evalWriteETagConditions(c, curETag, exists, isCreate); status != 0 {
		return status, code
	}

	return evalWriteTimeConditions(c, lastModified, exists)
}

// evalWriteETagConditions evaluates the If-None-Match / If-Match write ETag
// conditions, which take precedence over the time-based ones.
func evalWriteETagConditions(c writeConditions, curETag string, exists, isCreate bool) (status int, code string) {
	const conditionNotMet = "ConditionNotMet"

	if c.ifNoneMatch == "*" && exists {
		if isCreate {
			return http.StatusConflict, "BlobAlreadyExists"
		}

		return http.StatusPreconditionFailed, conditionNotMet
	}

	if c.ifMatch != "" && (!exists || !etagMatches(c.ifMatch, curETag)) {
		return http.StatusPreconditionFailed, conditionNotMet
	}

	if c.ifNoneMatch != "" && exists && etagMatches(c.ifNoneMatch, curETag) {
		return http.StatusPreconditionFailed, conditionNotMet
	}

	return 0, ""
}

// evalWriteTimeConditions evaluates the If-Unmodified-Since / If-Modified-Since
// write conditions against the blob's LastModified.
func evalWriteTimeConditions(c writeConditions, lastModified string, exists bool) (status int, code string) {
	const conditionNotMet = "ConditionNotMet"

	if !exists {
		return 0, ""
	}

	if c.ifUnmodifiedSince != "" && blobModifiedSince(lastModified, c.ifUnmodifiedSince) {
		return http.StatusPreconditionFailed, conditionNotMet
	}

	if c.ifModifiedSince != "" && !blobModifiedSince(lastModified, c.ifModifiedSince) {
		return http.StatusPreconditionFailed, conditionNotMet
	}

	return 0, ""
}

// blobModifiedSince reports whether a blob's stored LastModified (RFC3339) is
// strictly after the HTTP-date value of a conditional header. An unparseable
// timestamp on either side yields false so the condition does not fire.
func blobModifiedSince(lastModified, header string) bool {
	ht, err := http.ParseTime(strings.TrimSpace(header))
	if err != nil {
		return false
	}

	lm, err := time.Parse(time.RFC3339, lastModified)
	if err != nil {
		return false
	}

	return lm.After(ht)
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
	if versionID := r.URL.Query().Get("versionid"); versionID != "" {
		h.getBlobVersion(w, r, container, blob, versionID)
		return
	}

	if snapshot := r.URL.Query().Get("snapshot"); snapshot != "" {
		h.getBlobSnapshot(w, r, container, blob, snapshot)
		return
	}

	obj, err := h.bucket.GetObject(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	if readConditionHandled(w, r, obj.Info.ETag, obj.Info.LastModified) {
		return
	}

	serveBlobContent(w, r, &obj.Info, obj.Data)
}

// serveBlobContent writes a blob's body, honoring an optional byte range
// (x-ms-range preferred, else the standard Range header). With no range it
// returns the full body with 200 OK; a satisfiable range returns 206 Partial
// Content with a Content-Range header and only the requested slice; a
// syntactically invalid or unsatisfiable range returns 416 with
// Content-Range: bytes * /total, matching Azure. (x-ms-range-get-content-md5 is
// not honored — cloudemu does not compute the per-range MD5.)
func serveBlobContent(w http.ResponseWriter, r *http.Request, info *storagedriver.ObjectInfo, data []byte) {
	total := int64(len(data))

	rangeHeader := r.Header.Get("X-Ms-Range")
	if rangeHeader == "" {
		rangeHeader = r.Header.Get("Range")
	}

	if rangeHeader == "" {
		writeBlobHeaders(w, info, total)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data) //nolint:gosec // raw object bytes

		return
	}

	start, end, ok := parseByteRange(rangeHeader, total)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "InvalidRange",
			"the range specified is invalid for the current size of the resource")

		return
	}

	writeBlobHeaders(w, info, end-start+1)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(data[start : end+1]) //nolint:gosec // bounded slice of object bytes
}

// parseByteRange parses a single HTTP/Azure byte-range spec ("bytes=start-end",
// "bytes=start-", or the suffix form "bytes=-N") against a total size, returning
// the inclusive [start,end] bounds. ok is false for a malformed, multi-range,
// or unsatisfiable spec (which the caller turns into a 416).
func parseByteRange(header string, total int64) (start, end int64, ok bool) {
	const prefix = "bytes="

	if !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}

	spec := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, false // multi-range is not supported.
	}

	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false
	}

	startStr := strings.TrimSpace(spec[:dash])
	endStr := strings.TrimSpace(spec[dash+1:])

	if startStr == "" {
		return suffixRange(endStr, total)
	}

	return boundedRange(startStr, endStr, total)
}

// suffixRange resolves a "bytes=-N" spec (the final N bytes) against total.
func suffixRange(nStr string, total int64) (start, end int64, ok bool) {
	n, err := strconv.ParseInt(nStr, 10, 64)
	if err != nil || n <= 0 || total == 0 {
		return 0, 0, false
	}

	if n > total {
		n = total
	}

	return total - n, total - 1, true
}

// boundedRange resolves a "bytes=start-" or "bytes=start-end" spec against total.
func boundedRange(startStr, endStr string, total int64) (start, end int64, ok bool) {
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 || start >= total {
		return 0, 0, false
	}

	if endStr == "" {
		return start, total - 1, true
	}

	end, err = strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}

	if end >= total {
		end = total - 1
	}

	return start, end, true
}

// readConditionHandled evaluates the If-Match / If-None-Match read conditional
// headers against the current blob ETag before any body is written. It returns
// true, having written the response, when the read must short-circuit: a
// mismatched If-Match yields 412 Precondition Failed, and an If-None-Match that
// matches the current ETag (including the wildcard *) yields 304 Not Modified
// with no body. Returns false to let the read proceed.
func readConditionHandled(w http.ResponseWriter, r *http.Request, curETag, lastModified string) bool {
	if !hasReadConditions(r) {
		return false
	}

	if readPreconditionFailed(r, curETag, lastModified) {
		writeError(w, http.StatusPreconditionFailed, "ConditionNotMet", conditionMessage(http.StatusPreconditionFailed))
		return true
	}

	if readNotModified(r, curETag, lastModified) {
		w.Header().Set("ETag", fmt.Sprintf("%q", curETag))
		w.Header().Set("Last-Modified", httpDate(lastModified))
		w.WriteHeader(http.StatusNotModified)

		return true
	}

	return false
}

// hasReadConditions reports whether the request carries any read conditional
// header (the two ETag conditions or the two time-based conditions).
func hasReadConditions(r *http.Request) bool {
	return r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Match") != "" ||
		r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-Unmodified-Since") != ""
}

// readPreconditionFailed reports whether a read must fail 412: a mismatched
// If-Match, or (when no If-Match is set) an If-Unmodified-Since the blob was
// modified after. If-Match takes precedence over If-Unmodified-Since.
func readPreconditionFailed(r *http.Request, curETag, lastModified string) bool {
	if ifMatch := r.Header.Get("If-Match"); ifMatch != "" {
		return !etagCondMatches(ifMatch, curETag)
	}

	if unmod := r.Header.Get("If-Unmodified-Since"); unmod != "" {
		return blobModifiedSince(lastModified, unmod)
	}

	return false
}

// readNotModified reports whether a read must short-circuit to 304: an
// If-None-Match that matches the current ETag, or (when no If-None-Match is set)
// an If-Modified-Since the blob has not been modified since. If-None-Match takes
// precedence over If-Modified-Since.
func readNotModified(r *http.Request, curETag, lastModified string) bool {
	if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch != "" {
		return etagCondMatches(ifNoneMatch, curETag)
	}

	if mod := r.Header.Get("If-Modified-Since"); mod != "" {
		return !blobModifiedSince(lastModified, mod)
	}

	return false
}

// etagCondMatches reports whether a conditional-header ETag matches the stored
// ETag. The wildcard "*" matches any existing resource.
func etagCondMatches(header, stored string) bool {
	if strings.TrimSpace(header) == "*" {
		return true
	}

	return etagMatches(header, stored)
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
	serveBlobContent(w, r, &obj.Info, obj.Data)
}

// getBlobVersion serves GET /{container}/{blob}?versionid=… reading a specific
// version minted while account-level versioning was enabled.
func (h *Handler) getBlobVersion(w http.ResponseWriter, r *http.Request, container, blob, versionID string) {
	ext, ok := h.bucket.(storagedriver.AzureVersionedBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "blob versioning not supported")
		return
	}

	obj, err := ext.GetBlobVersion(r.Context(), container, blob, versionID)
	if err != nil {
		writeErr(w, err)
		return
	}

	serveBlobContent(w, r, &obj.Info, obj.Data)
}

func (h *Handler) headBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	if versionID := r.URL.Query().Get("versionid"); versionID != "" {
		h.headBlobVersion(w, r, container, blob, versionID)
		return
	}

	info, err := h.bucket.HeadObject(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	if readConditionHandled(w, r, info.ETag, info.LastModified) {
		return
	}

	writeBlobHeaders(w, info, info.Size)
	w.WriteHeader(http.StatusOK)
}

// headBlobVersion serves HEAD /{container}/{blob}?versionid=… returning a
// specific version's headers without its body.
func (h *Handler) headBlobVersion(w http.ResponseWriter, r *http.Request, container, blob, versionID string) {
	ext, ok := h.bucket.(storagedriver.AzureVersionedBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "blob versioning not supported")
		return
	}

	info, err := ext.HeadBlobVersion(r.Context(), container, blob, versionID)
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
	w.Header().Set("Accept-Ranges", "bytes")

	blobType := info.BlobType
	if blobType == "" {
		blobType = blobTypeBlockBlob
	}

	w.Header().Set("X-Ms-Blob-Type", blobType)

	if info.AccessTier != "" {
		w.Header().Set("X-Ms-Access-Tier", info.AccessTier)
	}

	// On a versioning-enabled account, reads carry the served version's id.
	setIfNonEmpty(w, "x-ms-version-id", info.VersionID)

	// System content properties round-trip on read (Get Blob / Get Blob
	// Properties) so tools like Terraform's azurerm_storage_blob don't see drift.
	setIfNonEmpty(w, "Cache-Control", info.CacheControl)
	setIfNonEmpty(w, "Content-Encoding", info.ContentEncoding)
	setIfNonEmpty(w, "Content-Language", info.ContentLanguage)
	setIfNonEmpty(w, "Content-Disposition", info.ContentDisposition)

	for k, v := range info.Metadata {
		w.Header().Set("x-ms-meta-"+k, v)
	}
}

// setIfNonEmpty sets header key to v only when v is non-empty, so an unset
// property does not emit a blank header.
func setIfNonEmpty(w http.ResponseWriter, key, v string) {
	if v != "" {
		w.Header().Set(key, v)
	}
}

func (h *Handler) deleteBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	if versionID := r.URL.Query().Get("versionid"); versionID != "" {
		h.deleteBlobVersion(w, r, container, blob, versionID)
		return
	}

	if h.checkLease(w, r, container, blob) {
		return
	}

	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	deleteBase := true

	if ext, ok := h.bucket.(storagedriver.AzureBlobExtensions); ok {
		var err error

		deleteBase, err = ext.DeleteBlobSnapshots(r.Context(), container, blob, r.Header.Get("X-Ms-Delete-Snapshots"))
		if err != nil {
			writeErr(w, err)
			return
		}
	}

	if deleteBase {
		if err := h.bucket.DeleteObject(r.Context(), container, blob); err != nil {
			writeErr(w, err)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// deleteBlobVersion serves DELETE /{container}/{blob}?versionid=… permanently
// removing one version. Deleting the base blob itself (no versionid) leaves the
// existing versions intact — that path runs through the normal deleteBlob flow.
func (h *Handler) deleteBlobVersion(w http.ResponseWriter, r *http.Request, container, blob, versionID string) {
	ext, ok := h.bucket.(storagedriver.AzureVersionedBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "blob versioning not supported")
		return
	}

	if err := ext.DeleteBlobVersion(r.Context(), container, blob, versionID); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// undeleteBlob serves PUT /{container}/{blob}?comp=undelete, restoring a
// soft-deleted blob to active. Requires the AzureSoftDeleteBlob capability.
func (h *Handler) undeleteBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	ext, ok := h.bucket.(storagedriver.AzureSoftDeleteBlob)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "blob soft delete not supported")
		return
	}

	if err := ext.UndeleteBlob(r.Context(), container, blob); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) copyBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	// Copy Blob requires the destination's lease id (if it has an active
	// lease); the source blob needs none.
	if h.checkLease(w, r, container, blob) {
		return
	}

	// The bare If-*/If-None-Match conditional headers apply to the destination
	// blob (source conditions use the x-ms-source-if-* headers, unsupported here).
	if h.conditionFailed(w, r, container, blob, false) {
		return
	}

	src := r.Header.Get("X-Ms-Copy-Source")
	srcBucket, srcKey := extractCopySource(src)

	if srcBucket == "" || srcKey == "" {
		writeError(w, http.StatusBadRequest, "InvalidInput", "invalid x-ms-copy-source")
		return
	}

	if err := h.performCopy(r, container, blob, storagedriver.CopySource{
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
		setIfNonEmpty(w, "x-ms-version-id", info.VersionID)
	}
	w.WriteHeader(http.StatusAccepted)
}

// performCopy runs the server-side copy, honoring Azure Copy Blob metadata
// semantics: when the request supplies any x-ms-meta-* header the destination
// takes EXACTLY that metadata (full replace, nothing inherited); with none, the
// destination inherits the source's metadata. The override path routes through
// the ObjectCopier capability; the inherit path uses the basic CopyObject.
func (h *Handler) performCopy(r *http.Request, container, blob string, src storagedriver.CopySource) error {
	override := extractMetadata(r.Header)

	if override != nil {
		if copier, ok := h.bucket.(storagedriver.ObjectCopier); ok {
			_, err := copier.CopyObjectV2(r.Context(), &storagedriver.CopyObjectRequest{
				DstBucket: container, DstKey: blob, Src: src,
				ReplaceMetadata: true, Metadata: override,
			})

			return err
		}
	}

	return h.bucket.CopyObject(r.Context(), container, blob, src)
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

// writeXML writes an XML response body. Every wire operation that returns an
// XML document (list/get) does so with 200 OK on success — a write that needs
// a different success status (201/202) sets headers and writes its own body.
func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeXML)
	w.WriteHeader(http.StatusOK)
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
	var opErr *storagedriver.BlobOpError
	if errors.As(err, &opErr) {
		writeError(w, opErr.Status, opErr.Code, opErr.Message)
		return
	}

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
