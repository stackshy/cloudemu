package objectstorage

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

// storageSnapshot is the full serialized state of the Object Storage mock:
// every bucket keyed by name. The namespace is derived from the tenancy rather
// than captured, so a restore into a differently configured emulator keeps that
// emulator's namespace instead of resurrecting a stale one.
type storageSnapshot struct {
	Buckets map[string]*bucketSnapshot `json:"buckets,omitempty"`
}

// bucketSnapshot captures a bucket's OCI settings, its current objects, its
// full version history, its pre-authenticated requests, its retention rules and
// its lifecycle policy. Object names, version ids, PAR OCIDs and redemption
// tokens are preserved, so an access URI issued before the snapshot still
// redeems after the restore. In-progress multipart uploads are transient and
// intentionally not captured, matching the other storage providers.
type bucketSnapshot struct {
	ID                  string                        `json:"id"`
	Name                string                        `json:"name"`
	CompartmentID       string                        `json:"compartmentId,omitempty"`
	CreatedBy           string                        `json:"createdBy,omitempty"`
	TimeCreated         string                        `json:"timeCreated,omitempty"`
	ETag                string                        `json:"etag,omitempty"`
	PublicAccessType    string                        `json:"publicAccessType,omitempty"`
	StorageTier         string                        `json:"storageTier,omitempty"`
	Versioning          string                        `json:"versioning,omitempty"`
	KMSKeyID            string                        `json:"kmsKeyId,omitempty"`
	AutoTiering         string                        `json:"autoTiering,omitempty"`
	ObjectEventsEnabled bool                          `json:"objectEventsEnabled,omitempty"`
	Metadata            map[string]string             `json:"metadata,omitempty"`
	FreeformTags        map[string]string             `json:"freeformTags,omitempty"`
	DefinedTags         map[string]map[string]string  `json:"definedTags,omitempty"`
	Lifecycle           *driver.LifecycleConfig       `json:"lifecycle,omitempty"`
	Objects             map[string]*objectSnapshot    `json:"objects,omitempty"`
	Versions            map[string][]*versionSnapshot `json:"versions,omitempty"`
	PARs                []*parSnapshot                `json:"pars,omitempty"`
	Retention           []*retentionSnapshot          `json:"retention,omitempty"`
}

// objectSnapshot is a current object. Data is omitted in a metadata-only
// (includeAssets=false) snapshot; Size is kept independently so Head and List
// stay correct without it.
type objectSnapshot struct {
	Name         string            `json:"name"`
	Data         []byte            `json:"data,omitempty"`
	Size         int64             `json:"size"`
	ContentType  string            `json:"contentType,omitempty"`
	ContentMD5   string            `json:"contentMd5,omitempty"`
	ETag         string            `json:"etag,omitempty"`
	TimeCreated  string            `json:"timeCreated,omitempty"`
	TimeModified string            `json:"timeModified,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	StorageTier  string            `json:"storageTier,omitempty"`
	VersionID    string            `json:"versionId,omitempty"`
}

// versionSnapshot is one entry in a name's version chain, oldest first. Its
// fields mirror the unexported objectVersion, which json.Marshal cannot see.
type versionSnapshot struct {
	VersionID    string            `json:"versionId"`
	Data         []byte            `json:"data,omitempty"`
	Size         int64             `json:"size"`
	ContentType  string            `json:"contentType,omitempty"`
	ContentMD5   string            `json:"contentMd5,omitempty"`
	ETag         string            `json:"etag,omitempty"`
	TimeModified string            `json:"timeModified,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	StorageTier  string            `json:"storageTier,omitempty"`
	DeleteMarker bool              `json:"deleteMarker,omitempty"`
}

// parSnapshot carries the redemption token as well as the OCID, so an access
// URI handed out before the snapshot still resolves afterwards.
type parSnapshot struct {
	ID                  string `json:"id"`
	Name                string `json:"name,omitempty"`
	Bucket              string `json:"bucket,omitempty"`
	ObjectName          string `json:"objectName,omitempty"`
	AccessType          string `json:"accessType,omitempty"`
	BucketListingAction string `json:"bucketListingAction,omitempty"`
	TimeCreated         string `json:"timeCreated,omitempty"`
	TimeExpires         string `json:"timeExpires,omitempty"`
	Token               string `json:"token,omitempty"`
}

type retentionSnapshot struct {
	ID             string             `json:"id"`
	DisplayName    string             `json:"displayName,omitempty"`
	Duration       *RetentionDuration `json:"duration,omitempty"`
	TimeRuleLocked string             `json:"timeRuleLocked,omitempty"`
	TimeCreated    string             `json:"timeCreated,omitempty"`
	TimeModified   string             `json:"timeModified,omitempty"`
	ETag           string             `json:"etag,omitempty"`
}

// Snapshot captures every bucket's state as JSON. When includeAssets is false
// the object and version bytes are omitted, mirroring the persist default that
// keeps snapshot files small.
func (m *Mock) Snapshot(_ context.Context, includeAssets bool) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := storageSnapshot{Buckets: make(map[string]*bucketSnapshot, m.buckets.Len())}

	for name, bkt := range m.buckets.All() {
		snap.Buckets[name] = snapshotBucket(bkt, includeAssets)
	}

	return json.Marshal(snap)
}

func snapshotBucket(bkt *bucketData, includeAssets bool) *bucketSnapshot {
	bs := &bucketSnapshot{
		ID: bkt.ID, Name: bkt.Name, CompartmentID: bkt.CompartmentID, CreatedBy: bkt.CreatedBy,
		TimeCreated: bkt.TimeCreated, ETag: bkt.ETag, PublicAccessType: bkt.PublicAccessType,
		StorageTier: bkt.StorageTier, Versioning: bkt.Versioning, KMSKeyID: bkt.KMSKeyID,
		AutoTiering: bkt.AutoTiering, ObjectEventsEnabled: bkt.ObjectEventsEnabled,
		Metadata: bkt.Metadata, FreeformTags: bkt.FreeformTags, DefinedTags: bkt.DefinedTags,
		Lifecycle: bkt.lifecycle,
		Objects:   make(map[string]*objectSnapshot, bkt.objects.Len()),
	}

	for name, obj := range bkt.objects.All() {
		bs.Objects[name] = &objectSnapshot{
			Name: obj.Name, Data: assetBytes(obj.Data, includeAssets), Size: obj.Size,
			ContentType: obj.ContentType, ContentMD5: obj.ContentMD5, ETag: obj.ETag,
			TimeCreated: obj.TimeCreated, TimeModified: obj.TimeModified,
			Metadata: obj.Metadata, StorageTier: obj.StorageTier, VersionID: obj.VersionID,
		}
	}

	snapshotVersions(bkt, bs, includeAssets)
	snapshotPARs(bkt, bs)
	snapshotRetention(bkt, bs)

	return bs
}

func snapshotVersions(bkt *bucketData, bs *bucketSnapshot, includeAssets bool) {
	if len(bkt.versions) == 0 {
		return
	}

	bs.Versions = make(map[string][]*versionSnapshot, len(bkt.versions))

	for name, chain := range bkt.versions {
		out := make([]*versionSnapshot, 0, len(chain))

		for _, v := range chain {
			out = append(out, &versionSnapshot{
				VersionID: v.versionID, Data: assetBytes(v.data, includeAssets), Size: v.size,
				ContentType: v.contentType, ContentMD5: v.contentMD5, ETag: v.etag,
				TimeModified: v.timeModified, Metadata: v.metadata,
				StorageTier: v.storageTier, DeleteMarker: v.deleteMarker,
			})
		}

		bs.Versions[name] = out
	}
}

func snapshotPARs(bkt *bucketData, bs *bucketSnapshot) {
	if bkt.pars.Len() == 0 {
		return
	}

	bs.PARs = make([]*parSnapshot, 0, bkt.pars.Len())

	for _, id := range bkt.pars.Keys() {
		par, ok := bkt.pars.Get(id)
		if !ok {
			continue
		}

		ps := &parSnapshot{
			ID: par.ID, Name: par.Name, Bucket: par.Bucket, ObjectName: par.ObjectName,
			AccessType: par.AccessType, BucketListingAction: par.BucketListingAction,
			TimeCreated: par.TimeCreated, Token: par.token,
		}

		if !par.TimeExpires.IsZero() {
			ps.TimeExpires = par.TimeExpires.UTC().Format(timeFormat)
		}

		bs.PARs = append(bs.PARs, ps)
	}
}

func snapshotRetention(bkt *bucketData, bs *bucketSnapshot) {
	if bkt.retention.Len() == 0 {
		return
	}

	bs.Retention = make([]*retentionSnapshot, 0, bkt.retention.Len())

	for _, id := range bkt.retention.Keys() {
		rule, ok := bkt.retention.Get(id)
		if !ok {
			continue
		}

		bs.Retention = append(bs.Retention, &retentionSnapshot{
			ID: rule.ID, DisplayName: rule.DisplayName, Duration: rule.Duration,
			TimeRuleLocked: rule.TimeRuleLocked, TimeCreated: rule.TimeCreated,
			TimeModified: rule.TimeModified, ETag: rule.ETag,
		})
	}
}

// assetBytes returns data only when assets are included, so a metadata-only
// snapshot omits object and version bodies.
func assetBytes(data []byte, includeAssets bool) []byte {
	if !includeAssets {
		return nil
	}

	return data
}

// Restore rebuilds every bucket under its original name, with its objects,
// version history, PARs, retention rules and lifecycle policy intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap storageSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("objectstorage: parse snapshot: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for name, bs := range snap.Buckets {
		m.buckets.Set(name, restoreBucket(bs, m.namespace))
	}

	return nil
}

func restoreBucket(bs *bucketSnapshot, namespace string) *bucketData {
	bkt := &bucketData{
		ID: bs.ID, Name: bs.Name, Namespace: namespace, CompartmentID: bs.CompartmentID,
		CreatedBy: bs.CreatedBy, TimeCreated: bs.TimeCreated, ETag: bs.ETag,
		PublicAccessType: bs.PublicAccessType, StorageTier: bs.StorageTier,
		Versioning: bs.Versioning, KMSKeyID: bs.KMSKeyID, AutoTiering: bs.AutoTiering,
		ObjectEventsEnabled: bs.ObjectEventsEnabled, Metadata: bs.Metadata,
		FreeformTags: bs.FreeformTags, DefinedTags: bs.DefinedTags,
		objects:    memstore.New[*objectData](),
		multiparts: memstore.New[*multipartUpload](),
		pars:       memstore.New[*parData](),
		retention:  memstore.New[*retentionRuleData](),
		lifecycle:  bs.Lifecycle,
	}

	for name, os := range bs.Objects {
		bkt.objects.Set(name, &objectData{
			Name: os.Name, Data: os.Data, Size: os.Size, ContentType: os.ContentType,
			ContentMD5: os.ContentMD5, ETag: os.ETag, TimeCreated: os.TimeCreated,
			TimeModified: os.TimeModified, Metadata: os.Metadata,
			StorageTier: os.StorageTier, VersionID: os.VersionID,
		})
	}

	restoreVersions(bkt, bs)
	restorePARs(bkt, bs)
	restoreRetention(bkt, bs)

	return bkt
}

func restoreVersions(bkt *bucketData, bs *bucketSnapshot) {
	if len(bs.Versions) == 0 {
		return
	}

	bkt.versions = make(map[string][]*objectVersion, len(bs.Versions))

	for name, chain := range bs.Versions {
		out := make([]*objectVersion, 0, len(chain))

		for _, v := range chain {
			out = append(out, &objectVersion{
				versionID: v.VersionID, data: v.Data, size: v.Size, contentType: v.ContentType,
				contentMD5: v.ContentMD5, etag: v.ETag, timeModified: v.TimeModified,
				metadata: v.Metadata, storageTier: v.StorageTier, deleteMarker: v.DeleteMarker,
			})
		}

		bkt.versions[name] = out
	}
}

func restorePARs(bkt *bucketData, bs *bucketSnapshot) {
	for _, ps := range bs.PARs {
		par := &parData{
			ID: ps.ID, Name: ps.Name, Bucket: ps.Bucket, ObjectName: ps.ObjectName,
			AccessType: ps.AccessType, BucketListingAction: ps.BucketListingAction,
			TimeCreated: ps.TimeCreated, token: ps.Token,
		}

		if ps.TimeExpires != "" {
			if t, err := time.Parse(timeFormat, ps.TimeExpires); err == nil {
				par.TimeExpires = t
			}
		}

		bkt.pars.Set(par.ID, par)
	}
}

func restoreRetention(bkt *bucketData, bs *bucketSnapshot) {
	for _, rs := range bs.Retention {
		bkt.retention.Set(rs.ID, &retentionRuleData{
			ID: rs.ID, DisplayName: rs.DisplayName, Duration: rs.Duration,
			TimeRuleLocked: rs.TimeRuleLocked, TimeCreated: rs.TimeCreated,
			TimeModified: rs.TimeModified, ETag: rs.ETag,
		})
	}
}
