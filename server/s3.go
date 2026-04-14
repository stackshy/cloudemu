package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/storage/driver"
)

const (
	s3DefaultMaxKeys = 1000
	s3Xmlns          = "http://s3.amazonaws.com/doc/2006-03-01/"
	// maxPutObjectSize limits PutObject request bodies to 5 GiB (S3 single PUT limit).
	maxPutObjectSize = 5 << 30
)

// handleS3 dispatches S3 REST requests based on method and path.
func (s *Server) handleS3(w http.ResponseWriter, r *http.Request) {
	bucket, key := parseS3Path(r.URL.Path)

	switch {
	case bucket == "":
		s.s3ListBuckets(w, r)
	case key == "":
		s.s3BucketOp(w, r, bucket)
	default:
		s.s3ObjectOp(w, r, bucket, key)
	}
}

// parseS3Path extracts bucket and key from a path-style URL.
// Example: "/bucket/key/with/slashes" returns ("bucket", "key/with/slashes").
func parseS3Path(path string) (bucket, key string) {
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

func (s *Server) s3ListBuckets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	buckets, err := s.drivers.Storage.ListBuckets(r.Context())
	if err != nil {
		writeS3Err(w, err)
		return
	}

	result := listAllMyBucketsResult{Xmlns: s3Xmlns}
	for _, b := range buckets {
		result.Buckets = append(result.Buckets, bucketXML{
			Name: b.Name, CreationDate: b.CreatedAt,
		})
	}

	writeXML(w, http.StatusOK, result)
}

func (s *Server) s3BucketOp(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		s.s3CreateBucket(w, r, bucket)
	case http.MethodDelete:
		s.s3DeleteBucket(w, r, bucket)
	case http.MethodGet:
		s.s3ListObjects(w, r, bucket)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) s3CreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := s.drivers.Storage.CreateBucket(r.Context(), bucket); err != nil {
		writeS3Err(w, err)
		return
	}

	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) s3DeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := s.drivers.Storage.DeleteBucket(r.Context(), bucket); err != nil {
		writeS3Err(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) s3ListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	opts := driver.ListOptions{
		Prefix:    r.URL.Query().Get("prefix"),
		Delimiter: r.URL.Query().Get("delimiter"),
		PageToken: r.URL.Query().Get("continuation-token"),
	}

	result, err := s.drivers.Storage.ListObjects(r.Context(), bucket, opts)
	if err != nil {
		writeS3Err(w, err)
		return
	}

	resp := listBucketResult{
		Xmlns:       s3Xmlns,
		Name:        bucket,
		Prefix:      opts.Prefix,
		Delimiter:   opts.Delimiter,
		MaxKeys:     s3DefaultMaxKeys,
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

	writeXML(w, http.StatusOK, resp)
}

func (s *Server) s3ObjectOp(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			s.s3CopyObject(w, r, bucket, key)
		} else {
			s.s3PutObject(w, r, bucket, key)
		}
	case http.MethodGet:
		s.s3GetObject(w, r, bucket, key)
	case http.MethodHead:
		s.s3HeadObject(w, r, bucket, key)
	case http.MethodDelete:
		s.s3DeleteObject(w, r, bucket, key)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *Server) s3PutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	limited := http.MaxBytesReader(w, r.Body, maxPutObjectSize)

	data, err := io.ReadAll(limited)
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, "IncompleteBody", "could not read body")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	metadata := extractS3Metadata(r.Header)

	if err := s.drivers.Storage.PutObject(r.Context(), bucket, key, data, contentType, metadata); err != nil {
		writeS3Err(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) s3GetObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	obj, err := s.drivers.Storage.GetObject(r.Context(), bucket, key)
	if err != nil {
		writeS3Err(w, err)
		return
	}

	writeObjectHeaders(w, &obj.Info, int64(len(obj.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data) //nolint:gosec // writing raw object bytes, not HTML
}

func (s *Server) s3HeadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	info, err := s.drivers.Storage.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeS3Err(w, err)
		return
	}

	writeObjectHeaders(w, info, info.Size)
	w.WriteHeader(http.StatusOK)
}

// writeObjectHeaders sets standard S3 object response headers.
func writeObjectHeaders(w http.ResponseWriter, info *driver.ObjectInfo, size int64) {
	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("ETag", fmt.Sprintf("%q", info.ETag))
	w.Header().Set("Last-Modified", toHTTPDate(info.LastModified))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))

	for k, v := range info.Metadata {
		w.Header().Set("X-Amz-Meta-"+k, v)
	}
}

func (s *Server) s3DeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if err := s.drivers.Storage.DeleteObject(r.Context(), bucket, key); err != nil {
		writeS3Err(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) s3CopyObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	src := r.Header.Get("X-Amz-Copy-Source")
	src = strings.TrimPrefix(src, "/")

	srcBucket, srcKey := parseS3Path(src)
	if srcBucket == "" || srcKey == "" {
		writeS3Error(w, http.StatusBadRequest, "InvalidArgument", "invalid copy source")
		return
	}

	if err := s.drivers.Storage.CopyObject(r.Context(), bucket, key, driver.CopySource{
		Bucket: srcBucket, Key: srcKey,
	}); err != nil {
		writeS3Err(w, err)
		return
	}

	obj, err := s.drivers.Storage.HeadObject(r.Context(), bucket, key)
	if err != nil {
		writeS3Err(w, err)
		return
	}

	writeXML(w, http.StatusOK, copyObjectResult{
		Xmlns:        s3Xmlns,
		ETag:         fmt.Sprintf("%q", obj.ETag),
		LastModified: obj.LastModified,
	})
}

// extractS3Metadata pulls x-amz-meta-* headers into a map.
func extractS3Metadata(h http.Header) map[string]string {
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

// writeS3Error writes an S3-format XML error response.
func writeS3Error(w http.ResponseWriter, status int, code, msg string) {
	writeXML(w, status, s3Error{Code: code, Message: msg})
}

// writeS3Err maps CloudEmu errors to S3 HTTP error responses.
func writeS3Err(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeS3Error(w, http.StatusNotFound, "NoSuchKey", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeS3Error(w, http.StatusConflict, "BucketAlreadyOwnedByYou", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeS3Error(w, http.StatusBadRequest, "InvalidArgument", err.Error())
	default:
		writeS3Error(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
