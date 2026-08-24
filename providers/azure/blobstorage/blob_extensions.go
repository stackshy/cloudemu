package blobstorage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"sort"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
)

const (
	blobTypeBlock  = "BlockBlob"
	blobTypeAppend = "AppendBlob"
	snapshotFormat = "2006-01-02T15:04:05."
	octetStream    = "application/octet-stream"
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

// CommitBlockList assembles a block blob from previously-staged blocks.
func (m *Mock) CommitBlockList(
	ctx context.Context, container, blob string, blockIDs []string, contentType string, metadata map[string]string,
) (*driver.ObjectInfo, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	data, err := assembleStagedBlocks(ctr, blob, blockIDs)
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

	ctr.objects.Set(blob, obj)
	ctr.staging.Delete(blob)

	m.emitMetric(container, map[string]float64{"Transactions": 1, "Ingress": float64(len(data))})

	info := objectInfo(obj)

	return &info, nil
}

// assembleStagedBlocks concatenates the named staged blocks in order.
func assembleStagedBlocks(ctr *containerMeta, blob string, blockIDs []string) ([]byte, error) {
	stg, ok := ctr.staging.Get(blob)
	if !ok {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "no staged blocks for blob %q", blob)
	}

	stg.mu.Lock()
	defer stg.mu.Unlock()

	var data []byte

	for _, id := range blockIDs {
		block, ok := stg.blocks[id]
		if !ok {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "block %q not staged for blob %q", id, blob)
		}

		data = append(data, block...)
	}

	return data, nil
}

// SetBlobMetadata replaces only a blob's metadata, preserving its content.
func (m *Mock) SetBlobMetadata(_ context.Context, container, blob string, metadata map[string]string) (*driver.ObjectInfo, error) {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return nil, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	obj.Metadata = maps.Clone(metadata)
	obj.LastModified = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
	obj.ETag = computeBlobETag(obj)

	info := objectInfo(obj)

	return &info, nil
}

// SetBlobProperties replaces only a blob's system properties.
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
func (m *Mock) SetBlobTier(_ context.Context, container, blob, tier string) error {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return err
	}

	obj.mu.Lock()
	obj.AccessTier = tier
	obj.mu.Unlock()

	return nil
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

	if contentType == "" {
		contentType = octetStream
	}

	obj := &blobObject{
		Key: blob, Data: []byte{}, Size: 0, ContentType: contentType,
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     maps.Clone(metadata), BlobType: blobTypeAppend,
	}
	obj.ETag = computeBlobETag(obj)

	ctr.objects.Set(blob, obj)

	info := objectInfo(obj)

	return &info, nil
}

// AppendBlock appends a block to the end of an append blob.
func (m *Mock) AppendBlock(
	_ context.Context, container, blob string, data []byte,
) (offset int64, committedBlocks int, info *driver.ObjectInfo, err error) {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return 0, 0, nil, err
	}

	if obj.BlobType != blobTypeAppend {
		return 0, 0, nil, cerrors.Newf(cerrors.FailedPrecondition, "blob %q is not an append blob", blob)
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	offset = obj.Size
	obj.Data = append(obj.Data, data...)
	obj.Size = int64(len(obj.Data))
	obj.appendBlocks++
	obj.LastModified = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
	obj.ETag = computeBlobETag(obj)

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
