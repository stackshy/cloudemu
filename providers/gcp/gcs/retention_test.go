package gcs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newClockedMock returns a Mock plus the FakeClock backing it, so retention
// math can be driven deterministically by advancing time.
func newClockedMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("test-project"))

	return New(opts), clk
}

func isImmutable(err error) bool {
	var immErr *driver.GCSImmutableError

	return errors.As(err, &immErr)
}

func mustPut(t *testing.T, m *Mock, bucket, key string) {
	t.Helper()

	_, err := m.PutObjectGCS(context.Background(), bucket, key, []byte("payload"), "text/plain", nil, nil, driver.GCSPrecondition{})
	require.NoError(t, err)
}

// TestRetentionPolicyBlocksDeleteUntilElapsed proves an object cannot be deleted
// before its retention period elapses, and can be deleted once the FakeClock is
// advanced past it.
func TestRetentionPolicyBlocksDeleteUntilElapsed(t *testing.T) {
	ctx := context.Background()
	m, clk := newClockedMock(t)

	require.NoError(t, m.CreateBucket(ctx, "b"))
	mustPut(t, m, "b", "obj")

	const period = int64(3600) // 1 hour

	require.NoError(t, m.SetBucketRetentionPolicyGCS(ctx, "b", period))

	// Delete before the period elapses is blocked.
	err := m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{})
	require.Error(t, err)
	assert.True(t, isImmutable(err), "expected GCSImmutableError, got %v", err)

	// Halfway through — still blocked.
	clk.Advance(30 * time.Minute)
	assert.True(t, isImmutable(m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{})))

	// Past the period — delete succeeds.
	clk.Advance(31 * time.Minute)
	require.NoError(t, m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{}))
}

// TestRetentionPolicyBlocksOverwrite proves a retained object cannot be
// overwritten until its retention elapses.
func TestRetentionPolicyBlocksOverwrite(t *testing.T) {
	ctx := context.Background()
	m, clk := newClockedMock(t)

	require.NoError(t, m.CreateBucket(ctx, "b"))
	mustPut(t, m, "b", "obj")
	require.NoError(t, m.SetBucketRetentionPolicyGCS(ctx, "b", 3600))

	_, err := m.PutObjectGCS(ctx, "b", "obj", []byte("new"), "text/plain", nil, nil, driver.GCSPrecondition{})
	require.Error(t, err)
	assert.True(t, isImmutable(err))

	// Copy overwrite is blocked too.
	require.NoError(t, m.CreateBucket(ctx, "src"))
	mustPut(t, m, "src", "s")
	err = m.CopyObject(ctx, "b", "obj", driver.CopySource{Bucket: "src", Key: "s"})
	require.Error(t, err)
	assert.True(t, isImmutable(err))

	clk.Advance(2 * time.Hour)
	_, err = m.PutObjectGCS(ctx, "b", "obj", []byte("new"), "text/plain", nil, nil, driver.GCSPrecondition{})
	require.NoError(t, err)
}

// TestRetentionLockCannotShortenOrRemove proves a locked retention policy can
// only be increased, never shortened or removed.
func TestRetentionLockCannotShortenOrRemove(t *testing.T) {
	ctx := context.Background()
	m, _ := newClockedMock(t)

	require.NoError(t, m.CreateBucket(ctx, "b"))
	require.NoError(t, m.SetBucketRetentionPolicyGCS(ctx, "b", 3600))
	require.NoError(t, m.LockBucketRetentionPolicyGCS(ctx, "b"))

	pol, err := m.BucketRetentionPolicyGCS(ctx, "b")
	require.NoError(t, err)
	require.NotNil(t, pol)
	assert.True(t, pol.IsLocked)
	assert.Equal(t, int64(3600), pol.RetentionPeriod)

	// Shorten is rejected.
	assert.True(t, isImmutable(m.SetBucketRetentionPolicyGCS(ctx, "b", 60)))
	// Remove (period 0) is rejected.
	assert.True(t, isImmutable(m.SetBucketRetentionPolicyGCS(ctx, "b", 0)))

	// Increase is allowed.
	require.NoError(t, m.SetBucketRetentionPolicyGCS(ctx, "b", 7200))

	pol, err = m.BucketRetentionPolicyGCS(ctx, "b")
	require.NoError(t, err)
	assert.Equal(t, int64(7200), pol.RetentionPeriod)
}

// TestTemporaryHoldBlocksDeleteAndOverwrite proves a temporary hold blocks
// deletion/overwrite even when no retention applies (or after it elapses), and
// releasing it re-enables both.
func TestTemporaryHoldBlocksDeleteAndOverwrite(t *testing.T) {
	ctx := context.Background()
	m, clk := newClockedMock(t)

	require.NoError(t, m.CreateBucket(ctx, "b"))
	mustPut(t, m, "b", "obj")

	setHold(t, m, "b", "obj", boolPtr(true), nil)

	// Blocked regardless of elapsed time.
	clk.Advance(24 * time.Hour)
	assert.True(t, isImmutable(m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{})))

	_, err := m.PutObjectGCS(ctx, "b", "obj", []byte("x"), "text/plain", nil, nil, driver.GCSPrecondition{})
	assert.True(t, isImmutable(err))

	// Release the hold — delete now succeeds.
	setHold(t, m, "b", "obj", boolPtr(false), nil)
	require.NoError(t, m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{}))
}

// TestEventBasedHoldBlocksAndResetsRetentionClock proves an event-based hold
// blocks deletion and, on release, restarts the retention clock from the release
// instant.
func TestEventBasedHoldBlocksAndResetsRetentionClock(t *testing.T) {
	ctx := context.Background()
	m, clk := newClockedMock(t)

	require.NoError(t, m.CreateBucket(ctx, "b"))
	mustPut(t, m, "b", "obj")
	require.NoError(t, m.SetBucketRetentionPolicyGCS(ctx, "b", 3600))

	// Put the object under an event-based hold.
	setHold(t, m, "b", "obj", nil, boolPtr(true))

	// Blocked while held, even after the original retention window would elapse.
	clk.Advance(2 * time.Hour)
	assert.True(t, isImmutable(m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{})))

	// Release the hold — the retention clock restarts from now, so the object is
	// still retained for a fresh full period.
	setHold(t, m, "b", "obj", nil, boolPtr(false))
	assert.True(t, isImmutable(m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{})),
		"retention clock must restart on event-based hold release")

	// Not yet elapsed halfway into the fresh window.
	clk.Advance(30 * time.Minute)
	assert.True(t, isImmutable(m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{})))

	// Past the fresh full period from release — delete succeeds.
	clk.Advance(31 * time.Minute)
	require.NoError(t, m.DeleteObjectGCS(ctx, "b", "obj", nil, driver.GCSPrecondition{}))
}

func setHold(t *testing.T, m *Mock, bucket, key string, temp, event *bool) {
	t.Helper()

	_, err := m.UpdateObjectGCS(context.Background(), bucket, key, driver.GCSObjectUpdate{
		TemporaryHold:  temp,
		EventBasedHold: event,
	}, driver.GCSPrecondition{})
	require.NoError(t, err)
}
