package gcs

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreNotificationsVersionsAndCounters is the regression guard for
// the data-loss bug: notification configs, noncurrent version history, and the
// generation/notification counters must all survive a Snapshot -> new Mock ->
// Restore round-trip, and the counters must keep minting past the restored max.
func TestSnapshotRestoreNotificationsVersionsAndCounters(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	const bucket = "b1"
	require.NoError(t, src.CreateBucket(ctx, bucket))
	require.NoError(t, src.SetBucketVersioning(ctx, bucket, true))

	// Overwrite an object so the first generation is archived as noncurrent.
	require.NoError(t, src.PutObject(ctx, bucket, "obj", []byte("v1"), "text/plain", nil))
	require.NoError(t, src.PutObject(ctx, bucket, "obj", []byte("v2"), "text/plain", nil))

	notif, err := src.CreateNotificationConfig(ctx, bucket, &driver.GCSNotificationConfig{
		Topic:         "projects/p/topics/t",
		PayloadFormat: "JSON_API_V1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, notif.ID)

	// Capture the live generation so we can assert the counter advances past it.
	live, err := src.HeadObject(ctx, bucket, "obj")
	require.NoError(t, err)
	maxGen := live.Generation

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	// 1. Notification config present after restore.
	notifs, err := dst.ListNotificationConfigs(ctx, bucket)
	require.NoError(t, err)
	require.Len(t, notifs, 1)
	assert.Equal(t, notif.ID, notifs[0].ID)
	assert.Equal(t, "projects/p/topics/t", notifs[0].Topic)

	got, err := dst.GetNotificationConfig(ctx, bucket, notif.ID)
	require.NoError(t, err)
	assert.Equal(t, notif.Topic, got.Topic)

	// 2. Noncurrent version present after restore (v1 archived + v2 live = 2).
	versioned, err := dst.ListObjectGenerations(ctx, bucket, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, versioned.Objects, 2)

	archived, err := dst.GetObjectGCS(ctx, bucket, "obj", ptrInt64(maxGen-1))
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), archived.Data)

	// 3. Generation counter continues monotonically: the next write mints a
	// generation strictly greater than every restored generation.
	require.NoError(t, dst.PutObject(ctx, bucket, "obj", []byte("v3"), "text/plain", nil))
	after, err := dst.HeadObject(ctx, bucket, "obj")
	require.NoError(t, err)
	assert.Greater(t, after.Generation, maxGen, "next generation must exceed the restored max")

	// 4. Notification id counter continues: the next config gets a fresh,
	// non-colliding id.
	notif2, err := dst.CreateNotificationConfig(ctx, bucket, &driver.GCSNotificationConfig{
		Topic:         "projects/p/topics/t2",
		PayloadFormat: "JSON_API_V1",
	})
	require.NoError(t, err)
	assert.NotEqual(t, notif.ID, notif2.ID, "next notification id must not collide with the restored one")

	list2, err := dst.ListNotificationConfigs(ctx, bucket)
	require.NoError(t, err)
	assert.Len(t, list2, 2)
}

// TestSnapshotRestoreEmptyBucketNilSafe confirms a bucket with no versions and no
// notifications round-trips cleanly (nil maps must not panic).
func TestSnapshotRestoreEmptyBucketNilSafe(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	require.NoError(t, src.CreateBucket(ctx, "plain"))
	require.NoError(t, src.PutObject(ctx, "plain", "k", []byte("data"), "text/plain", nil))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	obj, err := dst.GetObject(ctx, "plain", "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), obj.Data)

	notifs, err := dst.ListNotificationConfigs(ctx, "plain")
	require.NoError(t, err)
	assert.Empty(t, notifs)

	versioned, err := dst.ListObjectGenerations(ctx, "plain", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, versioned.Objects, 1)
}

func ptrInt64(v int64) *int64 { return &v }
