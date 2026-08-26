package gcs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// gcsSnapshot is the full serialized state of the GCS mock: every bucket keyed
// by name with its metadata, configuration sub-resources, and current objects.
// In-progress multipart uploads are transient and intentionally not captured.
type gcsSnapshot struct {
	Buckets map[string]*bucketSnapshot `json:"buckets,omitempty"`
}

type bucketSnapshot struct {
	Name           string                     `json:"name"`
	Region         string                     `json:"region,omitempty"`
	CreatedAt      string                     `json:"createdAt,omitempty"`
	Versioning     bool                       `json:"versioning,omitempty"`
	Lifecycle      *driver.LifecycleConfig    `json:"lifecycle,omitempty"`
	Policy         *driver.BucketPolicy       `json:"policy,omitempty"`
	CORS           *driver.CORSConfig         `json:"cors,omitempty"`
	Encryption     *driver.EncryptionConfig   `json:"encryption,omitempty"`
	Tags           map[string]string          `json:"tags,omitempty"`
	Location       string                     `json:"location,omitempty"`
	StorageClass   string                     `json:"storageClass,omitempty"`
	Metageneration int64                      `json:"metageneration,omitempty"`
	Updated        string                     `json:"updated,omitempty"`
	IAMPolicy      []byte                     `json:"iamPolicy,omitempty"`
	Objects        map[string]*objectSnapshot `json:"objects,omitempty"`
}

// objectSnapshot mirrors gcsObject (all exported already), but Data is dropped
// in a metadata-only (includeAssets=false) snapshot while Size is kept so the
// metadata stays correct.
type objectSnapshot struct {
	Key                string            `json:"key"`
	Data               []byte            `json:"data,omitempty"`
	Size               int64             `json:"size"`
	ContentType        string            `json:"contentType,omitempty"`
	ETag               string            `json:"etag,omitempty"`
	LastModified       string            `json:"lastModified,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	Generation         int64             `json:"generation,omitempty"`
	Metageneration     int64             `json:"metageneration,omitempty"`
	MD5                string            `json:"md5,omitempty"`
	CRC32C             string            `json:"crc32c,omitempty"`
	CacheControl       string            `json:"cacheControl,omitempty"`
	ContentEncoding    string            `json:"contentEncoding,omitempty"`
	ContentDisposition string            `json:"contentDisposition,omitempty"`
	ContentLanguage    string            `json:"contentLanguage,omitempty"`
	StorageClass       string            `json:"storageClass,omitempty"`
}

// Snapshot captures every bucket's state as JSON. When includeAssets is false
// the object bytes are omitted (metadata-only).
func (m *Mock) Snapshot(_ context.Context, includeAssets bool) (json.RawMessage, error) {
	snap := gcsSnapshot{Buckets: make(map[string]*bucketSnapshot, m.buckets.Len())}

	for name, bkt := range m.buckets.All() {
		snap.Buckets[name] = snapshotBucket(bkt, includeAssets)
	}

	return json.Marshal(snap)
}

func snapshotBucket(bkt *bucketMeta, includeAssets bool) *bucketSnapshot {
	bs := &bucketSnapshot{
		Name: bkt.Name, Region: bkt.Region, CreatedAt: bkt.CreatedAt,
		Versioning: bkt.versioning, Lifecycle: bkt.lifecycle, Policy: bkt.policy,
		CORS: bkt.corsConfig, Encryption: bkt.encryption, Tags: bkt.tags,
		Location: bkt.location, StorageClass: bkt.storageClass,
		Metageneration: bkt.metageneration, Updated: bkt.updated, IAMPolicy: bkt.iamPolicy,
		Objects: make(map[string]*objectSnapshot, bkt.objects.Len()),
	}

	for key, obj := range bkt.objects.All() {
		bs.Objects[key] = &objectSnapshot{
			Key: obj.Key, Data: assetBytes(obj.Data, includeAssets), Size: obj.Size,
			ContentType: obj.ContentType, ETag: obj.ETag, LastModified: obj.LastModified,
			Metadata: obj.Metadata, Tags: obj.Tags,
			Generation: obj.Generation, Metageneration: obj.Metageneration,
			MD5: obj.MD5, CRC32C: obj.CRC32C, CacheControl: obj.CacheControl,
			ContentEncoding: obj.ContentEncoding, ContentDisposition: obj.ContentDisposition,
			ContentLanguage: obj.ContentLanguage, StorageClass: obj.StorageClass,
		}
	}

	return bs
}

// assetBytes returns data only when assets are included, so metadata-only
// snapshots omit object bodies.
func assetBytes(data []byte, includeAssets bool) []byte {
	if !includeAssets {
		return nil
	}

	return data
}

// Restore rebuilds every bucket under its original name with its objects and
// configuration intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap gcsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("gcs: parse snapshot: %w", err)
	}

	for name, bs := range snap.Buckets {
		m.buckets.Set(name, restoreBucket(bs))
	}

	return nil
}

func restoreBucket(bs *bucketSnapshot) *bucketMeta {
	metagen := bs.Metageneration
	if metagen == 0 {
		metagen = 1
	}

	bkt := &bucketMeta{
		Name: bs.Name, Region: bs.Region, CreatedAt: bs.CreatedAt,
		objects:    memstore.New[*gcsObject](),
		multiparts: memstore.New[*gcsMultipartUpload](),
		versioning: bs.Versioning,
		lifecycle:  bs.Lifecycle, policy: bs.Policy, corsConfig: bs.CORS,
		encryption: bs.Encryption, tags: bs.Tags,
		location: bs.Location, storageClass: bs.StorageClass,
		metageneration: metagen, updated: bs.Updated, iamPolicy: bs.IAMPolicy,
		versions: make(map[string][]*gcsObject),
	}

	for key, os := range bs.Objects {
		bkt.objects.Set(key, &gcsObject{
			Key: os.Key, Data: os.Data, Size: os.Size, ContentType: os.ContentType,
			ETag: os.ETag, LastModified: os.LastModified, Metadata: os.Metadata, Tags: os.Tags,
			Generation: os.Generation, Metageneration: os.Metageneration,
			MD5: os.MD5, CRC32C: os.CRC32C, CacheControl: os.CacheControl,
			ContentEncoding: os.ContentEncoding, ContentDisposition: os.ContentDisposition,
			ContentLanguage: os.ContentLanguage, StorageClass: os.StorageClass,
		})
	}

	return bkt
}
