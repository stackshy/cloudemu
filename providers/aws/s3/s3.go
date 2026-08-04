package s3

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	s3DefaultPresignExpiry = 15 * time.Minute
	s3MaxPresignExpiry     = 7 * 24 * time.Hour
	s3DefaultMaxKeys       = 1000
	s3TimeFormat           = "2006-01-02T15:04:05Z"
	hoursPerDay            = 24
)

var (
	_ driver.Bucket          = (*Mock)(nil)
	_ driver.VersionedBucket = (*Mock)(nil)
)

type s3Object struct {
	Key          string
	Data         []byte
	ContentType  string
	ETag         string
	LastModified string
	Metadata     map[string]string
	Tags         map[string]string
	// VersionID is the version id of the current object on a versioned bucket
	// ("null" when suspended, empty when the bucket never had versioning).
	VersionID string
}

// s3Version is one entry in a key's version history (a stored object or a
// delete marker), oldest-first within bucketMeta.versions.
type s3Version struct {
	versionID    string
	data         []byte
	contentType  string
	etag         string
	lastModified string
	metadata     map[string]string
	deleteMarker bool
}

type multipartUpload struct {
	id          string
	key         string
	contentType string
	// mu guards parts: the SDK uploader sends parts concurrently (UploadPart
	// writes) while ListParts/CompleteMultipartUpload read them.
	mu        sync.Mutex
	parts     map[int][]byte
	createdAt string
}

type bucketMeta struct {
	Name       string
	Region     string
	CreatedAt  string
	objects    *memstore.Store[*s3Object]
	lifecycle  *driver.LifecycleConfig
	multiparts *memstore.Store[*multipartUpload]
	// versionStatus is "" (never configured), "Enabled", or "Suspended".
	// versionsMu guards versionStatus and the versions history map; versions
	// maps a key to its ordered (oldest-first) version chain.
	versionStatus string
	versionsMu    sync.Mutex
	versions      map[string][]*s3Version
	policy        *driver.BucketPolicy
	corsConfig    *driver.CORSConfig
	encryption    *driver.EncryptionConfig
	tags          map[string]string
	notifications []QueueNotification
}

// QueueNotification is one S3 bucket-notification target: an SQS queue that
// receives events whose names match one of Events (e.g. "s3:ObjectCreated:*").
type QueueNotification struct {
	ID       string
	QueueARN string
	Events   []string
}

// SQSDeliverer delivers an S3 event notification into an SQS queue by ARN. The
// SQS mock satisfies this, enabling real S3 -> SQS event delivery.
type SQSDeliverer interface {
	DeliverExternal(ctx context.Context, queueARN, body string) error
}

// Mock is an in-memory mock implementation of the AWS S3 service.
type Mock struct {
	buckets    *memstore.Store[*bucketMeta]
	opts       *config.Options
	monitoring mondriver.Monitoring
	sqs        SQSDeliverer
}

// SetSQSDeliverer wires the SQS backend so object-create events deliver to
// buckets' SQS notification targets.
func (m *Mock) SetSQSDeliverer(d SQSDeliverer) {
	m.sqs = d
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) emitMetric(metricName string, value float64, unit string, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/S3", MetricName: metricName, Value: value, Unit: unit,
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
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
		Name:       name,
		Region:     m.opts.Region,
		CreatedAt:  m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		objects:    memstore.New[*s3Object](),
		multiparts: memstore.New[*multipartUpload](),
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
	m.storeObject(bkt, key, &s3Object{
		Key:          key,
		Data:         dataCopy,
		ContentType:  contentType,
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata:     maps.Clone(metadata),
	})

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("PutRequests", 1, "Count", dims)
	m.emitMetric("BytesUploaded", float64(len(data)), "Bytes", dims)

	m.notifyObjectCreated(bkt, bucket, key, int64(len(data)))

	return nil
}

// Versioning status constants and the reserved id for the pre-/non-versioned
// object version.
const (
	versioningEnabled   = "Enabled"
	versioningSuspended = "Suspended"
	nullVersionID       = "null"
)

func newVersionID() string { return idgen.GenerateID("") }

// storeObject sets key's current object and, on a versioned bucket, records the
// new version in history (stamping obj.VersionID). Enabled appends a fresh
// version; Suspended overwrites the reusable "null" version; an unversioned
// bucket keeps no history.
func (m *Mock) storeObject(bkt *bucketMeta, key string, obj *s3Object) {
	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	switch bkt.versionStatus {
	case versioningEnabled:
		obj.VersionID = newVersionID()
		bkt.appendVersion(key, versionFromObject(obj))
	case versioningSuspended:
		obj.VersionID = nullVersionID
		bkt.replaceNullVersion(key, versionFromObject(obj))
	}

	bkt.objects.Set(key, obj)
}

func versionFromObject(obj *s3Object) *s3Version {
	return &s3Version{
		versionID:    obj.VersionID,
		data:         obj.Data,
		contentType:  obj.ContentType,
		etag:         obj.ETag,
		lastModified: obj.LastModified,
		metadata:     obj.Metadata,
	}
}

// appendVersion / replaceNullVersion mutate the history map; callers hold
// versionsMu.
func (b *bucketMeta) appendVersion(key string, v *s3Version) {
	if b.versions == nil {
		b.versions = make(map[string][]*s3Version)
	}
	b.versions[key] = append(b.versions[key], v)
}

func (b *bucketMeta) replaceNullVersion(key string, v *s3Version) {
	if b.versions == nil {
		b.versions = make(map[string][]*s3Version)
	}
	kept := make([]*s3Version, 0, len(b.versions[key])+1)
	for _, ex := range b.versions[key] {
		if ex.versionID != nullVersionID {
			kept = append(kept, ex)
		}
	}
	b.versions[key] = append(kept, v)
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

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("GetRequests", 1, "Count", dims)
	m.emitMetric("BytesDownloaded", float64(len(obj.Data)), "Bytes", dims)

	return &driver.Object{
		Info: driver.ObjectInfo{
			Key: obj.Key, Size: int64(len(obj.Data)), ContentType: obj.ContentType,
			ETag: obj.ETag, LastModified: obj.LastModified, Metadata: maps.Clone(obj.Metadata),
			VersionID: obj.VersionID,
		},
		Data: dataCopy,
	}, nil
}

func (m *Mock) DeleteObject(_ context.Context, bucket, key string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	_, _, existed := m.deleteTopLevelLocked(bkt, key)
	bkt.versionsMu.Unlock()

	// On an unversioned bucket, deleting a missing key is NotFound (the wire
	// handler maps that to an idempotent 204). A versioned bucket always records
	// a delete marker, so existed is true there.
	if !existed {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("DeleteRequests", 1, "Count", dims)

	return nil
}

// deleteTopLevelLocked applies a top-level (no versionId) delete. On Enabled it
// appends a delete marker; on Suspended it overwrites the null version with a
// null delete marker; unversioned removes the current object. Returns the
// affected version id, whether a delete marker was created, and whether
// anything existed to delete (always true when a marker is recorded). Callers
// hold versionsMu.
func (m *Mock) deleteTopLevelLocked(bkt *bucketMeta, key string) (versionID string, deleteMarker, existed bool) {
	now := m.opts.Clock.Now().UTC().Format(s3TimeFormat)

	switch bkt.versionStatus {
	case versioningEnabled:
		vid := newVersionID()
		bkt.appendVersion(key, &s3Version{versionID: vid, deleteMarker: true, lastModified: now})
		bkt.objects.Delete(key)
		return vid, true, true
	case versioningSuspended:
		bkt.replaceNullVersion(key, &s3Version{versionID: nullVersionID, deleteMarker: true, lastModified: now})
		bkt.objects.Delete(key)
		return nullVersionID, true, true
	default:
		if !bkt.objects.Has(key) {
			return "", false, false
		}
		bkt.objects.Delete(key)
		return "", false, true
	}
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
		ETag: obj.ETag, LastModified: obj.LastModified, Metadata: maps.Clone(obj.Metadata),
		VersionID: obj.VersionID,
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
		maxKeys = s3DefaultMaxKeys
	}

	page, err := pagination.Paginate(matchedObjects, opts.PageToken, maxKeys)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	// Clone metadata only for the page actually returned — cloning every
	// match would make a paged scan O(bucket) allocations per request.
	for i := range page.Items {
		page.Items[i].Metadata = maps.Clone(page.Items[i].Metadata)
	}

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("ListRequests", 1, "Count", dims)

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

	m.storeObject(dstBkt, dstKey, &s3Object{
		Key: dstKey, Data: dataCopy, ContentType: srcObj.ContentType,
		ETag: srcObj.ETag, LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata: meta,
	})

	dims := map[string]string{"BucketName": dstBucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("CopyRequests", 1, "Count", dims)

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
		expiry = s3DefaultPresignExpiry
	}

	if expiry > s3MaxPresignExpiry {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "expiry %v exceeds maximum of 7 days", expiry)
	}

	now := m.opts.Clock.Now().UTC()
	token := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Bucket+req.Key+now.String())))
	expiresAt := now.Add(expiry)
	seconds := int(expiry.Seconds())

	url := fmt.Sprintf(
		"https://%s.s3.%s.amazonaws.com/%s?X-Amz-Signature=%s&X-Amz-Expires=%d",
		req.Bucket, m.opts.Region, req.Key, token, seconds,
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
	expired := collectExpiredKeys(bkt, now)
	sort.Strings(expired)

	return expired, nil
}

func collectExpiredKeys(bkt *bucketMeta, now time.Time) []string {
	var result []string

	for _, key := range bkt.objects.Keys() {
		obj, objOk := bkt.objects.Get(key)
		if !objOk {
			continue
		}

		if objectExpired(obj, bkt.lifecycle, now) {
			result = append(result, key)
		}
	}

	return result
}

func objectExpired(obj *s3Object, cfg *driver.LifecycleConfig, now time.Time) bool {
	modified, err := time.Parse(s3TimeFormat, obj.LastModified)
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

		if rule.ExpirationDays > 0 && age >= time.Duration(rule.ExpirationDays)*hoursPerDay*time.Hour {
			return true
		}
	}

	return false
}

func (m *Mock) CreateMultipartUpload(_ context.Context, bucket, key, contentType string) (*driver.MultipartUpload, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	uploadID := idgen.GenerateID("upload-")
	now := m.opts.Clock.Now().UTC().Format(s3TimeFormat)

	bkt.multiparts.Set(uploadID, &multipartUpload{
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

func (m *Mock) UploadPart(_ context.Context, bucket, _, uploadID string, partNumber int, data []byte) (*driver.UploadPart, error) {
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

	mp.mu.Lock()
	mp.parts[partNumber] = dataCopy
	mp.mu.Unlock()

	etag := fmt.Sprintf("%x", sha256.Sum256(data))

	return &driver.UploadPart{
		PartNumber: partNumber, ETag: etag, Size: int64(len(data)),
	}, nil
}

// ListParts returns the parts buffered so far for an in-progress upload,
// ordered by part number (the driver keeps each part's bytes, so ETag and Size
// are reported exactly as UploadPart returned them).
func (m *Mock) ListParts(_ context.Context, bucket, _, uploadID string) ([]driver.UploadPart, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	mp, ok := bkt.multiparts.Get(uploadID)
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

func (m *Mock) CompleteMultipartUpload(_ context.Context, bucket, key, uploadID string, parts []driver.UploadPart) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	mp, ok := bkt.multiparts.Get(uploadID)
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
	data := assemblePartsInOrder(mp.parts, parts)
	mp.mu.Unlock()

	// storeObject (not a bare objects.Set) so a completed multipart upload is
	// versioned like a PutObject on a versioning-enabled bucket.
	m.storeObject(bkt, key, &s3Object{
		Key:          key,
		Data:         data,
		ContentType:  mp.contentType,
		ETag:         fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata:     make(map[string]string),
	})

	bkt.multiparts.Delete(uploadID)

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("PutRequests", 1, "Count", dims)
	m.emitMetric("BytesUploaded", float64(len(data)), "Bytes", dims)

	return nil
}

func assemblePartsInOrder(allParts map[int][]byte, parts []driver.UploadPart) []byte {
	// S3 assembles parts by ascending PartNumber regardless of the order the
	// client lists them in CompleteMultipartUpload; sort so an out-of-order
	// (or unsorted-SDK) Complete doesn't corrupt the object.
	ordered := append([]driver.UploadPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PartNumber < ordered[j].PartNumber })

	var data []byte
	for _, p := range ordered {
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

// SetBucketVersioning enables (Enabled) or, when disabled, suspends versioning.
// Real S3 has no "off" once configured, only Suspended. Prefer
// SetVersioningStatus for the full tri-state.
func (m *Mock) SetBucketVersioning(_ context.Context, bucket string, enabled bool) error {
	status := versioningSuspended
	if enabled {
		status = versioningEnabled
	}

	return m.SetVersioningStatus(context.Background(), bucket, status)
}

func (m *Mock) GetBucketVersioning(_ context.Context, bucket string) (bool, error) {
	status, err := m.VersioningStatus(context.Background(), bucket)
	if err != nil {
		return false, err
	}

	return status == versioningEnabled, nil
}

// SetVersioningStatus sets the bucket versioning status ("Enabled" or
// "Suspended"), retaining any existing version history.
func (m *Mock) SetVersioningStatus(_ context.Context, bucket, status string) error {
	if status != versioningEnabled && status != versioningSuspended {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid versioning status %q", status)
	}

	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	bkt.versionStatus = status
	bkt.versionsMu.Unlock()

	return nil
}

// VersioningStatus returns "Enabled", "Suspended", or "" (never configured).
func (m *Mock) VersioningStatus(_ context.Context, bucket string) (string, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	return bkt.versionStatus, nil
}

// GetObjectVersion returns a specific version (versionID != "") or the current
// object (versionID == ""). A delete-marker version is reported as NotFound.
func (m *Mock) GetObjectVersion(ctx context.Context, bucket, key, versionID string) (*driver.Object, error) {
	if versionID == "" {
		return m.GetObject(ctx, bucket, key)
	}

	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	v := findVersion(bkt, key, versionID)
	bkt.versionsMu.Unlock()

	if v == nil || v.deleteMarker {
		return nil, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
	}

	dataCopy := make([]byte, len(v.data))
	copy(dataCopy, v.data)

	return &driver.Object{Info: infoFromVersion(key, v), Data: dataCopy}, nil
}

// HeadObjectVersion returns metadata for a specific version.
func (m *Mock) HeadObjectVersion(ctx context.Context, bucket, key, versionID string) (*driver.ObjectInfo, error) {
	if versionID == "" {
		return m.HeadObject(ctx, bucket, key)
	}

	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	v := findVersion(bkt, key, versionID)
	bkt.versionsMu.Unlock()

	if v == nil || v.deleteMarker {
		return nil, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
	}

	info := infoFromVersion(key, v)

	return &info, nil
}

// DeleteObjectVersion removes a specific version, or (versionID == "") performs
// a top-level delete (delete marker on Enabled). Returns the affected version
// id and whether it was/created a delete marker.
func (m *Mock) DeleteObjectVersion(_ context.Context, bucket, key, versionID string) (string, bool, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return "", false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	if versionID == "" {
		vid, marker, _ := m.deleteTopLevelLocked(bkt, key)
		return vid, marker, nil
	}

	chain := bkt.versions[key]
	idx := -1

	var removed *s3Version

	for i, v := range chain {
		if v.versionID == versionID {
			idx, removed = i, v
			break
		}
	}

	if idx < 0 {
		return "", false, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
	}

	bkt.versions[key] = append(chain[:idx], chain[idx+1:]...)
	if len(bkt.versions[key]) == 0 {
		delete(bkt.versions, key)
	}

	m.recomputeCurrentLocked(bkt, key)

	return versionID, removed.deleteMarker, nil
}

// ListObjectVersions returns the full version history matching opts, newest
// version first within each key.
func (m *Mock) ListObjectVersions(_ context.Context, bucket string, opts driver.ListOptions) (*driver.VersionListResult, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	// Union of keys with history and keys present only as current objects
	// (written before versioning was enabled → reported as the "null" version).
	keySet := make(map[string]struct{})
	for k := range bkt.versions {
		keySet[k] = struct{}{}
	}

	for _, k := range bkt.objects.Keys() {
		keySet[k] = struct{}{}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	result := &driver.VersionListResult{}
	prefixSet := make(map[string]struct{})

	for _, k := range keys {
		if opts.Prefix != "" && !strings.HasPrefix(k, opts.Prefix) {
			continue
		}

		if opts.Delimiter != "" {
			rest := k[len(opts.Prefix):]
			if idx := strings.Index(rest, opts.Delimiter); idx >= 0 {
				prefixSet[opts.Prefix+rest[:idx+len(opts.Delimiter)]] = struct{}{}
				continue
			}
		}

		chain := bkt.versions[k]
		if len(chain) == 0 {
			if obj, has := bkt.objects.Get(k); has {
				result.Versions = append(result.Versions, driver.ObjectVersion{
					Key: k, VersionID: nullVersionID, IsLatest: true,
					Size: int64(len(obj.Data)), ETag: obj.ETag,
					ContentType: obj.ContentType, LastModified: obj.LastModified,
				})
			}

			continue
		}

		for i := len(chain) - 1; i >= 0; i-- {
			v := chain[i]
			result.Versions = append(result.Versions, driver.ObjectVersion{
				Key: k, VersionID: v.versionID, IsLatest: i == len(chain)-1,
				DeleteMarker: v.deleteMarker, Size: int64(len(v.data)), ETag: v.etag,
				ContentType: v.contentType, LastModified: v.lastModified,
			})
		}
	}

	for p := range prefixSet {
		result.CommonPrefixes = append(result.CommonPrefixes, p)
	}

	sort.Strings(result.CommonPrefixes)

	return result, nil
}

// recomputeCurrentLocked resets key's current object to its newest non-delete
// version, or removes it if the newest is a delete marker (or none remain).
// Callers hold versionsMu.
func (m *Mock) recomputeCurrentLocked(bkt *bucketMeta, key string) {
	chain := bkt.versions[key]
	if len(chain) == 0 {
		bkt.objects.Delete(key)
		return
	}

	latest := chain[len(chain)-1]
	if latest.deleteMarker {
		bkt.objects.Delete(key)
		return
	}

	bkt.objects.Set(key, objectFromVersion(key, latest))
}

func findVersion(bkt *bucketMeta, key, versionID string) *s3Version {
	for _, v := range bkt.versions[key] {
		if v.versionID == versionID {
			return v
		}
	}

	return nil
}

func objectFromVersion(key string, v *s3Version) *s3Object {
	return &s3Object{
		Key: key, Data: v.data, ContentType: v.contentType,
		ETag: v.etag, LastModified: v.lastModified, Metadata: maps.Clone(v.metadata),
		VersionID: v.versionID,
	}
}

func infoFromVersion(key string, v *s3Version) driver.ObjectInfo {
	return driver.ObjectInfo{
		Key: key, Size: int64(len(v.data)), ContentType: v.contentType,
		ETag: v.etag, LastModified: v.lastModified, Metadata: maps.Clone(v.metadata),
		VersionID: v.versionID, DeleteMarker: v.deleteMarker,
	}
}

// PutBucketPolicy sets the bucket policy.
func (m *Mock) PutBucketPolicy(_ context.Context, bucket string, policy driver.BucketPolicy) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	p := policy
	bkt.policy = &p

	return nil
}

// GetBucketPolicy returns the bucket policy.
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

// DeleteBucketPolicy removes the bucket policy.
func (m *Mock) DeleteBucketPolicy(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.policy = nil

	return nil
}

// PutCORSConfig sets the CORS configuration for a bucket.
func (m *Mock) PutCORSConfig(_ context.Context, bucket string, cfg driver.CORSConfig) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	c := cfg
	bkt.corsConfig = &c

	return nil
}

// GetCORSConfig returns the CORS configuration for a bucket.
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

// DeleteCORSConfig removes the CORS configuration for a bucket.
func (m *Mock) DeleteCORSConfig(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.corsConfig = nil

	return nil
}

// PutEncryptionConfig sets the default encryption for a bucket.
func (m *Mock) PutEncryptionConfig(_ context.Context, bucket string, cfg driver.EncryptionConfig) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	e := cfg
	bkt.encryption = &e

	return nil
}

// GetEncryptionConfig returns the default encryption for a bucket.
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

// PutObjectTagging sets tags on an object.
func (m *Mock) PutObjectTagging(_ context.Context, bucket, key string, tags map[string]string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj, ok := bkt.objects.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}

	obj.Tags = copied

	return nil
}

// GetObjectTagging returns tags for an object.
func (m *Mock) GetObjectTagging(_ context.Context, bucket, key string) (map[string]string, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj, ok := bkt.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
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

// DeleteObjectTagging removes all tags from an object.
func (m *Mock) DeleteObjectTagging(_ context.Context, bucket, key string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj, ok := bkt.objects.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	obj.Tags = nil

	return nil
}

// PutBucketTagging sets tags on a bucket.
func (m *Mock) PutBucketTagging(_ context.Context, bucket string, tags map[string]string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	copied := make(map[string]string, len(tags))
	for k, v := range tags {
		copied[k] = v
	}

	bkt.tags = copied

	return nil
}

// GetBucketTagging returns tags for a bucket.
func (m *Mock) GetBucketTagging(_ context.Context, bucket string) (map[string]string, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	if bkt.tags == nil {
		return map[string]string{}, nil
	}

	copied := make(map[string]string, len(bkt.tags))
	for k, v := range bkt.tags {
		copied[k] = v
	}

	return copied, nil
}

// DeleteBucketTagging removes all tags from a bucket.
func (m *Mock) DeleteBucketTagging(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.tags = nil

	return nil
}
