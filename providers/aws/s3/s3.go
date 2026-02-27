package s3

import (
	"context"
	"crypto/md5"
	"fmt"
	"sort"
	"strings"

	"github.com/NitinKumar004/cloudemu/config"
	cerrors "github.com/NitinKumar004/cloudemu/errors"
	"github.com/NitinKumar004/cloudemu/internal/memstore"
	"github.com/NitinKumar004/cloudemu/pagination"
	"github.com/NitinKumar004/cloudemu/storage/driver"
)

var _ driver.Bucket = (*Mock)(nil)

type s3Object struct {
	Key          string
	Data         []byte
	ContentType  string
	ETag         string
	LastModified string
	Metadata     map[string]string
}

type bucketMeta struct {
	Name      string
	Region    string
	CreatedAt string
	objects   *memstore.Store[*s3Object]
}

// Mock is an in-memory mock implementation of the AWS S3 service.
type Mock struct {
	buckets *memstore.Store[*bucketMeta]
	opts    *config.Options
}

// New creates a new S3 mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		buckets: memstore.New[*bucketMeta](),
		opts:    opts,
	}
}

func (m *Mock) CreateBucket(_ context.Context, name string) error {
	if name == "" {
		return cerrors.New(cerrors.InvalidArgument, "bucket name cannot be empty")
	}
	if m.buckets.Has(name) {
		return cerrors.Newf(cerrors.AlreadyExists, "bucket %q already exists", name)
	}
	m.buckets.Set(name, &bucketMeta{
		Name:      name,
		Region:    m.opts.Region,
		CreatedAt: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		objects:   memstore.New[*s3Object](),
	})
	return nil
}

func (m *Mock) DeleteBucket(_ context.Context, name string) error {
	bkt, ok := m.buckets.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", name)
	}
	if bkt.objects.Len() > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition, "bucket %q is not empty", name)
	}
	m.buckets.Delete(name)
	return nil
}

func (m *Mock) ListBuckets(_ context.Context) ([]driver.BucketInfo, error) {
	keys := m.buckets.Keys()
	sort.Strings(keys)
	result := make([]driver.BucketInfo, 0, len(keys))
	for _, k := range keys {
		bkt, ok := m.buckets.Get(k)
		if !ok {
			continue
		}
		result = append(result, driver.BucketInfo{
			Name:      bkt.Name,
			Region:    bkt.Region,
			CreatedAt: bkt.CreatedAt,
		})
	}
	return result, nil
}

func (m *Mock) PutObject(_ context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	bkt.objects.Set(key, &s3Object{
		Key:          key,
		Data:         dataCopy,
		ContentType:  contentType,
		ETag:         fmt.Sprintf("%x", md5.Sum(data)),
		LastModified: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Metadata:     metadata,
	})
	return nil
}

func (m *Mock) GetObject(_ context.Context, bucket, key string) (*driver.Object, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}
	obj, ok := bkt.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
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

func (m *Mock) DeleteObject(_ context.Context, bucket, key string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}
	if !bkt.objects.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}
	bkt.objects.Delete(key)
	return nil
}

func (m *Mock) HeadObject(_ context.Context, bucket, key string) (*driver.ObjectInfo, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}
	obj, ok := bkt.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}
	return &driver.ObjectInfo{
		Key: obj.Key, Size: int64(len(obj.Data)), ContentType: obj.ContentType,
		ETag: obj.ETag, LastModified: obj.LastModified, Metadata: obj.Metadata,
	}, nil
}

func (m *Mock) ListObjects(_ context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}
	allKeys := bkt.objects.Keys()
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
		obj, objOk := bkt.objects.Get(k)
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

func (m *Mock) CopyObject(_ context.Context, dstBucket, dstKey string, src driver.CopySource) error {
	srcBkt, ok := m.buckets.Get(src.Bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "source bucket %q not found", src.Bucket)
	}
	srcObj, ok := srcBkt.objects.Get(src.Key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "source object %q not found", src.Key)
	}
	dstBkt, ok := m.buckets.Get(dstBucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "destination bucket %q not found", dstBucket)
	}
	dataCopy := make([]byte, len(srcObj.Data))
	copy(dataCopy, srcObj.Data)
	meta := make(map[string]string, len(srcObj.Metadata))
	for k, v := range srcObj.Metadata {
		meta[k] = v
	}
	dstBkt.objects.Set(dstKey, &s3Object{
		Key: dstKey, Data: dataCopy, ContentType: srcObj.ContentType,
		ETag: srcObj.ETag, LastModified: m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Metadata: meta,
	})
	return nil
}
