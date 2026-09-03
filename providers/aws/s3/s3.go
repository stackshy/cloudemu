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
	"sync/atomic"
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
	_ driver.Bucket            = (*Mock)(nil)
	_ driver.VersionedBucket   = (*Mock)(nil)
	_ driver.RawBucketConfig   = (*Mock)(nil)
	_ driver.ObjectCopier      = (*Mock)(nil)
	_ driver.RegionalBucket    = (*Mock)(nil)
	_ driver.SystemPropsBucket = (*Mock)(nil)
	_ driver.ObjectLockBucket  = (*Mock)(nil)
)

// objectLock is the S3 Object Lock state carried by a single object version: a
// retention mode (GOVERNANCE/COMPLIANCE, empty when none) with its RetainUntil
// instant, and an independent legal-hold flag. The zero value is unprotected.
type objectLock struct {
	retentionMode string
	retainUntil   time.Time
	legalHold     bool
}

// protectsOverwrite reports whether the version's lock forbids destroying its
// bytes in place (a suspended/unversioned overwrite or a permanent delete before
// bypass is considered): legal hold ON, or a retention period not yet elapsed.
func (l objectLock) protectsOverwrite(now time.Time) bool {
	if l.legalHold {
		return true
	}

	return l.retentionMode != "" && now.Before(l.retainUntil)
}

// blocksDelete reports whether the version's lock forbids a permanent delete.
// Legal hold and an active COMPLIANCE retention always block; an active
// GOVERNANCE retention blocks unless bypassGovernance is set.
func (l objectLock) blocksDelete(now time.Time, bypassGovernance bool) bool {
	if l.legalHold {
		return true
	}

	if l.retentionMode == "" || !now.Before(l.retainUntil) {
		return false
	}

	if l.retentionMode == driver.ObjectLockGovernance && bypassGovernance {
		return false
	}

	return true
}

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
	// SystemProps holds the S3 system-defined object properties (Cache-Control,
	// Content-Encoding, Content-Disposition, Content-Language, Expires) and the
	// object's storage class, recorded on PutObject and echoed back on read.
	SystemProps driver.ObjectSystemProps
	// lock is the object's S3 Object Lock state (retention + legal hold) for the
	// current version; mirrors the latest version's lock on a versioned bucket.
	lock objectLock
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
	storageClass string
	// lock is the version's S3 Object Lock state (retention + legal hold).
	lock objectLock
}

type multipartUpload struct {
	id          string
	key         string
	contentType string
	// tags is the create-time object tag set (S3 x-amz-tagging on
	// CreateMultipartUpload), applied to the object when the upload completes.
	tags map[string]string
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
	// objectLockEnabled records whether the bucket was created with S3 Object
	// Lock enabled (x-amz-bucket-object-lock-enabled), which also forces
	// versioning on. Guarded by versionsMu (set alongside versionStatus).
	objectLockEnabled bool
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
	notifications []BucketNotification
	// rawConfigs holds opaque configuration sub-resource documents (policy, cors,
	// encryption, lifecycle, website, …) exactly as written, so the wire handler
	// can echo them back byte-for-byte. Guarded by rawConfigMu.
	rawConfigMu sync.Mutex
	rawConfigs  map[string][]byte
}

// Notification target kinds for a bucket-notification configuration.
const (
	NotifyQueue  = "queue"
	NotifyTopic  = "topic"
	NotifyLambda = "lambda"
)

// NotificationFilterRule is one S3Key name-filter rule of a bucket notification:
// Name is "prefix" or "suffix", Value the object-key fragment to match.
type NotificationFilterRule struct {
	Name  string
	Value string
}

// BucketNotification is one S3 bucket-notification target: an SQS queue, SNS
// topic, or Lambda function that receives events whose names match one of Events
// and whose object key satisfies every S3Key filter rule.
type BucketNotification struct {
	ID      string
	Target  string // NotifyQueue / NotifyTopic / NotifyLambda
	ARN     string
	Events  []string
	Filters []NotificationFilterRule
}

// SQSDeliverer delivers an S3 event notification into an SQS queue by ARN. The
// SQS mock satisfies this, enabling real S3 -> SQS event delivery.
type SQSDeliverer interface {
	DeliverExternal(ctx context.Context, queueARN, body string) error
}

// SNSPublisher publishes an S3 event notification to an SNS topic by ARN. The
// SNS mock satisfies this, enabling real S3 -> SNS event delivery.
type SNSPublisher interface {
	PublishExternal(ctx context.Context, topicARN, message string) error
}

// LambdaInvoker asynchronously invokes a Lambda function by ARN with an S3 event
// payload. The Lambda mock satisfies this, enabling real S3 -> Lambda delivery.
type LambdaInvoker interface {
	InvokeExternal(ctx context.Context, functionARN string, payload []byte) error
}

// Mock is an in-memory mock implementation of the AWS S3 service.
type Mock struct {
	buckets    *memstore.Store[*bucketMeta]
	opts       *config.Options
	monitoring mondriver.Monitoring
	sqs        SQSDeliverer
	sns        SNSPublisher
	lambda     LambdaInvoker
	eventSeq   atomic.Uint64 // monotonic source for object-event sequencer tokens
}

// SetSQSDeliverer wires the SQS backend so object events deliver to buckets' SQS
// notification targets.
func (m *Mock) SetSQSDeliverer(d SQSDeliverer) {
	m.sqs = d
}

// SetSNSPublisher wires the SNS backend so object events deliver to buckets' SNS
// topic notification targets.
func (m *Mock) SetSNSPublisher(p SNSPublisher) {
	m.sns = p
}

// SetLambdaInvoker wires the Lambda backend so object events invoke buckets'
// Lambda function notification targets.
func (m *Mock) SetLambdaInvoker(i LambdaInvoker) {
	m.lambda = i
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

func (m *Mock) CreateBucket(ctx context.Context, name string) error {
	return m.CreateBucketInRegion(ctx, name, "")
}

// CreateBucketInRegion creates a bucket, recording region when non-empty
// (S3 CreateBucketConfiguration.LocationConstraint). An empty region falls back
// to the mock's default region, so GetBucketLocation reports it back correctly.
func (m *Mock) CreateBucketInRegion(_ context.Context, name, region string) error {
	if name == "" {
		return cerrors.New(cerrors.InvalidArgument, "bucket name cannot be empty")
	}

	if m.buckets.Has(name) {
		return cerrors.Newf(cerrors.AlreadyExists, "bucket %q already exists", name)
	}

	if region == "" {
		region = m.opts.Region
	}

	m.buckets.Set(name, &bucketMeta{
		Name:       name,
		Region:     region,
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
	return m.PutObjectWithSystemProps(ctx, bucket, key, data, contentType, metadata, nil)
}

// PutObjectWithSystemProps implements driver.SystemPropsBucket: it writes the
// object like PutObject and additionally records the S3 system-defined object
// properties (Cache-Control, Content-Encoding, …) and storage class carried by
// props (nil = none), so a later Head/Get/List reflects them.
func (m *Mock) PutObjectWithSystemProps(
	ctx context.Context, bucket, key string, data []byte, contentType string,
	metadata map[string]string, props *driver.ObjectSystemProps,
) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj := m.newObject(key, data, contentType, metadata)
	if props != nil {
		obj.SystemProps = *props
	}

	if err := m.storeObject(bkt, key, obj); err != nil {
		return err
	}

	return m.afterPut(ctx, bkt, bucket, key, obj, data, contentType, metadata)
}

// PutObjectConditional implements driver.ConditionalBucket: it writes the object
// only if the If-None-Match / If-Match precondition holds, evaluating the guard
// and the store atomically under versionsMu so concurrent create-if-absent
// writers cannot both win. A failed precondition returns a FailedPrecondition
// error (mapped to 412 by the wire handler) and leaves any existing object
// untouched.
func (m *Mock) PutObjectConditional(
	ctx context.Context, bucket, key string, data []byte, contentType string,
	metadata map[string]string, pre driver.S3PutPrecondition,
) (*driver.ObjectInfo, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj := m.newObject(key, data, contentType, metadata)
	if err := m.storeObjectConditional(bkt, key, obj, pre); err != nil {
		return nil, err
	}

	if err := m.afterPut(ctx, bkt, bucket, key, obj, data, contentType, metadata); err != nil {
		return nil, err
	}

	return &driver.ObjectInfo{
		Key:          key,
		Size:         obj.Size,
		ContentType:  obj.ContentType,
		ETag:         obj.ETag,
		LastModified: obj.LastModified,
		Metadata:     obj.Metadata,
		VersionID:    obj.VersionID,
	}, nil
}

// newObject builds a fresh current-object record from a PutObject body, copying
// the bytes so a later caller mutation cannot alias stored data.
func (m *Mock) newObject(key string, data []byte, contentType string, metadata map[string]string) *s3Object {
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	return &s3Object{
		Key:          key,
		Data:         dataCopy,
		Size:         int64(len(data)),
		ContentType:  contentType,
		ETag:         md5Hex(data),
		LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata:     maps.Clone(metadata),
	}
}

// afterPut runs the side effects shared by every PutObject variant: persist the
// bytes to a configured storage engine, emit request metrics, and fire the
// s3:ObjectCreated notification. storeObject has already stamped obj.VersionID,
// so the engine ref matches what GetObject reads back.
func (m *Mock) afterPut(
	ctx context.Context, bkt *bucketMeta, bucket, key string, obj *s3Object,
	data []byte, contentType string, metadata map[string]string,
) error {
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

	m.notifyObjectCreated(ctx, bkt, bucket, key, int64(len(data)), obj.ETag, obj.VersionID)

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

// storeObject records key's current object under versionsMu (see
// storeObjectLocked for the versioning behavior).
func (m *Mock) storeObject(bkt *bucketMeta, key string, obj *s3Object) error {
	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	return m.storeObjectLocked(bkt, key, obj)
}

// storeObjectConditional evaluates an If-None-Match / If-Match precondition
// against the current object and, only if it holds, stores obj — both under a
// single versionsMu hold so the check and the write are atomic.
func (m *Mock) storeObjectConditional(bkt *bucketMeta, key string, obj *s3Object, pre driver.S3PutPrecondition) error {
	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	if err := evalPutPrecondition(bkt, key, pre); err != nil {
		return err
	}

	return m.storeObjectLocked(bkt, key, obj)
}

// storeObjectLocked records key's current object; the caller must hold
// versionsMu. On a versioned bucket it also appends/overwrites the version
// history (stamping obj.VersionID). Enabled appends a fresh version; Suspended
// overwrites the reusable "null" version; an unversioned bucket keeps no history.
//
// Object Lock: on a Suspended/unversioned bucket a write replaces the current
// object's bytes in place, so if that object is protected (legal hold or an
// unexpired retention) the overwrite is refused. On an Enabled bucket every
// write is a fresh version and the protected version is preserved, so no check
// is needed.
func (m *Mock) storeObjectLocked(bkt *bucketMeta, key string, obj *s3Object) error {
	// When an engine holds the bytes, the version record keeps only metadata +
	// size; its bytes live in the engine under the version id (dropData).
	dropData := m.opts.StorageEngine != nil

	switch bkt.versionStatus {
	case versioningEnabled:
		obj.VersionID = newVersionID()
		bkt.appendVersion(key, versionFromObject(obj, dropData))
	case versioningSuspended:
		if err := m.checkOverwriteLocked(bkt, key); err != nil {
			return err
		}

		obj.VersionID = nullVersionID
		bkt.replaceNullVersion(key, versionFromObject(obj, dropData))
	default:
		if err := m.checkOverwriteLocked(bkt, key); err != nil {
			return err
		}
	}

	bkt.objects.Set(key, obj)

	return nil
}

// checkOverwriteLocked refuses an in-place overwrite that would destroy a
// protected current object's bytes. Callers hold versionsMu.
func (m *Mock) checkOverwriteLocked(bkt *bucketMeta, key string) error {
	cur, ok := bkt.objects.Get(key)
	if !ok {
		return nil
	}

	if cur.lock.protectsOverwrite(m.opts.Clock.Now()) {
		return objectLockDeniedError()
	}

	return nil
}

// objectLockDeniedError is the error a protected-object delete/overwrite returns;
// the S3 wire layer renders it as 403 AccessDenied.
func objectLockDeniedError() error {
	return cerrors.New(cerrors.PermissionDenied,
		"Access Denied because object protected by object lock.")
}

// evalPutPrecondition reports whether a conditional PutObject may proceed. It
// runs under versionsMu, reading the current object (a delete marker leaves no
// current object, so the key reads as absent). If-None-Match: "*" requires the
// object be absent; a specific ETag requires no current object with that ETag;
// If-Match requires a current object whose ETag matches. A violated condition
// returns FailedPrecondition (mapped to 412 PreconditionFailed at the wire).
func evalPutPrecondition(bkt *bucketMeta, key string, pre driver.S3PutPrecondition) error {
	cur, exists := bkt.objects.Get(key)

	if pre.IfNoneMatch != "" {
		if pre.IfNoneMatch == "*" && exists {
			return failedPrecondition()
		}

		if pre.IfNoneMatch != "*" && exists && etagMatches(pre.IfNoneMatch, cur.ETag) {
			return failedPrecondition()
		}
	}

	if pre.IfMatch != "" && (!exists || !etagMatches(pre.IfMatch, cur.ETag)) {
		return failedPrecondition()
	}

	return nil
}

func failedPrecondition() error {
	return cerrors.New(cerrors.FailedPrecondition, "At least one of the preconditions you specified did not hold")
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
		storageClass: obj.SystemProps.StorageClass,
		lock:         obj.lock,
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

	info := driver.ObjectInfo{
		Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType,
		ETag: obj.ETag, LastModified: obj.LastModified, Metadata: maps.Clone(obj.Metadata),
		VersionID: obj.VersionID,
	}
	applyObjectSystemProps(&info, obj)

	return &driver.Object{Info: info, Data: dataCopy}, nil
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

	m.notifyObjectRemoved(ctx, bkt, bucket, key, vid, deleteMarker)

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

	info := &driver.ObjectInfo{
		Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType,
		ETag: obj.ETag, LastModified: obj.LastModified, Metadata: maps.Clone(obj.Metadata),
		VersionID: obj.VersionID,
	}
	applyObjectSystemProps(info, obj)

	return info, nil
}

// applyObjectSystemProps copies obj's S3 system-defined object properties and
// storage class onto info, so a Head/Get/List response reflects what PutObject
// recorded.
func applyObjectSystemProps(info *driver.ObjectInfo, obj *s3Object) {
	info.CacheControl = obj.SystemProps.CacheControl
	info.ContentEncoding = obj.SystemProps.ContentEncoding
	info.ContentDisposition = obj.SystemProps.ContentDisposition
	info.ContentLanguage = obj.SystemProps.ContentLanguage
	info.Expires = obj.SystemProps.Expires
	info.StorageClass = obj.SystemProps.StorageClass
}

func (m *Mock) ListObjects(_ context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	allKeys := bkt.objects.Keys()
	sort.Strings(allKeys)

	matchedObjects, commonPrefixSet := matchListKeys(bkt, allKeys, opts)

	maxKeys := opts.MaxKeys
	if maxKeys <= 0 {
		maxKeys = s3DefaultMaxKeys
	}

	// Real S3 caps object keys and rolled-up common prefixes JOINTLY by
	// MaxKeys over a single lexicographically-ordered stream: each prefix is
	// returned on exactly one page, and truncation carries an offset in the
	// continuation token. Merge the two into one ordered stream, paginate
	// that, then split the returned page back into keys and prefixes.
	entries := mergeListEntries(matchedObjects, commonPrefixSet)

	page, err := pagination.Paginate(entries, opts.PageToken, maxKeys)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	objects := make([]driver.ObjectInfo, 0, len(page.Items))
	commonPrefixes := make([]string, 0, len(page.Items))

	for i := range page.Items {
		e := &page.Items[i]
		if e.isPrefix {
			commonPrefixes = append(commonPrefixes, e.key)
			continue
		}

		// Clone metadata only for the page actually returned — cloning every
		// match would make a paged scan O(bucket) allocations per request.
		obj := e.obj
		obj.Metadata = maps.Clone(obj.Metadata)
		objects = append(objects, obj)
	}

	dims := map[string]string{"BucketName": bucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("ListRequests", 1, "Count", dims)

	return &driver.ListResult{
		Objects:        objects,
		CommonPrefixes: commonPrefixes,
		NextPageToken:  page.NextPageToken,
		IsTruncated:    page.HasMore,
	}, nil
}

// listEntry is one item in the combined listing stream: either an object key
// or a rolled-up common prefix. S3 caps keys and common prefixes jointly by
// MaxKeys over one lexicographic ordering, so pagination runs against the
// merged stream rather than over keys alone.
type listEntry struct {
	key      string
	isPrefix bool
	obj      driver.ObjectInfo
}

// mergeListEntries builds the single lexicographically-sorted stream of object
// keys and common prefixes. Keys are unique across the two sets — an object
// that rolls up into a prefix is excluded from matchedObjects — so the merged
// ordering is total and stable across paged calls, keeping offset tokens valid.
func mergeListEntries(objects []driver.ObjectInfo, prefixSet map[string]struct{}) []listEntry {
	entries := make([]listEntry, 0, len(objects)+len(prefixSet))

	for i := range objects {
		entries = append(entries, listEntry{key: objects[i].Key, obj: objects[i]})
	}

	for p := range prefixSet {
		entries = append(entries, listEntry{key: p, isPrefix: true})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	return entries
}

// matchListKeys applies the prefix, start-after, and delimiter filters to the
// sorted key set, returning the matched objects and the rolled-up common
// prefixes. start-after begins the listing strictly after the given key. It is
// applied unconditionally so the filtered array stays identical across a paged
// scan: our continuation is an offset into this array, and a real S3
// continuation always resumes past the start-after key anyway, so re-applying
// it on a resumed page is a correctness no-op that keeps the offset stable.
func matchListKeys(
	bkt *bucketMeta, allKeys []string, opts driver.ListOptions,
) (matchedObjects []driver.ObjectInfo, commonPrefixSet map[string]struct{}) {
	commonPrefixSet = make(map[string]struct{})

	for _, k := range allKeys {
		if opts.Prefix != "" && !strings.HasPrefix(k, opts.Prefix) {
			continue
		}

		if opts.StartAfter != "" && k <= opts.StartAfter {
			continue
		}

		if opts.Delimiter != "" {
			rest := k[len(opts.Prefix):]

			if idx := strings.Index(rest, opts.Delimiter); idx >= 0 {
				commonPrefixSet[opts.Prefix+rest[:idx+len(opts.Delimiter)]] = struct{}{}
				continue
			}
		}

		obj, objOk := bkt.objects.Get(k)
		if !objOk {
			continue
		}

		info := driver.ObjectInfo{
			Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType,
			ETag: obj.ETag, LastModified: obj.LastModified, Metadata: obj.Metadata,
		}
		applyObjectSystemProps(&info, obj)
		matchedObjects = append(matchedObjects, info)
	}

	return matchedObjects, commonPrefixSet
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
		Metadata: meta, Tags: maps.Clone(srcObj.Tags), SystemProps: srcObj.SystemProps,
	}

	if err := m.storeObject(dstBkt, dstKey, dstObj); err != nil {
		return err
	}

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

	m.notifyObjectCreated(ctx, dstBkt, dstBucket, dstKey, srcObj.Size, dstObj.ETag, dstObj.VersionID)

	return nil
}

// copySrcSnapshot is the resolved source object for a server-side copy.
type copySrcSnapshot struct {
	data         []byte
	size         int64
	contentType  string
	etag         string
	lastModified string
	metadata     map[string]string
	tags         map[string]string
	versionID    string
	systemProps  driver.ObjectSystemProps
}

// copyDstSystemProps resolves the destination object's system properties for a
// server-side copy: the content properties inherit from the source (default
// COPY directive) or come from the request (REPLACE), while the storage class
// always comes from the request's x-amz-storage-class (never inherited).
func copyDstSystemProps(req *driver.CopyObjectRequest, src *copySrcSnapshot) driver.ObjectSystemProps {
	props := src.systemProps
	if req.ReplaceMetadata {
		props = req.SystemProps
	}

	props.StorageClass = req.SystemProps.StorageClass

	return props
}

// CopyObjectV2 performs a full-fidelity S3 server-side copy: it honors a
// versioned source, the COPY/REPLACE metadata directive, and copy-source
// preconditions (a failed precondition aborts with FailedPrecondition; a
// delete-marker source version with InvalidArgument). The destination inherits
// the source ETag exactly (COPY) unless REPLACE supplies new metadata.
func (m *Mock) CopyObjectV2(ctx context.Context, req *driver.CopyObjectRequest) (*driver.CopyObjectResult, error) {
	src, err := m.resolveCopySource(ctx, req.Src, req.SrcVersionID)
	if err != nil {
		return nil, err
	}

	if err := checkCopyPreconditions(req, src); err != nil {
		return nil, err
	}

	dstBkt, ok := m.buckets.Get(req.DstBucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "destination bucket %q not found", req.DstBucket)
	}

	metadata, contentType := src.metadata, src.contentType
	if req.ReplaceMetadata {
		metadata = req.Metadata
		contentType = req.ContentType

		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	dataCopy := make([]byte, len(src.data))
	copy(dataCopy, src.data)

	// COPY tagging directive (the default) inherits the source object's tags;
	// REPLACE takes the request's x-amz-tagging tag set instead.
	tags := src.tags
	if req.ReplaceTags {
		tags = req.Tags
	}

	dstObj := &s3Object{
		Key: req.DstKey, Data: dataCopy, Size: src.size, ContentType: contentType,
		ETag: src.etag, LastModified: m.opts.Clock.Now().UTC().Format(s3TimeFormat),
		Metadata: maps.Clone(metadata), Tags: maps.Clone(tags), SystemProps: copyDstSystemProps(req, src),
	}

	if err := m.storeObject(dstBkt, req.DstKey, dstObj); err != nil {
		return nil, err
	}

	if err := m.engineStore(ctx, req.DstBucket, req.DstKey, dstObj.VersionID, contentType, dataCopy, metadata); err != nil {
		return nil, err
	}

	if m.opts.StorageEngine != nil {
		dstObj.Data = nil
	}

	dims := map[string]string{"BucketName": req.DstBucket}
	m.emitMetric("AllRequests", 1, "Count", dims)
	m.emitMetric("CopyRequests", 1, "Count", dims)

	m.notifyObjectCreated(ctx, dstBkt, req.DstBucket, req.DstKey, src.size, dstObj.ETag, dstObj.VersionID)

	return &driver.CopyObjectResult{
		ETag: dstObj.ETag, LastModified: dstObj.LastModified,
		VersionID: dstObj.VersionID, SourceVersionID: src.versionID,
	}, nil
}

// resolveCopySource loads the source object for a copy: a specific version when
// versionID is set (a delete-marker version is rejected with InvalidArgument),
// otherwise the current object.
func (m *Mock) resolveCopySource(ctx context.Context, src driver.CopySource, versionID string) (*copySrcSnapshot, error) {
	srcBkt, ok := m.buckets.Get(src.Bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "source bucket %q not found", src.Bucket)
	}

	if versionID != "" {
		return m.resolveCopySourceVersion(ctx, srcBkt, src, versionID)
	}

	srcObj, ok := srcBkt.objects.Get(src.Key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "source object %q not found", src.Key)
	}

	data, err := m.engineLoad(ctx, config.StorageRef{Bucket: src.Bucket, Key: src.Key, Version: srcObj.VersionID}, srcObj.Data)
	if err != nil {
		return nil, err
	}

	return &copySrcSnapshot{
		data: data, size: srcObj.Size, contentType: srcObj.ContentType, etag: srcObj.ETag,
		lastModified: srcObj.LastModified, metadata: srcObj.Metadata, tags: srcObj.Tags, versionID: srcObj.VersionID,
		systemProps: srcObj.SystemProps,
	}, nil
}

func (m *Mock) resolveCopySourceVersion(
	ctx context.Context, srcBkt *bucketMeta, src driver.CopySource, versionID string,
) (*copySrcSnapshot, error) {
	srcBkt.versionsMu.Lock()
	v := findVersion(srcBkt, src.Key, versionID)
	srcBkt.versionsMu.Unlock()

	if v == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "source version %q of %q not found", versionID, src.Key)
	}

	if v.deleteMarker {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "source version %q of %q is a delete marker", versionID, src.Key)
	}

	data, err := m.engineLoad(ctx, config.StorageRef{Bucket: src.Bucket, Key: src.Key, Version: versionID}, v.data)
	if err != nil {
		return nil, err
	}

	return &copySrcSnapshot{
		data: data, size: v.size, contentType: v.contentType, etag: v.etag,
		lastModified: v.lastModified, metadata: v.metadata, versionID: versionID,
		systemProps: driver.ObjectSystemProps{StorageClass: v.storageClass},
	}, nil
}

// checkCopyPreconditions evaluates the four x-amz-copy-source-if-* headers
// against the source, returning FailedPrecondition when one is not satisfied.
//
// AWS's CopyObject API reference documents a combined-precedence override: when
// both x-amz-copy-source-if-match and x-amz-copy-source-if-unmodified-since are
// present and if-match evaluates true, S3 returns 200 OK and copies the data
// even if if-unmodified-since evaluates false. if-match therefore overrides the
// if-unmodified-since result rather than the two being independently ANDed.
func checkCopyPreconditions(req *driver.CopyObjectRequest, src *copySrcSnapshot) error {
	etag := strings.Trim(src.etag, `"`)
	skipUnmodified := req.IfMatch != "" && !req.IfUnmodifiedSince.IsZero() && etagMatches(req.IfMatch, etag)

	if err := checkCopyETagPreconditions(req, src.etag); err != nil {
		return err
	}

	return checkCopyTimePreconditions(req, src.lastModified, skipUnmodified)
}

func checkCopyETagPreconditions(req *driver.CopyObjectRequest, etag string) error {
	etag = strings.Trim(etag, `"`)

	if req.IfMatch != "" && !etagMatches(req.IfMatch, etag) {
		return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-match precondition failed")
	}

	if req.IfNoneMatch != "" && etagMatches(req.IfNoneMatch, etag) {
		return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-none-match precondition failed")
	}

	return nil
}

// checkCopyTimePreconditions evaluates the two time-based copy-source headers.
// skipUnmodified suppresses the if-unmodified-since check when a true if-match
// header has taken precedence over it (see checkCopyPreconditions).
func checkCopyTimePreconditions(req *driver.CopyObjectRequest, lastModified string, skipUnmodified bool) error {
	if req.IfUnmodifiedSince.IsZero() && req.IfModifiedSince.IsZero() {
		return nil
	}

	mod, err := time.Parse(s3TimeFormat, lastModified)
	if err != nil {
		return nil // an unparseable timestamp can't be evaluated; do not block the copy
	}

	if !skipUnmodified && !req.IfUnmodifiedSince.IsZero() && mod.After(req.IfUnmodifiedSince) {
		return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-unmodified-since precondition failed")
	}

	if !req.IfModifiedSince.IsZero() && !mod.After(req.IfModifiedSince) {
		return cerrors.New(cerrors.FailedPrecondition, "x-amz-copy-source-if-modified-since precondition failed")
	}

	return nil
}

// etagMatches reports whether an If-[None-]Match header value matches the
// object ETag: "*" matches any object; otherwise a quote-insensitive,
// case-insensitive comparison.
func etagMatches(header, etag string) bool {
	header = strings.Trim(header, `"`)

	return header == "*" || strings.EqualFold(header, etag)
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

func (m *Mock) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string) (*driver.MultipartUpload, error) {
	return m.CreateMultipartUploadWithTagging(ctx, bucket, key, contentType, nil)
}

// CreateMultipartUploadWithTagging begins a multipart upload carrying the
// create-time x-amz-tagging tag set, applied to the object on completion.
func (m *Mock) CreateMultipartUploadWithTagging(
	_ context.Context, bucket, key, contentType string, tags map[string]string,
) (*driver.MultipartUpload, error) {
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
		tags:        maps.Clone(tags),
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
		Tags:         maps.Clone(mp.tags),
	}

	if err := m.storeObject(bkt, key, obj); err != nil {
		return err
	}

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

	m.notifyObjectCreated(ctx, bkt, bucket, key, int64(len(data)), obj.ETag, obj.VersionID)

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

	if v == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
	}

	if v.deleteMarker {
		return nil, &driver.DeleteMarkerError{LastModified: v.lastModified}
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

	if v == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
	}

	if v.deleteMarker {
		return nil, &driver.DeleteMarkerError{LastModified: v.lastModified}
	}

	info := infoFromVersion(key, v)

	return &info, nil
}

// DeleteObjectVersion removes a specific version, or (versionID == "") performs
// a top-level delete (delete marker on Enabled). Returns the affected version
// id and whether it was/created a delete marker. Object Lock protection is
// enforced: a protected version cannot be permanently removed (a top-level
// delete still records a delete marker, leaving protected versions intact).
func (m *Mock) DeleteObjectVersion(ctx context.Context, bucket, key, versionID string) (deletedID string, deleteMarker bool, err error) {
	return m.deleteObjectVersion(ctx, bucket, key, versionID, false)
}

// DeleteObjectVersionWithBypass implements driver.ObjectLockBucket: like
// DeleteObjectVersion, but bypassGovernance lifts a GOVERNANCE retention block
// (never COMPLIANCE, never a legal hold).
func (m *Mock) DeleteObjectVersionWithBypass(
	ctx context.Context, bucket, key, versionID string, bypassGovernance bool,
) (deletedID string, deleteMarker bool, err error) {
	return m.deleteObjectVersion(ctx, bucket, key, versionID, bypassGovernance)
}

func (m *Mock) deleteObjectVersion(
	ctx context.Context, bucket, key, versionID string, bypassGovernance bool,
) (deletedID string, deleteMarker bool, err error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return "", false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	if versionID == "" {
		vid, marker, existed := m.deleteTopLevelLocked(bkt, key)
		if !existed {
			// Unversioned bucket, key never existed: a no-op idempotent delete,
			// matching real S3 — nothing was removed, so no event fires.
			return "", false, nil
		}

		_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key})

		m.notifyObjectRemoved(ctx, bkt, bucket, key, vid, marker)

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

	// Object Lock: a protected version cannot be permanently removed.
	if removed.lock.blocksDelete(m.opts.Clock.Now(), bypassGovernance) {
		return "", false, objectLockDeniedError()
	}

	bkt.versions[key] = append(chain[:idx], chain[idx+1:]...)
	if len(bkt.versions[key]) == 0 {
		delete(bkt.versions, key)
	}

	m.recomputeCurrentLocked(bkt, key)

	_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key, Version: versionID})

	// An explicit versionId always permanently removes that version — even when
	// the removed version was itself a delete marker — so this always fires
	// ObjectRemoved:Delete, never DeleteMarkerCreated.
	m.notifyObjectRemoved(ctx, bkt, bucket, key, versionID, false)

	return versionID, removed.deleteMarker, nil
}

// ObjectLockEnabled implements driver.ObjectLockBucket: reports whether the
// bucket was created with Object Lock enabled.
func (m *Mock) ObjectLockEnabled(_ context.Context, bucket string) (bool, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	return bkt.objectLockEnabled, nil
}

// EnableObjectLock implements driver.ObjectLockBucket: marks the bucket
// Object-Lock-enabled and turns versioning on (Object Lock requires it).
func (m *Mock) EnableObjectLock(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	bkt.objectLockEnabled = true
	bkt.versionStatus = versioningEnabled

	return nil
}

// currentVersionLocked returns key's current (latest non-delete-marker) version,
// or nil when the newest version is a delete marker or no history exists.
// Callers hold versionsMu.
func currentVersionLocked(bkt *bucketMeta, key string) *s3Version {
	chain := bkt.versions[key]
	if len(chain) == 0 {
		return nil
	}

	latest := chain[len(chain)-1]
	if latest.deleteMarker {
		return nil
	}

	return latest
}

// lockTargetLocked resolves the lock state of the version a retention/legal-hold
// op addresses (the current version when versionID==""), returning a pointer to
// mutate and whether the target lives in the version history (so the caller can
// refresh the current object). Callers hold versionsMu.
func lockTargetLocked(bkt *bucketMeta, key, versionID string) (lock *objectLock, inHistory bool, err error) {
	if versionID != "" {
		if v := findVersion(bkt, key, versionID); v != nil && !v.deleteMarker {
			return &v.lock, true, nil
		}

		return nil, false, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
	}

	if v := currentVersionLocked(bkt, key); v != nil {
		return &v.lock, true, nil
	}

	if obj, ok := bkt.objects.Get(key); ok {
		return &obj.lock, false, nil
	}

	return nil, false, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bkt.Name)
}

// GetObjectRetention implements driver.ObjectLockBucket.
func (m *Mock) GetObjectRetention(_ context.Context, bucket, key, versionID string) (driver.ObjectRetention, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return driver.ObjectRetention{}, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	lock, _, err := lockTargetLocked(bkt, key, versionID)
	if err != nil {
		return driver.ObjectRetention{}, err
	}

	return driver.ObjectRetention{Mode: lock.retentionMode, RetainUntilDate: lock.retainUntil}, nil
}

// PutObjectRetention implements driver.ObjectLockBucket. First-setting or
// extending a retention is always allowed; shortening, removing, or downgrading
// an active GOVERNANCE retention requires bypassGovernance, and an active
// COMPLIANCE retention can never be shortened, removed, or downgraded.
func (m *Mock) PutObjectRetention(
	_ context.Context, bucket, key, versionID string, ret driver.ObjectRetention, bypassGovernance bool,
) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	lock, inHistory, err := lockTargetLocked(bkt, key, versionID)
	if err != nil {
		return err
	}

	existing := driver.ObjectRetention{Mode: lock.retentionMode, RetainUntilDate: lock.retainUntil}
	if !retentionChangeAllowed(existing, ret, m.opts.Clock.Now(), bypassGovernance) {
		return objectLockDeniedError()
	}

	lock.retentionMode = ret.Mode
	lock.retainUntil = ret.RetainUntilDate

	if inHistory {
		m.recomputeCurrentLocked(bkt, key)
	}

	return nil
}

// retentionChangeAllowed applies the S3 Object Lock retention-modification rules.
func retentionChangeAllowed(existing, next driver.ObjectRetention, now time.Time, bypassGovernance bool) bool {
	// No active retention (unset or already elapsed): any change is allowed.
	if existing.Mode == "" || !now.Before(existing.RetainUntilDate) {
		return true
	}

	// Extending in the same mode is always allowed.
	if next.Mode == existing.Mode && !next.RetainUntilDate.Before(existing.RetainUntilDate) {
		return true
	}

	// Any shorten/remove/downgrade of an active retention: COMPLIANCE never,
	// GOVERNANCE only with bypass.
	return existing.Mode == driver.ObjectLockGovernance && bypassGovernance
}

// GetObjectLegalHold implements driver.ObjectLockBucket.
func (m *Mock) GetObjectLegalHold(_ context.Context, bucket, key, versionID string) (bool, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	lock, _, err := lockTargetLocked(bkt, key, versionID)
	if err != nil {
		return false, err
	}

	return lock.legalHold, nil
}

// PutObjectLegalHold implements driver.ObjectLockBucket.
func (m *Mock) PutObjectLegalHold(_ context.Context, bucket, key, versionID string, on bool) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	lock, inHistory, err := lockTargetLocked(bkt, key, versionID)
	if err != nil {
		return err
	}

	lock.legalHold = on

	if inHistory {
		m.recomputeCurrentLocked(bkt, key)
	}

	return nil
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
					StorageClass: obj.SystemProps.StorageClass,
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
				StorageClass: v.storageClass,
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
		VersionID: v.versionID, lock: v.lock,
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
