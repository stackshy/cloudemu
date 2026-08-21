package objectstorage

import (
	"context"
	"maps"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// defaultListLimit is the page size OCI applies when the caller names none.
const defaultListLimit = 1000

// ObjectDetails is an object as OCI reports it in list and rename responses.
type ObjectDetails struct {
	Name         string
	Size         int64
	MD5          string
	ETag         string
	ContentType  string
	TimeCreated  string
	TimeModified string
	StorageTier  string
	Metadata     map[string]string
	VersionID    string
	DeleteMarker bool
}

// PutOptions carries the OCI-only fields that ride on a PutObject: the
// storage tier the object lands in and its opc-meta- user metadata.
type PutOptions struct {
	ContentType string
	StorageTier string
	Metadata    map[string]string
}

func (m *Mock) PutObject(
	ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string,
) error {
	_, err := m.PutObjectWith(ctx, bucket, key, data, PutOptions{
		ContentType: contentType,
		Metadata:    metadata,
	})

	return err
}

// PutObjectWith stores an object with OCI's per-object settings and returns
// what OCI stamps on the response.
func (m *Mock) PutObjectWith(
	_ context.Context, bucket, key string, data []byte, opts PutOptions,
) (*ObjectDetails, error) {
	if key == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "object name cannot be empty")
	}

	if opts.StorageTier != "" && !validStorageTier(opts.StorageTier) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported storageTier %q", opts.StorageTier)
	}

	m.mu.Lock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	if err := retentionBlocksLocked(bkt, key, m.opts.Clock.Now()); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	now := m.now()
	created := now

	if existing, ok := bkt.objects.Get(key); ok {
		created = existing.TimeCreated
	}

	obj := &objectData{
		Name:         key,
		Data:         cloneBytes(data),
		ContentType:  orDefault(opts.ContentType, "application/octet-stream"),
		ContentMD5:   contentMD5(data),
		ETag:         objectETag(data),
		TimeCreated:  created,
		TimeModified: now,
		Metadata:     cloneMeta(opts.Metadata),
		StorageTier:  orDefault(opts.StorageTier, bkt.StorageTier),
	}
	storeObjectLocked(bkt, obj)
	details := detailsOf(obj)
	m.mu.Unlock()

	m.emitMetric("PutRequests", 1, "Count", bucket)
	m.emitMetric("StoredBytes", float64(len(data)), "Bytes", bucket)

	return &details, nil
}

func (m *Mock) GetObject(_ context.Context, bucket, key string) (*driver.Object, error) {
	m.mu.RLock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}

	obj, err := objectLocked(bkt, key)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}

	out := &driver.Object{Info: infoOf(obj), Data: cloneBytes(obj.Data)}
	m.mu.RUnlock()

	m.emitMetric("GetRequests", 1, "Count", bucket)

	return out, nil
}

func (m *Mock) HeadObject(_ context.Context, bucket, key string) (*driver.ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	obj, err := objectLocked(bkt, key)
	if err != nil {
		return nil, err
	}

	info := infoOf(obj)

	return &info, nil
}

// ObjectDetailsOf returns the OCI projection of an object's current version,
// carrying the fields the portable ObjectInfo has no room for.
func (m *Mock) ObjectDetailsOf(_ context.Context, bucket, key string) (*ObjectDetails, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	obj, err := objectLocked(bkt, key)
	if err != nil {
		return nil, err
	}

	d := detailsOf(obj)

	return &d, nil
}

func (m *Mock) DeleteObject(_ context.Context, bucket, key string) error {
	m.mu.Lock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	if err := retentionBlocksLocked(bkt, key, m.opts.Clock.Now()); err != nil {
		m.mu.Unlock()
		return err
	}

	_, _, existed := m.deleteCurrentLocked(bkt, key)
	m.mu.Unlock()

	if !existed {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	m.emitMetric("DeleteRequests", 1, "Count", bucket)

	return nil
}

// RenameObject moves an object to a new name within the same bucket, OCI's
// atomic rename action. newName must not already exist.
func (m *Mock) RenameObject(_ context.Context, bucket, sourceName, newName string) (*ObjectDetails, error) {
	if sourceName == "" || newName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "sourceName and newName are required")
	}

	if sourceName == newName {
		return nil, cerrors.New(cerrors.InvalidArgument, "newName must differ from sourceName")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	src, err := objectLocked(bkt, sourceName)
	if err != nil {
		return nil, err
	}

	if bkt.objects.Has(newName) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "object %q already exists in bucket %q", newName, bucket)
	}

	if err := retentionBlocksLocked(bkt, sourceName, m.opts.Clock.Now()); err != nil {
		return nil, err
	}

	moved := *src
	moved.Name = newName
	moved.TimeModified = m.now()
	moved.Data = cloneBytes(src.Data)
	moved.Metadata = cloneMeta(src.Metadata)

	storeObjectLocked(bkt, &moved)
	m.deleteCurrentLocked(bkt, sourceName)

	details := detailsOf(&moved)

	return &details, nil
}

// CopyObject copies an object between buckets in this namespace. OCI runs the
// copy asynchronously; the wire layer records the work request.
func (m *Mock) CopyObject(_ context.Context, dstBucket, dstKey string, src driver.CopySource) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	srcBkt, err := m.bucketLocked(src.Bucket)
	if err != nil {
		return cerrors.Newf(cerrors.NotFound, "source bucket %q not found", src.Bucket)
	}

	srcObj, err := objectLocked(srcBkt, src.Key)
	if err != nil {
		return cerrors.Newf(cerrors.NotFound, "source object %q not found in bucket %q", src.Key, src.Bucket)
	}

	dstBkt, err := m.bucketLocked(dstBucket)
	if err != nil {
		return cerrors.Newf(cerrors.NotFound, "destination bucket %q not found", dstBucket)
	}

	if err := retentionBlocksLocked(dstBkt, dstKey, m.opts.Clock.Now()); err != nil {
		return err
	}

	now := m.now()
	storeObjectLocked(dstBkt, &objectData{
		Name:         dstKey,
		Data:         cloneBytes(srcObj.Data),
		ContentType:  srcObj.ContentType,
		ContentMD5:   srcObj.ContentMD5,
		ETag:         srcObj.ETag,
		TimeCreated:  now,
		TimeModified: now,
		Metadata:     cloneMeta(srcObj.Metadata),
		StorageTier:  srcObj.StorageTier,
	})

	return nil
}

func (m *Mock) ListObjects(_ context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	m.mu.RLock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		m.mu.RUnlock()
		return nil, err
	}

	matched, prefixes := matchObjectsLocked(bkt, opts)
	m.mu.RUnlock()

	limit := opts.MaxKeys
	if limit <= 0 {
		limit = defaultListLimit
	}

	page, err := pagination.Paginate(matched, opts.PageToken, limit)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	m.emitMetric("ListRequests", 1, "Count", bucket)

	return &driver.ListResult{
		Objects:        page.Items,
		CommonPrefixes: prefixes,
		NextPageToken:  page.NextPageToken,
		IsTruncated:    page.HasMore,
	}, nil
}

// matchObjectsLocked applies prefix and delimiter to the bucket's current
// objects, returning the matches in name order and the rolled-up prefixes.
func matchObjectsLocked(bkt *bucketData, opts driver.ListOptions) (matched []driver.ObjectInfo, prefixes []string) {
	for _, obj := range walkObjectsLocked(bkt, opts, &prefixes) {
		matched = append(matched, infoOf(obj))
	}

	return matched, prefixes
}

// walkObjectsLocked returns the objects matching opts in name order, rolling
// the delimiter-collapsed names into prefixes. Callers hold mu.
func walkObjectsLocked(bkt *bucketData, opts driver.ListOptions, prefixes *[]string) []*objectData {
	names := bkt.objects.Keys()
	sort.Strings(names)

	var matched []*objectData

	prefixSet := make(map[string]struct{})

	for _, n := range names {
		if opts.Prefix != "" && !strings.HasPrefix(n, opts.Prefix) {
			continue
		}

		if opts.Delimiter != "" {
			rest := n[len(opts.Prefix):]
			if idx := strings.Index(rest, opts.Delimiter); idx >= 0 {
				prefixSet[opts.Prefix+rest[:idx+len(opts.Delimiter)]] = struct{}{}
				continue
			}
		}

		if obj, ok := bkt.objects.Get(n); ok {
			matched = append(matched, obj)
		}
	}

	out := make([]string, 0, len(prefixSet))
	for p := range prefixSet {
		out = append(out, p)
	}

	sort.Strings(out)

	*prefixes = out

	return matched
}

// ListObjectDetails is ListObjects in OCI's shape, carrying the storage tier
// and MD5 the portable ObjectInfo has no field for.
func (m *Mock) ListObjectDetails(
	_ context.Context, bucket string, opts driver.ListOptions,
) (items []ObjectDetails, prefixes []string, nextPageToken string, err error) {
	m.mu.RLock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		m.mu.RUnlock()
		return nil, nil, "", err
	}

	objects := walkObjectsLocked(bkt, opts, &prefixes)

	matched := make([]ObjectDetails, 0, len(objects))
	for _, obj := range objects {
		matched = append(matched, detailsOf(obj))
	}

	m.mu.RUnlock()

	limit := opts.MaxKeys
	if limit <= 0 {
		limit = defaultListLimit
	}

	page, err := pagination.Paginate(matched, opts.PageToken, limit)
	if err != nil {
		return nil, nil, "", cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	return page.Items, prefixes, page.NextPageToken, nil
}

func infoOf(obj *objectData) driver.ObjectInfo {
	return driver.ObjectInfo{
		Key:          obj.Name,
		Size:         int64(len(obj.Data)),
		ContentType:  obj.ContentType,
		ETag:         obj.ETag,
		LastModified: obj.TimeModified,
		Metadata:     cloneMeta(obj.Metadata),
		VersionID:    obj.VersionID,
	}
}

func detailsOf(obj *objectData) ObjectDetails {
	return ObjectDetails{
		Name:         obj.Name,
		Size:         int64(len(obj.Data)),
		MD5:          obj.ContentMD5,
		ETag:         obj.ETag,
		ContentType:  obj.ContentType,
		TimeCreated:  obj.TimeCreated,
		TimeModified: obj.TimeModified,
		StorageTier:  obj.StorageTier,
		Metadata:     cloneMeta(obj.Metadata),
		VersionID:    obj.VersionID,
	}
}

// UpdateObjectMetadata replaces an object's opc-meta- user metadata, OCI's
// updateObjectStorageTier sibling for metadata-only changes.
func (m *Mock) UpdateObjectMetadata(_ context.Context, bucket, key string, metadata map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	obj, err := objectLocked(bkt, key)
	if err != nil {
		return err
	}

	obj.Metadata = maps.Clone(metadata)
	obj.TimeModified = m.now()

	return nil
}

// UpdateObjectStorageTier moves an object between storage tiers.
func (m *Mock) UpdateObjectStorageTier(_ context.Context, bucket, key, tier string) error {
	if !validStorageTier(tier) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported storageTier %q", tier)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	obj, err := objectLocked(bkt, key)
	if err != nil {
		return err
	}

	obj.StorageTier = tier

	return nil
}
