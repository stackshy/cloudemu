package s3

import (
	"context"
	"crypto/md5" //nolint:gosec // S3 object ETags are defined as MD5 digests, not a security primitive
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
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
	_ driver.RawBucketConfig = (*Mock)(nil)
)

type s3Object struct {
	Key string
	// Data is the in-memory object bytes, or nil when a StorageEngine holds the
	// bytes instead. Size is tracked independently so metadata (Head/List) stays
	// correct once Data has been offloaded to the engine.
	Data         []byte
	Size         int64
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
	size         int64
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
	// rawConfigs holds opaque configuration sub-resource documents (policy, cors,
	// encryption, lifecycle, website, …) exactly as written, so the wire handler
	// can echo them back byte-for-byte. Guarded by rawConfigMu.
	rawConfigMu sync.Mutex
	rawConfigs  map[string][]byte
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

// engineStore persists an object's bytes to the configured StorageEngine. It is
// a no-op (nil) when no engine is wired, so the in-memory Data stays the source
// of truth.
func (m *Mock) engineStore(ctx context.Context, bucket, key, version, contentType string, data []byte, metadata map[string]string) error {
	return storageengine.Put(ctx, m.opts.StorageEngine, config.StorageObject{
		Bucket: bucket, Key: key, Version: version, Data: data,
		ContentType: contentType, Metadata: metadata,
	})
}

// engineLoad returns the object bytes to serve, preferring the in-memory copy
// and falling back to the StorageEngine only when the bytes have been offloaded
// (inMemory == nil). With no engine wired it returns inMemory unchanged.
func (m *Mock) engineLoad(ctx context.Context, ref config.StorageRef, inMemory []byte) ([]byte, error) {
	if m.opts.StorageEngine == nil || inMemory != nil {
		return inMemory, nil
	}

	loaded, ok, err := storageengine.Get(ctx, m.opts.StorageEngine, ref)
	if err != nil {
		return nil, err
	}

	if ok {
		return loaded, nil
	}

	return inMemory, nil
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

	// A versioning-enabled/suspended bucket may hold no current objects yet still
	// retain noncurrent versions and/or delete markers. Real S3 requires every
	// object version (including delete markers) to be removed before the bucket
	// can be deleted, so a non-empty version history also fails DeleteBucket.
	if bkt.hasVersionHistory() {
		return cerrors.Newf(cerrors.FailedPrecondition, "bucket %q is not empty", name)
	}

	m.buckets.Delete(name)

	return nil
}

// hasVersionHistory reports whether the bucket retains any object version or
// delete marker in its version history.
func (b *bucketMeta) hasVersionHistory() bool {
	b.versionsMu.Lock()
	defer b.versionsMu.Unlock()

	for _, chain := range b.versions {
		if len(chain) > 0 {
			return true
		}
	}

	return false
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

// md5Hex returns the hex-encoded MD5 digest of data. Real S3 reports an object's
// ETag as its MD5 (32 hex chars); CLIs, transfer managers, and conditional-GET
// clients compare against this exact shape.
func md5Hex(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // S3 ETag is MD5 by spec, not a security control
	return hex.EncodeToString(sum[:])
}

// multipartETag computes the ETag S3 assigns a completed multipart object: the
// MD5 of the concatenated raw MD5 digests of each part, suffixed with "-N" where
// N is the part count. Tools detect multipart objects by that "-N" suffix.
func multipartETag(orderedParts [][]byte) string {
	var concat []byte
	for _, p := range orderedParts {
		sum := md5.Sum(p) //nolint:gosec // S3 ETag is MD5 by spec, not a security control
		concat = append(concat, sum[:]...)
	}

	final := md5.Sum(concat) //nolint:gosec // S3 ETag is MD5 by spec, not a security control

	return fmt.Sprintf("%x-%d", final, len(orderedParts))
}

func (m *Mock) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	obj := &s3Object{
		Key:          key,
		Data:         dataCopy,
		Size:         int64(len(data)),
		ContentType:  contentType,
		ETag:         md5Hex(data),
		LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata:     maps.Clone(metadata),
	}
	m.storeObject(bkt, key, obj)

	// storeObject stamps obj.VersionID, so the engine ref matches what GetObject
	// (and GetObjectVersion for a versioned PUT) reads back.
	if err := m.engineStore(ctx, bucket, key, obj.VersionID, contentType, data, metadata); err != nil {
		return err
	}

	if m.opts.StorageEngine != nil {
		obj.Data = nil
	}

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

	// When an engine holds the bytes, the version record keeps only metadata +
	// size; its bytes live in the engine under the version id (dropData).
	dropData := m.opts.StorageEngine != nil

	switch bkt.versionStatus {
	case versioningEnabled:
		obj.VersionID = newVersionID()
		bkt.appendVersion(key, versionFromObject(obj, dropData))
	case versioningSuspended:
		obj.VersionID = nullVersionID
		bkt.replaceNullVersion(key, versionFromObject(obj, dropData))
	}

	bkt.objects.Set(key, obj)
}

func versionFromObject(obj *s3Object, dropData bool) *s3Version {
	data := obj.Data
	if dropData {
		data = nil
	}

	return &s3Version{
		versionID:    obj.VersionID,
		data:         data,
		size:         obj.Size,
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

func (m *Mock) GetObject(ctx context.Context, bucket, key string) (*driver.Object, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj, ok := bkt.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	data, err := m.engineLoad(ctx, config.StorageRef{Bucket: bucket, Key: key, Version: obj.VersionID}, obj.Data)
	if err != nil {
		return nil, err
	}

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("GetRequests", 1, "Count", dims)
	m.emitMetric("BytesDownloaded", float64(obj.Size), "Bytes", dims)

	return &driver.Object{
		Info: driver.ObjectInfo{
			Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType,
			ETag: obj.ETag, LastModified: obj.LastModified, Metadata: maps.Clone(obj.Metadata),
			VersionID: obj.VersionID,
		},
		Data: dataCopy,
	}, nil
}

func (m *Mock) DeleteObject(ctx context.Context, bucket, key string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	vid, deleteMarker, existed := m.deleteTopLevelLocked(bkt, key)
	bkt.versionsMu.Unlock()

	// On an unversioned bucket, deleting a missing key is NotFound (the wire
	// handler maps that to an idempotent 204). A versioned bucket always records
	// a delete marker, so existed is true there.
	if !existed {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	// Purge the removed object's bytes from the engine, keyed by the version that
	// held them: unversioned (vid "", deleteMarker false) and suspended (vid
	// "null", replacing the null object) both remove real bytes; Enabled only
	// appends a delete marker (a real new vid) while prior versions keep their
	// bytes, so it is skipped. Best-effort — the in-memory delete already
	// succeeded and byte cleanup must not fail an idempotent delete.
	if !deleteMarker || vid == nullVersionID {
		_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key, Version: vid})
	}

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("DeleteRequests", 1, "Count", dims)

	m.notifyObjectRemoved(bkt, bucket, key)

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
		Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType,
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
			Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType,
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

	dstObj := &s3Object{
		Key: dstKey, Data: dataCopy, Size: srcObj.Size, ContentType: srcObj.ContentType,
		ETag: srcObj.ETag, LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata: meta,
	}
	m.storeObject(dstBkt, dstKey, dstObj)

	if m.opts.StorageEngine != nil {
		if err := storageengine.Copy(ctx, m.opts.StorageEngine,
			config.StorageRef{Bucket: dstBucket, Key: dstKey, Version: dstObj.VersionID},
			config.StorageRef{Bucket: src.Bucket, Key: src.Key, Version: srcObj.VersionID}); err != nil {
			return err
		}

		dstObj.Data = nil
	}

	dims := map[string]string{"BucketName": dstBucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("CopyRequests", 1, "Count", dims)

	m.notifyObjectCreated(dstBkt, dstBucket, dstKey, srcObj.Size)

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

	etag := md5Hex(data)

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
			ETag:       md5Hex(data),
			Size:       int64(len(data)),
		})
	}

	return out, nil
}

func (m *Mock) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []driver.UploadPart) error {
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
		stored, exists := mp.parts[p.PartNumber]
		if !exists {
			mp.mu.Unlock()
			return cerrors.Newf(cerrors.InvalidArgument, "part %d not found in upload %q", p.PartNumber, uploadID)
		}

		// Real S3 validates each supplied part ETag against the stored part and
		// rejects a mismatch with InvalidPart. An empty client ETag skips the
		// check (some callers omit it); a non-empty one must match the stored
		// part's MD5, case-insensitively.
		if p.ETag != "" && !strings.EqualFold(strings.Trim(p.ETag, `"`), md5Hex(stored)) {
			mp.mu.Unlock()
			return cerrors.Newf(cerrors.InvalidArgument, "part %d ETag does not match the uploaded part in upload %q", p.PartNumber, uploadID)
		}
	}

	ordered := orderedPartData(mp.parts, parts)
	mp.mu.Unlock()

	var data []byte
	for _, p := range ordered {
		data = append(data, p...)
	}

	// storeObject (not a bare objects.Set) so a completed multipart upload is
	// versioned like a PutObject on a versioning-enabled bucket.
	obj := &s3Object{
		Key:          key,
		Data:         data,
		Size:         int64(len(data)),
		ContentType:  mp.contentType,
		ETag:         multipartETag(ordered),
		LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata:     make(map[string]string),
	}
	m.storeObject(bkt, key, obj)

	// Only the assembled object goes to the engine; the individual parts stay in
	// memory until the upload completes.
	if err := m.engineStore(ctx, bucket, key, obj.VersionID, mp.contentType, data, obj.Metadata); err != nil {
		return err
	}

	if m.opts.StorageEngine != nil {
		obj.Data = nil
	}

	bkt.multiparts.Delete(uploadID)

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("PutRequests", 1, "Count", dims)
	m.emitMetric("BytesUploaded", float64(len(data)), "Bytes", dims)

	m.notifyObjectCreated(bkt, bucket, key, int64(len(data)))

	return nil
}

// orderedPartData returns each requested part's bytes in ascending PartNumber
// order. S3 assembles (and hashes) parts by ascending PartNumber regardless of
// the order the client lists them in CompleteMultipartUpload; sorting keeps an
// out-of-order (or unsorted-SDK) Complete from corrupting the object or ETag.
func orderedPartData(allParts map[int][]byte, parts []driver.UploadPart) [][]byte {
	ordered := append([]driver.UploadPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PartNumber < ordered[j].PartNumber })

	out := make([][]byte, 0, len(ordered))
	for _, p := range ordered {
		out = append(out, allParts[p.PartNumber])
	}

	return out
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

	data, err := m.engineLoad(ctx, config.StorageRef{Bucket: bucket, Key: key, Version: versionID}, v.data)
	if err != nil {
		return nil, err
	}

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

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
func (m *Mock) DeleteObjectVersion(ctx context.Context, bucket, key, versionID string) (deletedID string, deleteMarker bool, err error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return "", false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	if versionID == "" {
		vid, marker, _ := m.deleteTopLevelLocked(bkt, key)
		_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key})

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

	_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key, Version: versionID})

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
					Size: obj.Size, ETag: obj.ETag,
					ContentType: obj.ContentType, LastModified: obj.LastModified,
				})
			}

			continue
		}

		for i := len(chain) - 1; i >= 0; i-- {
			v := chain[i]
			result.Versions = append(result.Versions, driver.ObjectVersion{
				Key: k, VersionID: v.versionID, IsLatest: i == len(chain)-1,
				DeleteMarker: v.deleteMarker, Size: v.size, ETag: v.etag,
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
		Key: key, Data: v.data, Size: v.size, ContentType: v.contentType,
		ETag: v.etag, LastModified: v.lastModified, Metadata: maps.Clone(v.metadata),
		VersionID: v.versionID,
	}
}

func infoFromVersion(key string, v *s3Version) driver.ObjectInfo {
	return driver.ObjectInfo{
		Key: key, Size: v.size, ContentType: v.contentType,
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

// PutBucketConfig stores an opaque bucket-configuration document (policy, cors,
// encryption, lifecycle, website, …) verbatim so GetBucketConfig can echo it.
func (m *Mock) PutBucketConfig(_ context.Context, bucket, name string, body []byte) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	stored := make([]byte, len(body))
	copy(stored, body)

	bkt.rawConfigMu.Lock()
	if bkt.rawConfigs == nil {
		bkt.rawConfigs = make(map[string][]byte)
	}

	bkt.rawConfigs[name] = stored
	bkt.rawConfigMu.Unlock()

	return nil
}

// GetBucketConfig returns a stored configuration document, or NotFound when the
// sub-resource was never configured.
func (m *Mock) GetBucketConfig(_ context.Context, bucket, name string) ([]byte, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.rawConfigMu.Lock()
	defer bkt.rawConfigMu.Unlock()

	body, ok := bkt.rawConfigs[name]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "no %s configuration for bucket %q", name, bucket)
	}

	out := make([]byte, len(body))
	copy(out, body)

	return out, nil
}

// DeleteBucketConfig removes a stored configuration document (idempotent).
func (m *Mock) DeleteBucketConfig(_ context.Context, bucket, name string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.rawConfigMu.Lock()
	delete(bkt.rawConfigs, name)
	bkt.rawConfigMu.Unlock()

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
