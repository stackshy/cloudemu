package s3

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// s3Snapshot is the full serialized state of the S3 mock: every bucket keyed by
// name, plus the object-event sequence counter. In-progress multipart uploads
// are transient and intentionally not captured.
type s3Snapshot struct {
	Buckets  map[string]*bucketSnapshot `json:"buckets,omitempty"`
	EventSeq uint64                     `json:"eventSeq,omitempty"`
}

// bucketSnapshot captures a bucket's metadata, configuration sub-resources,
// current objects, and full version history. Object keys and version ids are
// preserved so a restore is transparent to clients.
type bucketSnapshot struct {
	Name          string                        `json:"name"`
	Region        string                        `json:"region,omitempty"`
	CreatedAt     string                        `json:"createdAt,omitempty"`
	VersionStatus string                        `json:"versionStatus,omitempty"`
	ObjectLock    bool                          `json:"objectLock,omitempty"`
	Tags          map[string]string             `json:"tags,omitempty"`
	Lifecycle     *driver.LifecycleConfig       `json:"lifecycle,omitempty"`
	Policy        *driver.BucketPolicy          `json:"policy,omitempty"`
	CORS          *driver.CORSConfig            `json:"cors,omitempty"`
	Encryption    *driver.EncryptionConfig      `json:"encryption,omitempty"`
	Notifications []BucketNotification          `json:"notifications,omitempty"`
	RawConfigs    map[string][]byte             `json:"rawConfigs,omitempty"`
	Objects       map[string]*objectSnapshot    `json:"objects,omitempty"`
	Versions      map[string][]*versionSnapshot `json:"versions,omitempty"`
}

// objectSnapshot is a current object. Data is omitted in a metadata-only
// (includeAssets=false) snapshot; Size is kept independently so metadata stays
// correct.
type objectSnapshot struct {
	Key          string            `json:"key"`
	Data         []byte            `json:"data,omitempty"`
	Size         int64             `json:"size"`
	ContentType  string            `json:"contentType,omitempty"`
	ETag         string            `json:"etag,omitempty"`
	LastModified string            `json:"lastModified,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	VersionID    string            `json:"versionId,omitempty"`
	Lock         *lockSnapshot     `json:"lock,omitempty"`
}

// lockSnapshot serializes an object version's S3 Object Lock state.
type lockSnapshot struct {
	RetentionMode string `json:"retentionMode,omitempty"`
	RetainUntil   string `json:"retainUntil,omitempty"`
	LegalHold     bool   `json:"legalHold,omitempty"`
}

// lockToSnapshot serializes an objectLock, returning nil when it is unset so
// unprotected objects add nothing to the snapshot.
func lockToSnapshot(l objectLock) *lockSnapshot {
	if l.retentionMode == "" && l.retainUntil.IsZero() && !l.legalHold {
		return nil
	}

	ls := &lockSnapshot{RetentionMode: l.retentionMode, LegalHold: l.legalHold}
	if !l.retainUntil.IsZero() {
		ls.RetainUntil = l.retainUntil.UTC().Format(time.RFC3339Nano)
	}

	return ls
}

// lockFromSnapshot rebuilds an objectLock from its serialized form.
func lockFromSnapshot(ls *lockSnapshot) objectLock {
	if ls == nil {
		return objectLock{}
	}

	l := objectLock{retentionMode: ls.RetentionMode, legalHold: ls.LegalHold}

	if ls.RetainUntil != "" {
		if t, err := time.Parse(time.RFC3339Nano, ls.RetainUntil); err == nil {
			l.retainUntil = t
		}
	}

	return l
}

// versionSnapshot is one entry in a key's version history (a stored version or a
// delete marker). Its fields mirror the unexported s3Version, since json.Marshal
// cannot see that type's unexported data.
type versionSnapshot struct {
	VersionID    string            `json:"versionId"`
	Data         []byte            `json:"data,omitempty"`
	Size         int64             `json:"size"`
	ContentType  string            `json:"contentType,omitempty"`
	ETag         string            `json:"etag,omitempty"`
	LastModified string            `json:"lastModified,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	DeleteMarker bool              `json:"deleteMarker,omitempty"`
	Lock         *lockSnapshot     `json:"lock,omitempty"`
}

// Snapshot captures every bucket's state as JSON. When includeAssets is false
// the object and version bytes are omitted (metadata-only), mirroring the
// persist default that keeps snapshot files small.
func (m *Mock) Snapshot(_ context.Context, includeAssets bool) (json.RawMessage, error) {
	snap := s3Snapshot{
		Buckets:  make(map[string]*bucketSnapshot, m.buckets.Len()),
		EventSeq: m.eventSeq.Load(),
	}

	for name, bkt := range m.buckets.All() {
		snap.Buckets[name] = snapshotBucket(bkt, includeAssets)
	}

	return json.Marshal(snap)
}

func snapshotBucket(bkt *bucketMeta, includeAssets bool) *bucketSnapshot {
	bs := &bucketSnapshot{
		Name: bkt.Name, Region: bkt.Region, CreatedAt: bkt.CreatedAt,
		Tags: bkt.tags, Lifecycle: bkt.lifecycle, Policy: bkt.policy,
		CORS: bkt.corsConfig, Encryption: bkt.encryption, Notifications: bkt.notifications,
		Objects: make(map[string]*objectSnapshot, bkt.objects.Len()),
	}

	for key, obj := range bkt.objects.All() {
		bs.Objects[key] = &objectSnapshot{
			Key: obj.Key, Data: assetBytes(obj.Data, includeAssets), Size: obj.Size,
			ContentType: obj.ContentType, ETag: obj.ETag, LastModified: obj.LastModified,
			Metadata: obj.Metadata, Tags: obj.Tags, VersionID: obj.VersionID,
			Lock: lockToSnapshot(obj.lock),
		}
	}

	snapshotBucketVersions(bkt, bs, includeAssets)
	snapshotBucketRawConfigs(bkt, bs)

	return bs
}

func snapshotBucketVersions(bkt *bucketMeta, bs *bucketSnapshot, includeAssets bool) {
	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	bs.VersionStatus = bkt.versionStatus
	bs.ObjectLock = bkt.objectLockEnabled

	if len(bkt.versions) == 0 {
		return
	}

	bs.Versions = make(map[string][]*versionSnapshot, len(bkt.versions))

	for key, chain := range bkt.versions {
		out := make([]*versionSnapshot, 0, len(chain))

		for _, v := range chain {
			out = append(out, &versionSnapshot{
				VersionID: v.versionID, Data: assetBytes(v.data, includeAssets), Size: v.size,
				ContentType: v.contentType, ETag: v.etag, LastModified: v.lastModified,
				Metadata: v.metadata, DeleteMarker: v.deleteMarker, Lock: lockToSnapshot(v.lock),
			})
		}

		bs.Versions[key] = out
	}
}

func snapshotBucketRawConfigs(bkt *bucketMeta, bs *bucketSnapshot) {
	bkt.rawConfigMu.Lock()
	defer bkt.rawConfigMu.Unlock()

	if len(bkt.rawConfigs) == 0 {
		return
	}

	bs.RawConfigs = make(map[string][]byte, len(bkt.rawConfigs))
	for k, v := range bkt.rawConfigs {
		bs.RawConfigs[k] = v
	}
}

// assetBytes returns data only when assets are included, so metadata-only
// snapshots omit object/version bodies.
func assetBytes(data []byte, includeAssets bool) []byte {
	if !includeAssets {
		return nil
	}

	return data
}

// Restore rebuilds every bucket under its original name with its objects,
// version history, and configuration intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap s3Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("s3: parse snapshot: %w", err)
	}

	m.eventSeq.Store(snap.EventSeq)

	for name, bs := range snap.Buckets {
		m.buckets.Set(name, restoreBucket(bs))
	}

	return nil
}

func restoreBucket(bs *bucketSnapshot) *bucketMeta {
	bkt := &bucketMeta{
		Name: bs.Name, Region: bs.Region, CreatedAt: bs.CreatedAt,
		objects:           memstore.New[*s3Object](),
		multiparts:        memstore.New[*multipartUpload](),
		objectLockEnabled: bs.ObjectLock,
		versionStatus:     bs.VersionStatus,
		lifecycle:         bs.Lifecycle, policy: bs.Policy, corsConfig: bs.CORS,
		encryption: bs.Encryption, tags: bs.Tags, notifications: bs.Notifications,
		rawConfigs: bs.RawConfigs,
	}

	for key, os := range bs.Objects {
		bkt.objects.Set(key, &s3Object{
			Key: os.Key, Data: os.Data, Size: os.Size, ContentType: os.ContentType,
			ETag: os.ETag, LastModified: os.LastModified, Metadata: os.Metadata,
			Tags: os.Tags, VersionID: os.VersionID, lock: lockFromSnapshot(os.Lock),
		})
	}

	restoreBucketVersions(bkt, bs)

	return bkt
}

func restoreBucketVersions(bkt *bucketMeta, bs *bucketSnapshot) {
	if len(bs.Versions) == 0 {
		return
	}

	bkt.versions = make(map[string][]*s3Version, len(bs.Versions))

	for key, chain := range bs.Versions {
		out := make([]*s3Version, 0, len(chain))

		for _, v := range chain {
			out = append(out, &s3Version{
				versionID: v.VersionID, data: v.Data, size: v.Size, contentType: v.ContentType,
				etag: v.ETag, lastModified: v.LastModified, metadata: v.Metadata, deleteMarker: v.DeleteMarker,
				lock: lockFromSnapshot(v.Lock),
			})
		}

		bkt.versions[key] = out
	}
}
