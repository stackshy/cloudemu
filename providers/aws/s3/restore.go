package s3

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

var _ driver.ObjectRestorer = (*Mock)(nil)

// S3 storage classes with restore semantics.
const (
	storageClassGlacier            = "GLACIER"
	storageClassDeepArchive        = "DEEP_ARCHIVE"
	storageClassIntelligentTiering = "INTELLIGENT_TIERING"
)

// restoreState is the restore of one archived object version. The zero value
// means no restore was requested. While now < readyAt the restore is running;
// from readyAt until expiresAt the restored copy can be read; at expiresAt S3
// deletes the copy and the object is archived again. The state is evaluated
// against the clock on every read, so no timer moves it.
type restoreState struct {
	tier      string
	days      int
	readyAt   time.Time
	expiresAt time.Time
}

// active reports whether a restore is running or its copy has not expired.
func (r restoreState) active(now time.Time) bool {
	return !r.readyAt.IsZero() && now.Before(r.expiresAt)
}

// inProgress reports whether the restore is still running.
func (r restoreState) inProgress(now time.Time) bool {
	return !r.readyAt.IsZero() && now.Before(r.readyAt)
}

// readable reports whether a completed, unexpired restored copy exists.
func (r restoreState) readable(now time.Time) bool {
	return r.active(now) && !r.inProgress(now)
}

// status is the restore state as reported on HEAD/GET/List: nil when no
// restore is active.
func (r restoreState) status(now time.Time) *driver.ObjectRestoreStatus {
	if !r.active(now) {
		return nil
	}

	if r.inProgress(now) {
		return &driver.ObjectRestoreStatus{InProgress: true}
	}

	return &driver.ObjectRestoreStatus{ExpiryDate: r.expiresAt}
}

// isArchived reports whether objects of storage class sc must be restored
// before they can be read.
func isArchived(sc string) bool {
	return sc == storageClassGlacier || sc == storageClassDeepArchive
}

// checkReadable returns an InvalidObjectStateError when an object of class sc
// with restore state r cannot be read or copied right now.
func checkReadable(sc string, r restoreState, now time.Time) error {
	if isArchived(sc) && !r.readable(now) {
		return &driver.InvalidObjectStateError{StorageClass: sc}
	}

	return nil
}

// restoreExpiry is when a restored copy completed at done and kept for days
// expires: S3 adds the days and rounds up to the next midnight UTC.
func restoreExpiry(done time.Time, days int) time.Time {
	t := done.UTC().AddDate(0, 0, days)

	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// restoreSettle is how long a restore at tier runs before its copy is ready.
// It is 0 (the restore completes at once) unless async settling is on.
func (m *Mock) restoreSettle(tier string) time.Duration {
	d := settle.DefaultRestoreStandardSettle

	switch tier {
	case driver.RestoreTierExpedited:
		d = settle.DefaultRestoreExpeditedSettle
	case driver.RestoreTierBulk:
		d = settle.DefaultRestoreBulkSettle
	}

	return m.opts.SettleDuration(d)
}

// validateRestore applies the S3 rules for a restore of an object of class sc.
func validateRestore(sc string, req driver.RestoreRequest) error {
	switch {
	case sc == storageClassIntelligentTiering && req.Days != 0:
		return cerrors.New(cerrors.InvalidArgument,
			"INTELLIGENT_TIERING objects are not subject to restore expiry. Do not specify Days when restoring this object")
	case sc == storageClassIntelligentTiering:
		// The emulator does not age objects into the Archive Access tiers, so
		// an INTELLIGENT_TIERING object is always in an active tier.
		return driver.ErrObjectAlreadyInActiveTier
	case !isArchived(sc):
		return &driver.InvalidObjectStateError{StorageClass: storageClassOrStandard(sc)}
	case req.Days == 0:
		return driver.ErrRestoreDaysRequired
	case req.Days < 0:
		return cerrors.New(cerrors.InvalidArgument, "Days must be a positive integer")
	case sc == storageClassDeepArchive && req.Tier == driver.RestoreTierExpedited:
		return cerrors.New(cerrors.InvalidArgument, "Expedited retrievals are not available for the DEEP_ARCHIVE storage class")
	}

	return nil
}

// storageClassOrStandard maps the empty (default) storage class to STANDARD.
func storageClassOrStandard(sc string) string {
	if sc == "" {
		return "STANDARD"
	}

	return sc
}

// nextRestore validates a restore request for an object of class sc and
// computes the restore state after it at now. A
// running restore cannot be changed (ErrRestoreAlreadyInProgress); a completed
// one has its expiry reset relative to now (accepted=false, S3 200); otherwise
// a new restore starts (accepted=true, S3 202).
func (m *Mock) nextRestore(sc string, cur restoreState, req driver.RestoreRequest, now time.Time) (restoreState, bool, error) {
	if err := validateRestore(sc, req); err != nil {
		return cur, false, err
	}

	tier := req.Tier
	if tier == "" {
		tier = driver.RestoreTierStandard
	}

	switch {
	case cur.inProgress(now):
		return cur, false, driver.ErrRestoreAlreadyInProgress
	case cur.active(now):
		cur.days = req.Days
		cur.expiresAt = restoreExpiry(now, req.Days)

		return cur, false, nil
	}

	readyAt := now.Add(m.restoreSettle(tier))

	return restoreState{
		tier: tier, days: req.Days, readyAt: readyAt, expiresAt: restoreExpiry(readyAt, req.Days),
	}, true, nil
}

// RestoreObject implements driver.ObjectRestorer: it starts a restore of an
// archived object version (the current one when versionID == ""), or extends
// the expiry of a completed restore. The version and, when it is the current
// version, the current object are replaced by copies carrying the new state,
// so readers holding the old records never see a partial update.
func (m *Mock) RestoreObject(
	_ context.Context, bucket, key, versionID string, req driver.RestoreRequest,
) (bool, error) {
	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return false, cerrors.Newf(cerrors.NotFound, "bucket %q not found", bucket)
	}

	bkt.versionsMu.Lock()
	defer bkt.versionsMu.Unlock()

	cur, curOK := bkt.objects.Get(key)

	ver, idx, err := restoreVersion(bkt, key, versionID, cur, curOK)
	if err != nil {
		return false, err
	}

	// The current object is the target unless a non-current version was named.
	targetsCurrent := curOK && (versionID == "" || cur.VersionID == versionID)
	if !targetsCurrent && ver == nil {
		return false, missingObjectErrLocked(bkt, key)
	}

	sc, state := restoreTarget(cur, ver, targetsCurrent)

	next, accepted, err := m.nextRestore(sc, state, req, m.opts.Clock.Now())
	if err != nil {
		return false, err
	}

	if ver != nil {
		cp := *ver
		cp.restore = next
		bkt.versions[key][idx] = &cp
	}

	if targetsCurrent {
		setCurrentRestore(bkt, key, cur, next)
	}

	return accepted, nil
}

// setCurrentRestore replaces key's current object with a copy carrying the
// restore state r. Callers hold versionsMu.
func setCurrentRestore(bkt *bucketMeta, key string, cur *s3Object, r restoreState) {
	cp := *cur
	cp.restore = r
	bkt.objects.Set(key, &cp)
}

// restoreVersion finds the version record a restore addresses: the named
// version, or the current object's version on a versioned bucket. It is nil
// on an unversioned bucket. A named version that is missing is NotFound; a
// delete marker is a DeleteMarkerError. Callers hold versionsMu.
func restoreVersion(bkt *bucketMeta, key, versionID string, cur *s3Object, curOK bool) (*s3Version, int, error) {
	vid := versionID
	if vid == "" && curOK {
		vid = cur.VersionID
	}

	if vid == "" {
		return nil, -1, nil
	}

	ver, idx := versionAt(bkt, key, vid)

	switch {
	case ver == nil && versionID != "":
		return nil, -1, cerrors.Newf(cerrors.NotFound, "version %q of %q not found", versionID, key)
	case ver != nil && ver.deleteMarker:
		return nil, -1, &driver.DeleteMarkerError{LastModified: ver.lastModified, VersionID: ver.versionID}
	}

	return ver, idx, nil
}

// restoreTarget returns the storage class and restore state of the record a
// restore addresses: the current object, or the named version.
func restoreTarget(cur *s3Object, ver *s3Version, targetsCurrent bool) (string, restoreState) {
	if targetsCurrent {
		return cur.SystemProps.StorageClass, cur.restore
	}

	return ver.systemProps.StorageClass, ver.restore
}

// versionAt returns key's version versionID and its index in the chain, or
// nil. Callers hold versionsMu.
func versionAt(bkt *bucketMeta, key, versionID string) (ver *s3Version, idx int) {
	for i, v := range bkt.versions[key] {
		if v.versionID == versionID {
			return v, i
		}
	}

	return nil, -1
}
