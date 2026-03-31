// Package gcs provides an in-memory mock implementation of Google Cloud Storage.
package gcs

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
	gcsDefaultPresignExpiry = 15 * time.Minute
	gcsMaxPresignExpiry     = 7 * 24 * time.Hour
	gcsDefaultMaxKeys       = 1000
	gcsTimeFormat           = "2006-01-02T15:04:05Z"
	gcsHoursPerDay          = 24
)

// Compile-time check that Mock implements driver.Bucket.
var _ driver.Bucket = (*Mock)(nil)

type gcsObject struct {
	Key          string
	Data         []byte
	ContentType  string
	ETag         string
	LastModified string
	Metadata     map[string]string
}

type gcsMultipartUpload struct {
	id          string
	key         string
	contentType string
	parts       map[int][]byte
	createdAt   string
}

type bucketMeta struct {
	Name       string
	Region     string
	CreatedAt  string
	objects    *memstore.Store[*gcsObject]
	lifecycle  *driver.LifecycleConfig
	multiparts *memstore.Store[*gcsMultipartUpload]
	versioning bool
	policy     *driver.BucketPolicy
	corsConfig *driver.CORSConfig
	encryption *driver.EncryptionConfig
}

// Mock is an in-memory mock implementation of Google Cloud Storage.
type Mock struct {
	buckets    *memstore.Store[*bucketMeta]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(ctx context.Context, metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(ctx, []mondriver.MetricDatum{
		{
			Namespace:  "storage.googleapis.com",
			MetricName: metricName,
			Value:      value,
			Unit:       "None",
			Dimensions: dims,
			Timestamp:  m.opts.Clock.Now(),
		},
	})
}

// New creates a new GCS mock.
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
		Name:       name,
		Region:     m.opts.Region,
		CreatedAt:  m.opts.Clock.Now().UTC().Format(gcsTimeFormat),
		objects:    memstore.New[*gcsObject](),
		multiparts: memstore.New[*gcsMultipartUpload](),
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

func (m *Mock) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	metaCopy := make(map[string]string, len(metadata))
	for k, v := range metadata {
		metaCopy[k] = v
	}

	bkt.objects.Set(key, &gcsObject{
		Key:          key,
		Data:         dataCopy,
		ContentType:  contentType,
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(gcsTimeFormat),
		Metadata:     metaCopy,
	})

	dims := map[string]string{"bucket_name": bucket}
	m.emitMetric(ctx, "api/request_count", 1, dims)
	m.emitMetric(ctx, "network/received_bytes_count", float64(len(data)), dims)

	return nil
}

func (m *Mock) GetObject(ctx context.Context, bucket, key string) (*driver.Object, error) {
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

	dims := map[string]string{"bucket_name": bucket}
	m.emitMetric(ctx, "api/request_count", 1, dims)
	m.emitMetric(ctx, "network/sent_bytes_count", float64(len(obj.Data)), dims)

	return &driver.Object{
		Info: driver.ObjectInfo{
			Key: obj.Key, Size: int64(len(obj.Data)), ContentType: obj.ContentType,
			ETag: obj.ETag, LastModified: obj.LastModified, Metadata: obj.Metadata,
		},
		Data: dataCopy,
	}, nil
}

func (m *Mock) DeleteObject(ctx context.Context, bucket, key string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if !bkt.objects.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	bkt.objects.Delete(key)

	m.emitMetric(ctx, "api/request_count", 1, map[string]string{"bucket_name": bucket})

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

func (m *Mock) ListObjects(ctx context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
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
		maxKeys = gcsDefaultMaxKeys
	}

	page, _ := pagination.Paginate(matchedObjects, opts.PageToken, maxKeys)

	m.emitMetric(ctx, "api/request_count", 1, map[string]string{"bucket_name": bucket})

	return &driver.ListResult{
		Objects:        page.Items,
		CommonPrefixes: commonPrefixes,
		NextPageToken:  page.NextPageToken,
		IsTruncated:    page.HasMore,
	}, nil
}

func (m *Mock) CopyObject(ctx context.Context, dstBucket, dstKey string, src driver.CopySource) error {
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

	dstBkt.objects.Set(dstKey, &gcsObject{
		Key: dstKey, Data: dataCopy, ContentType: srcObj.ContentType,
		ETag: srcObj.ETag, LastModified: m.opts.Clock.Now().UTC().Format(gcsTimeFormat),
		Metadata: meta,
	})

	dims := map[string]string{"bucket_name": dstBucket}
	m.emitMetric(ctx, "api/request_count", 1, dims)

	return nil
}

// GeneratePresignedURL generates a mock presigned URL.
// Note: expiry is tracked in the URL but not enforced on use — this is a mock limitation.
func (m *Mock) GeneratePresignedURL(_ context.Context, req driver.PresignedURLRequest) (*driver.PresignedURL, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodPut {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "method must be GET or PUT, got %q", req.Method)
	}

	if !m.buckets.Has(req.Bucket) {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", req.Bucket)
	}

	expiry := req.ExpiresIn
	if expiry <= 0 {
		expiry = gcsDefaultPresignExpiry
	}

	if expiry > gcsMaxPresignExpiry {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expiry %v exceeds maximum of 7 days", expiry)
	}

	now := m.opts.Clock.Now().UTC()
	sig := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Bucket+req.Key+now.String())))
	expiresAt := now.Add(expiry)
	seconds := int(expiry.Seconds())

	url := fmt.Sprintf(
		"https://storage.googleapis.com/%s/%s?X-Goog-Signature=%s&X-Goog-Expires=%d",
		req.Bucket, req.Key, sig, seconds,
	)

	return &driver.PresignedURL{URL: url, Method: req.Method, ExpiresAt: expiresAt}, nil
}

func (m *Mock) PutLifecycleConfig(_ context.Context, bucket string, cfg driver.LifecycleConfig) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	cfgCopy := driver.LifecycleConfig{Rules: make([]driver.LifecycleRule, len(cfg.Rules))}
	copy(cfgCopy.Rules, cfg.Rules)
	bkt.lifecycle = &cfgCopy

	return nil
}

func (m *Mock) GetLifecycleConfig(_ context.Context, bucket string) (*driver.LifecycleConfig, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if bkt.lifecycle == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no lifecycle configuration for bucket %q", bucket)
	}

	return bkt.lifecycle, nil
}

func (m *Mock) EvaluateLifecycle(_ context.Context, bucket string) ([]string, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if bkt.lifecycle == nil {
		return nil, nil
	}

	now := m.opts.Clock.Now().UTC()
	expired := collectExpiredGCSKeys(bkt, now)
	sort.Strings(expired)

	return expired, nil
}

func collectExpiredGCSKeys(bkt *bucketMeta, now time.Time) []string {
	var result []string

	for _, key := range bkt.objects.Keys() {
		obj, objOk := bkt.objects.Get(key)
		if !objOk {
			continue
		}

		if gcsObjectExpired(obj, bkt.lifecycle, now) {
			result = append(result, key)
		}
	}

	return result
}

func gcsObjectExpired(obj *gcsObject, cfg *driver.LifecycleConfig, now time.Time) bool {
	modified, err := time.Parse(gcsTimeFormat, obj.LastModified)
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

		if rule.ExpirationDays > 0 && age >= time.Duration(rule.ExpirationDays)*gcsHoursPerDay*time.Hour {
			return true
		}
	}

	return false
}

func (m *Mock) CreateMultipartUpload(
	_ context.Context, bucket, key, contentType string,
) (*driver.MultipartUpload, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	uploadID := idgen.GenerateID("upload-")
	now := m.opts.Clock.Now().UTC().Format(gcsTimeFormat)

	bkt.multiparts.Set(uploadID, &gcsMultipartUpload{
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
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	mp, ok := bkt.multiparts.Get(uploadID)
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
	ctx context.Context, bucket, key, uploadID string, parts []driver.UploadPart,
) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	mp, ok := bkt.multiparts.Get(uploadID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "upload %q not found", uploadID)
	}

	for _, p := range parts {
		if _, exists := mp.parts[p.PartNumber]; !exists {
			return cerrors.Newf(cerrors.InvalidArgument, "part %d not found in upload %q", p.PartNumber, uploadID)
		}
	}

	data := assembleGCSPartsInOrder(mp.parts, parts)

	bkt.objects.Set(key, &gcsObject{
		Key:          key,
		Data:         data,
		ContentType:  mp.contentType,
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(gcsTimeFormat),
		Metadata:     make(map[string]string),
	})

	bkt.multiparts.Delete(uploadID)

	dims := map[string]string{"bucket_name": bucket}
	m.emitMetric(ctx, "api/request_count", 1, dims)
	m.emitMetric(ctx, "network/received_bytes_count", float64(len(data)), dims)

	return nil
}

func assembleGCSPartsInOrder(allParts map[int][]byte, parts []driver.UploadPart) []byte {
	var data []byte
	for _, p := range parts {
		data = append(data, allParts[p.PartNumber]...)
	}

	return data
}

func (m *Mock) AbortMultipartUpload(_ context.Context, bucket, _, uploadID string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if !bkt.multiparts.Has(uploadID) {
		return cerrors.Newf(cerrors.NotFound, "upload %q not found", uploadID)
	}

	bkt.multiparts.Delete(uploadID)

	return nil
}

func (m *Mock) ListMultipartUploads(_ context.Context, bucket string) ([]driver.MultipartUpload, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	keys := bkt.multiparts.Keys()
	sort.Strings(keys)

	result := make([]driver.MultipartUpload, 0, len(keys))

	for _, k := range keys {
		mp, mpOk := bkt.multiparts.Get(k)
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
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versioning = enabled

	return nil
}

func (m *Mock) GetBucketVersioning(_ context.Context, bucket string) (bool, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	return bkt.versioning, nil
}

func (m *Mock) PutBucketPolicy(_ context.Context, bucket string, policy driver.BucketPolicy) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	p := policy
	bkt.policy = &p

	return nil
}

func (m *Mock) GetBucketPolicy(_ context.Context, bucket string) (*driver.BucketPolicy, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if bkt.policy == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no policy set for bucket %q", bucket)
	}

	p := *bkt.policy

	return &p, nil
}

func (m *Mock) DeleteBucketPolicy(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.policy = nil

	return nil
}

func (m *Mock) PutCORSConfig(_ context.Context, bucket string, cfg driver.CORSConfig) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	c := cfg
	bkt.corsConfig = &c

	return nil
}

func (m *Mock) GetCORSConfig(_ context.Context, bucket string) (*driver.CORSConfig, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if bkt.corsConfig == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no CORS config set for bucket %q", bucket)
	}

	c := *bkt.corsConfig

	return &c, nil
}

func (m *Mock) DeleteCORSConfig(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.corsConfig = nil

	return nil
}

func (m *Mock) PutEncryptionConfig(_ context.Context, bucket string, cfg driver.EncryptionConfig) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	e := cfg
	bkt.encryption = &e

	return nil
}

func (m *Mock) GetEncryptionConfig(_ context.Context, bucket string) (*driver.EncryptionConfig, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if bkt.encryption == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no encryption config set for bucket %q", bucket)
	}

	e := *bkt.encryption

	return &e, nil
}
