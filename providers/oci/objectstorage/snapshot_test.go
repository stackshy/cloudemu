package objectstorage_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// TestSnapshotRestoreRoundTrip seeds a bucket with objects, a version chain, a
// PAR, a retention rule and a lifecycle policy, snapshots, restores into a
// fresh mock and asserts everything comes back under its original identity —
// bucket OCID, object version ids and the PAR's redemption token included.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)

	bucket := newBucket(t, src, testBucket)

	require.NoError(t, src.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningEnabled))
	require.NoError(t, src.PutObject(ctx, testBucket, "logs/a.txt", []byte("v1"), "text/plain", nil))

	first, err := src.HeadObject(ctx, testBucket, "logs/a.txt")
	require.NoError(t, err)

	_, err = src.PutObjectWith(ctx, testBucket, "logs/a.txt", []byte("v2"), objectstorage.PutOptions{
		ContentType: "text/plain",
		StorageTier: objectstorage.TierInfrequentAccess,
		Metadata:    map[string]string{"owner": "ada"},
	})
	require.NoError(t, err)

	par, err := src.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
		Name: "read", ObjectName: "logs/a.txt", AccessType: objectstorage.PARObjectRead,
		TimeExpires: time.Now().Add(time.Hour).UTC(),
	})
	require.NoError(t, err)

	rule, err := src.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName: "hold",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 10, TimeUnit: objectstorage.RetentionDays},
	})
	require.NoError(t, err)

	require.NoError(t, src.PutLifecycleConfig(ctx, testBucket, driver.LifecycleConfig{
		Rules: []driver.LifecycleRule{{ID: "expire", Prefix: "logs/", ExpirationDays: 30, Enabled: true}},
	}))

	require.NoError(t, src.PutBucketTagging(ctx, testBucket, map[string]string{"env": "dev"}))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	restored, err := dst.BucketDetails(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, bucket.ID, restored.ID, "the bucket keeps its OCID")
	assert.Equal(t, objectstorage.VersioningEnabled, restored.Versioning)
	assert.Equal(t, map[string]string{"env": "dev"}, restored.FreeformTags)

	current, err := dst.GetObject(ctx, testBucket, "logs/a.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), current.Data)
	assert.Equal(t, map[string]string{"owner": "ada"}, current.Info.Metadata)

	details, err := dst.ObjectDetailsOf(ctx, testBucket, "logs/a.txt")
	require.NoError(t, err)
	assert.Equal(t, objectstorage.TierInfrequentAccess, details.StorageTier)
	assert.Equal(t, int64(2), details.Size)

	old, err := dst.GetObjectVersion(ctx, testBucket, "logs/a.txt", first.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), old.Data, "the version chain keeps its ids")

	// The PAR still resolves by its original redemption token.
	resolved, err := dst.ResolvePAR(ctx, tokenFrom(t, par.AccessURI))
	require.NoError(t, err)
	assert.Equal(t, par.ID, resolved.ID)
	assert.Equal(t, objectstorage.PARObjectRead, resolved.AccessType)

	gotRule, err := dst.GetRetentionRule(ctx, testBucket, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(10), gotRule.Duration.TimeAmount)

	cfg, err := dst.GetLifecycleConfig(ctx, testBucket)
	require.NoError(t, err)
	require.Len(t, cfg.Rules, 1)
	assert.Equal(t, 30, cfg.Rules[0].ExpirationDays)

	// The restored bucket is still writable, and its retention rule still bites.
	require.NoError(t, dst.PutObject(ctx, testBucket, "fresh", []byte("x"), "text/plain", nil))

	err = dst.DeleteObject(ctx, testBucket, "logs/a.txt")
	require.Error(t, err, "the restored retention rule still holds the object")
}

// A metadata-only snapshot keeps every identity and size but drops the bodies,
// which is the persist default.
func TestSnapshotWithoutAssets(t *testing.T) {
	ctx := t.Context()
	src := newMock(t)
	newBucket(t, src, testBucket)

	require.NoError(t, src.PutObject(ctx, testBucket, "k", []byte("hello"), "text/plain", nil))

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "aGVsbG8=", "the body is not captured")

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	head, err := dst.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, int64(5), head.Size, "the size survives without the bytes")

	list, err := dst.ListObjects(ctx, testBucket, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, list.Objects, 1)
	assert.Equal(t, int64(5), list.Objects[0].Size)
}

func TestRestoreRejectsMalformedSnapshot(t *testing.T) {
	m := newMock(t)

	err := m.Restore(t.Context(), []byte("{"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse snapshot")
}

// A persist restart resets the process id counter while restored PARs keep
// their tokens and OCIDs. A PAR minted after the restore must not reuse either,
// or the restored PAR's access URI would resolve to the new one.
func TestPARsDoNotCollideAfterRestore(t *testing.T) {
	ctx := t.Context()

	// Both "processes" run the same sequence from a zeroed counter, so the
	// counter alone would hand the second PAR the first one's token and OCID.
	idgen.Reset()

	src := newMock(t)
	newBucket(t, src, "secret")
	require.NoError(t, src.PutObject(ctx, "secret", "payroll.csv", []byte("salaries"), "text/csv", nil))

	old, err := src.CreatePAR(ctx, "secret", objectstorage.PARSpec{
		Name: "payroll", ObjectName: "payroll.csv", AccessType: objectstorage.PARObjectRead,
		TimeExpires: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	// Restart: the counter starts over, exactly as a fresh process would.
	idgen.Reset()

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))
	newBucket(t, dst, "public")
	require.NoError(t, dst.PutObject(ctx, "public", "readme.txt", []byte("hello"), "text/plain", nil))

	fresh, err := dst.CreatePAR(ctx, "public", objectstorage.PARSpec{
		Name: "readme", ObjectName: "readme.txt", AccessType: objectstorage.PARObjectRead,
		TimeExpires: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	assert.NotEqual(t, old.ID, fresh.ID)
	assert.NotEqual(t, tokenFrom(t, old.AccessURI), tokenFrom(t, fresh.AccessURI))

	for _, tc := range []struct {
		par    *objectstorage.PreauthenticatedRequest
		bucket string
		object string
	}{
		{old, "secret", "payroll.csv"},
		{fresh, "public", "readme.txt"},
	} {
		resolved, err := dst.ResolvePAR(ctx, tokenFrom(t, tc.par.AccessURI))
		require.NoError(t, err)
		assert.Equal(t, tc.par.ID, resolved.ID)
		assert.Equal(t, tc.bucket, resolved.Bucket)
		assert.True(t, objectstorage.PARAllows(resolved, http.MethodGet, tc.object))
	}
}
