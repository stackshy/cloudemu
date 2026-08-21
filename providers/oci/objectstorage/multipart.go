package objectstorage

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Part numbers OCI accepts.
const (
	minPartNumber = 1
	maxPartNumber = 10000
)

// MultipartUploadSpec is a multipart upload to create, with the OCI-only
// fields the portable CreateMultipartUpload has no room for.
type MultipartUploadSpec struct {
	Object      string
	ContentType string
	StorageTier string
	Metadata    map[string]string
}

func (m *Mock) CreateMultipartUpload(
	ctx context.Context, bucket, key, contentType string,
) (*driver.MultipartUpload, error) {
	return m.CreateMultipartUploadWith(ctx, bucket, MultipartUploadSpec{
		Object: key, ContentType: contentType,
	})
}

// CreateMultipartUploadWith starts a multipart upload carrying OCI's storage
// tier and user metadata.
func (m *Mock) CreateMultipartUploadWith(
	_ context.Context, bucket string, spec MultipartUploadSpec,
) (*driver.MultipartUpload, error) {
	if spec.Object == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "object name cannot be empty")
	}

	if spec.StorageTier != "" && !validStorageTier(spec.StorageTier) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported storageTier %q", spec.StorageTier)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	uploadID := idgen.GenerateID("")
	now := m.now()

	bkt.multiparts.Set(uploadID, &multipartUpload{
		id:          uploadID,
		object:      spec.Object,
		contentType: orDefault(spec.ContentType, "application/octet-stream"),
		metadata:    cloneMeta(spec.Metadata),
		storageTier: orDefault(spec.StorageTier, bkt.StorageTier),
		parts:       make(map[int][]byte),
		timeCreated: now,
	})

	return &driver.MultipartUpload{
		UploadID: uploadID, Bucket: bucket, Key: spec.Object, CreatedAt: now,
	}, nil
}

func (m *Mock) UploadPart(
	_ context.Context, bucket, key, uploadID string, partNumber int, data []byte,
) (*driver.UploadPart, error) {
	if partNumber < minPartNumber || partNumber > maxPartNumber {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"uploadPartNum must be between %d and %d, got %d", minPartNumber, maxPartNumber, partNumber)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mp, err := m.uploadLocked(bucket, key, uploadID)
	if err != nil {
		return nil, err
	}

	mp.parts[partNumber] = cloneBytes(data)

	return &driver.UploadPart{
		PartNumber: partNumber, ETag: objectETag(data), Size: int64(len(data)),
	}, nil
}

// ListParts returns the parts buffered so far, ordered by part number.
func (m *Mock) ListParts(_ context.Context, bucket, key, uploadID string) ([]driver.UploadPart, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mp, err := m.uploadLocked(bucket, key, uploadID)
	if err != nil {
		return nil, err
	}

	nums := make([]int, 0, len(mp.parts))
	for n := range mp.parts {
		nums = append(nums, n)
	}

	sort.Ints(nums)

	out := make([]driver.UploadPart, 0, len(nums))

	for _, n := range nums {
		out = append(out, driver.UploadPart{
			PartNumber: n, ETag: objectETag(mp.parts[n]), Size: int64(len(mp.parts[n])),
		})
	}

	return out, nil
}

// CompleteMultipartUpload assembles the named parts, in ascending part-number
// order, into the upload's object.
func (m *Mock) CompleteMultipartUpload(
	_ context.Context, bucket, key, uploadID string, parts []driver.UploadPart,
) error {
	if len(parts) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "partsToCommit cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	mp, ok := bkt.multiparts.Get(uploadID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "upload %q not found in bucket %q", uploadID, bucket)
	}

	if key != "" && key != mp.object {
		return cerrors.Newf(cerrors.InvalidArgument,
			"upload %q is for object %q, not %q", uploadID, mp.object, key)
	}

	ordered := append([]driver.UploadPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PartNumber < ordered[j].PartNumber })

	var data []byte

	for _, p := range ordered {
		buf, exists := mp.parts[p.PartNumber]
		if !exists {
			return cerrors.Newf(cerrors.InvalidArgument, "part %d was never uploaded to %q", p.PartNumber, uploadID)
		}

		data = append(data, buf...)
	}

	if err := retentionBlocksLocked(bkt, mp.object, m.opts.Clock.Now()); err != nil {
		return err
	}

	now := m.now()
	storeObjectLocked(bkt, &objectData{
		Name:         mp.object,
		Data:         data,
		ContentType:  mp.contentType,
		ContentMD5:   contentMD5(data),
		ETag:         objectETag(data),
		TimeCreated:  now,
		TimeModified: now,
		Metadata:     cloneMeta(mp.metadata),
		StorageTier:  mp.storageTier,
	})

	bkt.multiparts.Delete(uploadID)

	return nil
}

func (m *Mock) AbortMultipartUpload(_ context.Context, bucket, key, uploadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	mp, ok := bkt.multiparts.Get(uploadID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "upload %q not found in bucket %q", uploadID, bucket)
	}

	if key != "" && key != mp.object {
		return cerrors.Newf(cerrors.InvalidArgument,
			"upload %q is for object %q, not %q", uploadID, mp.object, key)
	}

	bkt.multiparts.Delete(uploadID)

	return nil
}

func (m *Mock) ListMultipartUploads(_ context.Context, bucket string) ([]driver.MultipartUpload, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	ids := bkt.multiparts.Keys()
	sort.Strings(ids)

	out := make([]driver.MultipartUpload, 0, len(ids))

	for _, id := range ids {
		mp, ok := bkt.multiparts.Get(id)
		if !ok {
			continue
		}

		out = append(out, driver.MultipartUpload{
			UploadID: mp.id, Bucket: bucket, Key: mp.object, CreatedAt: mp.timeCreated,
		})
	}

	return out, nil
}

// uploadLocked resolves an upload, checking the object name when the caller
// supplied one. Callers hold mu.
func (m *Mock) uploadLocked(bucket, key, uploadID string) (*multipartUpload, error) {
	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	mp, ok := bkt.multiparts.Get(uploadID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "upload %q not found in bucket %q", uploadID, bucket)
	}

	if key != "" && key != mp.object {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"upload %q is for object %q, not %q", uploadID, mp.object, key)
	}

	return mp, nil
}
