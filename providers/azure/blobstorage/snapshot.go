package blobstorage

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

// blobSnapshot is the full serialized state of the Azure Blob Storage mock:
// every container keyed by name, plus the account-level sub-resource stores.
// In-progress multipart uploads and uncommitted staged blocks are transient and
// intentionally not captured.
type blobSnapshot struct {
	Containers        map[string]*containerSnapshot `json:"containers,omitempty"`
	BucketAttrs       json.RawMessage               `json:"bucketAttrs,omitempty"`
	AccountKeys       json.RawMessage               `json:"accountKeys,omitempty"`
	BlobServiceProps  json.RawMessage               `json:"blobServiceProps,omitempty"`
	AccountEncryption json.RawMessage               `json:"accountEncryption,omitempty"`
}

type containerSnapshot struct {
	Name           string                         `json:"name"`
	Region         string                         `json:"region,omitempty"`
	CreatedAt      string                         `json:"createdAt,omitempty"`
	Versioning     bool                           `json:"versioning,omitempty"`
	Lifecycle      *driver.LifecycleConfig        `json:"lifecycle,omitempty"`
	Policy         *driver.BucketPolicy           `json:"policy,omitempty"`
	CORS           *driver.CORSConfig             `json:"cors,omitempty"`
	Encryption     *driver.EncryptionConfig       `json:"encryption,omitempty"`
	Tags           map[string]string              `json:"tags,omitempty"`
	Metadata       map[string]string              `json:"metadata,omitempty"`
	PublicAccess   string                         `json:"publicAccess,omitempty"`
	AccessPolicies []driver.SignedIdentifier      `json:"accessPolicies,omitempty"`
	SnapshotSeq    int                            `json:"snapshotSeq,omitempty"`
	VersionSeq     int                            `json:"versionSeq,omitempty"`
	Objects        map[string]*blobObjectSnapshot `json:"objects,omitempty"`
	Snapshots      map[string]*blobObjectSnapshot `json:"snapshots,omitempty"`
	Versions       map[string]*blobObjectSnapshot `json:"versions,omitempty"`
}

// blobObjectSnapshot mirrors blobObject, promoting its meaningful unexported
// fields (append count and the full Lease Blob state) to exported ones so they
// survive JSON. The mutex is deliberately excluded.
type blobObjectSnapshot struct {
	Key                   string             `json:"key"`
	Data                  []byte             `json:"data,omitempty"`
	Size                  int64              `json:"size"`
	ContentType           string             `json:"contentType,omitempty"`
	ETag                  string             `json:"etag,omitempty"`
	LastModified          string             `json:"lastModified,omitempty"`
	Metadata              map[string]string  `json:"metadata,omitempty"`
	Tags                  map[string]string  `json:"tags,omitempty"`
	BlobType              string             `json:"blobType,omitempty"`
	VersionID             string             `json:"versionId,omitempty"`
	AccessTier            string             `json:"accessTier,omitempty"`
	ContentEncoding       string             `json:"contentEncoding,omitempty"`
	ContentLanguage       string             `json:"contentLanguage,omitempty"`
	ContentDisposition    string             `json:"contentDisposition,omitempty"`
	CacheControl          string             `json:"cacheControl,omitempty"`
	CommittedBlocks       []driver.BlockInfo `json:"committedBlocks,omitempty"`
	AppendBlocks          int                `json:"appendBlocks,omitempty"`
	LeaseState            string             `json:"leaseState,omitempty"`
	LeaseID               string             `json:"leaseId,omitempty"`
	LeaseDurationSec      int32              `json:"leaseDurationSec,omitempty"`
	LeaseExpiresAt        time.Time          `json:"leaseExpiresAt,omitempty"`
	LeaseBreakAt          time.Time          `json:"leaseBreakAt,omitempty"`
	LeaseModTimeAtAcquire string             `json:"leaseModTimeAtAcquire,omitempty"`
}

// Snapshot captures every container's state as JSON. When includeAssets is false
// the blob bytes are omitted (metadata-only).
func (m *Mock) Snapshot(_ context.Context, includeAssets bool) (json.RawMessage, error) {
	snap := blobSnapshot{Containers: make(map[string]*containerSnapshot, m.containers.Len())}

	for name, c := range m.containers.All() {
		snap.Containers[name] = snapshotContainer(c, includeAssets)
	}

	if err := m.snapshotAccountStores(&snap); err != nil {
		return nil, err
	}

	return json.Marshal(snap)
}

func (m *Mock) snapshotAccountStores(snap *blobSnapshot) error {
	dumps := []struct {
		dst *json.RawMessage
		fn  func() ([]byte, error)
	}{
		{&snap.BucketAttrs, m.bucketAttrs.Snapshot},
		{&snap.AccountKeys, m.accountKeys.Snapshot},
		{&snap.BlobServiceProps, m.blobServiceProps.Snapshot},
		{&snap.AccountEncryption, m.accountEncryption.Snapshot},
	}

	for _, d := range dumps {
		b, err := d.fn()
		if err != nil {
			return fmt.Errorf("blobstorage: snapshot account store: %w", err)
		}

		*d.dst = b
	}

	return nil
}

func snapshotContainer(c *containerMeta, includeAssets bool) *containerSnapshot {
	cs := &containerSnapshot{
		Name: c.Name, Region: c.Region, CreatedAt: c.CreatedAt,
		Versioning: c.versioning, Lifecycle: c.lifecycle, Policy: c.policy,
		CORS: c.corsConfig, Encryption: c.encryption, Tags: c.tags,
		Metadata: c.metadata, PublicAccess: c.publicAccess, AccessPolicies: c.accessPolicies,
		Objects:   make(map[string]*blobObjectSnapshot, c.objects.Len()),
		Snapshots: make(map[string]*blobObjectSnapshot, c.snapshots.Len()),
		Versions:  make(map[string]*blobObjectSnapshot, c.versions.Len()),
	}

	c.mu.Lock()
	cs.SnapshotSeq = c.snapshotSeq
	cs.VersionSeq = c.versionSeq
	c.mu.Unlock()

	for key, obj := range c.objects.All() {
		cs.Objects[key] = snapshotBlob(obj, includeAssets)
	}

	for key, obj := range c.snapshots.All() {
		cs.Snapshots[key] = snapshotBlob(obj, includeAssets)
	}

	for key, obj := range c.versions.All() {
		cs.Versions[key] = snapshotBlob(obj, includeAssets)
	}

	return cs
}

func snapshotBlob(obj *blobObject, includeAssets bool) *blobObjectSnapshot {
	obj.mu.Lock()
	defer obj.mu.Unlock()

	data := obj.Data
	if !includeAssets {
		data = nil
	}

	return &blobObjectSnapshot{
		Key: obj.Key, Data: data, Size: obj.Size, ContentType: obj.ContentType,
		ETag: obj.ETag, LastModified: obj.LastModified, Metadata: obj.Metadata, Tags: obj.Tags,
		BlobType: obj.BlobType, VersionID: obj.VersionID, AccessTier: obj.AccessTier, ContentEncoding: obj.ContentEncoding,
		ContentLanguage: obj.ContentLanguage, ContentDisposition: obj.ContentDisposition,
		CacheControl: obj.CacheControl, CommittedBlocks: obj.CommittedBlocks, AppendBlocks: obj.appendBlocks,
		LeaseState: obj.leaseState, LeaseID: obj.leaseID, LeaseDurationSec: obj.leaseDurationSec,
		LeaseExpiresAt: obj.leaseExpiresAt, LeaseBreakAt: obj.leaseBreakAt,
		LeaseModTimeAtAcquire: obj.leaseModTimeAtAcquire,
	}
}

// Restore rebuilds every container under its original name with its blobs,
// snapshots, and configuration intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap blobSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("blobstorage: parse snapshot: %w", err)
	}

	for name, cs := range snap.Containers {
		m.containers.Set(name, restoreContainer(cs))
	}

	return m.restoreAccountStores(&snap)
}

func (m *Mock) restoreAccountStores(snap *blobSnapshot) error {
	loads := []struct {
		src json.RawMessage
		fn  func([]byte) error
	}{
		{snap.BucketAttrs, m.bucketAttrs.LoadSnapshot},
		{snap.AccountKeys, m.accountKeys.LoadSnapshot},
		{snap.BlobServiceProps, m.blobServiceProps.LoadSnapshot},
		{snap.AccountEncryption, m.accountEncryption.LoadSnapshot},
	}

	for _, l := range loads {
		if len(l.src) == 0 {
			continue
		}

		if err := l.fn(l.src); err != nil {
			return fmt.Errorf("blobstorage: restore account store: %w", err)
		}
	}

	return nil
}

func restoreContainer(cs *containerSnapshot) *containerMeta {
	c := &containerMeta{
		Name: cs.Name, Region: cs.Region, CreatedAt: cs.CreatedAt,
		objects:    memstore.New[*blobObject](),
		multiparts: memstore.New[*blobMultipartUpload](),
		versioning: cs.Versioning,
		lifecycle:  cs.Lifecycle, policy: cs.Policy, corsConfig: cs.CORS,
		encryption: cs.Encryption, tags: cs.Tags, metadata: cs.Metadata,
		publicAccess: cs.PublicAccess, accessPolicies: cs.AccessPolicies,
		staging:     memstore.New[*blockStaging](),
		snapshots:   memstore.New[*blobObject](),
		versions:    memstore.New[*blobObject](),
		snapshotSeq: cs.SnapshotSeq,
		versionSeq:  cs.VersionSeq,
	}

	for key, os := range cs.Objects {
		c.objects.Set(key, restoreBlob(os))
	}

	for key, os := range cs.Snapshots {
		c.snapshots.Set(key, restoreBlob(os))
	}

	for key, os := range cs.Versions {
		c.versions.Set(key, restoreBlob(os))
	}

	return c
}

func restoreBlob(os *blobObjectSnapshot) *blobObject {
	return &blobObject{
		Key: os.Key, Data: os.Data, Size: os.Size, ContentType: os.ContentType,
		ETag: os.ETag, LastModified: os.LastModified, Metadata: os.Metadata, Tags: os.Tags,
		BlobType: os.BlobType, VersionID: os.VersionID, AccessTier: os.AccessTier, ContentEncoding: os.ContentEncoding,
		ContentLanguage: os.ContentLanguage, ContentDisposition: os.ContentDisposition,
		CacheControl: os.CacheControl, CommittedBlocks: os.CommittedBlocks, appendBlocks: os.AppendBlocks,
		leaseState: os.LeaseState, leaseID: os.LeaseID, leaseDurationSec: os.LeaseDurationSec,
		leaseExpiresAt: os.LeaseExpiresAt, leaseBreakAt: os.LeaseBreakAt,
		leaseModTimeAtAcquire: os.LeaseModTimeAtAcquire,
	}
}
