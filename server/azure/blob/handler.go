// Package blob implements the Azure Blob Storage REST+XML wire protocol as a
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
package blob

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
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

	w.Header().Set("x-ms-version", xmsVersion)

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
		if r.Header.Get("x-ms-copy-source") != "" {
			h.copyBlob(w, r, container, blob)
			return
		}

		h.putBlob(w, r, container, blob)
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
				ETag:         "\"0x8DAB0\"",
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

	w.Header().Set("ETag", "\"0x8DAB0\"")
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) deleteContainer(w http.ResponseWriter, r *http.Request, container string) {
	if err := h.bucket.DeleteBucket(r.Context(), container); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (*Handler) getContainerProperties(w http.ResponseWriter, _ *http.Request, _ string) {
	w.Header().Set("ETag", "\"0x8DAB0\"")
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
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

	for _, obj := range result.Objects {
		out.Blobs.Blobs = append(out.Blobs.Blobs, blobXML{
			Name: obj.Key,
			Properties: blobPropsXML{
				LastModified:  httpDate(obj.LastModified),
				ETag:          fmt.Sprintf("%q", obj.ETag),
				ContentLength: obj.Size,
				ContentType:   obj.ContentType,
				BlobType:      "BlockBlob",
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
	limited := http.MaxBytesReader(w, r.Body, maxPutBodyBytes)

	data, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "could not read body")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	metadata := extractMetadata(r.Header)

	if err := h.bucket.PutObject(r.Context(), container, blob, data, contentType, metadata); err != nil {
		writeErr(w, err)
		return
	}

	w.Header().Set("ETag", "\"0x8DAB0\"")
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.Header().Set("x-ms-request-server-encrypted", "true")
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) getBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	obj, err := h.bucket.GetObject(r.Context(), container, blob)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeBlobHeaders(w, &obj.Info, int64(len(obj.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // raw object bytes
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
	w.Header().Set("x-ms-blob-type", "BlockBlob")

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
	src := r.Header.Get("x-ms-copy-source")
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

	w.Header().Set("x-ms-copy-id", "00000000-0000-0000-0000-000000000001")
	w.Header().Set("x-ms-copy-status", "success")
	w.Header().Set("ETag", "\"0x8DAB0\"")
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
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
	w.Header().Set("x-ms-error-code", code)
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
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
