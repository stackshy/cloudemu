// Package s3 implements the S3 REST+XML protocol as a server.Handler.
// Point the real aws-sdk-go-v2 S3 client at a Server registered with this
// handler and operations work against an in-memory storage driver.
package s3

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire"
	"github.com/stackshy/cloudemu/storage/driver"
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
	if err := h.bucket.DeleteObject(r.Context(), bucket, key); err != nil {
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
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NoSuchKey", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "BucketAlreadyOwnedByYou", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidArgument", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
