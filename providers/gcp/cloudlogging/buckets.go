package cloudlogging

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// defaultBucketID and requiredBucketID are the two special log buckets Cloud
// Logging auto-provisions for every project in the "global" location. Neither
// can be deleted; _Required additionally cannot be modified at all and starts
// locked with a much longer retention, mirroring the bucket that captures
// Admin Activity / mandatory audit logs in real GCP.
const (
	defaultBucketID  = "_Default"
	requiredBucketID = "_Required"
	globalLocation   = "global"
	anyLocation      = "-"

	defaultBucketRetentionDays  = 30
	requiredBucketRetentionDays = 400

	bucketLifecycleActive = "ACTIVE"
)

func bucketKey(project, location, name string) string {
	return project + "/" + location + "/" + name
}

// ensureDefaultBuckets lazily provisions the _Default and _Required buckets
// for a project's "global" location the first time any bucket call touches it,
// mirroring how a real Cloud Logging project always has both from creation.
// SetIfAbsent makes first-touch safe under concurrent callers and never
// clobbers a bucket that already exists (e.g. one restored from a snapshot).
func (m *Mock) ensureDefaultBuckets(project, location string) {
	if location != globalLocation {
		return
	}

	now := m.opts.Clock.Now().UTC()

	specs := []struct {
		name      string
		retention int32
		locked    bool
	}{
		{defaultBucketID, defaultBucketRetentionDays, false},
		{requiredBucketID, requiredBucketRetentionDays, true},
	}

	for _, spec := range specs {
		m.buckets.SetIfAbsent(bucketKey(project, location, spec.name), &driver.LogBucket{
			Name:           spec.name,
			Location:       location,
			RetentionDays:  spec.retention,
			Locked:         spec.locked,
			LifecycleState: bucketLifecycleActive,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
}

// CreateBucket creates a user-defined log bucket under project/location. The
// reserved ids _Default and _Required always exist already and cannot be
// created through this call.
func (m *Mock) CreateBucket(_ context.Context, project, location string, bucket *driver.LogBucket) (*driver.LogBucket, error) {
	if bucket.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "bucket id is required")
	}

	if bucket.Name == defaultBucketID || bucket.Name == requiredBucketID {
		return nil, errors.Newf(errors.InvalidArgument, "bucket id %q is reserved", bucket.Name)
	}

	m.ensureDefaultBuckets(project, location)

	key := bucketKey(project, location, bucket.Name)
	if m.buckets.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "bucket %q already exists", bucket.Name)
	}

	retention := bucket.RetentionDays
	if retention == 0 {
		retention = defaultBucketRetentionDays
	}

	now := m.opts.Clock.Now().UTC()

	stored := *bucket
	stored.Location = location
	stored.RetentionDays = retention
	stored.LifecycleState = bucketLifecycleActive
	stored.CreatedAt = now
	stored.UpdatedAt = now

	m.buckets.Set(key, &stored)

	result := stored

	return &result, nil
}

// GetBucket returns the bucket named name under project/location.
func (m *Mock) GetBucket(_ context.Context, project, location, name string) (*driver.LogBucket, error) {
	m.ensureDefaultBuckets(project, location)

	b, ok := m.buckets.Get(bucketKey(project, location, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "bucket %q not found", name)
	}

	result := *b

	return &result, nil
}

// ListBuckets lists all buckets under project/location, in name order.
// location "-" lists buckets across every location, mirroring the wildcard
// real Cloud Logging (and gcloud) accepts.
func (m *Mock) ListBuckets(_ context.Context, project, location string) ([]driver.LogBucket, error) {
	prefix := project + "/"

	if location == anyLocation {
		m.ensureDefaultBuckets(project, globalLocation)
	} else {
		m.ensureDefaultBuckets(project, location)
		prefix = bucketKey(project, location, "")
	}

	buckets := make([]driver.LogBucket, 0)

	for key, b := range m.buckets.All() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		buckets = append(buckets, *b)
	}

	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })

	return buckets, nil
}

// UpdateBucket applies a partial update to an existing bucket's mutable
// fields, atomically under the store lock so the guard checks and the write
// cannot be split by a concurrent update. See applyBucketUpdate for the
// invariants enforced.
func (m *Mock) UpdateBucket(_ context.Context, project, location, name string, update driver.BucketUpdate) (*driver.LogBucket, error) {
	m.ensureDefaultBuckets(project, location)

	key := bucketKey(project, location, name)
	now := m.opts.Clock.Now().UTC()

	var (
		result   driver.LogBucket
		guardErr error
	)

	found := m.buckets.Update(key, func(existing *driver.LogBucket) *driver.LogBucket {
		next, err := applyBucketUpdate(existing, update, name, now)
		if err != nil {
			guardErr = err
			return existing
		}

		result = *next

		return next
	})

	if !found {
		return nil, errors.Newf(errors.NotFound, "bucket %q not found", name)
	}

	if guardErr != nil {
		return nil, guardErr
	}

	return &result, nil
}

// applyBucketUpdate computes the new value of existing for a partial update.
// _Required cannot be modified at all; a locked bucket's retention cannot be
// reduced, and a locked bucket can never be unlocked — both are
// FailedPrecondition, mirroring real Cloud Logging.
func applyBucketUpdate(existing *driver.LogBucket, update driver.BucketUpdate, name string, now time.Time) (*driver.LogBucket, error) {
	if existing.Name == requiredBucketID {
		return nil, errors.Newf(errors.FailedPrecondition, "bucket %q cannot be modified", name)
	}

	updated := *existing

	if update.SetRetentionDays {
		if existing.Locked && update.RetentionDays < existing.RetentionDays {
			return nil, errors.Newf(errors.FailedPrecondition,
				"retention period of locked bucket %q cannot be reduced", name)
		}

		updated.RetentionDays = update.RetentionDays
	}

	if update.SetDescription {
		updated.Description = update.Description
	}

	if update.SetLocked {
		if existing.Locked && !update.Locked {
			return nil, errors.Newf(errors.FailedPrecondition, "bucket %q is locked and cannot be unlocked", name)
		}

		updated.Locked = update.Locked
	}

	updated.UpdatedAt = now

	return &updated, nil
}

// DeleteBucket removes a user-defined bucket, atomically checking the guard
// (reserved id, locked) and deleting under the store lock so a concurrent
// lock/unlock cannot race the delete. _Default and _Required can never be
// deleted; neither can a locked bucket (real Cloud Logging requires
// unlocking first, which this API does not support once a bucket is locked).
func (m *Mock) DeleteBucket(_ context.Context, project, location, name string) error {
	m.ensureDefaultBuckets(project, location)

	key := bucketKey(project, location, name)

	var guardErr error

	found := m.buckets.UpdateOrDelete(key, func(b *driver.LogBucket) (*driver.LogBucket, bool) {
		if b.Name == defaultBucketID || b.Name == requiredBucketID {
			guardErr = errors.Newf(errors.FailedPrecondition, "bucket %q cannot be deleted", name)
			return b, true
		}

		if b.Locked {
			guardErr = errors.Newf(errors.FailedPrecondition, "locked bucket %q cannot be deleted", name)
			return b, true
		}

		return nil, false
	})

	if !found {
		return errors.Newf(errors.NotFound, "bucket %q not found", name)
	}

	return guardErr
}
