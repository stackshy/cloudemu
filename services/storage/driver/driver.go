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
	// ResourceGroup is the Azure resource group the account was created under.
	// Recorded on the ARM create-or-update so a resource-group cascade delete can
	// find the accounts it must remove; empty for non-ARM (S3/GCS) buckets.
	ResourceGroup string
	// Tags are the ARM resource tags submitted on create-or-update, round-tripped
	// back on GET / list.
	Tags map[string]string
}

// AccountEncryption is the storage-account encryption configuration requested
// on create/update (ARM Properties.Encryption): KeySource is
// "Microsoft.Storage" (platform-managed, the default) or "Microsoft.Keyvault"
// (customer-managed key), in which case KeyVaultURI/KeyName/KeyVersion
// identify the CMK. A zero value means unset, which the handler renders as
// the platform-managed default. Kept separate from AccountAttributes (rather
// than a field on it) so that struct stays small enough to pass by value.
type AccountEncryption struct {
	KeySource   string
	KeyVaultURI string
	KeyName     string
	KeyVersion  string
}

// AccountEncryptionConfig is an OPTIONAL Azure-specific capability,
// discovered by type assertion (like BucketAttributes), that persists and
// echoes back an account's service-side encryption configuration
// (Properties.Encryption on create/update), so a customer-managed-key request
// doesn't silently downgrade to the platform-managed default on the next GET.
type AccountEncryptionConfig interface {
	SetAccountEncryption(account string, enc AccountEncryption)
	AccountEncryption(ctx context.Context, account string) (AccountEncryption, error)
}

// BlobServiceProperties are the storage-account-level Blob service settings
// configured via Set/Get Blob Service Properties
// (…/storageAccounts/{account}/blobServices/default): versioning, soft
// delete, change feed, and CORS. Real Azure applies these once per storage
// account, not per container — distinct from the per-bucket CORS/versioning
// surface on the Bucket interface that S3/GCS also implement.
type BlobServiceProperties struct {
	IsVersioningEnabled bool
	ChangeFeedEnabled   bool
	// ChangeFeedRetentionDays is 0 for infinite retention (unset).
	ChangeFeedRetentionDays int
	DeleteRetentionEnabled  bool
	// DeleteRetentionDays is 0 when unset.
	DeleteRetentionDays int
	CORS                []CORSRule
}

// BlobServiceConfig is an OPTIONAL Azure-specific capability, discovered by
// type assertion (like BucketAttributes), that persists and echoes back the
// account-level Blob service properties sub-resource. S3/GCS have no
// equivalent and don't implement it.
type BlobServiceConfig interface {
	SetBlobServiceProperties(ctx context.Context, account string, props BlobServiceProperties) error
	BlobServiceProperties(ctx context.Context, account string) (BlobServiceProperties, error)
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

// BlockListEntry is one entry in a Put Block List request: a block id together
// with the source list Azure must resolve it against. List is "Committed"
// (a block already committed on the blob), "Uncommitted" (a freshly staged
// block), or "Latest" (the staged block if present, else the committed one).
// Preserving the source list lets a commit reference already-committed blocks,
// so the "re-commit existing blocks + a new one" append pattern works.
type BlockListEntry struct {
	ID   string
	List string
}

// Block source-list values for BlockListEntry.List.
const (
	BlockListCommitted   = "Committed"
	BlockListUncommitted = "Uncommitted"
	BlockListLatest      = "Latest"
)

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
	// from the given block entries, in order. Each entry names a block id and the
	// source list to resolve it against (Committed/Uncommitted/Latest), so a
	// commit can reference blocks already committed on the blob, not just freshly
	// staged ones. Content properties from props (nil when none supplied) are
	// persisted on the blob.
	CommitBlockList(
		ctx context.Context, container, blob string, blocks []BlockListEntry,
		contentType string, props *BlobProperties, metadata map[string]string,
	) (*ObjectInfo, error)

	// PutBlockBlob writes a block blob's content together with its system content
	// properties (Put Blob), so Content-Encoding/Cache-Control/Content-Language/
	// Content-Disposition round-trip on a later read. props may be nil.
	PutBlockBlob(
		ctx context.Context, container, blob string, data []byte, props *BlobProperties, metadata map[string]string,
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

// AzureVersionedBlob is an OPTIONAL Azure-specific capability, discovered by
// type assertion, that models automatic blob versioning (x-ms-version-id). When
// account-level versioning is enabled (Set Blob Service Properties,
// isVersioningEnabled) every write to a blob mints a new immutable version, the
// most recent of which is the current version served by the base blob; older
// versions stay readable, listable, and individually deletable by their id.
//
// It is distinct from the S3-shaped VersionedBucket: Azure enables versioning
// once at the account/blob-service level (not per bucket with a status) and does
// not use delete markers — deleting the base blob simply retains the existing
// versions. S3/GCS don't implement it.
//
// Note: version bytes are captured in memory at write time; combining an
// external StorageEngine with versioning captures version metadata only.
type AzureVersionedBlob interface {
	// VersioningEnabled reports whether account-level blob versioning is on.
	VersioningEnabled(ctx context.Context) (bool, error)
	// GetBlobVersion reads a specific version of a blob (GET ?versionid=…).
	GetBlobVersion(ctx context.Context, container, blob, versionID string) (*Object, error)
	// HeadBlobVersion returns a specific version's info (HEAD ?versionid=…).
	HeadBlobVersion(ctx context.Context, container, blob, versionID string) (*ObjectInfo, error)
	// DeleteBlobVersion permanently removes a specific version (DELETE
	// ?versionid=…). Removing the current version also removes the base blob.
	DeleteBlobVersion(ctx context.Context, container, blob, versionID string) error
	// ListBlobVersions returns every version (current and previous) of the blobs
	// matching opts, so a List Blobs include=versions can render each with its
	// VersionID and IsLatest (IsCurrentVersion) marker.
	ListBlobVersions(ctx context.Context, container string, opts ListOptions) (*VersionListResult, error)
}

// DeletedBlob is one soft-deleted blob reported by ListDeletedBlobs (List Blobs
// include=deleted). It carries the blob's info together with the soft-delete
// bookkeeping the wire layer echoes: when it was deleted and how many retention
// days remain before it is permanently purged.
type DeletedBlob struct {
	Info ObjectInfo
	// DeletedTime is when the blob was soft-deleted (blob time format, UTC).
	DeletedTime string
	// RemainingRetentionDays is the whole days left in the retention window
	// before the soft-deleted blob is permanently removed.
	RemainingRetentionDays int
}

// DeletedBlobListResult is the result of a ListDeletedBlobs operation.
type DeletedBlobListResult struct {
	Blobs []DeletedBlob
}

// AzureSoftDeleteBlob is an OPTIONAL Azure-specific capability, discovered by
// type assertion, that models blob soft delete. When the account-level delete
// retention policy is enabled (Set Blob Service Properties,
// deleteRetentionPolicy.enabled/days) a Delete Blob retains the blob instead of
// removing it: it disappears from a normal List but reappears under List Blobs
// include=deleted with Deleted=true, a DeletedTime, and a RemainingRetentionDays
// countdown, and Undelete Blob (PUT ?comp=undelete) restores it. After the
// retention window elapses the blob is permanently gone.
//
// It is distinct from the S3 delete-marker model and from Azure blob versioning:
// soft delete engages only when the account retention policy is on AND versioning
// is off (with versioning enabled, retained versions are the recovery mechanism,
// so a delete leaves the versions intact instead of soft-deleting the base blob).
// S3/GCS don't implement it.
//
// Note: soft-deleted bytes are captured in memory at delete time; combining an
// external StorageEngine with soft delete captures soft-deleted metadata only.
type AzureSoftDeleteBlob interface {
	// SoftDeleteEnabled reports whether soft delete is currently in effect for
	// the data plane (the account retention policy is enabled and versioning off).
	SoftDeleteEnabled(ctx context.Context) (bool, error)
	// UndeleteBlob restores a soft-deleted blob to active (PUT ?comp=undelete).
	// It is a no-op success when the blob is already active, and NotFound when no
	// active or soft-deleted blob of that name exists.
	UndeleteBlob(ctx context.Context, container, blob string) error
	// ListDeletedBlobs returns the soft-deleted blobs matching opts, so a List
	// Blobs include=deleted can render each with Deleted=true and its retention
	// bookkeeping.
	ListDeletedBlobs(ctx context.Context, container string, opts ListOptions) (*DeletedBlobListResult, error)
}

// PageRange is one contiguous [Start,End] byte span (inclusive) of a page blob
// that currently holds written data, as reported by Get Page Ranges.
type PageRange struct {
	Start int64
	End   int64
}

// AzurePageBlob is an OPTIONAL Azure-specific capability, discovered by type
// assertion, that models page blobs — fixed-capacity blobs written in 512-byte
// pages at arbitrary offsets (the backing type for Azure managed disks). A page
// blob is created empty at a declared size (all pages read as zeros); Put Page
// writes an aligned range, Clear Page zeroes one, and Get Page Ranges reports
// the ranges that currently hold written data (adjacent written pages coalesced
// into one range). All ranges are aligned to the 512-byte page boundary.
//
// It is distinct from block/append blobs: those grow by staging or appending
// whole blocks, whereas a page blob is a random-access fixed-size byte array.
// S3/GCS don't implement it.
//
// Note: page bytes are captured in memory; a page blob is not offloaded to an
// external StorageEngine.
type AzurePageBlob interface {
	// CreatePageBlob creates an empty page blob of size bytes (a multiple of 512)
	// with all pages zero-valued and no written ranges (Put Blob,
	// x-ms-blob-type: PageBlob, x-ms-blob-content-length: size). props may be nil.
	CreatePageBlob(
		ctx context.Context, container, blob string, size int64, props *BlobProperties, metadata map[string]string,
	) (*ObjectInfo, error)
	// PutPage writes data over the inclusive byte range [start,end] of a page blob
	// (Put Page, ?comp=page, x-ms-page-write: update). start and end must align to
	// the 512-byte page boundary (start%512==0, (end+1)%512==0), lie within the
	// blob, and len(data) must equal end-start+1.
	PutPage(ctx context.Context, container, blob string, start, end int64, data []byte) (*ObjectInfo, error)
	// ClearPage zeroes the inclusive byte range [start,end] of a page blob and
	// drops it from the written ranges (Clear Page, ?comp=page,
	// x-ms-page-write: clear). Same alignment/bounds rules as PutPage.
	ClearPage(ctx context.Context, container, blob string, start, end int64) (*ObjectInfo, error)
	// GetPageRanges returns the page blob's written ranges (Get Page Ranges,
	// GET ?comp=pagelist), coalesced and ordered by Start, together with the
	// blob's total size.
	GetPageRanges(ctx context.Context, container, blob string) (ranges []PageRange, blobSize int64, err error)
}

// TaggedBlob is one blob returned by FindBlobsByTags: its container, name, and
// the full index-tag set that matched the query.
type TaggedBlob struct {
	Container string
	Name      string
	Tags      map[string]string
}

// AzureFindBlobsByTags is an OPTIONAL Azure-specific capability, discovered by
// type assertion, that searches blobs by their index tags (Find Blobs by Tags,
// GET /?comp=blobs&where=… at the account level or
// GET /{container}?restype=container&comp=blobs&where=… scoped to one
// container). It returns every live blob whose tags satisfy all of the
// equality conditions in match; an empty match matches every tagged blob.
// container is "" for an account-wide search or a container name to scope it.
// The tags themselves are the ones set via Set Blob Tags (PutObjectTagging).
type AzureFindBlobsByTags interface {
	FindBlobsByTags(ctx context.Context, container string, match map[string]string) ([]TaggedBlob, error)
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
	// Created is the object's original creation time (GCS timeCreated), set once
	// when the generation is first written and preserved across metadata-only
	// updates while LastModified advances. Empty for providers that don't model a
	// distinct creation time (callers fall back to LastModified).
	Created  string
	Metadata map[string]string
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
	// Generation is the GCS object generation — a unique, monotonically
	// increasing id minted on every write of the object's data. Zero for
	// providers that don't model generations (S3/Azure).
	Generation int64
	// Metageneration is the GCS object metageneration — starts at 1 for each
	// generation and increments on each metadata-only update. Zero for
	// non-GCS providers.
	Metageneration int64
	// MD5 is the base64-encoded MD5 digest of the object bytes (GCS md5Hash).
	// Empty for providers that don't compute it.
	MD5 string
	// CRC32C is the base64-encoded big-endian CRC32C (Castagnoli) of the object
	// bytes (GCS crc32c). Empty for providers that don't compute it.
	CRC32C string
	// CacheControl / ContentEncoding / ContentDisposition / ContentLanguage are
	// GCS/S3 system object properties, settable via an object metadata update.
	// Empty when unset.
	CacheControl       string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	// StorageClass is the GCS object storage class (STANDARD/NEARLINE/COLDLINE/
	// ARCHIVE), defaulting to the bucket's default class at insert; on S3 it is
	// the object's storage class (STANDARD, STANDARD_IA, GLACIER, …), where empty
	// is treated as STANDARD. Empty for providers that don't model it.
	StorageClass string
	// Expires is the S3 Expires system header (an HTTP-date string) recorded on
	// PutObject and echoed on GET/HEAD. Empty when unset; providers that don't
	// model it leave it empty.
	Expires string
	// TemporaryHold / EventBasedHold report the GCS object WORM holds. While
	// either is set the object cannot be deleted or overwritten. Zero for
	// providers that don't model holds.
	TemporaryHold  bool
	EventBasedHold bool
	// RetentionExpiration is the GCS retentionExpirationTime — the RFC3339 instant
	// before which the object cannot be deleted or overwritten under the bucket's
	// retention policy. Empty when the bucket has no retention policy (or an
	// eventBasedHold is currently pinning the object).
	RetentionExpiration string
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
	// StorageClass is the version's S3 storage class; empty is treated as
	// STANDARD. Providers that don't model it leave it empty.
	StorageClass string
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

// Object-lock retention modes (S3 Object Lock). GOVERNANCE can be bypassed by a
// principal holding s3:BypassGovernanceRetention; COMPLIANCE can be bypassed by
// no one, not even the root account, until the retention period elapses.
const (
	ObjectLockGovernance = "GOVERNANCE"
	ObjectLockCompliance = "COMPLIANCE"
)

// ObjectRetention is an S3 Object Lock retention setting on a single object
// version: a Mode (GOVERNANCE or COMPLIANCE) and the UTC instant until which the
// version is retained. A zero value (empty Mode, zero RetainUntilDate) means no
// retention is configured.
type ObjectRetention struct {
	Mode            string
	RetainUntilDate time.Time
}

// ObjectLockBucket is an OPTIONAL S3-specific capability (discovered by type
// assertion, like VersionedBucket) that ENFORCES S3 Object Lock (WORM). Retention
// (GOVERNANCE/COMPLIANCE + RetainUntilDate) and legal hold are recorded per
// object version; while a version is protected — legal hold ON, or a retention
// period that has not elapsed — its bytes cannot be permanently deleted or
// overwritten. A GOVERNANCE retention (but not the version's legal hold) can be
// lifted with s3:BypassGovernanceRetention; a COMPLIANCE retention cannot be
// shortened, removed, or bypassed by anyone until it expires. Object Lock builds
// on versioning, so it layers on VersionedBucket. Providers without it keep the
// plain versioned behavior with no WORM protection.
type ObjectLockBucket interface {
	VersionedBucket

	// ObjectLockEnabled reports whether the bucket was created with Object Lock
	// enabled (x-amz-bucket-object-lock-enabled), so retention/legal hold may be
	// configured on its objects.
	ObjectLockEnabled(ctx context.Context, bucket string) (bool, error)
	// EnableObjectLock marks a bucket Object-Lock-enabled and turns on versioning
	// (Object Lock requires it). Idempotent.
	EnableObjectLock(ctx context.Context, bucket string) error

	// GetObjectRetention returns the retention on a version (the current version
	// when versionID==""). A zero ObjectRetention means none is set.
	GetObjectRetention(ctx context.Context, bucket, key, versionID string) (ObjectRetention, error)
	// PutObjectRetention sets the retention on a version (current when
	// versionID==""). First-setting or extending a retention is always allowed;
	// shortening, removing, or downgrading an ACTIVE GOVERNANCE retention requires
	// bypassGovernance, and an active COMPLIANCE retention can never be shortened,
	// removed, or downgraded. A disallowed change returns a PermissionDenied error.
	PutObjectRetention(
		ctx context.Context, bucket, key, versionID string, ret ObjectRetention, bypassGovernance bool,
	) error
	// GetObjectLegalHold reports whether legal hold is ON for a version (current
	// when versionID=="").
	GetObjectLegalHold(ctx context.Context, bucket, key, versionID string) (bool, error)
	// PutObjectLegalHold sets legal hold ON/OFF for a version (current when
	// versionID==""). Always allowed.
	PutObjectLegalHold(ctx context.Context, bucket, key, versionID string, on bool) error

	// DeleteObjectVersionWithBypass is DeleteObjectVersion with Object Lock
	// enforcement: a protected version cannot be permanently removed, and
	// bypassGovernance lifts only a GOVERNANCE block (never COMPLIANCE, never a
	// legal hold). A blocked delete returns a PermissionDenied error. A top-level
	// delete (versionID=="") still records a delete marker without touching the
	// protected versions beneath it.
	DeleteObjectVersionWithBypass(
		ctx context.Context, bucket, key, versionID string, bypassGovernance bool,
	) (deletedVersionID string, deleteMarker bool, err error)
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
	// SystemProps carries the destination's storage class (from x-amz-storage-class,
	// which is never inherited from the source) and, when ReplaceMetadata is set,
	// the replacement system properties. With ReplaceMetadata false the destination
	// inherits the source object's system properties; StorageClass still applies.
	SystemProps ObjectSystemProps
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

// S3PutPrecondition carries the S3 conditional-write headers (If-None-Match /
// If-Match) sent on a plain PutObject, the guard AWS added for S3 conditional
// writes. A zero value means an unconditional write. IfNoneMatch == "*" means
// "the object must not already exist" (create-if-absent); a specific ETag means
// "no current object with that ETag may exist". IfMatch == "<etag>" means "the
// current object's ETag must match" (optimistic replace). A failed condition
// must abort the write with a FailedPrecondition error, leaving any existing
// object untouched, and must be evaluated atomically with the store so a
// Get-then-Put race cannot lose an update.
type S3PutPrecondition struct {
	IfNoneMatch string
	IfMatch     string
}

// ConditionalBucket is an OPTIONAL capability (discovered by type assertion like
// VersionedBucket) letting a driver perform an atomic conditional PutObject that
// honors the If-None-Match / If-Match request headers. The returned ObjectInfo
// carries the stored ETag and (on a versioned bucket) the minted VersionID so
// the wire handler can answer with them.
type ConditionalBucket interface {
	PutObjectConditional(
		ctx context.Context, bucket, key string, data []byte, contentType string,
		metadata map[string]string, pre S3PutPrecondition,
	) (*ObjectInfo, error)
}

// ObjectSystemProps carries the S3 system-defined object properties and storage
// class that travel with an object on PutObject/CopyObject. Every field is
// optional; an empty field means the property is unset (StorageClass empty is
// treated as STANDARD). It is kept separate from the shared PutObject signature
// so drivers that don't model these properties are unaffected.
type ObjectSystemProps struct {
	CacheControl       string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	Expires            string
	StorageClass       string
}

// SystemPropsBucket is an OPTIONAL capability (discovered by type assertion like
// ConditionalBucket) for a driver that persists the S3 system-defined object
// properties (Cache-Control, Content-Encoding, Content-Disposition,
// Content-Language, Expires) and storage class alongside an object, so the wire
// handler can round-trip them on GET/HEAD/List. props is nil when the caller
// sent none of them. Providers without it keep the plain PutObject behavior and
// leave these properties unset.
type SystemPropsBucket interface {
	PutObjectWithSystemProps(
		ctx context.Context, bucket, key string, data []byte, contentType string,
		metadata map[string]string, props *ObjectSystemProps,
	) error
}

// GCSPrecondition carries the GCS write preconditions
// (ifGenerationMatch/ifGenerationNotMatch/ifMetagenerationMatch/
// ifMetagenerationNotMatch query parameters). A nil pointer means the caller
// did not send that precondition. ifGenerationMatch == 0 means "the object must
// not already exist" (create-if-absent).
type GCSPrecondition struct {
	IfGenerationMatch        *int64
	IfGenerationNotMatch     *int64
	IfMetagenerationMatch    *int64
	IfMetagenerationNotMatch *int64
}

// GCSPreconditionError signals a failed GCS write precondition. Real GCS answers
// with 412 Precondition Failed and reason "conditionNotMet", which does NOT map
// to the canonical FailedPrecondition→409 the storage wire layer uses elsewhere,
// so providers return this typed error and the GCS handler matches it with
// errors.As to emit the exact 412 response.
type GCSPreconditionError struct {
	Message string
}

func (e *GCSPreconditionError) Error() string {
	if e.Message == "" {
		return "conditionNotMet"
	}

	return e.Message
}

// GCSImmutableError signals a delete or overwrite blocked by a bucket retention
// policy that has not yet elapsed or by an active object hold (temporaryHold /
// eventBasedHold). Real GCS answers 403 Forbidden with a reason such as
// "retentionPolicyNotMet"; providers return this typed error and the GCS handler
// matches it with errors.As to emit the exact 403.
type GCSImmutableError struct {
	// Reason is the GCS error reason (e.g. "retentionPolicyNotMet").
	Reason string
	// Message is the human-readable explanation returned to the caller.
	Message string
}

func (e *GCSImmutableError) Error() string {
	if e.Message == "" {
		return "object is immutable (retention policy or hold)"
	}

	return e.Message
}

// GCSObjectUpdate carries the mutable properties an Objects: patch/update sets.
// A nil pointer field leaves that property unchanged; a nil Metadata map leaves
// custom metadata unchanged, while a non-nil map is merged (a nil value deletes
// that key).
type GCSObjectUpdate struct {
	ContentType        *string
	CacheControl       *string
	ContentEncoding    *string
	ContentDisposition *string
	ContentLanguage    *string
	Metadata           map[string]*string
	// TemporaryHold / EventBasedHold set or clear the object's WORM holds
	// (Objects: patch). A nil pointer leaves the hold unchanged. Releasing an
	// eventBasedHold (true→false) resets the object's retention clock.
	TemporaryHold  *bool
	EventBasedHold *bool
}

// GCSObjectAttrs carries the object system properties settable at INSERT time
// (Objects: insert), so they persist on the first write rather than only via a
// later patch. Empty StorageClass means "use the bucket's default class".
type GCSObjectAttrs struct {
	CacheControl       string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	StorageClass       string
}

// GCSRetentionPolicy is a bucket retention policy (WORM). RetentionPeriod is in
// seconds; objects cannot be deleted or overwritten until they are older than
// it. EffectiveTime is when the policy took effect; IsLocked reports whether it
// has been made permanent (it can then only be increased, never shortened or
// removed).
type GCSRetentionPolicy struct {
	RetentionPeriod int64
	EffectiveTime   string
	IsLocked        bool
}

// GCSComposeSource names one source component of an Objects: compose request.
// Generation is nil to use the source's live generation, or a specific
// archived/live generation to pin.
type GCSComposeSource struct {
	Key        string
	Generation *int64
}

// GCSBucketMeta are the GCS-specific bucket attributes the shared BucketInfo
// doesn't carry: multi-region/region location, default storage class, and the
// metageneration/updated pair that back etag/ifMetagenerationMatch concurrency.
type GCSBucketMeta struct {
	Location       string
	StorageClass   string
	Metageneration int64
	Updated        string
}

// GCSExtensions is an OPTIONAL GCS-specific capability, discovered by type
// assertion (like VersionedBucket). It persists GCS bucket/object behaviors the
// shared Bucket interface can't express: preconditioned writes with generation
// minting, object metadata patch, server-side compose, versioned (all-
// generation) listing, and bucket location/storageClass/IAM + metageneration.
// S3/Azure don't implement it and keep their own semantics.
type GCSExtensions interface {
	// PutObjectGCS writes an object honoring pre (a failed condition returns a
	// *GCSPreconditionError) and returns the stored object's info with the newly
	// minted generation. A non-nil attrs persists the insert-time system
	// properties.
	PutObjectGCS(
		ctx context.Context, bucket, key string, data []byte, contentType string,
		metadata map[string]string, attrs *GCSObjectAttrs, pre GCSPrecondition,
	) (*ObjectInfo, error)
	// GetObjectGCS returns an object's bytes+info, selecting a specific
	// generation when generation is non-nil (else the live object).
	GetObjectGCS(ctx context.Context, bucket, key string, generation *int64) (*Object, error)
	// HeadObjectGCS returns an object's info, selecting a specific generation
	// when generation is non-nil (else the live object).
	HeadObjectGCS(ctx context.Context, bucket, key string, generation *int64) (*ObjectInfo, error)
	// DeleteObjectGCS deletes an object honoring pre and optional generation
	// addressing. On a versioning-enabled bucket a live delete (nil generation)
	// archives the current generation as noncurrent instead of removing it; a
	// generation-addressed delete is always permanent.
	DeleteObjectGCS(ctx context.Context, bucket, key string, generation *int64, pre GCSPrecondition) error
	// UpdateObjectGCS mutates an existing object's system properties and/or
	// custom metadata without touching its data, bumping metageneration; a failed
	// pre returns a *GCSPreconditionError.
	UpdateObjectGCS(ctx context.Context, bucket, key string, upd GCSObjectUpdate, pre GCSPrecondition) (*ObjectInfo, error)
	// ComposeObjectGCS concatenates the source objects' bytes (in order) into
	// dstKey, minting a new generation for the destination and honoring the
	// destination pre and each source's pinned generation.
	ComposeObjectGCS(
		ctx context.Context, bucket, dstKey string, srcs []GCSComposeSource,
		contentType string, metadata map[string]string, pre GCSPrecondition,
	) (*ObjectInfo, error)
	// ListObjectGenerations returns every generation (current + archived) of the
	// objects matching opts, for a versions=true listing.
	ListObjectGenerations(ctx context.Context, bucket string, opts ListOptions) (*ListResult, error)

	// SetBucketAttrsGCS records the bucket's location and default storage class
	// (empty values leave the current value unchanged).
	SetBucketAttrsGCS(ctx context.Context, bucket, location, storageClass string) error
	// BucketAttrsGCS returns the bucket's GCS-specific attributes.
	BucketAttrsGCS(ctx context.Context, bucket string) (GCSBucketMeta, error)
	// TouchBucket bumps the bucket's metageneration and updated timestamp,
	// called after any bucket configuration change.
	TouchBucket(ctx context.Context, bucket string) error
	// SetBucketIAMPolicy / BucketIAMPolicy persist and return the bucket's IAM
	// policy document verbatim (Buckets: setIamPolicy / getIamPolicy).
	SetBucketIAMPolicy(ctx context.Context, bucket string, policyJSON []byte) error
	BucketIAMPolicy(ctx context.Context, bucket string) ([]byte, error)

	// CreateNotificationConfig registers a Pub/Sub notification config on a
	// bucket (Notifications: insert), returning the stored config with its
	// minted id and etag. GCS emits an event to the config's topic on matching
	// object changes.
	CreateNotificationConfig(ctx context.Context, bucket string, cfg *GCSNotificationConfig) (GCSNotificationConfig, error)
	// GetNotificationConfig returns a bucket's notification config by id
	// (Notifications: get).
	GetNotificationConfig(ctx context.Context, bucket, id string) (GCSNotificationConfig, error)
	// ListNotificationConfigs returns every notification config on a bucket
	// (Notifications: list).
	ListNotificationConfigs(ctx context.Context, bucket string) ([]GCSNotificationConfig, error)
	// DeleteNotificationConfig removes a bucket's notification config by id
	// (Notifications: delete).
	DeleteNotificationConfig(ctx context.Context, bucket, id string) error
}

// GCSNotificationConfig is one bucket Pub/Sub notification configuration: which
// Pub/Sub topic receives events, which object-change event types fire, and the
// payload format + custom attributes attached to each message.
type GCSNotificationConfig struct {
	// ID is the server-minted numeric notification id (also the etag).
	ID string
	// Topic is the destination in the //pubsub.googleapis.com/projects/{p}/
	// topics/{t} form the storage SDK sends and reads back.
	Topic string
	// PayloadFormat is JSON_API_V1 (object resource JSON as data) or NONE.
	PayloadFormat string
	// EventTypes filters which changes fire; empty means all event types.
	EventTypes []string
	// CustomAttributes are added verbatim to every published message.
	CustomAttributes map[string]string
	// ObjectNamePrefix fires only for objects whose name has this prefix.
	ObjectNamePrefix string
	// Etag mirrors the id, as real GCS returns.
	Etag string
}
