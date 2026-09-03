package blobstorage

import (
	"context"
	"sort"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Compile-time check that Mock satisfies the optional AzureSoftDeleteBlob
// capability the blob wire handler reaches by type assertion.
var _ driver.AzureSoftDeleteBlob = (*Mock)(nil)

// softDeleteActive reports whether a Delete Blob should soft-delete rather than
// hard-delete, returning the retention window (days) to stamp on the retained
// blob. Soft delete engages only when the account delete-retention policy is
// enabled with a positive day count AND account versioning is off: with
// versioning on, retained versions are the recovery mechanism, so a delete
// leaves them intact instead of soft-deleting the base blob.
func (m *Mock) softDeleteActive() (retentionDays int, active bool) {
	props, _ := m.blobServiceProps.Get(AccountName)

	if !props.DeleteRetentionEnabled || props.DeleteRetentionDays <= 0 || props.IsVersioningEnabled {
		return 0, false
	}

	return props.DeleteRetentionDays, true
}

// SoftDeleteEnabled implements driver.AzureSoftDeleteBlob.
func (m *Mock) SoftDeleteEnabled(_ context.Context) (bool, error) {
	_, active := m.softDeleteActive()

	return active, nil
}

// softDeleteObject moves a live blob into the container's soft-deleted store,
// stamping the deletion time and the retention window so RemainingRetentionDays
// can count down. The caller has already confirmed soft delete is active.
func (m *Mock) softDeleteObject(ctr *containerMeta, key string, obj *blobObject, retentionDays int) {
	retained := cloneBlobObject(obj)
	retained.DeletedTime = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
	retained.deletedRetentionDays = retentionDays

	ctr.objects.Delete(key)
	ctr.softDeleted.Set(key, retained)
}

// remainingRetentionDays returns the whole days left before a soft-deleted blob
// is permanently purged, and whether it has already expired (window elapsed).
func remainingRetentionDays(obj *blobObject, now time.Time) (remaining int, expired bool) {
	deleted, err := time.Parse(blobTimeFormat, obj.DeletedTime)
	if err != nil {
		// A soft-deleted record always carries a parseable DeletedTime; treat an
		// unparseable one as freshly deleted rather than silently purging it.
		return obj.deletedRetentionDays, false
	}

	const hoursPerDay = 24

	elapsedDays := int(now.Sub(deleted).Hours()) / hoursPerDay
	remaining = obj.deletedRetentionDays - elapsedDays

	return remaining, remaining <= 0
}

// UndeleteBlob implements driver.AzureSoftDeleteBlob, restoring a soft-deleted
// blob to active. It is a no-op success when the blob is already active (nothing
// soft-deleted), and NotFound when neither an active nor a live soft-deleted
// blob of that name exists.
func (m *Mock) UndeleteBlob(_ context.Context, container, blob string) error {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	retained, ok := ctr.softDeleted.Get(blob)
	if !ok {
		if ctr.objects.Has(blob) {
			return nil // already active — Undelete is a no-op
		}

		return cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", blob, container)
	}

	if _, expired := remainingRetentionDays(retained, m.opts.Clock.Now().UTC()); expired {
		ctr.softDeleted.Delete(blob)

		return cerrors.Newf(cerrors.NotFound, "blob %q not found in container %q", blob, container)
	}

	restored := cloneBlobObject(retained)
	restored.DeletedTime = ""
	restored.deletedRetentionDays = 0

	ctr.objects.Set(blob, restored)
	ctr.softDeleted.Delete(blob)

	return nil
}

// ListDeletedBlobs implements driver.AzureSoftDeleteBlob, returning the
// soft-deleted blobs matching opts.Prefix, sorted by name. Records whose
// retention window has elapsed are purged and omitted (lazy expiry — there is no
// background sweeper).
func (m *Mock) ListDeletedBlobs(
	_ context.Context, container string, opts driver.ListOptions,
) (*driver.DeletedBlobListResult, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	now := m.opts.Clock.Now().UTC()

	var blobs []driver.DeletedBlob

	for _, key := range ctr.softDeleted.Keys() {
		obj, objOk := ctr.softDeleted.Get(key)
		if !objOk || !matchesPrefix(obj.Key, opts.Prefix) {
			continue
		}

		remaining, expired := remainingRetentionDays(obj, now)
		if expired {
			ctr.softDeleted.Delete(key)

			continue
		}

		blobs = append(blobs, driver.DeletedBlob{
			Info:                   objectInfo(obj),
			DeletedTime:            obj.DeletedTime,
			RemainingRetentionDays: remaining,
		})
	}

	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Info.Key < blobs[j].Info.Key })

	return &driver.DeletedBlobListResult{Blobs: blobs}, nil
}
