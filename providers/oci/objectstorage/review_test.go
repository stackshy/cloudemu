package objectstorage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

func codeOf(t *testing.T, err error) string {
	t.Helper()

	var se *objectstorage.ServiceError

	require.True(t, errors.As(err, &se), "want a ServiceError, got %v", err)
	assert.NotEmpty(t, se.Error())

	return se.Code
}

// A rename moves the bucket, its objects, versions and PARs, and with an engine
// wired the bytes follow to the new name's references.
func TestRenameBucket(t *testing.T) {
	ctx := context.Background()

	for _, wired := range []bool{false, true} {
		eng := newFakeStorageEngine()

		m := newMock(t)
		if wired {
			m = newEngineMock(t, eng)
		}

		newBucket(t, m, "old")
		newBucket(t, m, "taken")
		require.NoError(t, m.SetVersioningStatus(ctx, "old", objectstorage.VersioningEnabled))
		require.NoError(t, m.PutObject(ctx, "old", "k", []byte("v1"), "text/plain", nil))
		require.NoError(t, m.PutObject(ctx, "old", "k", []byte("v2"), "text/plain", nil))

		par, err := m.CreatePAR(ctx, "old", objectstorage.PARSpec{
			Name: "p", ObjectName: "k", AccessType: objectstorage.PARObjectRead, TimeExpires: inAnHour(),
		})
		require.NoError(t, err)

		taken := "taken"
		_, err = m.UpdateBucket(ctx, "old", objectstorage.BucketUpdate{Name: &taken})
		require.Error(t, err)
		assert.Equal(t, objectstorage.CodeBucketAlreadyExists, codeOf(t, err))

		bad := "bad name!"
		_, err = m.UpdateBucket(ctx, "old", objectstorage.BucketUpdate{Name: &bad})
		require.Error(t, err)
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

		next := "new"
		got, err := m.UpdateBucket(ctx, "old", objectstorage.BucketUpdate{Name: &next})
		require.NoError(t, err)
		assert.Equal(t, "new", got.Name)

		_, err = m.BucketDetails(ctx, "old")
		require.Error(t, err)

		obj, err := m.GetObject(ctx, "new", "k")
		require.NoError(t, err)
		assert.Equal(t, []byte("v2"), obj.Data, "engine=%v", wired)

		list, err := m.ListObjectVersions(ctx, "new", driver.ListOptions{})
		require.NoError(t, err)
		require.Len(t, list.Versions, 2)

		old, err := m.GetObjectVersion(ctx, "new", "k", list.Versions[1].VersionID)
		require.NoError(t, err)
		assert.Equal(t, []byte("v1"), old.Data, "engine=%v", wired)

		resolved, err := m.ResolvePAR(ctx, tokenFrom(t, par.AccessURI))
		require.NoError(t, err)
		assert.Equal(t, "new", resolved.Bucket, "PARs follow the bucket")

		if wired {
			assert.False(t, eng.has("old", "k", list.Versions[0].VersionID), "no bytes left under the old name")
		}
	}
}

func TestBucketAndObjectPreconditions(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	b := newBucket(t, m, testBucket)

	_, err := m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{IfMatch: "stale"})
	require.Error(t, err)
	assert.Equal(t, objectstorage.CodeIfMatchFailed, codeOf(t, err))

	_, err = m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{IfMatch: b.ETag})
	require.NoError(t, err)

	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v"), "text/plain", nil))

	err = m.DeleteBucket(ctx, testBucket)
	require.Error(t, err)
	assert.Equal(t, objectstorage.CodeBucketNotEmpty, codeOf(t, err))
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err), "the portable code still applies")

	_, err = m.PutObjectWith(ctx, testBucket, "k", []byte("x"), objectstorage.PutOptions{IfNoneMatch: "some-etag"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "a write's if-none-match supports only *")

	_, err = m.PutObjectWith(ctx, testBucket, "k", []byte("x"), objectstorage.PutOptions{IfMatch: "*"})
	require.NoError(t, err, "if-match * matches any existing object")

	head, err := m.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)

	_, _, err = m.DeleteObjectIf(ctx, testBucket, "k", "", "stale")
	require.Error(t, err)
	assert.Equal(t, objectstorage.CodeIfMatchFailed, codeOf(t, err))

	_, _, err = m.DeleteObjectIf(ctx, testBucket, "k", "", head.ETag)
	require.NoError(t, err)

	err = m.DeleteBucketIf(ctx, testBucket, "stale")
	require.Error(t, err)
	assert.Equal(t, objectstorage.CodeIfMatchFailed, codeOf(t, err))
}

func TestDeleteVersionIfMatch(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	newBucket(t, m, testBucket)
	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningEnabled))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v1"), "text/plain", nil))

	first, err := m.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v2"), "text/plain", nil))

	_, _, err = m.DeleteObjectIf(ctx, testBucket, "k", first.VersionID, "stale")
	require.Error(t, err, "if-match is checked against the version being deleted")

	_, _, err = m.DeleteObjectIf(ctx, testBucket, "k", "nope", "anything")
	require.Error(t, err)

	_, _, err = m.DeleteObjectIf(ctx, testBucket, "k", first.VersionID, first.ETag)
	require.NoError(t, err)
}

func TestCopyObjectWithProvider(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	newBucket(t, m, "src")
	newBucket(t, m, "dst")
	require.NoError(t, m.SetVersioningStatus(ctx, "src", objectstorage.VersioningEnabled))
	require.NoError(t, m.PutObject(ctx, "src", "a", []byte("v1"), "text/plain", map[string]string{"o": "1"}))

	first, err := m.HeadObject(ctx, "src", "a")
	require.NoError(t, err)
	require.NoError(t, m.PutObject(ctx, "src", "a", []byte("v2"), "text/plain", nil))

	base := objectstorage.CopySpec{SourceBucket: "src", SourceObject: "a", DestinationBucket: "dst", DestinationObject: "b"}

	for name, mutate := range map[string]func(*objectstorage.CopySpec){
		"bad tier":            func(s *objectstorage.CopySpec) { s.StorageTier = "Glacier" },
		"missing src bucket":  func(s *objectstorage.CopySpec) { s.SourceBucket = "nope" },
		"missing src object":  func(s *objectstorage.CopySpec) { s.SourceObject = "nope" },
		"missing src version": func(s *objectstorage.CopySpec) { s.SourceVersionID = "nope" },
		"stale src if-match":  func(s *objectstorage.CopySpec) { s.SourceIfMatch = first.ETag },
		"missing dst bucket":  func(s *objectstorage.CopySpec) { s.DestinationBucket = "nope" },
		"dst if-match absent": func(s *objectstorage.CopySpec) { s.DestinationIfMatch = "x" },
	} {
		spec := base
		mutate(&spec)
		require.Error(t, m.CopyObjectWith(ctx, spec), name)
	}

	spec := base
	spec.SourceVersionID = first.VersionID
	require.NoError(t, m.CopyObjectWith(ctx, spec))

	got, err := m.GetObject(ctx, "dst", "b")
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), got.Data)
	assert.Equal(t, map[string]string{"o": "1"}, got.Info.Metadata, "nil Metadata keeps the source's")
	assert.NotEqual(t, first.ETag, got.Info.ETag, "a copy mints its own ETag")

	spec.DestinationIfNoneMatch = "*"
	err = m.CopyObjectWith(ctx, spec)
	require.Error(t, err)
	assert.Equal(t, objectstorage.CodeIfNoneMatchFailed, codeOf(t, err))
}

func TestLifecycleRuleValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	newBucket(t, m, testBucket)

	ok := objectstorage.LifecycleRule{
		Name: "r", Action: objectstorage.LifecycleDelete, TimeAmount: 1, TimeUnit: objectstorage.UnitDays,
	}

	for name, mutate := range map[string]func(*objectstorage.LifecycleRule){
		"no name":          func(r *objectstorage.LifecycleRule) { r.Name = "" },
		"zero amount":      func(r *objectstorage.LifecycleRule) { r.TimeAmount = 0 },
		"bad unit":         func(r *objectstorage.LifecycleRule) { r.TimeUnit = "days" },
		"bad target":       func(r *objectstorage.LifecycleRule) { r.Target = "buckets" },
		"bad action":       func(r *objectstorage.LifecycleRule) { r.Action = "MOVE" },
		"abort on objects": func(r *objectstorage.LifecycleRule) { r.Action = objectstorage.LifecycleAbort },
		"archive on uploads": func(r *objectstorage.LifecycleRule) {
			r.Action, r.Target = objectstorage.LifecycleArchive, objectstorage.TargetMultipartUploads
		},
		"unterminated class": func(r *objectstorage.LifecycleRule) { r.ExclusionPatterns = []string{"[a"} },
	} {
		rule := ok
		mutate(&rule)

		_, err := m.PutLifecyclePolicy(ctx, testBucket, []objectstorage.LifecycleRule{rule})
		require.Error(t, err, name)
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), name)
	}

	// Character classes and ? are part of the pattern language.
	rule := ok
	rule.InclusionPatterns = []string{"log-[0-9]?.txt"}
	_, err := m.PutLifecyclePolicy(ctx, testBucket, []objectstorage.LifecycleRule{rule})
	require.NoError(t, err)

	_, err = m.PutLifecyclePolicy(ctx, "missing", []objectstorage.LifecycleRule{ok})
	require.Error(t, err)

	_, err = m.GetLifecyclePolicy(ctx, "missing")
	require.Error(t, err)

	// The portable path round-trips through the OCI rules it expands to.
	require.NoError(t, m.PutLifecycleConfig(ctx, testBucket, driver.LifecycleConfig{Rules: []driver.LifecycleRule{
		{ID: "all", Prefix: "p/", Enabled: true, ExpirationDays: 30, TransitionDays: 10,
			TransitionStorageClass: objectstorage.TierInfrequentAccess, AbortMultipartDays: 7},
	}}))

	policy, err := m.GetLifecyclePolicy(ctx, testBucket)
	require.NoError(t, err)
	require.Len(t, policy.Rules, 3)
	assert.Equal(t, objectstorage.LifecycleInfrequent, policy.Rules[1].Action)
	assert.Equal(t, objectstorage.TargetMultipartUploads, policy.Rules[2].Target)

	cfg, err := m.GetLifecycleConfig(ctx, testBucket)
	require.NoError(t, err)
	require.Len(t, cfg.Rules, 3)
	assert.Equal(t, 30, cfg.Rules[0].ExpirationDays)
	assert.Equal(t, 10, cfg.Rules[1].TransitionDays)
	assert.Equal(t, 7, cfg.Rules[2].AbortMultipartDays)

	// A previous-versions rule has no portable form.
	rule = ok
	rule.Target = objectstorage.TargetPreviousVersions
	_, err = m.PutLifecyclePolicy(ctx, testBucket, []objectstorage.LifecycleRule{rule})
	require.NoError(t, err)

	_, err = m.GetLifecycleConfig(ctx, testBucket)
	assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
}

func TestRegion(t *testing.T) {
	assert.Equal(t, "us-ashburn-1", newMock(t).Region())
}

func inAnHour() time.Time { return time.Now().Add(time.Hour) }
