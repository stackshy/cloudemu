package blobstorage

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Compile-time check that Mock satisfies the optional AzureVersionedBlob
// capability the blob wire handler reaches by type assertion.
var _ driver.AzureVersionedBlob = (*Mock)(nil)

// versioningEnabled reports whether account-level blob versioning is on for the
// single storage account this data plane models (Set Blob Service Properties,
// isVersioningEnabled).
func (m *Mock) versioningEnabled() bool {
	props, _ := m.blobServiceProps.Get(AccountName)

	return props.IsVersioningEnabled
}

// recordVersion mints a new immutable version of obj when account versioning is
// enabled, stamping obj.VersionID (which becomes the blob's current version) and
// storing a deep copy under the container's versions store so it survives a
// later overwrite or base-blob delete. A no-op when versioning is disabled.
//
// The caller must have exclusive access to obj: either obj is a freshly built,
// not-yet-published object (whole-blob write paths) or the caller holds obj.mu
// (in-place metadata/properties/tier/append mutators).
func (m *Mock) recordVersion(ctr *containerMeta, obj *blobObject) {
	if !m.versioningEnabled() {
		return
	}

	ctr.mu.Lock()
	ctr.versionSeq++
	seq := ctr.versionSeq
	ctr.mu.Unlock()

	versionID := m.opts.Clock.Now().UTC().Format(snapshotFormat) + fmt.Sprintf("%07dZ", seq)
	obj.VersionID = versionID

	ctr.versions.Set(versionKey(obj.Key, versionID), cloneBlobObject(obj))
}

// versionKey namespaces a version by its blob so distinct blobs don't collide.
func versionKey(blob, versionID string) string {
	return blob + "\x00" + versionID
}

// cloneBlobObject deep-copies a blob's content, metadata, and system properties
// into a standalone immutable record for the versions store. The live lease
// state and the object mutex are intentionally excluded — a version is a
// point-in-time content snapshot, not a leasable live blob.
func cloneBlobObject(obj *blobObject) *blobObject {
	return &blobObject{
		Key: obj.Key, Data: append([]byte(nil), obj.Data...), Size: obj.Size,
		ContentType: obj.ContentType, ETag: obj.ETag, LastModified: obj.LastModified,
		Metadata: maps.Clone(obj.Metadata), Tags: maps.Clone(obj.Tags),
		BlobType: obj.BlobType, AccessTier: obj.AccessTier, VersionID: obj.VersionID,
		ContentEncoding: obj.ContentEncoding, ContentLanguage: obj.ContentLanguage,
		ContentDisposition: obj.ContentDisposition, CacheControl: obj.CacheControl,
		CommittedBlocks: append([]driver.BlockInfo(nil), obj.CommittedBlocks...),
		appendBlocks:    obj.appendBlocks,
	}
}

// getContainerBlob fetches both a container and one of its live blobs, erroring
// if either is absent. Used by the in-place mutators, which need the container
// to record a new version.
func (m *Mock) getContainerBlob(container, blob string) (*containerMeta, *blobObject, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	obj, ok := ctr.objects.Get(blob)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", blob, container)
	}

	return ctr, obj, nil
}

// VersioningEnabled implements driver.AzureVersionedBlob.
func (m *Mock) VersioningEnabled(_ context.Context) (bool, error) {
	return m.versioningEnabled(), nil
}

// getVersion fetches a stored version, erroring if the container or version is
// absent.
func (m *Mock) getVersion(container, blob, versionID string) (*blobObject, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	ver, ok := ctr.versions.Get(versionKey(blob, versionID))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound,
			"blob %q version %q not found in container %q", blob, versionID, container)
	}

	return ver, nil
}

// GetBlobVersion reads a specific version of a blob (GET ?versionid=…).
func (m *Mock) GetBlobVersion(_ context.Context, container, blob, versionID string) (*driver.Object, error) {
	ver, err := m.getVersion(container, blob, versionID)
	if err != nil {
		return nil, err
	}

	return &driver.Object{Info: objectInfo(ver), Data: append([]byte(nil), ver.Data...)}, nil
}

// HeadBlobVersion returns a specific version's info (HEAD ?versionid=…).
func (m *Mock) HeadBlobVersion(_ context.Context, container, blob, versionID string) (*driver.ObjectInfo, error) {
	ver, err := m.getVersion(container, blob, versionID)
	if err != nil {
		return nil, err
	}

	info := objectInfo(ver)

	return &info, nil
}

// DeleteBlobVersion permanently removes a specific version (DELETE
// ?versionid=…). Removing the version that is currently the base blob's version
// also removes the base blob, so a later base read returns NotFound.
func (m *Mock) DeleteBlobVersion(_ context.Context, container, blob, versionID string) error {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	key := versionKey(blob, versionID)
	if !ctr.versions.Has(key) {
		return cerrors.Newf(cerrors.NotFound,
			"blob %q version %q not found in container %q", blob, versionID, container)
	}

	ctr.versions.Delete(key)

	if base, ok := ctr.objects.Get(blob); ok && base.VersionID == versionID {
		ctr.objects.Delete(blob)
	}

	return nil
}

// ListBlobVersions returns every version (current and previous) of the blobs
// matching opts, sorted by blob name then version id (so the current version,
// which carries the newest id, sorts last within a name — matching Azure).
func (m *Mock) ListBlobVersions(_ context.Context, container string, opts driver.ListOptions) (*driver.VersionListResult, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	current := currentVersionIDs(ctr)
	versions, seen := collectRecordedVersions(ctr, opts.Prefix, current)
	versions = appendUnrecordedBaseBlobs(ctr, opts.Prefix, seen, versions)

	sort.Slice(versions, func(i, j int) bool {
		if versions[i].Key != versions[j].Key {
			return versions[i].Key < versions[j].Key
		}

		return versions[i].VersionID < versions[j].VersionID
	})

	return &driver.VersionListResult{Versions: versions}, nil
}

// collectRecordedVersions gathers every stored version whose key matches prefix,
// marking each with whether it is the current version, and returns the set of
// (key, version) pairs it emitted so a base-blob fallback can skip duplicates.
func collectRecordedVersions(
	ctr *containerMeta, prefix string, current map[string]string,
) (versions []driver.ObjectVersion, seen map[string]struct{}) {
	seen = make(map[string]struct{})

	for _, vk := range ctr.versions.Keys() {
		ver, ok := ctr.versions.Get(vk)
		if !ok || !matchesPrefix(ver.Key, prefix) {
			continue
		}

		seen[versionKey(ver.Key, ver.VersionID)] = struct{}{}

		versions = append(versions, versionEntry(ver, current[ver.Key] == ver.VersionID))
	}

	return versions, seen
}

// appendUnrecordedBaseBlobs adds any live base blob whose current version was
// never recorded (e.g. written before versioning was enabled) so the listing is
// complete.
func appendUnrecordedBaseBlobs(
	ctr *containerMeta, prefix string, seen map[string]struct{}, versions []driver.ObjectVersion,
) []driver.ObjectVersion {
	for _, k := range ctr.objects.Keys() {
		obj, ok := ctr.objects.Get(k)
		if !ok || !matchesPrefix(obj.Key, prefix) {
			continue
		}

		if _, dup := seen[versionKey(obj.Key, obj.VersionID)]; dup {
			continue
		}

		entry := versionEntry(obj, true)
		versions = append(versions, entry)
	}

	return versions
}

// matchesPrefix reports whether key falls under prefix (an empty prefix matches
// everything).
func matchesPrefix(key, prefix string) bool {
	return prefix == "" || strings.HasPrefix(key, prefix)
}

// currentVersionIDs maps each live base blob's key to its current version id,
// so ListBlobVersions can mark the current version.
func currentVersionIDs(ctr *containerMeta) map[string]string {
	current := make(map[string]string)

	for _, k := range ctr.objects.Keys() {
		if obj, ok := ctr.objects.Get(k); ok {
			current[obj.Key] = obj.VersionID
		}
	}

	return current
}

// versionEntry renders a stored version as a driver.ObjectVersion.
func versionEntry(obj *blobObject, isLatest bool) driver.ObjectVersion {
	return driver.ObjectVersion{
		Key: obj.Key, VersionID: obj.VersionID, IsLatest: isLatest,
		Size: obj.Size, ETag: obj.ETag, ContentType: obj.ContentType,
		LastModified: obj.LastModified,
	}
}
