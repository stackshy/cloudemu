// Package driver defines the interface for storage service implementations.
package driver

import "context"

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
}
