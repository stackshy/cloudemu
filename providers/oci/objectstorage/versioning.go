package objectstorage

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
)

// newVersionID mints an object version id. OCI version ids are opaque.
func newVersionID() string { return idgen.GenerateID("") }

// setVersioningLocked applies a versioning state to a bucket. Enabling it
// seeds every current object that has no history yet into its chain, so the
// first overwrite after enabling keeps the original as a prior version.
// Callers hold mu for writing.
func (m *Mock) setVersioningLocked(ctx context.Context, bucket string, bkt *bucketData, status string) error {
	bkt.Versioning = status

	if status != VersioningEnabled {
		return nil
	}

	if bkt.versions == nil {
		bkt.versions = make(map[string][]*objectVersion)
	}

	for _, name := range bkt.objects.Keys() {
		obj, ok := bkt.objects.Get(name)
		if !ok || len(bkt.versions[name]) > 0 {
			continue
		}

		if err := m.assignVersionLocked(ctx, bucket, obj); err != nil {
			return err
		}

		appendVersion(bkt, name, versionOf(obj))
	}

	return nil
}

// assignVersionLocked gives a pre-versioning object a version id, moving its
// engine bytes to the versioned reference. Callers hold mu for writing.
func (m *Mock) assignVersionLocked(ctx context.Context, bucket string, obj *objectData) error {
	prev := obj.VersionID
	next := newVersionID()

	if m.engineWired() {
		if err := storageengine.Copy(ctx, m.opts.StorageEngine,
			engineRef(bucket, obj.Name, next), engineRef(bucket, obj.Name, prev)); err != nil {
			return err
		}

		_ = storageengine.Delete(ctx, m.opts.StorageEngine, engineRef(bucket, obj.Name, prev))
	}

	obj.VersionID = next

	return nil
}

// storeObjectLocked writes an object as the bucket's current version and, on a
// versioned bucket, records it in history. Enabled appends a fresh version;
// Suspended overwrites the reusable "null" version; a bucket that never had
// versioning keeps no history. Callers hold mu.
func storeObjectLocked(bkt *bucketData, obj *objectData) {
	switch bkt.Versioning {
	case VersioningEnabled:
		obj.VersionID = newVersionID()
		appendVersion(bkt, obj.Name, versionOf(obj))
	case VersioningSuspended:
		obj.VersionID = nullVersionID
		replaceNullVersion(bkt, obj.Name, versionOf(obj))
	}

	bkt.objects.Set(obj.Name, obj)
}

// deleteCurrentLocked applies a delete with no version id. Enabled appends a
// delete marker, Suspended replaces the null version with one, and an
// unversioned bucket removes the object outright. Callers hold mu.
func (m *Mock) deleteCurrentLocked(bkt *bucketData, name string) (versionID string, deleteMarker, existed bool) {
	now := m.now()

	switch bkt.Versioning {
	case VersioningEnabled:
		vid := newVersionID()
		appendVersion(bkt, name, &objectVersion{versionID: vid, deleteMarker: true, timeModified: now})
		bkt.objects.Delete(name)

		return vid, true, true
	case VersioningSuspended:
		replaceNullVersion(bkt, name, &objectVersion{versionID: nullVersionID, deleteMarker: true, timeModified: now})
		bkt.objects.Delete(name)

		return nullVersionID, true, true
	default:
		if !bkt.objects.Has(name) {
			return "", false, false
		}

		bkt.objects.Delete(name)

		return "", false, true
	}
}

func appendVersion(bkt *bucketData, name string, v *objectVersion) {
	if bkt.versions == nil {
		bkt.versions = make(map[string][]*objectVersion)
	}

	bkt.versions[name] = append(bkt.versions[name], v)
}

func replaceNullVersion(bkt *bucketData, name string, v *objectVersion) {
	if bkt.versions == nil {
		bkt.versions = make(map[string][]*objectVersion)
	}

	kept := make([]*objectVersion, 0, len(bkt.versions[name])+1)

	for _, ex := range bkt.versions[name] {
		if ex.versionID != nullVersionID {
			kept = append(kept, ex)
		}
	}

	bkt.versions[name] = append(kept, v)
}

func versionOf(obj *objectData) *objectVersion {
	return &objectVersion{
		versionID:    obj.VersionID,
		data:         obj.Data,
		size:         obj.Size,
		contentType:  obj.ContentType,
		contentMD5:   obj.ContentMD5,
		etag:         obj.ETag,
		timeModified: obj.TimeModified,
		metadata:     obj.Metadata,
		storageTier:  obj.StorageTier,
	}
}

func objectOfVersion(name string, v *objectVersion) *objectData {
	return &objectData{
		Name:         name,
		Data:         v.data,
		Size:         v.size,
		ContentType:  v.contentType,
		ContentMD5:   v.contentMD5,
		ETag:         v.etag,
		TimeCreated:  v.timeModified,
		TimeModified: v.timeModified,
		Metadata:     cloneMeta(v.metadata),
		StorageTier:  v.storageTier,
		VersionID:    v.versionID,
	}
}

func infoOfVersion(name string, v *objectVersion) driver.ObjectInfo {
	return driver.ObjectInfo{
		Key:          name,
		Size:         v.size,
		ContentType:  v.contentType,
		ETag:         v.etag,
		LastModified: v.timeModified,
		Metadata:     cloneMeta(v.metadata),
		VersionID:    v.versionID,
		DeleteMarker: v.deleteMarker,
	}
}

// SetBucketVersioning enables versioning, or suspends it when disabling. OCI
// never returns a bucket to Disabled once it has been enabled; use
// SetVersioningStatus for the full tri-state.
func (m *Mock) SetBucketVersioning(ctx context.Context, bucket string, enabled bool) error {
	status := VersioningSuspended
	if enabled {
		status = VersioningEnabled
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	return m.setVersioningLocked(ctx, bucket, bkt, status)
}

func (m *Mock) GetBucketVersioning(_ context.Context, bucket string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return false, err
	}

	return bkt.Versioning == VersioningEnabled, nil
}

// SetVersioningStatus sets the bucket's versioning state. OCI's Disabled is
// accepted only while the bucket has never been versioned.
func (m *Mock) SetVersioningStatus(ctx context.Context, bucket, status string) error {
	if !validVersioning(status) {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid versioning status %q", status)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	if status == VersioningDisabled && bkt.Versioning != VersioningDisabled {
		return cerrors.New(cerrors.InvalidArgument,
			"versioning cannot be set back to Disabled once enabled; use Suspended")
	}

	return m.setVersioningLocked(ctx, bucket, bkt, status)
}

// VersioningStatus returns "Disabled", "Enabled" or "Suspended".
func (m *Mock) VersioningStatus(_ context.Context, bucket string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return "", err
	}

	return bkt.Versioning, nil
}

// GetObjectVersion returns a specific version, or the current object when
// versionID is empty. A delete marker reports NotFound.
func (m *Mock) GetObjectVersion(ctx context.Context, bucket, key, versionID string) (*driver.Object, error) {
	if versionID == "" {
		return m.GetObject(ctx, bucket, key)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	v, err := m.findVersionLocked(bucket, key, versionID)
	if err != nil {
		return nil, err
	}

	data, err := m.engineLoad(ctx, engineRef(bucket, key, versionID), v.data)
	if err != nil {
		return nil, err
	}

	return &driver.Object{Info: infoOfVersion(key, v), Data: cloneBytes(data)}, nil
}

// HeadObjectVersion returns metadata for a specific version.
func (m *Mock) HeadObjectVersion(ctx context.Context, bucket, key, versionID string) (*driver.ObjectInfo, error) {
	if versionID == "" {
		return m.HeadObject(ctx, bucket, key)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	v, err := m.findVersionLocked(bucket, key, versionID)
	if err != nil {
		return nil, err
	}

	info := infoOfVersion(key, v)

	return &info, nil
}

// findVersionLocked resolves a stored (non-delete-marker) version. Callers
// hold mu.
func (m *Mock) findVersionLocked(bucket, key, versionID string) (*objectVersion, error) {
	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	for _, v := range bkt.versions[key] {
		if v.versionID != versionID {
			continue
		}

		if v.deleteMarker {
			return nil, cerrors.Newf(cerrors.NotFound, "version %q of %q is a delete marker", versionID, key)
		}

		return v, nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
}

// DeleteObjectVersion removes one version, or performs a top-level delete when
// versionID is empty. A top-level delete that finds nothing is NotFound, as
// DeleteObject is; a versioned bucket always records a delete marker.
func (m *Mock) DeleteObjectVersion(
	ctx context.Context, bucket, key, versionID string,
) (deletedVersionID string, deleteMarker bool, err error) {
	return m.DeleteObjectIf(ctx, bucket, key, versionID, "")
}

// DeleteObjectIf is DeleteObjectVersion guarded by an if-match ETag, checked
// against the version being deleted: the current object when versionID is
// empty.
func (m *Mock) DeleteObjectIf(
	ctx context.Context, bucket, key, versionID, ifMatch string,
) (deletedVersionID string, deleteMarker bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return "", false, err
	}

	if ifMatch != "" {
		if matchErr := checkETag(targetETagLocked(bkt, key, versionID), ifMatch, ""); matchErr != nil {
			return "", false, matchErr
		}
	}

	if holdErr := retentionBlocksLocked(bkt, key, m.opts.Clock.Now()); holdErr != nil {
		return "", false, holdErr
	}

	if versionID == "" {
		vid, marker, existed := m.deleteCurrentLocked(bkt, key)
		if !existed {
			return "", false, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
		}

		m.purgeLocked(ctx, bucket, key, vid, marker)

		return vid, marker, nil
	}

	removed, err := removeVersionLocked(bkt, key, versionID)
	if err != nil {
		return "", false, err
	}

	m.purgeLocked(ctx, bucket, key, versionID, removed.deleteMarker)

	return versionID, removed.deleteMarker, nil
}

// removeVersionLocked drops one version from a name's chain and recomputes the
// current object. Callers hold mu.
func removeVersionLocked(bkt *bucketData, key, versionID string) (*objectVersion, error) {
	chain := bkt.versions[key]

	for i, v := range chain {
		if v.versionID != versionID {
			continue
		}

		bkt.versions[key] = append(chain[:i], chain[i+1:]...)
		if len(bkt.versions[key]) == 0 {
			delete(bkt.versions, key)
		}

		recomputeCurrentLocked(bkt, key)

		return v, nil
	}

	return nil, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
}

// targetETagLocked is the ETag of the version a delete addresses, or empty when
// it does not exist. Callers hold mu.
func targetETagLocked(bkt *bucketData, key, versionID string) string {
	if versionID == "" {
		if obj, ok := bkt.objects.Get(key); ok {
			return obj.ETag
		}

		return ""
	}

	for _, v := range bkt.versions[key] {
		if v.versionID == versionID && !v.deleteMarker {
			return v.etag
		}
	}

	return ""
}

// recomputeCurrentLocked resets a name's current object to its newest stored
// version, removing it when the newest is a delete marker or none remain.
// Callers hold mu.
func recomputeCurrentLocked(bkt *bucketData, name string) {
	chain := bkt.versions[name]
	if len(chain) == 0 {
		bkt.objects.Delete(name)
		return
	}

	latest := chain[len(chain)-1]
	if latest.deleteMarker {
		bkt.objects.Delete(name)
		return
	}

	bkt.objects.Set(name, objectOfVersion(name, latest))
}

// ListObjectVersions returns every version and delete marker matching opts,
// newest first within each name.
func (m *Mock) ListObjectVersions(
	_ context.Context, bucket string, opts driver.ListOptions,
) (*driver.VersionListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	result := &driver.VersionListResult{}
	prefixSet := make(map[string]struct{})

	for _, name := range versionedNamesLocked(bkt) {
		if opts.Prefix != "" && !strings.HasPrefix(name, opts.Prefix) {
			continue
		}

		if opts.Delimiter != "" {
			rest := name[len(opts.Prefix):]
			if idx := strings.Index(rest, opts.Delimiter); idx >= 0 {
				prefixSet[opts.Prefix+rest[:idx+len(opts.Delimiter)]] = struct{}{}
				continue
			}
		}

		result.Versions = append(result.Versions, versionsOfLocked(bkt, name)...)
	}

	for p := range prefixSet {
		result.CommonPrefixes = append(result.CommonPrefixes, p)
	}

	sort.Strings(result.CommonPrefixes)

	return result, nil
}

// versionedNamesLocked is the union of names with history and names present
// only as a current object. Callers hold mu.
func versionedNamesLocked(bkt *bucketData) []string {
	set := make(map[string]struct{}, len(bkt.versions))
	for n := range bkt.versions {
		set[n] = struct{}{}
	}

	for _, n := range bkt.objects.Keys() {
		set[n] = struct{}{}
	}

	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}

	sort.Strings(names)

	return names
}

// versionsOfLocked projects one name's chain newest-first. A name with no
// history is reported as its single "null" version. Callers hold mu.
func versionsOfLocked(bkt *bucketData, name string) []driver.ObjectVersion {
	chain := bkt.versions[name]
	if len(chain) == 0 {
		obj, ok := bkt.objects.Get(name)
		if !ok {
			return nil
		}

		return []driver.ObjectVersion{{
			Key: name, VersionID: nullVersionID, IsLatest: true,
			Size: obj.Size, ETag: obj.ETag,
			ContentType: obj.ContentType, LastModified: obj.TimeModified,
		}}
	}

	out := make([]driver.ObjectVersion, 0, len(chain))

	for i := len(chain) - 1; i >= 0; i-- {
		v := chain[i]
		out = append(out, driver.ObjectVersion{
			Key: name, VersionID: v.versionID, IsLatest: i == len(chain)-1,
			DeleteMarker: v.deleteMarker, Size: v.size, ETag: v.etag,
			ContentType: v.contentType, LastModified: v.timeModified,
		})
	}

	return out
}
