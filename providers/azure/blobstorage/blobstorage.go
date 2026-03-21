// Package blobstorage provides an in-memory mock implementation of Azure Blob Storage.
package blobstorage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/pagination"
	"github.com/stackshy/cloudemu/storage/driver"
)

const (
	blobDefaultSASExpiry = time.Hour
	blobDefaultMaxKeys   = 1000
	blobTimeFormat       = "2006-01-02T15:04:05Z"
	blobAccountName      = "cloudemu"
	blobHoursPerDay      = 24
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

type blobMultipartUpload struct {
	id          string
	key         string
	contentType string
	parts       map[int][]byte
	createdAt   string
}

type containerMeta struct {
	Name       string
	Region     string
	CreatedAt  string
	objects    *memstore.Store[*blobObject]
	lifecycle  *driver.LifecycleConfig
	multiparts *memstore.Store[*blobMultipartUpload]
	versioning bool
}

// Mock is an in-memory mock implementation of Azure Blob Storage.
type Mock struct {
	containers *memstore.Store[*containerMeta]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

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
		Name:       name,
		Region:     m.opts.Region,
		CreatedAt:  m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		objects:    memstore.New[*blobObject](),
		multiparts: memstore.New[*blobMultipartUpload](),
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
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     metadata,
	})

	m.emitMetric(bucket, map[string]float64{"Transactions": 1, "Ingress": float64(len(data))})

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

	m.emitMetric(bucket, map[string]float64{"Transactions": 1, "Egress": float64(len(obj.Data))})

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

	m.emitMetric(bucket, map[string]float64{"Transactions": 1})

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
		maxKeys = blobDefaultMaxKeys
	}

	page, _ := pagination.Paginate(matchedObjects, opts.PageToken, maxKeys)

	m.emitMetric(bucket, map[string]float64{"Transactions": 1})

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
		ETag: srcObj.ETag, LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata: meta,
	})

	return nil
}

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
		blobAccountName, req.Bucket, req.Key, sig,
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
	mp.parts[partNumber] = dataCopy

	etag := fmt.Sprintf("%x", sha256.Sum256(data))

	return &driver.UploadPart{
		PartNumber: partNumber, ETag: etag, Size: int64(len(data)),
	}, nil
}

func (m *Mock) CompleteMultipartUpload(
	_ context.Context, bucket, key, uploadID string, _ []driver.UploadPart,
) error {
	ctr, ok := m.containers.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", bucket)
	}

	mp, ok := ctr.multiparts.Get(uploadID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "upload %q not found", uploadID)
	}

	data := assembleBlobParts(mp.parts)

	ctr.objects.Set(key, &blobObject{
		Key:          key,
		Data:         data,
		ContentType:  mp.contentType,
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
		Metadata:     make(map[string]string),
	})

	ctr.multiparts.Delete(uploadID)

	return nil
}

func assembleBlobParts(parts map[int][]byte) []byte {
	keys := make([]int, 0, len(parts))
	for k := range parts {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	var data []byte
	for _, k := range keys {
		data = append(data, parts[k]...)
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
