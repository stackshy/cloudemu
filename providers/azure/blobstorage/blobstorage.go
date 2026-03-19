// Package blobstorage provides an in-memory mock implementation of Azure Blob Storage.
package blobstorage

import (
	"context"
	"crypto/md5"
	"fmt"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/pagination"
	"github.com/stackshy/cloudemu/storage/driver"
)

// Compile-time check that Mock implements driver.Bucket.
var _ driver.Bucket = (*Mock)(nil)

type blobObject struct {
	Key          string
	Data         []byte
	ContentType  string
	ETag         string
	LastModified string
	Metadata     map[string]string
}

type containerMeta struct {
	Name      string
	Region    string
	CreatedAt string
	objects   *memstore.Store[*blobObject]
}

// Mock is an in-memory mock implementation of Azure Blob Storage.
type Mock struct {
	containers *memstore.Store[*containerMeta]
	opts       *config.Options
}

// New creates a new Azure Blob Storage mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		containers: memstore.New[*containerMeta](),
		opts:       opts,
	}
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
		Name:      name,
		Region:    m.opts.Region,
		CreatedAt: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		objects:   memstore.New[*blobObject](),
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

// PutObject stores a blob in a container.
func (m *Mock) PutObject(_ context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	ctr.objects.Set(key, &blobObject{
		Key:          key,
		Data:         dataCopy,
		ContentType:  contentType,
		ETag:         fmt.Sprintf("%x", md5.Sum(data)),
		LastModified: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Metadata:     metadata,
	})
	return nil
}

// GetObject retrieves a blob from a container.
func (m *Mock) GetObject(_ context.Context, bucket, key string) (*driver.Object, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}
	obj, ok := ctr.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}
	dataCopy := make([]byte, len(obj.Data))
	copy(dataCopy, obj.Data)
	return &driver.Object{
		Info: driver.ObjectInfo{
			Key: obj.Key, Size: int64(len(obj.Data)), ContentType: obj.ContentType,
			ETag: obj.ETag, LastModified: obj.LastModified, Metadata: obj.Metadata,
		},
		Data: dataCopy,
	}, nil
}

// DeleteObject deletes a blob from a container.
func (m *Mock) DeleteObject(_ context.Context, bucket, key string) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}
	if !ctr.objects.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", key, bucket)
	}
	ctr.objects.Delete(key)
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
	return &driver.ObjectInfo{
		Key: obj.Key, Size: int64(len(obj.Data)), ContentType: obj.ContentType,
		ETag: obj.ETag, LastModified: obj.LastModified, Metadata: obj.Metadata,
	}, nil
}

// ListObjects lists blobs in a container with optional prefix/delimiter filtering.
func (m *Mock) ListObjects(_ context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}
	allKeys := ctr.objects.Keys()
	sort.Strings(allKeys)

	var matchedObjects []driver.ObjectInfo
	commonPrefixSet := make(map[string]struct{})

	for _, k := range allKeys {
		if opts.Prefix != "" && !strings.HasPrefix(k, opts.Prefix) {
			continue
		}
		if opts.Delimiter != "" {
			rest := k[len(opts.Prefix):]
			idx := strings.Index(rest, opts.Delimiter)
			if idx >= 0 {
				commonPrefixSet[opts.Prefix+rest[:idx+len(opts.Delimiter)]] = struct{}{}
				continue
			}
		}
		obj, objOk := ctr.objects.Get(k)
		if !objOk {
			continue
		}
		matchedObjects = append(matchedObjects, driver.ObjectInfo{
			Key: obj.Key, Size: int64(len(obj.Data)), ContentType: obj.ContentType,
			ETag: obj.ETag, LastModified: obj.LastModified, Metadata: obj.Metadata,
		})
	}

	commonPrefixes := make([]string, 0, len(commonPrefixSet))
	for p := range commonPrefixSet {
		commonPrefixes = append(commonPrefixes, p)
	}
	sort.Strings(commonPrefixes)

	maxKeys := opts.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 1000
	}

	page, _ := pagination.Paginate(matchedObjects, opts.PageToken, maxKeys)
	return &driver.ListResult{
		Objects:        page.Items,
		CommonPrefixes: commonPrefixes,
		NextPageToken:  page.NextPageToken,
		IsTruncated:    page.HasMore,
	}, nil
}

// CopyObject copies a blob from one location to another.
func (m *Mock) CopyObject(_ context.Context, dstBucket, dstKey string, src driver.CopySource) error {
	srcCtr, ok := m.containers.Get(src.Bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "source container %q not found", src.Bucket)
	}
	srcObj, ok := srcCtr.objects.Get(src.Key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "source blob %q not found", src.Key)
	}
	dstCtr, ok := m.containers.Get(dstBucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "destination container %q not found", dstBucket)
	}
	dataCopy := make([]byte, len(srcObj.Data))
	copy(dataCopy, srcObj.Data)
	meta := make(map[string]string, len(srcObj.Metadata))
	for k, v := range srcObj.Metadata {
		meta[k] = v
	}
	dstCtr.objects.Set(dstKey, &blobObject{
		Key: dstKey, Data: dataCopy, ContentType: srcObj.ContentType,
		ETag: srcObj.ETag, LastModified: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Metadata: meta,
	})
	return nil
}
