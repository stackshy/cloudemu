package objectstorage

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// newVersionID mints an object version id. OCI version ids are opaque.
func newVersionID() string { return idgen.GenerateID("") }

// setVersioningLocked applies a versioning state to a bucket, allocating the
// history map the first time versioning is enabled. Callers hold mu.
func setVersioningLocked(bkt *bucketData, status string) {
	bkt.Versioning = status

	if status == VersioningEnabled && bkt.versions == nil {
		bkt.versions = make(map[string][]*objectVersion)
	}
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
		Size:         int64(len(v.data)),
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
func (m *Mock) SetBucketVersioning(_ context.Context, bucket string, enabled bool) error {
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

	setVersioningLocked(bkt, status)

	return nil
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
func (m *Mock) SetVersioningStatus(_ context.Context, bucket, status string) error {
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

	setVersioningLocked(bkt, status)

	return nil
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

	return &driver.Object{Info: infoOfVersion(key, v), Data: cloneBytes(v.data)}, nil
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
	_ context.Context, bucket, key, versionID string,
) (deletedVersionID string, deleteMarker bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return "", false, err
	}

	if err := retentionBlocksLocked(bkt, key, m.opts.Clock.Now()); err != nil {
		return "", false, err
	}

	if versionID == "" {
		vid, marker, existed := m.deleteCurrentLocked(bkt, key)
		if !existed {
			return "", false, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", key, bucket)
		}

		return vid, marker, nil
	}

	chain := bkt.versions[key]

	idx := -1

	var removed *objectVersion

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

	recomputeCurrentLocked(bkt, key)

	return versionID, removed.deleteMarker, nil
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
			Size: int64(len(obj.Data)), ETag: obj.ETag,
			ContentType: obj.ContentType, LastModified: obj.TimeModified,
		}}
	}

	out := make([]driver.ObjectVersion, 0, len(chain))

	for i := len(chain) - 1; i >= 0; i-- {
		v := chain[i]
		out = append(out, driver.ObjectVersion{
			Key: name, VersionID: v.versionID, IsLatest: i == len(chain)-1,
			DeleteMarker: v.deleteMarker, Size: int64(len(v.data)), ETag: v.etag,
			ContentType: v.contentType, LastModified: v.timeModified,
		})
	}

	return out
}
