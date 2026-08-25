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
	Name       string                     `json:"name"`
	Region     string                     `json:"region,omitempty"`
	CreatedAt  string                     `json:"createdAt,omitempty"`
	Versioning bool                       `json:"versioning,omitempty"`
	Lifecycle  *driver.LifecycleConfig    `json:"lifecycle,omitempty"`
	Policy     *driver.BucketPolicy       `json:"policy,omitempty"`
	CORS       *driver.CORSConfig         `json:"cors,omitempty"`
	Encryption *driver.EncryptionConfig   `json:"encryption,omitempty"`
	Tags       map[string]string          `json:"tags,omitempty"`
	Objects    map[string]*objectSnapshot `json:"objects,omitempty"`
}

// objectSnapshot mirrors gcsObject (all exported already), but Data is dropped
// in a metadata-only (includeAssets=false) snapshot while Size is kept so the
// metadata stays correct.
type objectSnapshot struct {
	Key          string            `json:"key"`
	Data         []byte            `json:"data,omitempty"`
	Size         int64             `json:"size"`
	ContentType  string            `json:"contentType,omitempty"`
	ETag         string            `json:"etag,omitempty"`
	LastModified string            `json:"lastModified,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
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
		Objects: make(map[string]*objectSnapshot, bkt.objects.Len()),
	}

	for key, obj := range bkt.objects.All() {
		bs.Objects[key] = &objectSnapshot{
			Key: obj.Key, Data: assetBytes(obj.Data, includeAssets), Size: obj.Size,
			ContentType: obj.ContentType, ETag: obj.ETag, LastModified: obj.LastModified,
			Metadata: obj.Metadata, Tags: obj.Tags,
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
	bkt := &bucketMeta{
		Name: bs.Name, Region: bs.Region, CreatedAt: bs.CreatedAt,
		objects:    memstore.New[*gcsObject](),
		multiparts: memstore.New[*gcsMultipartUpload](),
		versioning: bs.Versioning,
		lifecycle:  bs.Lifecycle, policy: bs.Policy, corsConfig: bs.CORS,
		encryption: bs.Encryption, tags: bs.Tags,
	}

	for key, os := range bs.Objects {
		bkt.objects.Set(key, &gcsObject{
			Key: os.Key, Data: os.Data, Size: os.Size, ContentType: os.ContentType,
			ETag: os.ETag, LastModified: os.LastModified, Metadata: os.Metadata, Tags: os.Tags,
		})
	}

	return bkt
}
