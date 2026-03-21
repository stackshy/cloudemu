// Package driver defines the interface for storage service implementations.
package driver

import (
	"context"
	"time"
)

// BucketInfo describes a storage bucket.
type BucketInfo struct {
	Name      string
	Region    string
	CreatedAt string
}

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified string
	Metadata     map[string]string
}

// Object is an object with its data.
type Object struct {
	Info ObjectInfo
	Data []byte
}

// ListOptions configures a list operation.
type ListOptions struct {
	Prefix    string
	Delimiter string
	MaxKeys   int
	PageToken string
}

// ListResult is the result of a list operation.
type ListResult struct {
	Objects        []ObjectInfo
	CommonPrefixes []string
	NextPageToken  string
	IsTruncated    bool
}

// CopySource identifies the source for a copy operation.
type CopySource struct {
	Bucket string
	Key    string
}

// PresignedURLRequest describes a presigned URL to generate.
type PresignedURLRequest struct {
	Bucket    string
	Key       string
	Method    string // "GET" or "PUT"
	ExpiresIn time.Duration
}

// PresignedURL is a generated presigned URL.
type PresignedURL struct {
	URL       string
	Method    string
	ExpiresAt time.Time
}

// LifecycleRule defines an object lifecycle policy rule.
type LifecycleRule struct {
	ID                       string
	Enabled                  bool
	Prefix                   string
	ExpirationDays           int
	TransitionDays           int
	TransitionStorageClass   string
	AbortMultipartDays       int
	NoncurrentExpirationDays int
}

// LifecycleConfig is a set of lifecycle rules for a bucket.
type LifecycleConfig struct {
	Rules []LifecycleRule
}

// MultipartUpload represents an in-progress multipart upload.
type MultipartUpload struct {
	UploadID  string
	Bucket    string
	Key       string
	CreatedAt string
}

// UploadPart represents a part of a multipart upload.
type UploadPart struct {
	PartNumber int
	ETag       string
	Size       int64
}

// Bucket is the interface that storage provider implementations must satisfy.
type Bucket interface {
	CreateBucket(ctx context.Context, name string) error
	DeleteBucket(ctx context.Context, name string) error
	ListBuckets(ctx context.Context) ([]BucketInfo, error)

	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error
	GetObject(ctx context.Context, bucket, key string) (*Object, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	HeadObject(ctx context.Context, bucket, key string) (*ObjectInfo, error)
	ListObjects(ctx context.Context, bucket string, opts ListOptions) (*ListResult, error)
	CopyObject(ctx context.Context, dstBucket, dstKey string, src CopySource) error

	// Presigned URLs
	GeneratePresignedURL(ctx context.Context, req PresignedURLRequest) (*PresignedURL, error)

	// Lifecycle policies
	PutLifecycleConfig(ctx context.Context, bucket string, config LifecycleConfig) error
	GetLifecycleConfig(ctx context.Context, bucket string) (*LifecycleConfig, error)
	EvaluateLifecycle(ctx context.Context, bucket string) ([]string, error)

	// Multipart uploads
	CreateMultipartUpload(ctx context.Context, bucket, key, contentType string) (*MultipartUpload, error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int, data []byte) (*UploadPart, error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []UploadPart) error
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
	ListMultipartUploads(ctx context.Context, bucket string) ([]MultipartUpload, error)

	// Versioning
	SetBucketVersioning(ctx context.Context, bucket string, enabled bool) error
	GetBucketVersioning(ctx context.Context, bucket string) (bool, error)
}
