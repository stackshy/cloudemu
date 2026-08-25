// Package driver defines the interface for storage service implementations.
package driver

import (
	"context"
	"fmt"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ErrDeleteMarker is returned by GetObjectVersion/HeadObjectVersion when the
// requested version is a delete marker. S3 answers a version-addressed GET/HEAD
// of a delete marker with 405 Method Not Allowed and x-amz-delete-marker: true
// (not 404), so the wire layer matches this with errors.Is to emit that exact
// response instead of NoSuchKey.
var ErrDeleteMarker = cerrors.New(cerrors.NotFound, "the specified version is a delete marker")

// DeleteMarkerError carries the delete marker's LastModified timestamp alongside
// the ErrDeleteMarker sentinel. S3 returns that timestamp in the Last-Modified
// header of the 405 response for a version-addressed GET/HEAD of a delete marker,
// so providers return this (it unwraps to ErrDeleteMarker, so errors.Is still
// matches) to let the wire layer emit the header.
type DeleteMarkerError struct {
	LastModified string
}

func (*DeleteMarkerError) Error() string { return ErrDeleteMarker.Error() }

func (*DeleteMarkerError) Unwrap() error { return ErrDeleteMarker }

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
	// Location is the account's region (e.g. westus2). Empty until an ARM
	// create-or-update stamps it; the handler falls back to a default.
	Location string
	// Tags are the ARM resource tags submitted on create-or-update, round-tripped
	// back on GET / list.
	Tags map[string]string
}

// AccountKey is one access key of an Azure storage account (Microsoft.Storage
// ListKeys / RegenerateKey). Value is a base64-encoded secret.
type AccountKey struct {
	KeyName      string
	Value        string
	Permissions  string // "Full" or "Read"
	CreationTime string // RFC3339
}

// StorageAccountKeys is an OPTIONAL Azure-specific capability, discovered by
// type assertion (like the networking AzureNetworkInterfaces surface). An Azure
// storage-account backend exposes its shared access keys so a data-plane client
// building a SharedKeyCredential can fetch and rotate them. S3/GCS buckets have
// no equivalent and don't implement it.
type StorageAccountKeys interface {
	// ListStorageAccountKeys returns the account's access keys, generating a
	// stable key1/key2 pair on first access.
	ListStorageAccountKeys(ctx context.Context, account string) ([]AccountKey, error)
	// RegenerateStorageAccountKey rotates the value of the named key (key1/key2)
	// and returns the full, updated key list.
	RegenerateStorageAccountKey(ctx context.Context, account, keyName string) ([]AccountKey, error)
}

// BlobProperties are the settable system (HTTP) properties of a blob, updated by
// the Azure Set Blob Properties operation (?comp=properties). Empty fields clear
// the corresponding property, matching Azure semantics.
type BlobProperties struct {
	ContentType        string
	ContentEncoding    string
	ContentLanguage    string
	ContentDisposition string
	CacheControl       string
}

// BlobOpError reports an Azure Blob operation failure that carries an exact
// HTTP status and x-ms-error-code the wire layer must echo verbatim. Several
// blob operations (Lease Blob, Delete Blob with snapshots present, Get Blob on
// an archived blob) don't fit the canonical cerrors taxonomy: the same
// underlying condition maps to different statuses depending on the operation
// (e.g. a lease mismatch is 412 for a blob write but 409 for a lease-management
// call), so those providers return this directly instead of a cerrors code.
type BlobOpError struct {
	Status  int // HTTP status code, e.g. 409, 412
	Code    string
	Message string
}

func (e *BlobOpError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// BlockInfo describes one block in a block blob's committed or uncommitted
// block list (Put Block List / Get Block List).
type BlockInfo struct {
	Name string
	Size int64
}

// BlobLeaseResult is the outcome of a successful Lease Blob acquire, renew, or
// change operation.
type BlobLeaseResult struct {
	LeaseID      string
	ETag         string
	LastModified string
}

// SignedIdentifier is one stored access policy entry on a container (Set/Get
// Container ACL, ?restype=container&comp=acl). Start/Expiry are RFC3339
// timestamps, empty when unset.
type SignedIdentifier struct {
	ID         string
	Start      string
	Expiry     string
	Permission string
}

// AzureBlobExtensions is an OPTIONAL Azure-specific blob data-plane capability,
// discovered by type assertion. It covers operations with no AWS/GCS equivalent
// (block staging + commit, metadata/properties/tier updates, snapshots, append
// blobs, container metadata) so they are not forced onto the shared Bucket
// interface every provider implements.
type AzureBlobExtensions interface {
	// StageBlock buffers an uncommitted block (Put Block, ?comp=block) for a blob
	// under blockID; the blob need not exist yet.
	StageBlock(ctx context.Context, container, blob, blockID string, data []byte) error
	// CommitBlockList assembles a block blob (Put Block List, ?comp=blocklist)
	// from the previously-staged blocks named by blockIDs, in the given order.
	CommitBlockList(
		ctx context.Context, container, blob string, blockIDs []string, contentType string, metadata map[string]string,
	) (*ObjectInfo, error)

	// SetBlobMetadata replaces only a blob's metadata (Set Blob Metadata,
	// ?comp=metadata), preserving its content, and returns the new info.
	SetBlobMetadata(ctx context.Context, container, blob string, metadata map[string]string) (*ObjectInfo, error)
	// SetBlobProperties replaces only a blob's system properties (Set Blob
	// Properties, ?comp=properties), preserving its content.
	SetBlobProperties(ctx context.Context, container, blob string, props *BlobProperties) (*ObjectInfo, error)
	// SetBlobTier sets a blob's access tier (Set Blob Tier, ?comp=tier),
	// preserving its content and ETag. tier must be one of Hot/Cool/Cold/
	// Archive; any other value is an InvalidArgument error. statusCode is 200
	// when the new tier takes effect immediately or 202 when a blob is
	// rehydrating out of Archive.
	SetBlobTier(ctx context.Context, container, blob, tier string) (statusCode int, err error)

	// CreateBlobSnapshot captures an immutable snapshot (Snapshot Blob,
	// ?comp=snapshot) of a blob, preserving the base blob, and returns the
	// snapshot's opaque timestamp identifier.
	CreateBlobSnapshot(ctx context.Context, container, blob string) (snapshot string, info *ObjectInfo, err error)
	// GetBlobSnapshot reads a previously captured snapshot (GET ?snapshot=…).
	GetBlobSnapshot(ctx context.Context, container, blob, snapshot string) (*Object, error)

	// CreateAppendBlob creates an empty append blob (Put Blob with
	// x-ms-blob-type: AppendBlob).
	CreateAppendBlob(ctx context.Context, container, blob, contentType string, metadata map[string]string) (*ObjectInfo, error)
	// AppendBlock appends a block to the end of an append blob (Append Block,
	// ?comp=appendblock). offset is the byte position the block was committed at;
	// committedBlocks is the total number of blocks appended so far.
	AppendBlock(
		ctx context.Context, container, blob string, data []byte,
	) (offset int64, committedBlocks int, info *ObjectInfo, err error)

	// SetContainerMetadata replaces a container's metadata (Set Container
	// Metadata, ?restype=container&comp=metadata).
	SetContainerMetadata(ctx context.Context, container string, metadata map[string]string) error
	// ContainerMetadata returns a container's metadata (Get Container Properties /
	// Get Container Metadata).
	ContainerMetadata(ctx context.Context, container string) (map[string]string, error)

	// GetBlockList returns the blob's committed and uncommitted blocks (Get
	// Block List, GET ?comp=blocklist).
	GetBlockList(ctx context.Context, container, blob string) (committed, uncommitted []BlockInfo, err error)

	// AcquireLease acquires a lease on a blob (Lease Blob, ?comp=lease,
	// x-ms-lease-action: acquire). durationSeconds is -1 for an infinite lease
	// or 15-60 for a fixed duration; proposedLeaseID may be empty to let the
	// provider generate one.
	AcquireLease(ctx context.Context, container, blob string, durationSeconds int32, proposedLeaseID string) (*BlobLeaseResult, error)
	// RenewLease renews the blob's current lease.
	RenewLease(ctx context.Context, container, blob, leaseID string) (*BlobLeaseResult, error)
	// ChangeLease changes the blob's lease ID.
	ChangeLease(ctx context.Context, container, blob, leaseID, proposedLeaseID string) (*BlobLeaseResult, error)
	// ReleaseLease releases the blob's current lease.
	ReleaseLease(ctx context.Context, container, blob, leaseID string) (*BlobLeaseResult, error)
	// BreakLease breaks the blob's current lease. breakPeriod is nil when the
	// caller omitted x-ms-lease-break-period. Returns the seconds remaining
	// until a new lease may be acquired.
	BreakLease(ctx context.Context, container, blob string, breakPeriod *int32) (leaseTimeSeconds int32, err error)
	// CheckBlobLease validates a write/delete request's x-ms-lease-id header
	// against any active lease on the blob, returning a *BlobOpError when the
	// request must be rejected. A blob with no active lease and no header
	// always passes (nil), as does a blob that doesn't exist yet.
	CheckBlobLease(ctx context.Context, container, blob, headerLeaseID string) error

	// DeleteBlobSnapshots applies the Azure delete-snapshots directive
	// (x-ms-delete-snapshots: "" | "include" | "only") ahead of a Delete Blob.
	// mode "" with existing snapshots fails with SnapshotsPresent (409); modes
	// "include"/"only" delete the blob's snapshots. deleteBaseBlob reports
	// whether the caller must still delete the base blob itself via
	// Bucket.DeleteObject (false only for mode "only").
	DeleteBlobSnapshots(ctx context.Context, container, blob, mode string) (deleteBaseBlob bool, err error)

	// SetContainerAccessPolicy sets a container's public access level and
	// stored access policies (Set Container ACL,
	// ?restype=container&comp=acl).
	SetContainerAccessPolicy(ctx context.Context, container, publicAccess string, policies []SignedIdentifier) error
	// ContainerAccessPolicy returns a container's public access level and
	// stored access policies (Get Container ACL).
	ContainerAccessPolicy(ctx context.Context, container string) (publicAccess string, policies []SignedIdentifier, err error)
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
	// BlobType is the Azure blob type ("BlockBlob" or "AppendBlob"). Empty is
	// treated as BlockBlob; non-Azure providers leave it empty.
	BlobType string
	// AccessTier is the Azure blob access tier (Hot/Cool/Cold/Archive), set by
	// Set Blob Tier. Empty when unset; non-Azure providers leave it empty.
	AccessTier string
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
	// StartAfter makes listing begin strictly after this key (lexicographic),
	// matching the S3 ListObjectsV2 start-after parameter. It applies only to a
	// fresh listing (no PageToken); on a resumed page S3 ignores it. Providers
	// that do not model start-after ignore this field.
	StartAfter string
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

// RawBucketConfig is an OPTIONAL capability (discovered by type assertion, like
// VersionedBucket) a storage provider implements to persist and echo back opaque
// bucket-configuration sub-resource documents — policy (JSON), cors, encryption,
// lifecycle, website, and the like (XML) — byte-for-byte. The S3 handler uses it
// to make PutBucketX/GetBucketX/DeleteBucketX round-trip; providers that don't
// implement it fall back to the read-only "not configured" responses.
type RawBucketConfig interface {
	// PutBucketConfig stores document body under the sub-resource name (e.g.
	// "policy", "cors") for bucket, replacing any previous document.
	PutBucketConfig(ctx context.Context, bucket, name string, body []byte) error
	// GetBucketConfig returns the stored document, or NotFound when none was set.
	GetBucketConfig(ctx context.Context, bucket, name string) ([]byte, error)
	// DeleteBucketConfig removes the stored document (idempotent).
	DeleteBucketConfig(ctx context.Context, bucket, name string) error
}

// CopySource identifies the source for a copy operation.
type CopySource struct {
	Bucket string
	Key    string
}

// CopyObjectRequest describes an S3 server-side copy with the semantics the
// basic CopyObject cannot express: a versioned source, a metadata directive
// (COPY vs REPLACE), and copy-source preconditions. Carried by the optional
// ObjectCopier capability.
type CopyObjectRequest struct {
	DstBucket string
	DstKey    string
	Src       CopySource
	// SrcVersionID selects a specific source version ("" = current version).
	SrcVersionID string
	// ReplaceMetadata is true for x-amz-metadata-directive: REPLACE — the
	// destination takes Metadata and ContentType from the request instead of
	// inheriting the source object's.
	ReplaceMetadata bool
	Metadata        map[string]string
	ContentType     string
	// Tags is the destination object's tag set, applied only when ReplaceTags is
	// true (x-amz-tagging with x-amz-tagging-directive: REPLACE). With ReplaceTags
	// false (the default COPY tagging directive) the destination inherits the
	// source object's tags.
	Tags        map[string]string
	ReplaceTags bool
	// Copy-source preconditions; a zero value means the header was absent. A
	// failed precondition must abort the copy with a FailedPrecondition error.
	IfMatch           string
	IfNoneMatch       string
	IfModifiedSince   time.Time
	IfUnmodifiedSince time.Time
}

// CopyObjectResult reports the outcome of an ObjectCopier copy.
type CopyObjectResult struct {
	ETag         string
	LastModified string
	// VersionID is the destination version id ("" when the destination bucket
	// is unversioned); SourceVersionID is the source version actually copied.
	VersionID       string
	SourceVersionID string
}

// ObjectCopier is an OPTIONAL capability (discovered by type assertion, like
// VersionedBucket) for a full-fidelity S3 server-side copy: a versioned source,
// the COPY/REPLACE metadata directive, and copy-source preconditions. A failed
// precondition is reported as a FailedPrecondition error; a delete-marker source
// version as an InvalidArgument error. Providers without it fall back to the
// basic Bucket.CopyObject (current version, COPY directive, no preconditions).
type ObjectCopier interface {
	CopyObjectV2(ctx context.Context, req *CopyObjectRequest) (*CopyObjectResult, error)
}

// RegionalBucket is an OPTIONAL capability a storage provider implements to
// create a bucket in a caller-specified region (S3
// CreateBucketConfiguration.LocationConstraint), so GetBucketLocation reports
// that region back. Providers without it create buckets in their default region.
type RegionalBucket interface {
	CreateBucketInRegion(ctx context.Context, name, region string) error
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
