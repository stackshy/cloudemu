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

// AccountAttributes are the storage-account cost/identity attributes an Azure
// storage account carries but an S3/GCS bucket does not (SKU redundancy, kind,
// access tier). Surfaced through the optional BucketAttributes capability.
type AccountAttributes struct {
	SKU        string // e.g. Standard_LRS, Premium_LRS
	Kind       string // e.g. StorageV2, BlobStorage
	AccessTier string // Hot / Cool
}

// BucketAttributes is an OPTIONAL capability, discovered by type assertion (like
// the networking NetworkInterfaces capability): a provider whose buckets map to
// a richer resource (Azure storage accounts) exposes their SKU/kind/access-tier
// for cost discovery. S3/GCS don't implement it and contribute nothing.
type BucketAttributes interface {
	BucketAttributes(ctx context.Context, bucket string) (AccountAttributes, error)
}

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified string
	Metadata     map[string]string
	// VersionID is the object's version identifier on a versioning-enabled
	// bucket ("null" on a suspended/unversioned bucket, empty when the bucket
	// never had versioning). Providers without versioning leave it empty.
	VersionID string
	// DeleteMarker reports whether this version is a delete marker.
	DeleteMarker bool
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

// ObjectVersion is one version (or delete marker) of a key, as reported by
// ListObjectVersions.
type ObjectVersion struct {
	Key          string
	VersionID    string
	IsLatest     bool
	DeleteMarker bool
	Size         int64
	ETag         string
	ContentType  string
	LastModified string
}

// VersionListResult is the result of a ListObjectVersions operation: every
// version and delete marker matching the options, newest-first within each key.
type VersionListResult struct {
	Versions       []ObjectVersion
	CommonPrefixes []string
}

// VersionedBucket is an optional extension a storage provider implements when
// it retains per-object version history (real S3 semantics). The S3 handler
// uses it, when present, to honor bucket versioning status, version-addressable
// GET/HEAD/DELETE, and ListObjectVersions. Providers that don't implement it
// keep the flat single-version behavior of Bucket.
type VersionedBucket interface {
	Bucket

	// SetVersioningStatus sets the bucket's versioning status: "Enabled" or
	// "Suspended". VersioningStatus returns "Enabled", "Suspended", or "" (never
	// configured).
	SetVersioningStatus(ctx context.Context, bucket, status string) error
	VersioningStatus(ctx context.Context, bucket string) (string, error)

	// GetObjectVersion / HeadObjectVersion fetch a specific version by ID. A
	// delete-marker version yields a NotFound (with DeleteMarker set on the
	// returned info where applicable).
	GetObjectVersion(ctx context.Context, bucket, key, versionID string) (*Object, error)
	HeadObjectVersion(ctx context.Context, bucket, key, versionID string) (*ObjectInfo, error)

	// DeleteObjectVersion removes a specific version when versionID != "".
	// With versionID == "" it performs a top-level delete: on an Enabled bucket
	// that appends a delete marker (deleteMarker=true) and returns its new ID;
	// otherwise it removes the current object. deletedVersionID is the affected
	// version's ID (empty when nothing was deleted on an unversioned bucket).
	DeleteObjectVersion(ctx context.Context, bucket, key, versionID string) (deletedVersionID string, deleteMarker bool, err error)

	// ListObjectVersions returns the full version history matching opts.
	ListObjectVersions(ctx context.Context, bucket string, opts ListOptions) (*VersionListResult, error)
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

// BucketPolicy represents a bucket access policy.
type BucketPolicy struct {
	Version    string
	Statements []PolicyStatement
}

// PolicyStatement represents a single statement in a bucket policy.
type PolicyStatement struct {
	Effect    string   // "Allow" or "Deny"
	Principal string   // "*" or specific principal
	Actions   []string // e.g., "s3:GetObject"
	Resources []string // e.g., "arn:aws:s3:::bucket/*"
}

// CORSRule defines a CORS rule for a bucket.
type CORSRule struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	ExposeHeaders  []string
	MaxAgeSeconds  int
}

// CORSConfig is a set of CORS rules for a bucket.
type CORSConfig struct {
	Rules []CORSRule
}

// EncryptionConfig describes the default encryption for a bucket.
type EncryptionConfig struct {
	Enabled   bool
	Algorithm string // "AES256" or "aws:kms"
	KeyID     string // KMS key ID (optional)
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
	// ListParts returns the parts buffered so far for an in-progress upload,
	// ordered by part number. It errors with NotFound if the upload is unknown.
	ListParts(ctx context.Context, bucket, key, uploadID string) ([]UploadPart, error)

	// Versioning
	SetBucketVersioning(ctx context.Context, bucket string, enabled bool) error
	GetBucketVersioning(ctx context.Context, bucket string) (bool, error)

	// Bucket Policy
	PutBucketPolicy(ctx context.Context, bucket string, policy BucketPolicy) error
	GetBucketPolicy(ctx context.Context, bucket string) (*BucketPolicy, error)
	DeleteBucketPolicy(ctx context.Context, bucket string) error

	// CORS
	PutCORSConfig(ctx context.Context, bucket string, config CORSConfig) error
	GetCORSConfig(ctx context.Context, bucket string) (*CORSConfig, error)
	DeleteCORSConfig(ctx context.Context, bucket string) error

	// Encryption
	PutEncryptionConfig(ctx context.Context, bucket string, config EncryptionConfig) error
	GetEncryptionConfig(ctx context.Context, bucket string) (*EncryptionConfig, error)

	// Object Tagging
	PutObjectTagging(ctx context.Context, bucket, key string, tags map[string]string) error
	GetObjectTagging(ctx context.Context, bucket, key string) (map[string]string, error)
	DeleteObjectTagging(ctx context.Context, bucket, key string) error

	// Bucket Tagging
	PutBucketTagging(ctx context.Context, bucket string, tags map[string]string) error
	GetBucketTagging(ctx context.Context, bucket string) (map[string]string, error)
	DeleteBucketTagging(ctx context.Context, bucket string) error
}
