package blobstorage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
)

const (
	blobTypeBlock  = "BlockBlob"
	blobTypeAppend = "AppendBlob"
	snapshotFormat = "2006-01-02T15:04:05."
	octetStream    = "application/octet-stream"

	accessTierHot     = "Hot"
	accessTierCool    = "Cool"
	accessTierCold    = "Cold"
	accessTierArchive = "Archive"

	// Lease Blob states. See
	// https://learn.microsoft.com/en-us/rest/api/storageservices/lease-blob.
	leaseStateAvailable = ""
	leaseStateLeased    = "leased"
	leaseStateBreaking  = "breaking"
	leaseStateBroken    = "broken"
	leaseStateExpired   = "expired"

	leaseDurationInfinite int32 = -1
	leaseDurationMinSec   int32 = 15
	leaseDurationMaxSec   int32 = 60
)

// Compile-time check that Mock satisfies the optional AzureBlobExtensions
// capability the blob wire handler reaches by type assertion.
var _ driver.AzureBlobExtensions = (*Mock)(nil)

// StageBlock buffers an uncommitted block for a blob under blockID.
func (m *Mock) StageBlock(_ context.Context, container, blob, blockID string, data []byte) error {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	stg, ok := ctr.staging.Get(blob)
	if !ok {
		stg = &blockStaging{blocks: make(map[string][]byte)}
		ctr.staging.Set(blob, stg)
	}

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	stg.mu.Lock()
	stg.blocks[blockID] = dataCopy
	stg.mu.Unlock()

	return nil
}

// CommitBlockList assembles a block blob from the given block entries. Each
// entry is resolved against its source list (Committed/Uncommitted/Latest), so
// a commit can re-reference blocks already committed on the blob — the "append
// by re-committing existing blocks plus a new one" pattern.
func (m *Mock) CommitBlockList(
	ctx context.Context, container, blob string, blocks []driver.BlockListEntry,
	contentType string, props *driver.BlobProperties, metadata map[string]string,
) (*driver.ObjectInfo, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	// Immutable storage (WORM): overwriting a protected blob is blocked.
	if err := m.enforceImmutable(ctr, blob); err != nil {
		return nil, err
	}

	existing, _ := ctr.objects.Get(blob)

	data, blockInfos, committedData, err := assembleBlocks(ctr, blob, blocks, existing)
	if err != nil {
		return nil, err
	}

	if contentType == "" {
		contentType = octetStream
	}

	obj := &blobObject{
		Key: blob, Size: int64(len(data)), ContentType: contentType,
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     maps.Clone(metadata), BlobType: blobTypeBlock,
		CommittedBlocks: blockInfos, committedBlockData: committedData,
	}

	if props != nil {
		obj.ContentEncoding = props.ContentEncoding
		obj.ContentLanguage = props.ContentLanguage
		obj.ContentDisposition = props.ContentDisposition
		obj.CacheControl = props.CacheControl
	}

	obj.ETag = fmt.Sprintf("%x", sha256.Sum256(data))

	if m.opts.StorageEngine != nil {
		if err := storageengine.Put(ctx, m.opts.StorageEngine, config.StorageObject{
			Bucket: container, Key: blob, Data: data, ContentType: contentType, Metadata: obj.Metadata,
		}); err != nil {
			return nil, err
		}
	} else {
		obj.Data = data
	}

	m.carryOverLease(ctr, blob, obj)
	m.recordVersion(ctr, obj)
	ctr.objects.Set(blob, obj)
	ctr.staging.Delete(blob)

	m.emitMetric(container, map[string]float64{"Transactions": 1, "Ingress": float64(len(data))})
	m.emitBlobCreatedAPI(ctx, obj, container, blobEventAPIPutBlockList)
	m.dispatchFunctionTrigger(ctx, obj, container)

	info := objectInfo(obj)

	return &info, nil
}

// assembleBlocks concatenates the requested blocks in order, resolving each
// against its source list, and returns the assembled bytes, the per-block
// id/size list for Get Block List, and the retained per-block bytes so a later
// commit can re-reference these now-committed blocks.
func assembleBlocks(
	ctr *containerMeta, blob string, entries []driver.BlockListEntry, existing *blobObject,
) (data []byte, blocks []driver.BlockInfo, committedData map[string][]byte, err error) {
	staged := snapshotStagedBlocks(ctr, blob)

	var committed map[string][]byte
	if existing != nil {
		committed = existing.committedBlockData
	}

	committedData = make(map[string][]byte, len(entries))
	blocks = make([]driver.BlockInfo, 0, len(entries))

	for _, e := range entries {
		block, ok := resolveBlock(e, staged, committed)
		if !ok {
			return nil, nil, nil, cerrors.Newf(cerrors.InvalidArgument,
				"block %q (%s) not found for blob %q", e.ID, e.List, blob)
		}

		data = append(data, block...)
		blocks = append(blocks, driver.BlockInfo{Name: e.ID, Size: int64(len(block))})
		committedData[e.ID] = block
	}

	return data, blocks, committedData, nil
}

// resolveBlock returns a block's bytes from the source list named by the entry:
// Committed reads the blob's existing committed blocks, Uncommitted reads the
// freshly staged blocks, and Latest (the default) prefers a staged block, then
// falls back to a committed one.
func resolveBlock(e driver.BlockListEntry, staged, committed map[string][]byte) ([]byte, bool) {
	switch e.List {
	case driver.BlockListCommitted:
		block, ok := committed[e.ID]
		return block, ok
	case driver.BlockListUncommitted:
		block, ok := staged[e.ID]
		return block, ok
	default: // Latest: staged first, else committed.
		if block, ok := staged[e.ID]; ok {
			return block, true
		}

		block, ok := committed[e.ID]

		return block, ok
	}
}

// snapshotStagedBlocks copies a blob's staged blocks under the staging lock so
// block resolution runs without holding it.
func snapshotStagedBlocks(ctr *containerMeta, blob string) map[string][]byte {
	stg, ok := ctr.staging.Get(blob)
	if !ok {
		return nil
	}

	stg.mu.Lock()
	defer stg.mu.Unlock()

	staged := make(map[string][]byte, len(stg.blocks))

	for id, block := range stg.blocks {
		cp := make([]byte, len(block))
		copy(cp, block)
		staged[id] = cp
	}

	return staged
}

// SetBlobMetadata replaces only a blob's metadata, preserving its content.
func (m *Mock) SetBlobMetadata(_ context.Context, container, blob string, metadata map[string]string) (*driver.ObjectInfo, error) {
	ctr, obj, err := m.getContainerBlob(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	obj.Metadata = maps.Clone(metadata)
	obj.LastModified = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
	obj.ETag = computeBlobETag(obj)

	m.recordVersion(ctr, obj)

	info := objectInfo(obj)

	return &info, nil
}

// SetBlobProperties replaces only a blob's system properties. Per Azure's
// "Versioning on write operations" list this is an in-place update that does
// NOT mint a new version (only Put Blob / Put Block List / Copy Blob / Set Blob
// Metadata do).
func (m *Mock) SetBlobProperties(_ context.Context, container, blob string, props *driver.BlobProperties) (*driver.ObjectInfo, error) {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	obj.ContentType = props.ContentType
	obj.ContentEncoding = props.ContentEncoding
	obj.ContentLanguage = props.ContentLanguage
	obj.ContentDisposition = props.ContentDisposition
	obj.CacheControl = props.CacheControl
	obj.LastModified = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
	obj.ETag = computeBlobETag(obj)

	info := objectInfo(obj)

	return &info, nil
}

// SetBlobTier sets a blob's access tier, preserving its content and ETag.
// Valid tiers for a block blob are Hot/Cool/Cold/Archive; anything else is
// rejected. Moving a blob out of Archive returns 202 (rehydration pending)
// rather than 200, matching real Azure's status-code table.
// https://learn.microsoft.com/en-us/rest/api/storageservices/set-blob-tier
func (m *Mock) SetBlobTier(_ context.Context, container, blob, tier string) (int, error) {
	if !validAccessTier(tier) {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"invalid x-ms-access-tier %q: must be one of Hot, Cool, Cold, Archive", tier)
	}

	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return 0, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	status := http.StatusOK
	if obj.AccessTier == accessTierArchive && tier != accessTierArchive {
		status = http.StatusAccepted
	}

	obj.AccessTier = tier

	return status, nil
}

// validAccessTier reports whether tier is one of the four block-blob access
// tiers cloudemu supports (Hot/Cool/Cold/Archive). Real Azure also accepts
// premium page-blob tiers (P4..P60) and the Smart preview tier; cloudemu's
// in-memory blobs are block blobs only, so those are rejected here.
func validAccessTier(tier string) bool {
	switch tier {
	case accessTierHot, accessTierCool, accessTierCold, accessTierArchive:
		return true
	default:
		return false
	}
}

// CreateBlobSnapshot captures an immutable snapshot of a blob. Snapshots are
// stored on the container so they outlive a base-blob overwrite.
func (m *Mock) CreateBlobSnapshot(_ context.Context, container, blob string) (string, *driver.ObjectInfo, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return "", nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	obj, ok := ctr.objects.Get(blob)
	if !ok {
		return "", nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", blob, container)
	}

	ctr.mu.Lock()
	ctr.snapshotSeq++
	seq := ctr.snapshotSeq
	ctr.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	snapshotID := now.Format(snapshotFormat) + fmt.Sprintf("%07dZ", seq)

	obj.mu.Lock()
	snap := &blobObject{
		Key: obj.Key, Data: append([]byte(nil), obj.Data...), Size: obj.Size,
		ContentType: obj.ContentType, ETag: obj.ETag, LastModified: obj.LastModified,
		Metadata: maps.Clone(obj.Metadata), BlobType: obj.BlobType, AccessTier: obj.AccessTier,
		ContentEncoding: obj.ContentEncoding, ContentLanguage: obj.ContentLanguage,
		ContentDisposition: obj.ContentDisposition, CacheControl: obj.CacheControl,
	}
	info := objectInfo(obj)
	obj.mu.Unlock()

	ctr.snapshots.Set(snapshotKey(blob, snapshotID), snap)

	return snapshotID, &info, nil
}

// GetBlobSnapshot reads a previously captured snapshot.
func (m *Mock) GetBlobSnapshot(_ context.Context, container, blob, snapshot string) (*driver.Object, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	snap, ok := ctr.snapshots.Get(snapshotKey(blob, snapshot))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "snapshot %q not found for blob %q", snapshot, blob)
	}

	return &driver.Object{Info: objectInfo(snap), Data: append([]byte(nil), snap.Data...)}, nil
}

// snapshotKey namespaces a snapshot by its blob so distinct blobs don't collide.
func snapshotKey(blob, snapshot string) string {
	return blob + "\x00" + snapshot
}

// CreateAppendBlob creates an empty append blob.
func (m *Mock) CreateAppendBlob(
	_ context.Context, container, blob, contentType string, metadata map[string]string,
) (*driver.ObjectInfo, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	// Immutable storage (WORM): re-creating an append blob over a protected key
	// would replace its content with empty — block it. A fresh key passes.
	if err := m.enforceImmutable(ctr, blob); err != nil {
		return nil, err
	}

	if contentType == "" {
		contentType = octetStream
	}

	obj := &blobObject{
		Key: blob, Data: []byte{}, Size: 0, ContentType: contentType,
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     maps.Clone(metadata), BlobType: blobTypeAppend,
	}
	obj.ETag = computeBlobETag(obj)

	m.recordVersion(ctr, obj)
	ctr.objects.Set(blob, obj)

	info := objectInfo(obj)

	return &info, nil
}

// AppendBlock appends a block to the end of an append blob.
func (m *Mock) AppendBlock(
	_ context.Context, container, blob string, data []byte,
) (offset int64, committedBlocks int, info *driver.ObjectInfo, err error) {
	ctr, obj, err := m.getContainerBlob(container, blob)
	if err != nil {
		return 0, 0, nil, err
	}

	if obj.BlobType != blobTypeAppend {
		return 0, 0, nil, cerrors.Newf(cerrors.FailedPrecondition, "blob %q is not an append blob", blob)
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	// Immutable storage (WORM): appending to a protected append blob is blocked.
	if berr := immutabilityBlock(obj, m.opts.Clock.Now().UTC()); berr != nil {
		return 0, 0, nil, berr
	}

	offset = obj.Size
	obj.Data = append(obj.Data, data...)
	obj.Size = int64(len(obj.Data))
	obj.appendBlocks++
	obj.LastModified = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
	obj.ETag = computeBlobETag(obj)

	m.recordVersion(ctr, obj)

	m.emitMetric(container, map[string]float64{"Transactions": 1, "Ingress": float64(len(data))})

	out := objectInfo(obj)

	return offset, obj.appendBlocks, &out, nil
}

// SetContainerMetadata replaces a container's metadata.
func (m *Mock) SetContainerMetadata(_ context.Context, container string, metadata map[string]string) error {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	ctr.metadata = maps.Clone(metadata)

	return nil
}

// ContainerMetadata returns a container's metadata.
func (m *Mock) ContainerMetadata(_ context.Context, container string) (map[string]string, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	return maps.Clone(ctr.metadata), nil
}

// getBlobObject fetches a blob's in-memory record, erroring if the container or
// blob is absent.
func (m *Mock) getBlobObject(container, blob string) (*blobObject, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	obj, ok := ctr.objects.Get(blob)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", blob, container)
	}

	return obj, nil
}

// computeBlobETag derives an ETag from a blob's content and mutable system
// state, so a metadata/property/append update yields a changed ETag (as real
// Azure does) while a pure content write stays sha256(content)-derived.
func computeBlobETag(obj *blobObject) string {
	h := sha256.New()
	h.Write(obj.Data)
	fmt.Fprintf(h, "\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		obj.BlobType, obj.ContentType, obj.ContentEncoding,
		obj.ContentLanguage, obj.ContentDisposition, obj.CacheControl)

	keys := make([]string, 0, len(obj.Metadata))
	for k := range obj.Metadata {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		fmt.Fprintf(h, "\x00%s=%s", k, obj.Metadata[k])
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}

// GetBlockList returns the blob's committed blocks (from the most recent Put
// Block List commit) and its currently staged, uncommitted blocks.
func (m *Mock) GetBlockList(_ context.Context, container, blob string) (committed, uncommitted []driver.BlockInfo, err error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	obj, objOk := ctr.objects.Get(blob)
	stg, stgOk := ctr.staging.Get(blob)

	if !objOk && !stgOk {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", blob, container)
	}

	if objOk && obj.BlobType == blobTypeBlock {
		committed = append(committed, obj.CommittedBlocks...)
	}

	if stgOk {
		stg.mu.Lock()
		for id, data := range stg.blocks {
			uncommitted = append(uncommitted, driver.BlockInfo{Name: id, Size: int64(len(data))})
		}
		stg.mu.Unlock()

		// Real Azure returns the uncommitted list in alphabetical order.
		sort.Slice(uncommitted, func(i, j int) bool { return uncommitted[i].Name < uncommitted[j].Name })
	}

	return committed, uncommitted, nil
}

// effectiveLeaseState computes a blob's current lease state, lazily applying
// the leased->expired and breaking->broken transitions that real Azure ties
// to elapsed time rather than to an explicit call. Pure: callers persist any
// resulting transition themselves when they need to (lease-management calls
// do; read-only checks don't need to).
func effectiveLeaseState(obj *blobObject, now time.Time) string {
	switch obj.leaseState {
	case leaseStateLeased:
		if !obj.leaseExpiresAt.IsZero() && !now.Before(obj.leaseExpiresAt) {
			return leaseStateExpired
		}

		return leaseStateLeased
	case leaseStateBreaking:
		if !obj.leaseBreakAt.IsZero() && !now.Before(obj.leaseBreakAt) {
			return leaseStateBroken
		}

		return leaseStateBreaking
	case leaseStateBroken:
		return leaseStateBroken
	default:
		return leaseStateAvailable
	}
}

// carryOverLease copies an existing blob's lease bookkeeping onto the
// replacement object built by an overwrite (Put Blob / Commit Block List /
// Copy Blob), so an authorized write does not silently clear an active lease.
// Real Azure keeps a fixed or infinite lease active across writes made with
// the correct lease id; without this the lease evaporates after the first
// write and any no-lease writer can then overwrite the blob.
func (m *Mock) carryOverLease(ctr *containerMeta, key string, next *blobObject) {
	prev, ok := ctr.objects.Get(key)
	if !ok {
		return
	}

	prev.mu.Lock()
	defer prev.mu.Unlock()

	next.leaseState = prev.leaseState
	next.leaseID = prev.leaseID
	next.leaseDurationSec = prev.leaseDurationSec
	next.leaseExpiresAt = prev.leaseExpiresAt
	next.leaseBreakAt = prev.leaseBreakAt
	next.leaseModTimeAtAcquire = prev.leaseModTimeAtAcquire

	// A write authorized under an active lease advances the blob's
	// last-modified; record it as the lease's reference time so a later renew
	// of that same lease (after it expires) still treats the blob as
	// unmodified by anyone else.
	if s := effectiveLeaseState(prev, m.opts.Clock.Now().UTC()); s == leaseStateLeased || s == leaseStateBreaking {
		next.leaseModTimeAtAcquire = next.LastModified
	}
}

// resetLease clears a blob's lease state entirely (Available, no lease id).
func resetLease(obj *blobObject) {
	obj.leaseState = leaseStateAvailable
	obj.leaseID = ""
	obj.leaseDurationSec = 0
	obj.leaseExpiresAt = time.Time{}
	obj.leaseBreakAt = time.Time{}
	obj.leaseModTimeAtAcquire = ""
}

// grantLease puts a blob into the Leased state under leaseID for
// durationSeconds, starting from now.
func grantLease(obj *blobObject, now time.Time, leaseID string, durationSeconds int32) {
	obj.leaseState = leaseStateLeased
	obj.leaseID = leaseID
	obj.leaseDurationSec = durationSeconds

	if durationSeconds == leaseDurationInfinite {
		obj.leaseExpiresAt = time.Time{}
	} else {
		obj.leaseExpiresAt = now.Add(time.Duration(durationSeconds) * time.Second)
	}

	obj.leaseBreakAt = time.Time{}
	obj.leaseModTimeAtAcquire = obj.LastModified
}

// leaseManagementError builds the 409 Conflict cloudemu returns for a
// lease-management call (acquire/renew/change/release/break) that cannot
// proceed given the blob's current lease state.
func leaseManagementError(code, msg string) error {
	return &driver.BlobOpError{Status: http.StatusConflict, Code: code, Message: msg}
}

// AcquireLease acquires a lease on a blob. See
// https://learn.microsoft.com/en-us/rest/api/storageservices/lease-blob for
// the full acquire-outcome table this follows.
func (m *Mock) AcquireLease(
	_ context.Context, container, blob string, durationSeconds int32, proposedLeaseID string,
) (*driver.BlobLeaseResult, error) {
	if durationSeconds != leaseDurationInfinite &&
		(durationSeconds < leaseDurationMinSec || durationSeconds > leaseDurationMaxSec) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"x-ms-lease-duration must be -1 or between %d and %d seconds", leaseDurationMinSec, leaseDurationMaxSec)
	}

	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	state := effectiveLeaseState(obj, now)

	switch state {
	case leaseStateBreaking:
		return nil, leaseManagementError("LeaseAlreadyPresent", "there is already a lease present")
	case leaseStateLeased:
		if proposedLeaseID == "" || proposedLeaseID != obj.leaseID {
			return nil, leaseManagementError("LeaseAlreadyPresent", "there is already a lease present")
		}
	}

	leaseID := proposedLeaseID
	if leaseID == "" {
		leaseID = idgen.SyntheticGUID(idgen.GenerateID("lease-"))
	}

	grantLease(obj, now, leaseID, durationSeconds)

	return &driver.BlobLeaseResult{LeaseID: leaseID, ETag: obj.ETag, LastModified: obj.LastModified}, nil
}

// RenewLease renews the blob's current lease, resetting its duration clock.
func (m *Mock) RenewLease(_ context.Context, container, blob, leaseID string) (*driver.BlobLeaseResult, error) {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	state := effectiveLeaseState(obj, now)

	if state == leaseStateAvailable || state == leaseStateBroken || state == leaseStateBreaking {
		return nil, leaseManagementError("LeaseNotPresentWithLeaseOperation", "there is currently no lease on the blob")
	}

	if leaseID == "" || leaseID != obj.leaseID {
		return nil, leaseManagementError("LeaseIdMismatchWithLeaseOperation",
			"the lease id specified did not match the lease id for the blob")
	}

	// A lease that has merely expired (not released) can still be renewed with
	// its old id, but only if the blob hasn't changed since — otherwise someone
	// else has taken and released the blob in between.
	if state == leaseStateExpired && obj.leaseModTimeAtAcquire != obj.LastModified {
		return nil, leaseManagementError("LeaseIdMismatchWithLeaseOperation",
			"the blob has been modified since the lease was last active")
	}

	grantLease(obj, now, obj.leaseID, obj.leaseDurationSec)

	return &driver.BlobLeaseResult{LeaseID: obj.leaseID, ETag: obj.ETag, LastModified: obj.LastModified}, nil
}

// ChangeLease changes the blob's lease id from leaseID to proposedLeaseID.
func (m *Mock) ChangeLease(
	_ context.Context, container, blob, leaseID, proposedLeaseID string,
) (*driver.BlobLeaseResult, error) {
	if proposedLeaseID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "x-ms-proposed-lease-id is required for change")
	}

	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	state := effectiveLeaseState(obj, now)

	if state != leaseStateLeased || leaseID == "" || leaseID != obj.leaseID {
		return nil, leaseManagementError("LeaseIdMismatchWithLeaseOperation",
			"the lease id specified did not match the lease id for the blob")
	}

	obj.leaseID = proposedLeaseID

	return &driver.BlobLeaseResult{LeaseID: proposedLeaseID, ETag: obj.ETag, LastModified: obj.LastModified}, nil
}

// ReleaseLease releases the blob's current lease, making it immediately
// available for a new Acquire.
func (m *Mock) ReleaseLease(_ context.Context, container, blob, leaseID string) (*driver.BlobLeaseResult, error) {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	state := effectiveLeaseState(obj, now)

	if state == leaseStateAvailable || leaseID == "" || leaseID != obj.leaseID {
		return nil, leaseManagementError("LeaseIdMismatchWithLeaseOperation",
			"the lease id specified did not match the lease id for the blob")
	}

	etag, lastModified := obj.ETag, obj.LastModified
	resetLease(obj)

	return &driver.BlobLeaseResult{ETag: etag, LastModified: lastModified}, nil
}

// BreakLease breaks the blob's current lease. When breakPeriod is nil, a
// fixed-duration lease breaks after its remaining time and an infinite lease
// breaks immediately; when set, it caps (but for an already-breaking lease,
// can only shorten) the wait.
func (m *Mock) BreakLease(_ context.Context, container, blob string, breakPeriod *int32) (int32, error) {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return 0, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	now := m.opts.Clock.Now().UTC()
	state := effectiveLeaseState(obj, now)

	switch state {
	case leaseStateAvailable:
		return 0, leaseManagementError("LeaseNotPresentWithLeaseOperation", "there is currently no lease on the blob")
	case leaseStateBroken:
		return 0, nil // idempotent: breaking an already-broken lease succeeds as a no-op.
	case leaseStateExpired:
		obj.leaseState = leaseStateBroken
		obj.leaseBreakAt = time.Time{}

		return 0, nil
	}

	breakAfter := computeBreakAfter(obj, now, breakPeriod)
	if state == leaseStateBreaking {
		if remaining := obj.leaseBreakAt.Sub(now); remaining > 0 && breakAfter > remaining {
			breakAfter = remaining
		}
	}

	if breakAfter <= 0 {
		obj.leaseState = leaseStateBroken
		obj.leaseBreakAt = time.Time{}

		return 0, nil
	}

	obj.leaseState = leaseStateBreaking
	obj.leaseBreakAt = now.Add(breakAfter)

	return int32(breakAfter.Seconds()), nil
}

// computeBreakAfter resolves how long from now a Leased/Breaking lease should
// take to break, applying the break-period cap described in the Lease Blob
// remarks: a break period only shortens a fixed-duration lease's remaining
// time, never lengthens it, and an infinite lease with no period breaks
// immediately.
func computeBreakAfter(obj *blobObject, now time.Time, breakPeriod *int32) time.Duration {
	var remaining time.Duration
	if obj.leaseDurationSec != leaseDurationInfinite && !obj.leaseExpiresAt.IsZero() {
		if remaining = obj.leaseExpiresAt.Sub(now); remaining < 0 {
			remaining = 0
		}
	}

	if breakPeriod == nil {
		return remaining // infinite lease: 0 (immediate); fixed lease: its remaining time.
	}

	candidate := time.Duration(*breakPeriod) * time.Second

	if obj.leaseDurationSec != leaseDurationInfinite && candidate > remaining {
		return remaining
	}

	return candidate
}

// writeLeaseError builds the write-gating error a blob operation that
// requires a lease id returns. Real Azure always answers 412 here (see the
// Delete Blob / Put Blob docs), unlike the 409s lease-management calls use.
// https://learn.microsoft.com/en-us/rest/api/storageservices/delete-blob
func writeLeaseError(code, msg string) error {
	return &driver.BlobOpError{Status: http.StatusPreconditionFailed, Code: code, Message: msg}
}

// CheckBlobLease validates a write/delete request's x-ms-lease-id header
// against any active lease on the blob.
func (m *Mock) CheckBlobLease(_ context.Context, container, blob, headerLeaseID string) error {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil // let the caller's own existence check surface NotFound.
	}

	obj, ok := ctr.objects.Get(blob)
	if !ok {
		return nil
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	state := effectiveLeaseState(obj, m.opts.Clock.Now().UTC())

	switch state {
	case leaseStateLeased, leaseStateBreaking:
		if headerLeaseID == "" {
			return writeLeaseError("LeaseIdMissing", "there is currently a lease on the blob and no lease id was specified")
		}

		if headerLeaseID != obj.leaseID {
			return writeLeaseError("LeaseIdMismatchWithBlobOperation",
				"the lease id specified did not match the lease id for the blob")
		}

		return nil
	default: // Available, Broken, Expired: any lease id supplied here is stale/invalid.
		if headerLeaseID != "" {
			return writeLeaseError("LeaseNotPresentWithBlobOperation", "there is currently no lease on the blob")
		}

		return nil
	}
}

// DeleteBlobSnapshots applies the Azure delete-snapshots directive ahead of a
// Delete Blob. See
// https://learn.microsoft.com/en-us/rest/api/storageservices/delete-blob.
func (m *Mock) DeleteBlobSnapshots(_ context.Context, container, blob, mode string) (bool, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return false, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	prefix := snapshotKey(blob, "")

	var snapKeys []string

	for _, k := range ctr.snapshots.Keys() {
		if strings.HasPrefix(k, prefix) {
			snapKeys = append(snapKeys, k)
		}
	}

	switch mode {
	case "":
		if len(snapKeys) > 0 {
			return false, &driver.BlobOpError{
				Status: http.StatusConflict, Code: "SnapshotsPresent",
				Message: "the specified blob has snapshots and no x-ms-delete-snapshots header was specified",
			}
		}

		return true, nil
	case "only":
		for _, k := range snapKeys {
			ctr.snapshots.Delete(k)
		}

		return false, nil
	case "include":
		for _, k := range snapKeys {
			ctr.snapshots.Delete(k)
		}

		return true, nil
	default:
		return false, cerrors.Newf(cerrors.InvalidArgument, "invalid x-ms-delete-snapshots value %q", mode)
	}
}

// SetContainerAccessPolicy sets a container's public access level and stored
// access policies.
func (m *Mock) SetContainerAccessPolicy(
	_ context.Context, container, publicAccess string, policies []driver.SignedIdentifier,
) error {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	ctr.mu.Lock()
	defer ctr.mu.Unlock()

	ctr.publicAccess = publicAccess

	ctr.accessPolicies = append([]driver.SignedIdentifier(nil), policies...)

	return nil
}

// ContainerAccessPolicy returns a container's public access level and stored
// access policies.
func (m *Mock) ContainerAccessPolicy(_ context.Context, container string) (string, []driver.SignedIdentifier, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return "", nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	ctr.mu.Lock()
	defer ctr.mu.Unlock()

	return ctr.publicAccess, append([]driver.SignedIdentifier(nil), ctr.accessPolicies...), nil
}
