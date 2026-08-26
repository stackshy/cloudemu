// Package gcs provides an in-memory mock implementation of Google Cloud Storage.
package gcs

import (
	"context"
	"crypto/md5" //nolint:gosec // GCS md5Hash is a content digest, not a security primitive
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
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
	gcsDefaultPresignExpiry = 15 * time.Minute
	gcsMaxPresignExpiry     = 7 * 24 * time.Hour
	gcsDefaultMaxKeys       = 1000
	gcsTimeFormat           = "2006-01-02T15:04:05Z"
	gcsHoursPerDay          = 24
	gcsDefaultStorageClass  = "STANDARD"
)

// Compile-time check that Mock implements driver.Bucket and the GCS-specific
// optional capability.
var (
	_ driver.Bucket        = (*Mock)(nil)
	_ driver.GCSExtensions = (*Mock)(nil)
)

type gcsObject struct {
	Key                string
	Data               []byte
	Size               int64
	ContentType        string
	ETag               string
	LastModified       string
	Created            string
	Metadata           map[string]string
	Tags               map[string]string
	Generation         int64
	Metageneration     int64
	MD5                string
	CRC32C             string
	CacheControl       string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	StorageClass       string
}

type gcsMultipartUpload struct {
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
	objects    *memstore.Store[*gcsObject]
	lifecycle  *driver.LifecycleConfig
	multiparts *memstore.Store[*gcsMultipartUpload]
	versioning bool
	policy     *driver.BucketPolicy
	corsConfig *driver.CORSConfig
	encryption *driver.EncryptionConfig
	tags       map[string]string

	// mu guards the GCS-specific mutable fields below (location/storageClass,
	// metageneration/updated, iamPolicy, and the archived version history).
	mu             sync.Mutex
	location       string
	storageClass   string
	metageneration int64
	updated        string
	iamPolicy      []byte
	// versions holds archived (non-current) object generations keyed by object
	// key, oldest-first, populated on overwrite when versioning is enabled.
	versions map[string][]*gcsObject
}

// Mock is an in-memory mock implementation of Google Cloud Storage.
type Mock struct {
	buckets    *memstore.Store[*bucketMeta]
	opts       *config.Options
	monitoring mondriver.Monitoring
	// gen is the source of unique, monotonically increasing object generations.
	gen atomic.Int64
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

	now := m.opts.Clock.Now().UTC().Format(gcsTimeFormat)

	m.buckets.Set(name, &bucketMeta{
		Name:           name,
		Region:         m.opts.Region,
		CreatedAt:      now,
		objects:        memstore.New[*gcsObject](),
		multiparts:     memstore.New[*gcsMultipartUpload](),
		metageneration: 1,
		updated:        now,
		versions:       make(map[string][]*gcsObject),
	})

	return nil
}

// checksums returns the base64-encoded MD5 (GCS md5Hash) and big-endian CRC32C
// (Castagnoli, GCS crc32c) of data.
func checksums(data []byte) (md5b64, crc32cb64 string) {
	sum := md5.Sum(data) //nolint:gosec // GCS md5Hash is a content digest, not security
	md5b64 = base64.StdEncoding.EncodeToString(sum[:])

	crc := crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))

	var b [crc32.Size]byte

	binary.BigEndian.PutUint32(b[:], crc)
	crc32cb64 = base64.StdEncoding.EncodeToString(b[:])

	return md5b64, crc32cb64
}

// toInfo projects a stored object into the driver's ObjectInfo. When cloneMeta
// is true the metadata map is deep-copied (callers that hand the info straight
// to an external consumer); list paths pass false and clone only the page.
func toInfo(o *gcsObject, cloneMeta bool) driver.ObjectInfo {
	meta := o.Metadata
	if cloneMeta {
		meta = maps.Clone(o.Metadata)
	}

	return driver.ObjectInfo{
		Key: o.Key, Size: o.Size, ContentType: o.ContentType, ETag: o.ETag,
		LastModified: o.LastModified, Created: o.Created, Metadata: meta,
		Generation: o.Generation, Metageneration: o.Metageneration,
		MD5: o.MD5, CRC32C: o.CRC32C,
		CacheControl: o.CacheControl, ContentEncoding: o.ContentEncoding,
		ContentDisposition: o.ContentDisposition, ContentLanguage: o.ContentLanguage,
		StorageClass: o.StorageClass,
	}
}

func (m *Mock) DeleteBucket(_ context.Context, name string) error {
	bkt, ok := m.buckets.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", name)
	}

	if bkt.objects.Len() > 0 || bucketHasNoncurrentVersions(bkt) {
		return cerrors.Newf(cerrors.FailedPrecondition, "bucket %q is not empty", name)
	}

	m.buckets.Delete(name)

	return nil
}

// bucketHasNoncurrentVersions reports whether any archived (noncurrent) object
// generation is still retained. Real GCS refuses to delete a bucket that holds
// noncurrent versions — not just live objects — with a 409 not-empty.
func bucketHasNoncurrentVersions(bkt *bucketMeta) bool {
	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	for _, versions := range bkt.versions {
		if len(versions) > 0 {
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

func (m *Mock) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string, metadata map[string]string) error {
	_, err := m.putObject(ctx, bucket, key, data, contentType, metadata, nil, driver.GCSPrecondition{})

	return err
}

// putObject is the shared write path behind PutObject and PutObjectGCS. It
// evaluates any preconditions, archives the current generation when versioning
// is enabled, mints a fresh generation, and returns the stored object's info. A
// non-nil attrs persists the insert-time system properties.
func (m *Mock) putObject(
	ctx context.Context, bucket, key string, data []byte, contentType string,
	metadata map[string]string, attrs *driver.GCSObjectAttrs, pre driver.GCSPrecondition,
) (*driver.ObjectInfo, error) {
	var oa driver.GCSObjectAttrs
	if attrs != nil {
		oa = *attrs
	}

	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	current, exists := bkt.objects.Get(key)
	if err := checkPrecondition(pre, current, exists); err != nil {
		return nil, err
	}

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	metaCopy := make(map[string]string, len(metadata))
	for k, v := range metadata {
		metaCopy[k] = v
	}

	stored := dataCopy

	if m.opts.StorageEngine != nil {
		err := storageengine.Put(ctx, m.opts.StorageEngine, config.StorageObject{
			Bucket: bucket, Key: key, Data: dataCopy, ContentType: contentType, Metadata: metaCopy,
		})
		if err != nil {
			return nil, err
		}

		stored = nil
	}

	archiveVersion(bkt, key, current, exists)

	md5b64, crc32cb64 := checksums(data)

	now := m.opts.Clock.Now().UTC().Format(gcsTimeFormat)

	obj := &gcsObject{
		Key:                key,
		Data:               stored,
		Size:               int64(len(data)),
		ContentType:        contentType,
		ETag:               fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified:       now,
		Created:            now,
		Metadata:           metaCopy,
		Generation:         m.gen.Add(1),
		Metageneration:     1,
		MD5:                md5b64,
		CRC32C:             crc32cb64,
		CacheControl:       oa.CacheControl,
		ContentEncoding:    oa.ContentEncoding,
		ContentDisposition: oa.ContentDisposition,
		ContentLanguage:    oa.ContentLanguage,
		StorageClass:       defaultObjectStorageClass(bkt, oa.StorageClass),
	}
	bkt.objects.Set(key, obj)

	dims := map[string]string{"bucket_name": bucket}
	m.emitMetric(ctx, "api/request_count", 1, dims)
	m.emitMetric(ctx, "network/received_bytes_count", float64(len(data)), dims)

	info := toInfo(obj, true)

	return &info, nil
}

// checkPrecondition evaluates GCS write preconditions against the object
// currently stored under the key, returning a *driver.GCSPreconditionError when
// a condition is not met.
func checkPrecondition(pre driver.GCSPrecondition, current *gcsObject, exists bool) error {
	var gen, metagen int64
	if exists {
		gen, metagen = current.Generation, current.Metageneration
	}

	if err := checkGenerationMatch(pre.IfGenerationMatch, gen, exists); err != nil {
		return err
	}

	if err := checkGenerationNotMatch(pre.IfGenerationNotMatch, gen, exists); err != nil {
		return err
	}

	return checkMetagenerationConditions(pre, metagen, exists)
}

func checkGenerationMatch(want *int64, gen int64, exists bool) error {
	if want == nil {
		return nil
	}

	if (*want == 0 && exists) || (*want != 0 && (!exists || gen != *want)) {
		return &driver.GCSPreconditionError{Message: "conditionNotMet: ifGenerationMatch"}
	}

	return nil
}

func checkGenerationNotMatch(want *int64, gen int64, exists bool) error {
	if want == nil {
		return nil
	}

	if (*want == 0 && !exists) || (exists && gen == *want) {
		return &driver.GCSPreconditionError{Message: "conditionNotMet: ifGenerationNotMatch"}
	}

	return nil
}

func checkMetagenerationConditions(pre driver.GCSPrecondition, metagen int64, exists bool) error {
	if want := pre.IfMetagenerationMatch; want != nil && (!exists || metagen != *want) {
		return &driver.GCSPreconditionError{Message: "conditionNotMet: ifMetagenerationMatch"}
	}

	if want := pre.IfMetagenerationNotMatch; want != nil && exists && metagen == *want {
		return &driver.GCSPreconditionError{Message: "conditionNotMet: ifMetagenerationNotMatch"}
	}

	return nil
}

// archiveVersion retains the current object generation when versioning is
// enabled, so a versions=true listing can still surface it after an overwrite.
func archiveVersion(bkt *bucketMeta, key string, current *gcsObject, exists bool) {
	if !exists || !bkt.versioning {
		return
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if bkt.versions == nil {
		bkt.versions = make(map[string][]*gcsObject)
	}

	bkt.versions[key] = append(bkt.versions[key], current)
}

// defaultObjectStorageClass resolves an object's storage class: the explicit
// class when supplied, else the bucket's default class, else STANDARD.
func defaultObjectStorageClass(bkt *bucketMeta, explicit string) string {
	if explicit != "" {
		return explicit
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if bkt.storageClass != "" {
		return bkt.storageClass
	}

	return gcsDefaultStorageClass
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

	dataCopy, err := m.objectBytes(ctx, bucket, obj)
	if err != nil {
		return nil, err
	}

	dims := map[string]string{"bucket_name": bucket}
	m.emitMetric(ctx, "api/request_count", 1, dims)
	m.emitMetric(ctx, "network/sent_bytes_count", float64(obj.Size), dims)

	return &driver.Object{
		Info: toInfo(obj, true),
		Data: dataCopy,
	}, nil
}

// objectBytes returns a copy of an object's bytes, loading them from the
// storage engine when one is wired and the in-memory copy was dropped.
func (m *Mock) objectBytes(ctx context.Context, bucket string, obj *gcsObject) ([]byte, error) {
	if m.opts.StorageEngine != nil && obj.Data == nil {
		data, ok, err := storageengine.Get(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: obj.Key})
		if err != nil {
			return nil, err
		}

		if ok {
			return data, nil
		}
	}

	dataCopy := make([]byte, len(obj.Data))
	copy(dataCopy, obj.Data)

	return dataCopy, nil
}

func (m *Mock) DeleteObject(ctx context.Context, bucket, key string) error {
	return m.deleteObject(ctx, bucket, key, nil, driver.GCSPrecondition{})
}

// deleteObject is the shared delete path. Preconditions are evaluated against
// the live object. A generation-addressed delete is always permanent; a live
// delete on a versioning-enabled bucket archives the current generation as
// noncurrent instead of removing it.
func (m *Mock) deleteObject(ctx context.Context, bucket, key string, generation *int64, pre driver.GCSPrecondition) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	current, exists := bkt.objects.Get(key)
	if err := checkPrecondition(pre, current, exists); err != nil {
		return err
	}

	if generation != nil {
		return m.deleteGeneration(ctx, bkt, bucket, key, *generation, current, exists)
	}

	if !exists {
		return cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	// Versioning-enabled live delete archives the current generation (it becomes
	// noncurrent) — real GCS retains it, listable via versions=true.
	if bkt.versioning {
		archiveVersion(bkt, key, current, true)
		bkt.objects.Delete(key)
		m.emitMetric(ctx, "api/request_count", 1, map[string]string{"bucket_name": bucket})

		return nil
	}

	bkt.objects.Delete(key)

	// Best-effort byte purge — the in-memory delete already succeeded, so a
	// backing cleanup failure must not fail an idempotent object delete.
	_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key})

	m.emitMetric(ctx, "api/request_count", 1, map[string]string{"bucket_name": bucket})

	return nil
}

// DeleteObjectGCS deletes an object honoring pre and optional generation
// addressing.
func (m *Mock) DeleteObjectGCS(ctx context.Context, bucket, key string, generation *int64, pre driver.GCSPrecondition) error {
	return m.deleteObject(ctx, bucket, key, generation, pre)
}

// deleteGeneration permanently removes one specific generation (live or
// archived) of a key. Deletions addressed by generation are never archived.
func (m *Mock) deleteGeneration(
	ctx context.Context, bkt *bucketMeta, bucket, key string, gen int64, current *gcsObject, liveExists bool,
) error {
	if liveExists && current.Generation == gen {
		bkt.objects.Delete(key)
		m.purgeIfGone(ctx, bkt, bucket, key)
		m.emitMetric(ctx, "api/request_count", 1, map[string]string{"bucket_name": bucket})

		return nil
	}

	bkt.mu.Lock()

	versions := bkt.versions[key]
	for i, v := range versions {
		if v.Generation != gen {
			continue
		}

		bkt.versions[key] = append(versions[:i], versions[i+1:]...)
		if len(bkt.versions[key]) == 0 {
			delete(bkt.versions, key)
		}

		bkt.mu.Unlock()
		m.purgeIfGone(ctx, bkt, bucket, key)
		m.emitMetric(ctx, "api/request_count", 1, map[string]string{"bucket_name": bucket})

		return nil
	}

	bkt.mu.Unlock()

	return cerrors.Newf(cerrors.NotFound, "object %q generation %d not found in bucket %q", key, gen, bucket)
}

// purgeIfGone drops the storage-engine bytes for a key only once no live or
// archived generation references it (the engine is keyed by bucket/key).
func (m *Mock) purgeIfGone(ctx context.Context, bkt *bucketMeta, bucket, key string) {
	if bkt.objects.Has(key) {
		return
	}

	bkt.mu.Lock()
	remaining := len(bkt.versions[key])
	bkt.mu.Unlock()

	if remaining > 0 {
		return
	}

	_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: bucket, Key: key})
}

// GetObjectGCS returns an object's bytes+info, selecting a specific generation
// when generation is non-nil (else the live object).
func (m *Mock) GetObjectGCS(ctx context.Context, bucket, key string, generation *int64) (*driver.Object, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj, ok := findGeneration(bkt, key, generation)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	dataCopy, err := m.objectBytes(ctx, bucket, obj)
	if err != nil {
		return nil, err
	}

	dims := map[string]string{"bucket_name": bucket}
	m.emitMetric(ctx, "api/request_count", 1, dims)
	m.emitMetric(ctx, "network/sent_bytes_count", float64(obj.Size), dims)

	return &driver.Object{Info: toInfo(obj, true), Data: dataCopy}, nil
}

// HeadObjectGCS returns an object's info, selecting a specific generation when
// generation is non-nil (else the live object).
func (m *Mock) HeadObjectGCS(_ context.Context, bucket, key string, generation *int64) (*driver.ObjectInfo, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	obj, ok := findGeneration(bkt, key, generation)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	info := toInfo(obj, true)

	return &info, nil
}

// findGeneration resolves a key to its live object (generation nil, or matching
// the live generation) or an archived generation.
func findGeneration(bkt *bucketMeta, key string, generation *int64) (*gcsObject, bool) {
	current, exists := bkt.objects.Get(key)
	if generation == nil {
		return current, exists
	}

	if exists && current.Generation == *generation {
		return current, true
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	for _, v := range bkt.versions[key] {
		if v.Generation == *generation {
			return v, true
		}
	}

	return nil, false
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

	info := toInfo(obj, true)

	return &info, nil
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

		matchedObjects = append(matchedObjects, toInfo(obj, false))
	}

	entries := foldedListEntries(matchedObjects, commonPrefixSet)

	maxKeys := opts.MaxKeys
	if maxKeys <= 0 {
		maxKeys = gcsDefaultMaxKeys
	}

	page, err := pagination.Paginate(entries, opts.PageToken, maxKeys)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	objects, prefixes := splitListPage(page.Items)

	m.emitMetric(ctx, "api/request_count", 1, map[string]string{"bucket_name": bucket})

	return &driver.ListResult{
		Objects:        objects,
		CommonPrefixes: prefixes,
		NextPageToken:  page.NextPageToken,
		IsTruncated:    page.HasMore,
	}, nil
}

// gcsListEntry is one item in a folded delimiter listing — either a matched
// object or a rolled-up common prefix — carrying the lexicographic sort key.
type gcsListEntry struct {
	name     string
	isPrefix bool
	obj      driver.ObjectInfo
}

// foldedListEntries merges the matched objects and rolled-up common prefixes
// into a single lexicographically sorted stream, so a delimiter listing counts
// each prefix toward maxResults and returns it on exactly one page (the page
// position is carried in the offset token). Prefixes are already deduped by the
// set, so each appears once.
func foldedListEntries(objects []driver.ObjectInfo, prefixSet map[string]struct{}) []gcsListEntry {
	entries := make([]gcsListEntry, 0, len(objects)+len(prefixSet))

	for i := range objects {
		entries = append(entries, gcsListEntry{name: objects[i].Key, obj: objects[i]})
	}

	for p := range prefixSet {
		entries = append(entries, gcsListEntry{name: p, isPrefix: true})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	return entries
}

// splitListPage separates a folded page back into object infos and common
// prefixes (order preserved). Object metadata is cloned only for the page
// actually returned — cloning every match would make a paged scan O(bucket)
// allocations per request.
func splitListPage(items []gcsListEntry) (objects []driver.ObjectInfo, prefixes []string) {
	for i := range items {
		if items[i].isPrefix {
			prefixes = append(prefixes, items[i].name)
			continue
		}

		info := items[i].obj
		info.Metadata = maps.Clone(info.Metadata)
		objects = append(objects, info)
	}

	return objects, prefixes
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

	stored := dataCopy

	if m.opts.StorageEngine != nil {
		err := storageengine.Copy(ctx, m.opts.StorageEngine,
			config.StorageRef{Bucket: dstBucket, Key: dstKey},
			config.StorageRef{Bucket: src.Bucket, Key: src.Key})
		if err != nil {
			return err
		}

		stored = nil
	}

	dstCurrent, dstExists := dstBkt.objects.Get(dstKey)
	archiveVersion(dstBkt, dstKey, dstCurrent, dstExists)

	copyNow := m.opts.Clock.Now().UTC().Format(gcsTimeFormat)

	dstBkt.objects.Set(dstKey, &gcsObject{
		Key: dstKey, Data: stored, Size: srcObj.Size, ContentType: srcObj.ContentType,
		ETag: srcObj.ETag, LastModified: copyNow, Created: copyNow,
		Metadata: meta, Generation: m.gen.Add(1), Metageneration: 1,
		MD5: srcObj.MD5, CRC32C: srcObj.CRC32C,
		CacheControl: srcObj.CacheControl, ContentEncoding: srcObj.ContentEncoding,
		ContentDisposition: srcObj.ContentDisposition, ContentLanguage: srcObj.ContentLanguage,
		StorageClass: defaultObjectStorageClass(dstBkt, srcObj.StorageClass),
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

	mp.mu.Lock()
	mp.parts[partNumber] = dataCopy
	mp.mu.Unlock()

	etag := fmt.Sprintf("%x", sha256.Sum256(data))

	return &driver.UploadPart{
		PartNumber: partNumber, ETag: etag, Size: int64(len(data)),
	}, nil
}

// ListParts returns the parts buffered so far for an in-progress upload,
// ordered by part number.
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

	mp.mu.Lock()
	for _, p := range parts {
		if _, exists := mp.parts[p.PartNumber]; !exists {
			mp.mu.Unlock()
			return cerrors.Newf(cerrors.InvalidArgument, "part %d not found in upload %q", p.PartNumber, uploadID)
		}
	}
	data := assembleGCSPartsInOrder(mp.parts, parts)
	mp.mu.Unlock()

	stored := data

	if m.opts.StorageEngine != nil {
		err := storageengine.Put(ctx, m.opts.StorageEngine, config.StorageObject{
			Bucket: bucket, Key: key, Data: data, ContentType: mp.contentType,
		})
		if err != nil {
			return err
		}

		stored = nil
	}

	md5b64, crc32cb64 := checksums(data)

	current, exists := bkt.objects.Get(key)
	archiveVersion(bkt, key, current, exists)

	completeNow := m.opts.Clock.Now().UTC().Format(gcsTimeFormat)

	bkt.objects.Set(key, &gcsObject{
		Key:            key,
		Data:           stored,
		Size:           int64(len(data)),
		ContentType:    mp.contentType,
		ETag:           fmt.Sprintf("%x", sha256.Sum256(data)),
		LastModified:   completeNow,
		Created:        completeNow,
		Metadata:       make(map[string]string),
		Generation:     m.gen.Add(1),
		Metageneration: 1,
		MD5:            md5b64,
		CRC32C:         crc32cb64,
		StorageClass:   defaultObjectStorageClass(bkt, ""),
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

// PutObjectTagging sets labels on an object.
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

// GetObjectTagging returns labels for an object.
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

// DeleteObjectTagging removes all labels from an object.
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

// PutBucketTagging sets labels on a bucket.
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

// GetBucketTagging returns labels for a bucket.
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

// DeleteBucketTagging removes all labels from a bucket.
func (m *Mock) DeleteBucketTagging(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.tags = nil

	return nil
}

// PutObjectGCS writes an object honoring GCS write preconditions and returns the
// stored object's info with the newly minted generation.
func (m *Mock) PutObjectGCS(
	ctx context.Context, bucket, key string, data []byte, contentType string,
	metadata map[string]string, attrs *driver.GCSObjectAttrs, pre driver.GCSPrecondition,
) (*driver.ObjectInfo, error) {
	return m.putObject(ctx, bucket, key, data, contentType, metadata, attrs, pre)
}

// UpdateObjectGCS mutates an existing object's system properties and/or custom
// metadata without touching its data, bumping metageneration. A failed pre
// returns a *driver.GCSPreconditionError.
func (m *Mock) UpdateObjectGCS(
	_ context.Context, bucket, key string, upd driver.GCSObjectUpdate, pre driver.GCSPrecondition,
) (*driver.ObjectInfo, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	cur, ok := bkt.objects.Get(key)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
	}

	if err := checkPrecondition(pre, cur, true); err != nil {
		return nil, err
	}

	next := *cur
	next.Metageneration = cur.Metageneration + 1
	// A metadata-only patch advances the update time but leaves the object's
	// creation time (timeCreated) fixed — real GCS bumps only `updated`.
	next.LastModified = m.opts.Clock.Now().UTC().Format(gcsTimeFormat)
	next.Metadata = mergeMetadata(cur.Metadata, upd.Metadata)

	applyStringUpdate(&next.ContentType, upd.ContentType)
	applyStringUpdate(&next.CacheControl, upd.CacheControl)
	applyStringUpdate(&next.ContentEncoding, upd.ContentEncoding)
	applyStringUpdate(&next.ContentDisposition, upd.ContentDisposition)
	applyStringUpdate(&next.ContentLanguage, upd.ContentLanguage)

	bkt.objects.Set(key, &next)

	info := toInfo(&next, true)

	return &info, nil
}

func applyStringUpdate(dst, v *string) {
	if v != nil {
		*dst = *v
	}
}

// mergeMetadata clones cur and applies patch: a nil-valued patch entry deletes
// the key, a non-nil value sets it. A nil patch map leaves metadata unchanged.
func mergeMetadata(cur map[string]string, patch map[string]*string) map[string]string {
	out := maps.Clone(cur)
	if out == nil {
		out = make(map[string]string)
	}

	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}

		out[k] = *v
	}

	return out
}

// ComposeObjectGCS concatenates the source objects' bytes (in order) into
// dstKey, minting a new generation for the destination and honoring the
// destination pre and each source's pinned generation.
func (m *Mock) ComposeObjectGCS(
	ctx context.Context, bucket, dstKey string, srcs []driver.GCSComposeSource,
	contentType string, metadata map[string]string, pre driver.GCSPrecondition,
) (*driver.ObjectInfo, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	var composed []byte

	for _, s := range srcs {
		srcObj, ok := findGeneration(bkt, s.Key, s.Generation)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "source object %q not found in bucket %q", s.Key, bucket)
		}

		bytesOf, err := m.objectBytes(ctx, bucket, srcObj)
		if err != nil {
			return nil, err
		}

		composed = append(composed, bytesOf...)

		if contentType == "" {
			contentType = srcObj.ContentType
		}
	}

	return m.putObject(ctx, bucket, dstKey, composed, contentType, metadata, nil, pre)
}

// ListObjectGenerations returns every generation (current + archived) of the
// objects matching opts, for a versions=true listing.
func (m *Mock) ListObjectGenerations(_ context.Context, bucket string, opts driver.ListOptions) (*driver.ListResult, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	all := collectAllVersions(bkt)

	var matched []driver.ObjectInfo

	for _, o := range all {
		if opts.Prefix != "" && !strings.HasPrefix(o.Key, opts.Prefix) {
			continue
		}

		matched = append(matched, toInfo(o, true))
	}

	maxKeys := opts.MaxKeys
	if maxKeys <= 0 {
		maxKeys = gcsDefaultMaxKeys
	}

	page, err := pagination.Paginate(matched, opts.PageToken, maxKeys)
	if err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid page token: %v", err)
	}

	return &driver.ListResult{
		Objects:       page.Items,
		NextPageToken: page.NextPageToken,
		IsTruncated:   page.HasMore,
	}, nil
}

// collectAllVersions returns archived-then-current generations for every key
// (including keys whose live generation was deleted but whose noncurrent
// generations are retained), sorted by (key, generation) so a versioned listing
// is deterministic.
func collectAllVersions(bkt *bucketMeta) []*gcsObject {
	liveKeys := bkt.objects.Keys()

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	seen := make(map[string]struct{}, len(liveKeys))

	var all []*gcsObject

	for _, k := range liveKeys {
		seen[k] = struct{}{}

		if archived := bkt.versions[k]; len(archived) > 0 {
			all = append(all, archived...)
		}

		if cur, ok := bkt.objects.Get(k); ok {
			all = append(all, cur)
		}
	}

	// Keys whose live generation was deleted keep their noncurrent generations
	// on a versioned bucket — real GCS still lists these under versions=true.
	for k, archived := range bkt.versions {
		if _, live := seen[k]; live {
			continue
		}

		all = append(all, archived...)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Key != all[j].Key {
			return all[i].Key < all[j].Key
		}

		return all[i].Generation < all[j].Generation
	})

	return all
}

// SetBucketAttrsGCS records the bucket's location and default storage class;
// empty values leave the current value unchanged.
func (m *Mock) SetBucketAttrsGCS(_ context.Context, bucket, location, storageClass string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if location != "" {
		bkt.location = location
	}

	if storageClass != "" {
		bkt.storageClass = storageClass
	}

	return nil
}

// BucketAttrsGCS returns the bucket's GCS-specific attributes.
func (m *Mock) BucketAttrsGCS(_ context.Context, bucket string) (driver.GCSBucketMeta, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return driver.GCSBucketMeta{}, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	return driver.GCSBucketMeta{
		Location:       bkt.location,
		StorageClass:   bkt.storageClass,
		Metageneration: bkt.metageneration,
		Updated:        bkt.updated,
	}, nil
}

// TouchBucket bumps the bucket's metageneration and updated timestamp.
func (m *Mock) TouchBucket(_ context.Context, bucket string) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	bkt.metageneration++
	bkt.updated = m.opts.Clock.Now().UTC().Format(gcsTimeFormat)

	return nil
}

// SetBucketIAMPolicy persists the bucket's IAM policy document verbatim.
func (m *Mock) SetBucketIAMPolicy(_ context.Context, bucket string, policyJSON []byte) error {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	bkt.iamPolicy = append([]byte(nil), policyJSON...)

	return nil
}

// BucketIAMPolicy returns the bucket's IAM policy document, or NotFound when
// none has been set.
func (m *Mock) BucketIAMPolicy(_ context.Context, bucket string) ([]byte, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.mu.Lock()
	defer bkt.mu.Unlock()

	if bkt.iamPolicy == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "no IAM policy set for bucket %q", bucket)
	}

	return append([]byte(nil), bkt.iamPolicy...), nil
}
