// Package blobstorage provides an in-memory mock implementation of Azure Blob Storage.
package blobstorage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
)

const (
	blobDefaultSASExpiry = time.Hour
	blobDefaultMaxKeys   = 1000
	blobTimeFormat       = "2006-01-02T15:04:05Z"
	// AccountName is the single storage account the blob data plane models. The
	// blob URL host and the Event Grid system-topic routing key both derive from
	// it, so a system-topic subscription created for this account (a bus of this
	// name on the Event Grid mock) receives the account's Blob events.
	AccountName     = "cloudemu"
	blobHoursPerDay = 24
)

// Compile-time check that Mock implements driver.Bucket.
var _ driver.Bucket = (*Mock)(nil)

type blobObject struct {
	Key          string
	Data         []byte
	Size         int64
	ContentType  string
	ETag         string
	LastModified string
	Metadata     map[string]string
	Tags         map[string]string
	// BlobType is "BlockBlob" (default, empty) or "AppendBlob".
	BlobType string
	// VersionID is this blob's version identifier when account-level versioning
	// is enabled — a timestamp id minted on the write that produced it. Empty on
	// an account that never had versioning enabled. On a live (base) blob it is
	// the current version's id; on a copy held in the container's versions store
	// it identifies that historical version.
	VersionID string
	// AccessTier is the blob access tier (Hot/Cool/Cold/Archive), set by Set Blob
	// Tier; empty when unset.
	AccessTier string
	// DeletedTime is when this blob was soft-deleted (blob time format, UTC),
	// stamped when it moves into the container's soft-deleted store. Empty on a
	// live blob.
	DeletedTime string
	// deletedRetentionDays is the account retention window (days) captured at
	// soft-delete time, used to count down RemainingRetentionDays independent of a
	// later policy change. Zero on a live blob.
	deletedRetentionDays int
	// Content* are additional system properties settable via Set Blob Properties.
	ContentEncoding    string
	ContentLanguage    string
	ContentDisposition string
	CacheControl       string
	// CommittedBlocks records the block id/size pairs assembled by the most
	// recent Put Block List commit, in commit order, for Get Block List.
	CommittedBlocks []driver.BlockInfo
	// committedBlockData retains the bytes of each committed block by id, so a
	// later Put Block List can re-reference an already-committed block
	// (<Committed>/<Latest>) instead of only freshly staged ones.
	committedBlockData map[string][]byte
	// mu guards the in-place mutations of the new Azure ops (append, metadata /
	// property / tier updates, leases). Whole-object replacements via the
	// container store don't need it.
	mu sync.Mutex
	// appendBlocks counts committed Append Block operations (append blobs only).
	appendBlocks int
	// pages tracks which 512-byte pages of a page blob currently hold written
	// (non-cleared) data, keyed by zero-based page index. nil for non-page blobs.
	// Get Page Ranges coalesces the set into contiguous byte ranges; Put Page adds
	// indices and Clear Page removes them. Guarded by mu alongside Data.
	pages map[int64]bool

	// Lease Blob state. leaseState is one of leaseStateAvailable (""),
	// leaseStateLeased, leaseStateBreaking, or leaseStateBroken; a "leased" or
	// "breaking" state additionally transitions to expired/broken lazily (see
	// effectiveLeaseState) once its deadline passes, without a background timer.
	leaseState       string
	leaseID          string
	leaseDurationSec int32
	leaseExpiresAt   time.Time
	leaseBreakAt     time.Time
	// leaseModTimeAtAcquire is the blob's LastModified at the last successful
	// Acquire/Renew, used to detect "blob modified since lease" on a Renew of
	// an expired-but-unreleased lease.
	leaseModTimeAtAcquire string

	// Immutable-storage (WORM) state, guarded by mu. immutabilityMode is one of
	// driver.BlobImmutabilityUnlocked/Locked (empty when no time-based policy is
	// set); immutabilityExpiry is its retain-until instant. legalHold, when true,
	// protects the blob independent of any policy. While the blob has an unexpired
	// policy OR a legal hold, delete and overwrite are blocked.
	immutabilityMode   string
	immutabilityExpiry time.Time
	legalHold          bool
}

type blobMultipartUpload struct {
	id          string
	key         string
	contentType string
	// mu guards parts: the SDK uploader sends parts concurrently (UploadPart
	// writes) while ListParts/CompleteMultipartUpload read them.
	mu        sync.Mutex
	parts     map[int][]byte
	createdAt string
}

type containerMeta struct {
	Name       string
	Region     string
	CreatedAt  string
	objects    *memstore.Store[*blobObject]
	lifecycle  *driver.LifecycleConfig
	multiparts *memstore.Store[*blobMultipartUpload]
	versioning bool
	policy     *driver.BucketPolicy
	corsConfig *driver.CORSConfig
	encryption *driver.EncryptionConfig
	tags       map[string]string
	// metadata is the container's x-ms-meta-* metadata (Set Container Metadata).
	metadata map[string]string
	// publicAccess is the container's public access level (Set/Get Container
	// ACL), empty for private (the Azure default).
	publicAccess string
	// accessPolicies are the container's stored access policies (Set/Get
	// Container ACL).
	accessPolicies []driver.SignedIdentifier
	// staging holds uncommitted blocks (Put Block) keyed by blob name.
	staging *memstore.Store[*blockStaging]
	// snapshots holds immutable blob snapshots keyed by snapshotKey(blob, id).
	// Snapshots live at the container level so they survive a base-blob
	// overwrite, matching real Azure snapshot lifetime.
	snapshots *memstore.Store[*blobObject]
	// versions holds immutable blob versions keyed by versionKey(blob, id) when
	// account-level versioning is enabled. Like snapshots they live at the
	// container level so a version survives a base-blob overwrite or delete,
	// matching real Azure version lifetime.
	versions *memstore.Store[*blobObject]
	// softDeleted holds soft-deleted blobs keyed by blob name when the account
	// delete-retention policy is enabled. A Delete Blob moves the live blob here
	// (retaining its bytes) instead of removing it; Undelete Blob moves it back,
	// and the retention window purges it lazily.
	softDeleted *memstore.Store[*blobObject]
	// mu guards snapshotSeq/versionSeq (and any future container-scoped counters).
	// It ALSO serializes every objects↔softDeleted transition (soft delete and
	// Undelete) so a blob is atomically moved between the two stores: without one
	// lock spanning both writes, a racing Delete and Undelete on the same blob can
	// interleave their independent per-store writes and leave the blob in NEITHER
	// store (permanent data loss). Held only across the two-store mutation, never
	// while emitting metrics/events.
	mu          sync.Mutex
	snapshotSeq int
	versionSeq  int
}

// blockStaging buffers the uncommitted blocks staged for one blob before a
// Put Block List commits them.
type blockStaging struct {
	mu     sync.Mutex
	blocks map[string][]byte
}

// Mock is an in-memory mock implementation of Azure Blob Storage.
type Mock struct {
	containers *memstore.Store[*containerMeta]
	// bucketAttrs holds Azure storage-account attributes (SKU/kind/access tier/
	// location/tags) per container, for the BucketAttributes discovery capability.
	bucketAttrs *memstore.Store[driver.AccountAttributes]
	// accountKeys holds the shared access keys per storage account, generated
	// lazily on first ListStorageAccountKeys.
	accountKeys *memstore.Store[[]driver.AccountKey]
	// blobServiceProps holds the account-level Blob service properties
	// (versioning/soft-delete/change-feed/CORS) set via the blobServices/default
	// ARM sub-resource, for the BlobServiceConfig capability.
	blobServiceProps *memstore.Store[driver.BlobServiceProperties]
	// accountEncryption holds the account's service-side encryption
	// configuration (Properties.Encryption), for the AccountEncryptionConfig
	// capability. Kept separate from bucketAttrs so that struct stays small.
	accountEncryption *memstore.Store[driver.AccountEncryption]
	opts              *config.Options
	monitoring        mondriver.Monitoring
	// eventgrid, when wired, receives a Microsoft.Storage.BlobCreated/BlobDeleted
	// event on every blob write/delete so an Event Grid system-topic subscription
	// for this account can deliver it. nil in library/typed use, where blob writes
	// simply skip event emission.
	eventgrid EventGridPublisher
	// blobEventSeq backs the monotonically increasing sequencer stamped on each
	// emitted blob event (Event Grid's per-blob ordering token).
	blobEventSeq atomic.Uint64
}

// Compile-time check that Mock satisfies the optional BucketAttributes
// discovery capability, so a signature typo fails the build rather than
// silently failing the runtime type assertion in walkStorage.
var _ driver.BucketAttributes = (*Mock)(nil)

// Compile-time check that Mock satisfies the optional BlobServiceConfig
// capability the ARM storage-account handler reaches by type assertion.
var _ driver.BlobServiceConfig = (*Mock)(nil)

// Compile-time check that Mock satisfies the optional AccountEncryptionConfig
// capability the ARM storage-account handler reaches by type assertion.
var _ driver.AccountEncryptionConfig = (*Mock)(nil)

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(container string, metrics map[string]float64) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, 0, len(metrics))

	for name, value := range metrics {
		data = append(data, mondriver.MetricDatum{
			Namespace:  "Microsoft.Storage/storageAccounts",
			MetricName: name,
			Value:      value,
			Unit:       "None",
			Dimensions: map[string]string{"containerName": container},
			Timestamp:  now,
		})
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// New creates a new Azure Blob Storage mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		containers:        memstore.New[*containerMeta](),
		bucketAttrs:       memstore.New[driver.AccountAttributes](),
		accountKeys:       memstore.New[[]driver.AccountKey](),
		blobServiceProps:  memstore.New[driver.BlobServiceProperties](),
		accountEncryption: memstore.New[driver.AccountEncryption](),
		opts:              opts,
	}
}

// SetBucketAttributes seeds the Azure storage-account attributes (SKU/kind/
// access tier) for a container, so tests and the ARM layer can vary them.
func (m *Mock) SetBucketAttributes(name string, attrs driver.AccountAttributes) {
	m.bucketAttrs.Set(name, attrs)
}

// BucketAttributes implements the storage BucketAttributes optional capability,
// returning the seeded attributes or the real-Azure defaults (Standard_LRS /
// StorageV2 / Hot) so a cost discoverer always sees a priceable SKU.
func (m *Mock) BucketAttributes(_ context.Context, bucket string) (driver.AccountAttributes, error) {
	a, ok := m.bucketAttrs.Get(bucket)
	if !ok {
		return driver.AccountAttributes{SKU: "Standard_LRS", Kind: "StorageV2", AccessTier: "Hot"}, nil
	}

	if a.SKU == "" {
		a.SKU = "Standard_LRS"
	}

	if a.Kind == "" {
		a.Kind = "StorageV2"
	}

	if a.AccessTier == "" {
		a.AccessTier = "Hot"
	}

	return a, nil
}

// UpdateBucketAttributes atomically applies fn to a container's stored
// attributes (Azure storage-account PATCH — AccountsClient.Update), seeding
// the real-Azure baseline (Standard_LRS/StorageV2/Hot) first if none was set
// yet. Routed through memstore's Update rather than a Get-then-Set pair so a
// concurrent PATCH/create never loses an update.
func (m *Mock) UpdateBucketAttributes(
	_ context.Context, name string, fn func(driver.AccountAttributes) driver.AccountAttributes,
) (driver.AccountAttributes, error) {
	m.bucketAttrs.SetIfAbsent(name, driver.AccountAttributes{SKU: "Standard_LRS", Kind: "StorageV2", AccessTier: "Hot"})

	var updated driver.AccountAttributes

	m.bucketAttrs.Update(name, func(v driver.AccountAttributes) driver.AccountAttributes {
		updated = fn(v)
		return updated
	})

	return updated, nil
}

// SetBlobServiceProperties implements the storage BlobServiceConfig optional
// capability (…/blobServices/default PUT), replacing any previously stored
// properties for the account wholesale — matching real Azure's Set Blob
// Service Properties, which takes a complete properties document each call.
func (m *Mock) SetBlobServiceProperties(_ context.Context, account string, props driver.BlobServiceProperties) error {
	m.blobServiceProps.Set(account, props)

	return nil
}

// BlobServiceProperties implements the storage BlobServiceConfig optional
// capability (…/blobServices/default GET), returning the zero value (all
// features disabled) for an account that never had properties set — matching
// real Azure's defaults for a freshly created account.
func (m *Mock) BlobServiceProperties(_ context.Context, account string) (driver.BlobServiceProperties, error) {
	props, _ := m.blobServiceProps.Get(account)

	return props, nil
}

// SetAccountEncryption implements the storage AccountEncryptionConfig
// optional capability, storing the account's requested encryption
// configuration so it round-trips on a later GET instead of always reporting
// the platform-managed default.
func (m *Mock) SetAccountEncryption(account string, enc driver.AccountEncryption) {
	m.accountEncryption.Set(account, enc)
}

// AccountEncryption implements the storage AccountEncryptionConfig optional
// capability, returning the zero value (platform-managed default) for an
// account that never requested customer-managed-key encryption.
func (m *Mock) AccountEncryption(_ context.Context, account string) (driver.AccountEncryption, error) {
	enc, _ := m.accountEncryption.Get(account)

	return enc, nil
}

// CreateBucket creates a new blob container.
func (m *Mock) CreateBucket(_ context.Context, name string) error {
	if name == "" {
		return cerrors.New(cerrors.InvalidArgument, "container name cannot be empty")
	}

	if m.containers.Has(name) {
		return cerrors.Newf(cerrors.AlreadyExists, "container %q already exists", name)
	}

	m.containers.Set(name, &containerMeta{
		Name:        name,
		Region:      m.opts.Region,
		CreatedAt:   m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		objects:     memstore.New[*blobObject](),
		multiparts:  memstore.New[*blobMultipartUpload](),
		staging:     memstore.New[*blockStaging](),
		snapshots:   memstore.New[*blobObject](),
		versions:    memstore.New[*blobObject](),
		softDeleted: memstore.New[*blobObject](),
	})

	return nil
}

// DeleteBucket deletes a blob container.
func (m *Mock) DeleteBucket(_ context.Context, name string) error {
	ctr, ok := m.containers.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", name)
	}

	if ctr.objects.Len() > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition, "container %q is not empty", name)
	}

	m.containers.Delete(name)

	return nil
}

// ListBuckets lists all blob containers.
func (m *Mock) ListBuckets(_ context.Context) ([]driver.BucketInfo, error) {
	keys := m.containers.Keys()
	sort.Strings(keys)

	result := make([]driver.BucketInfo, 0, len(keys))

	for _, k := range keys {
		ctr, ok := m.containers.Get(k)
		if !ok {
			continue
		}

		result = append(result, driver.BucketInfo{
			Name:      ctr.Name,
			Region:    ctr.Region,
			CreatedAt: ctr.CreatedAt,
		})
	}

	return result, nil
}

// PutObject stores a blob in a container. When a real StorageEngine is wired the
// bytes flow through it and the in-memory object holds metadata only (Data nil).
func (m *Mock) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	_, err := m.putBlockBlobInternal(ctx, bucket, key, data, contentType, nil, metadata)

	return err
}

// PutBlockBlob writes a block blob's content together with its system content
// properties (Put Blob), so Content-Encoding/Cache-Control/Content-Language/
// Content-Disposition round-trip on a later read. props may be nil.
func (m *Mock) PutBlockBlob(
	ctx context.Context, bucket, key string, data []byte, props *driver.BlobProperties, metadata map[string]string,
) (*driver.ObjectInfo, error) {
	contentType := ""
	if props != nil {
		contentType = props.ContentType
	}

	return m.putBlockBlobInternal(ctx, bucket, key, data, contentType, props, metadata)
}

// putBlockBlobInternal is the shared Put Blob path: it stores the content,
// optional content properties (props may be nil), and metadata, carrying over
// any active lease, and returns the new object info.
func (m *Mock) putBlockBlobInternal(
	ctx context.Context, bucket, key string, data []byte, contentType string,
	props *driver.BlobProperties, metadata map[string]string,
) (*driver.ObjectInfo, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	// Immutable storage (WORM): overwriting a protected blob is blocked.
	if err := m.enforceImmutable(ctr, key); err != nil {
		return nil, err
	}

	size := int64(len(data))
	meta := maps.Clone(metadata)

	obj := &blobObject{
		Key:          key,
		Size:         size,
		ContentType:  contentType,
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     meta,
	}

	if props != nil {
		obj.ContentEncoding = props.ContentEncoding
		obj.ContentLanguage = props.ContentLanguage
		obj.ContentDisposition = props.ContentDisposition
		obj.CacheControl = props.CacheControl
	}

	if m.opts.StorageEngine != nil {
		if err := storageengine.Put(ctx, m.opts.StorageEngine, config.StorageObject{
			Bucket: bucket, Key: key, Data: data, ContentType: contentType, Metadata: meta,
		}); err != nil {
			return nil, err
		}
	} else {
		dataCopy := make([]byte, len(data))
		copy(dataCopy, data)
		obj.Data = dataCopy
	}

	m.carryOverLease(ctr, key, obj)
	m.recordVersion(ctr, obj)
	ctr.objects.Set(key, obj)

	m.emitMetric(bucket, map[string]float64{"Transactions": 1, "Ingress": float64(size)})
	m.emitBlobCreated(ctx, obj, bucket)

	info := objectInfo(obj)

	return &info, nil
}

// GetObject retrieves a blob from a container.
func (m *Mock) GetObject(ctx context.Context, bucket, key string) (*driver.Object, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	obj, ok := ctr.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}

	// Get Blob doesn't support reading blob content while the blob's tier is
	// Archive: the data has been moved to offline storage and must first be
	// rehydrated by Set Blob Tier to an online tier. Get Blob Properties/Head/
	// List Blobs still succeed and report the Archive tier.
	// https://learn.microsoft.com/en-us/rest/api/storageservices/set-blob-tier
	if obj.AccessTier == accessTierArchive {
		return nil, &driver.BlobOpError{
			Status: http.StatusConflict, Code: "BlobArchived",
			Message: "This operation is not permitted on an archived blob.",
		}
	}

	data, err := m.loadObjectData(ctx, bucket, obj)
	if err != nil {
		return nil, err
	}

	m.emitMetric(bucket, map[string]float64{"Transactions": 1, "Egress": float64(obj.Size)})

	return &driver.Object{
		Info: objectInfo(obj),
		Data: data,
	}, nil
}

// objectInfo renders a blobObject as a driver.ObjectInfo with cloned metadata.
func objectInfo(obj *blobObject) driver.ObjectInfo {
	return driver.ObjectInfo{
		Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType,
		ETag: obj.ETag, LastModified: obj.LastModified, Metadata: maps.Clone(obj.Metadata),
		BlobType: obj.BlobType, AccessTier: obj.AccessTier, VersionID: obj.VersionID,
		CacheControl: obj.CacheControl, ContentEncoding: obj.ContentEncoding,
		ContentDisposition: obj.ContentDisposition, ContentLanguage: obj.ContentLanguage,
	}
}

// loadObjectData returns the object's bytes: from the wired StorageEngine when
// one is configured and the in-memory copy was dropped (Data nil), otherwise a
// copy of the in-memory bytes. Azure has a flat versioning flag with no version
// history, so the engine reference carries no version.
func (m *Mock) loadObjectData(ctx context.Context, bucket string, obj *blobObject) ([]byte, error) {
	if m.opts.StorageEngine != nil && obj.Data == nil {
		data, ok, err := storageengine.Get(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: obj.Key})
		if err != nil {
			return nil, err
		}

		if ok {
			return data, nil
		}
	}

	dataCopy := make([]byte, len(obj.Data))
	copy(dataCopy, obj.Data)

	return dataCopy, nil
}

// DeleteObject deletes a blob from a container.
func (m *Mock) DeleteObject(ctx context.Context, bucket, key string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	obj, ok := ctr.objects.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}

	// Immutable storage (WORM): an unexpired immutability policy or an active
	// legal hold blocks the delete before any soft-delete/purge runs.
	if err := m.enforceImmutable(ctr, key); err != nil {
		return err
	}

	// When soft delete is in effect, a Delete Blob retains the blob (bytes and
	// all) in the container's soft-deleted store rather than removing it, so
	// Undelete can restore it within the retention window. The active byte purge
	// below is skipped precisely because the bytes must survive.
	if retentionDays, soft := m.softDeleteActive(); soft {
		m.softDeleteObject(ctr, key, obj, retentionDays)
		m.emitMetric(bucket, map[string]float64{"Transactions": 1})
		m.emitBlobDeleted(ctx, obj, bucket)

		return nil
	}

	ctr.objects.Delete(key)

	// Best-effort byte purge — the in-memory delete already succeeded, so a
	// backing cleanup failure must not fail an idempotent object delete.
	_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key})

	m.emitMetric(bucket, map[string]float64{"Transactions": 1})
	m.emitBlobDeleted(ctx, obj, bucket)

	return nil
}

// HeadObject returns metadata for a blob without its data.
func (m *Mock) HeadObject(_ context.Context, bucket, key string) (*driver.ObjectInfo, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	obj, ok := ctr.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}

	info := objectInfo(obj)

	return &info, nil
}

// listEntry is one item in the merged list stream — either a blob or a
// delimiter-rolled-up common prefix — so both count toward maxresults and
// paginate together (matching real Azure's List Blobs).
type listEntry struct {
	name     string
	isPrefix bool
	obj      *blobObject
}

// ListObjects lists blobs in a container with optional prefix/delimiter filtering.
func (m *Mock) ListObjects(_ context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	entries := collectListEntries(ctr, opts)

	maxKeys := opts.MaxKeys
	if maxKeys <= 0 {
		maxKeys = blobDefaultMaxKeys
	}

	// Blobs and common prefixes fold into ONE sorted stream so both count
	// toward maxresults and neither is duplicated nor skipped across pages.
	page, err := pagination.PaginateSorted(entries, func(a, b listEntry) bool {
		if a.name != b.name {
			return a.name < b.name
		}

		return !a.isPrefix && b.isPrefix
	}, opts.PageToken, maxKeys)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	objects, commonPrefixes := splitListPage(page.Items)

	m.emitMetric(bucket, map[string]float64{"Transactions": 1})

	return &driver.ListResult{
		Objects:        objects,
		CommonPrefixes: commonPrefixes,
		NextPageToken:  page.NextPageToken,
		IsTruncated:    page.HasMore,
	}, nil
}

// collectListEntries builds the unsorted merged blob+prefix stream for a list
// request, applying the prefix filter and (when set) the delimiter rollup.
func collectListEntries(ctr *containerMeta, opts driver.ListOptions) []listEntry {
	allKeys := ctr.objects.Keys()
	sort.Strings(allKeys)

	var entries []listEntry

	seenPrefix := make(map[string]struct{})

	for _, k := range allKeys {
		if opts.Prefix != "" && !strings.HasPrefix(k, opts.Prefix) {
			continue
		}

		if opts.Delimiter != "" {
			rest := k[len(opts.Prefix):]

			if idx := strings.Index(rest, opts.Delimiter); idx >= 0 {
				prefix := opts.Prefix + rest[:idx+len(opts.Delimiter)]
				if _, dup := seenPrefix[prefix]; !dup {
					seenPrefix[prefix] = struct{}{}

					entries = append(entries, listEntry{name: prefix, isPrefix: true})
				}

				continue
			}
		}

		obj, objOk := ctr.objects.Get(k)
		if !objOk {
			continue
		}

		entries = append(entries, listEntry{name: obj.Key, obj: obj})
	}

	return entries
}

// splitListPage separates a paginated merged page back into the blob objects
// and common prefixes the driver.ListResult reports, preserving page order.
// objectInfo (which clones metadata) runs only for the page actually returned,
// not for every match, keeping a paged scan cheap on a large container.
func splitListPage(items []listEntry) (objects []driver.ObjectInfo, commonPrefixes []string) {
	for i := range items {
		if items[i].isPrefix {
			commonPrefixes = append(commonPrefixes, items[i].name)
			continue
		}

		objects = append(objects, objectInfo(items[i].obj))
	}

	return objects, commonPrefixes
}

// CopyObject copies a blob from one location to another, inheriting the source
// blob's metadata (the default Azure Copy Blob behavior when the request carries
// no x-ms-meta-* headers).
func (m *Mock) CopyObject(ctx context.Context, dstBucket, dstKey string, src driver.CopySource) error {
	_, err := m.copyBlobInternal(ctx, dstBucket, dstKey, src, nil, false)

	return err
}

// CopyObjectV2 implements the ObjectCopier capability so the Azure Blob wire
// layer can express Copy Blob's metadata override: when ReplaceMetadata is set
// the destination takes exactly req.Metadata instead of inheriting the source's.
// Content properties are always inherited from the source (Azure Copy Blob does
// not let the caller override them).
func (m *Mock) CopyObjectV2(ctx context.Context, req *driver.CopyObjectRequest) (*driver.CopyObjectResult, error) {
	info, err := m.copyBlobInternal(ctx, req.DstBucket, req.DstKey, req.Src, req.Metadata, req.ReplaceMetadata)
	if err != nil {
		return nil, err
	}

	return &driver.CopyObjectResult{ETag: info.ETag, LastModified: info.LastModified}, nil
}

// copyBlobInternal is the shared copy path for CopyObject and CopyObjectV2. When
// replaceMeta is true the destination's metadata is exactly overrideMeta;
// otherwise it inherits the source blob's metadata.
func (m *Mock) copyBlobInternal(
	ctx context.Context, dstBucket, dstKey string, src driver.CopySource, overrideMeta map[string]string, replaceMeta bool,
) (*driver.ObjectInfo, error) {
	srcCtr, ok := m.containers.Get(src.Bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "source container %q not found", src.Bucket)
	}

	srcObj, ok := srcCtr.objects.Get(src.Key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "source blob %q not found", src.Key)
	}

	dstCtr, ok := m.containers.Get(dstBucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "destination container %q not found", dstBucket)
	}

	// Immutable storage (WORM): overwriting a protected destination blob is blocked.
	if err := m.enforceImmutable(dstCtr, dstKey); err != nil {
		return nil, err
	}

	meta := maps.Clone(srcObj.Metadata)
	if replaceMeta {
		meta = maps.Clone(overrideMeta)
	}

	dstObj := &blobObject{
		Key: dstKey, Size: srcObj.Size, ContentType: srcObj.ContentType,
		ETag: srcObj.ETag, LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata: meta, BlobType: srcObj.BlobType, AccessTier: srcObj.AccessTier,
		ContentEncoding: srcObj.ContentEncoding, ContentLanguage: srcObj.ContentLanguage,
		ContentDisposition: srcObj.ContentDisposition, CacheControl: srcObj.CacheControl,
	}

	if m.opts.StorageEngine != nil {
		if err := storageengine.Copy(ctx, m.opts.StorageEngine,
			config.StorageRef{Bucket: dstBucket, Key: dstKey},
			config.StorageRef{Bucket: src.Bucket, Key: src.Key}); err != nil {
			return nil, err
		}
	} else {
		dataCopy := make([]byte, len(srcObj.Data))
		copy(dataCopy, srcObj.Data)
		dstObj.Data = dataCopy
	}

	m.carryOverLease(dstCtr, dstKey, dstObj)
	m.recordVersion(dstCtr, dstObj)
	dstCtr.objects.Set(dstKey, dstObj)

	m.emitMetric(dstBucket, map[string]float64{"Transactions": 1})
	m.emitBlobCreatedAPI(ctx, dstObj, dstBucket, blobEventAPICopyBlob)

	info := objectInfo(dstObj)

	return &info, nil
}

// GeneratePresignedURL generates a mock presigned URL.
// Note: expiry is tracked in the URL but not enforced on use — this is a mock limitation.
func (m *Mock) GeneratePresignedURL(_ context.Context, req driver.PresignedURLRequest) (*driver.PresignedURL, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodPut {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "method must be GET or PUT, got %q", req.Method)
	}

	if !m.containers.Has(req.Bucket) {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", req.Bucket)
	}

	expiry := req.ExpiresIn
	if expiry <= 0 {
		expiry = blobDefaultSASExpiry
	}

	now := m.opts.Clock.Now().UTC()
	sig := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Bucket+req.Key+now.String())))
	expiresAt := now.Add(expiry)
	permissions := "r"

	if req.Method == http.MethodPut {
		permissions = "w"
	}

	url := fmt.Sprintf(
		"https://%s.blob.core.windows.net/%s/%s?sv=2023-11-03&sig=%s&se=%s&sp=%s",
		AccountName, req.Bucket, req.Key, sig,
		expiresAt.Format(blobTimeFormat), permissions,
	)

	return &driver.PresignedURL{URL: url, Method: req.Method, ExpiresAt: expiresAt}, nil
}

func (m *Mock) PutLifecycleConfig(_ context.Context, bucket string, cfg driver.LifecycleConfig) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	cfgCopy := driver.LifecycleConfig{Rules: make([]driver.LifecycleRule, len(cfg.Rules))}
	copy(cfgCopy.Rules, cfg.Rules)
	ctr.lifecycle = &cfgCopy

	return nil
}

func (m *Mock) GetLifecycleConfig(_ context.Context, bucket string) (*driver.LifecycleConfig, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	if ctr.lifecycle == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no lifecycle configuration for container %q", bucket)
	}

	return ctr.lifecycle, nil
}

func (m *Mock) EvaluateLifecycle(_ context.Context, bucket string) ([]string, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	if ctr.lifecycle == nil {
		return nil, nil
	}

	now := m.opts.Clock.Now().UTC()
	expired := collectExpiredBlobKeys(ctr, now)
	sort.Strings(expired)

	return expired, nil
}

func collectExpiredBlobKeys(ctr *containerMeta, now time.Time) []string {
	var result []string

	for _, key := range ctr.objects.Keys() {
		obj, objOk := ctr.objects.Get(key)
		if !objOk {
			continue
		}

		if blobExpired(obj, ctr.lifecycle, now) {
			result = append(result, key)
		}
	}

	return result
}

func blobExpired(obj *blobObject, cfg *driver.LifecycleConfig, now time.Time) bool {
	modified, err := time.Parse(blobTimeFormat, obj.LastModified)
	if err != nil {
		return false
	}

	age := now.Sub(modified)

	for _, rule := range cfg.Rules {
		if !rule.Enabled {
			continue
		}

		if rule.Prefix != "" && !strings.HasPrefix(obj.Key, rule.Prefix) {
			continue
		}

		if rule.ExpirationDays > 0 && age >= time.Duration(rule.ExpirationDays)*blobHoursPerDay*time.Hour {
			return true
		}
	}

	return false
}

func (m *Mock) CreateMultipartUpload(
	_ context.Context, bucket, key, contentType string,
) (*driver.MultipartUpload, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	uploadID := idgen.GenerateID("upload-")
	now := m.opts.Clock.Now().UTC().Format(blobTimeFormat)

	ctr.multiparts.Set(uploadID, &blobMultipartUpload{
		id:          uploadID,
		key:         key,
		contentType: contentType,
		parts:       make(map[int][]byte),
		createdAt:   now,
	})

	return &driver.MultipartUpload{
		UploadID: uploadID, Bucket: bucket, Key: key, CreatedAt: now,
	}, nil
}

func (m *Mock) UploadPart(
	_ context.Context, bucket, _, uploadID string, partNumber int, data []byte,
) (*driver.UploadPart, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	mp, ok := ctr.multiparts.Get(uploadID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "upload %q not found", uploadID)
	}

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	mp.mu.Lock()
	mp.parts[partNumber] = dataCopy
	mp.mu.Unlock()

	etag := fmt.Sprintf("%x", sha256.Sum256(data))

	return &driver.UploadPart{
		PartNumber: partNumber, ETag: etag, Size: int64(len(data)),
	}, nil
}

// ListParts returns the parts buffered so far for an in-progress upload,
// ordered by part number.
func (m *Mock) ListParts(_ context.Context, bucket, _, uploadID string) ([]driver.UploadPart, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	mp, ok := ctr.multiparts.Get(uploadID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "upload %q not found", uploadID)
	}

	mp.mu.Lock()
	defer mp.mu.Unlock()

	nums := make([]int, 0, len(mp.parts))
	for n := range mp.parts {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	out := make([]driver.UploadPart, 0, len(nums))
	for _, n := range nums {
		data := mp.parts[n]
		out = append(out, driver.UploadPart{
			PartNumber: n,
			ETag:       fmt.Sprintf("%x", sha256.Sum256(data)),
			Size:       int64(len(data)),
		})
	}

	return out, nil
}

func (m *Mock) CompleteMultipartUpload(
	ctx context.Context, bucket, key, uploadID string, parts []driver.UploadPart,
) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	mp, ok := ctr.multiparts.Get(uploadID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "upload %q not found", uploadID)
	}

	mp.mu.Lock()
	for _, p := range parts {
		if _, exists := mp.parts[p.PartNumber]; !exists {
			mp.mu.Unlock()
			return cerrors.Newf(cerrors.InvalidArgument, "part %d not found in upload %q", p.PartNumber, uploadID)
		}
	}
	data := assembleBlobPartsInOrder(mp.parts, parts)
	mp.mu.Unlock()

	size := int64(len(data))

	obj := &blobObject{
		Key:          key,
		Size:         size,
		ContentType:  mp.contentType,
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     make(map[string]string),
	}

	if m.opts.StorageEngine != nil {
		if err := storageengine.Put(ctx, m.opts.StorageEngine, config.StorageObject{
			Bucket: bucket, Key: key, Data: data, ContentType: mp.contentType,
		}); err != nil {
			return err
		}
	} else {
		obj.Data = data
	}

	m.recordVersion(ctr, obj)
	ctr.objects.Set(key, obj)

	ctr.multiparts.Delete(uploadID)

	m.emitMetric(bucket, map[string]float64{"Transactions": 1, "Ingress": float64(size)})

	return nil
}

func assembleBlobPartsInOrder(allParts map[int][]byte, parts []driver.UploadPart) []byte {
	var data []byte
	for _, p := range parts {
		data = append(data, allParts[p.PartNumber]...)
	}

	return data
}

func (m *Mock) AbortMultipartUpload(_ context.Context, bucket, _, uploadID string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	if !ctr.multiparts.Has(uploadID) {
		return cerrors.Newf(cerrors.NotFound, "upload %q not found", uploadID)
	}

	ctr.multiparts.Delete(uploadID)

	return nil
}

func (m *Mock) ListMultipartUploads(_ context.Context, bucket string) ([]driver.MultipartUpload, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	keys := ctr.multiparts.Keys()
	sort.Strings(keys)

	result := make([]driver.MultipartUpload, 0, len(keys))

	for _, k := range keys {
		mp, mpOk := ctr.multiparts.Get(k)
		if !mpOk {
			continue
		}

		result = append(result, driver.MultipartUpload{
			UploadID: mp.id, Bucket: bucket, Key: mp.key, CreatedAt: mp.createdAt,
		})
	}

	return result, nil
}

// SetBucketVersioning enables or disables versioning on a bucket.
// Note: this sets the flag but does not maintain object version history — mock limitation.
func (m *Mock) SetBucketVersioning(_ context.Context, bucket string, enabled bool) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	ctr.versioning = enabled

	return nil
}

func (m *Mock) GetBucketVersioning(_ context.Context, bucket string) (bool, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return false, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	return ctr.versioning, nil
}

func (m *Mock) PutBucketPolicy(_ context.Context, bucket string, policy driver.BucketPolicy) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	p := policy
	ctr.policy = &p

	return nil
}

func (m *Mock) GetBucketPolicy(_ context.Context, bucket string) (*driver.BucketPolicy, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	if ctr.policy == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no policy set for container %q", bucket)
	}

	p := *ctr.policy

	return &p, nil
}

func (m *Mock) DeleteBucketPolicy(_ context.Context, bucket string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	ctr.policy = nil

	return nil
}

func (m *Mock) PutCORSConfig(_ context.Context, bucket string, cfg driver.CORSConfig) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	c := cfg
	ctr.corsConfig = &c

	return nil
}

func (m *Mock) GetCORSConfig(_ context.Context, bucket string) (*driver.CORSConfig, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	if ctr.corsConfig == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no CORS config set for container %q", bucket)
	}

	c := *ctr.corsConfig

	return &c, nil
}

func (m *Mock) DeleteCORSConfig(_ context.Context, bucket string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	ctr.corsConfig = nil

	return nil
}

func (m *Mock) PutEncryptionConfig(_ context.Context, bucket string, cfg driver.EncryptionConfig) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	e := cfg
	ctr.encryption = &e

	return nil
}

func (m *Mock) GetEncryptionConfig(_ context.Context, bucket string) (*driver.EncryptionConfig, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	if ctr.encryption == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no encryption config set for container %q", bucket)
	}

	e := *ctr.encryption

	return &e, nil
}

// PutObjectTagging sets tags on a blob.
func (m *Mock) PutObjectTagging(_ context.Context, bucket, key string, tags map[string]string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	obj, ok := ctr.objects.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}

	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}

	obj.Tags = copied

	return nil
}

// GetObjectTagging returns tags for a blob.
func (m *Mock) GetObjectTagging(_ context.Context, bucket, key string) (map[string]string, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	obj, ok := ctr.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}

	if obj.Tags == nil {
		return map[string]string{}, nil
	}

	copied := make(map[string]string, len(obj.Tags))
	for k, v := range obj.Tags {
		copied[k] = v
	}

	return copied, nil
}

// DeleteObjectTagging removes all tags from a blob.
func (m *Mock) DeleteObjectTagging(_ context.Context, bucket, key string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	obj, ok := ctr.objects.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}

	obj.Tags = nil

	return nil
}

// PutBucketTagging sets tags on a container.
func (m *Mock) PutBucketTagging(_ context.Context, bucket string, tags map[string]string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}

	ctr.tags = copied

	return nil
}

// GetBucketTagging returns tags for a container.
func (m *Mock) GetBucketTagging(_ context.Context, bucket string) (map[string]string, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	if ctr.tags == nil {
		return map[string]string{}, nil
	}

	copied := make(map[string]string, len(ctr.tags))
	for k, v := range ctr.tags {
		copied[k] = v
	}

	return copied, nil
}

// DeleteBucketTagging removes all tags from a container.
func (m *Mock) DeleteBucketTagging(_ context.Context, bucket string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	ctr.tags = nil

	return nil
}
