package blobstorage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// enableSoftDelete turns on the account delete-retention policy (days) for the
// single account the data plane models.
func enableSoftDelete(t *testing.T, m *Mock, days int) {
	t.Helper()
	require.NoError(t, m.SetBlobServiceProperties(context.Background(), AccountName,
		driver.BlobServiceProperties{DeleteRetentionEnabled: true, DeleteRetentionDays: days}))
}

func TestSoftDeleteRetainsAndUndeletes(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	enableSoftDelete(t, m, 7)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	active, err := m.SoftDeleteEnabled(ctx)
	require.NoError(t, err)
	assert.True(t, active)

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("hello"), "text/plain", nil))

	// Delete retains the blob: base read now fails, but it surfaces under the
	// deleted listing with the full retention window remaining.
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))

	_, err = m.HeadObject(ctx, "c1", "k1")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	del, err := m.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, del.Blobs, 1)
	assert.Equal(t, "k1", del.Blobs[0].Info.Key)
	assert.Equal(t, 7, del.Blobs[0].RemainingRetentionDays)
	assert.NotEmpty(t, del.Blobs[0].DeletedTime)

	// The live listing omits it.
	live, err := m.ListObjects(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, live.Objects)

	// Undelete restores it to active with its bytes intact.
	require.NoError(t, m.UndeleteBlob(ctx, "c1", "k1"))

	got, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got.Data))

	del, err = m.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, del.Blobs)
}

func TestSoftDeleteDisabledHardDeletes(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	active, err := m.SoftDeleteEnabled(ctx)
	require.NoError(t, err)
	assert.False(t, active)

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("hello"), "text/plain", nil))
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))

	del, err := m.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, del.Blobs)

	// Undelete has nothing to restore -> NotFound.
	err = m.UndeleteBlob(ctx, "c1", "k1")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

// TestSoftDeleteRetentionExpiry advances the fake clock past the retention
// window: the soft-deleted blob is then purged from the deleted listing and can
// no longer be undeleted.
func TestSoftDeleteRetentionExpiry(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()
	enableSoftDelete(t, m, 3)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("hello"), "text/plain", nil))
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))

	// One day in: two days remain.
	clk.Advance(24 * time.Hour)
	del, err := m.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, del.Blobs, 1)
	assert.Equal(t, 2, del.Blobs[0].RemainingRetentionDays)

	// Past the window: purged and un-undeletable.
	clk.Advance(3 * 24 * time.Hour)
	del, err = m.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, del.Blobs)

	err = m.UndeleteBlob(ctx, "c1", "k1")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

// TestSoftDeleteVersioningTakesPrecedence confirms that when versioning is also
// enabled a delete keeps versions (not soft delete) as the recovery path.
func TestSoftDeleteVersioningTakesPrecedence(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	require.NoError(t, m.SetBlobServiceProperties(ctx, AccountName, driver.BlobServiceProperties{
		IsVersioningEnabled: true, DeleteRetentionEnabled: true, DeleteRetentionDays: 7,
	}))
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	active, err := m.SoftDeleteEnabled(ctx)
	require.NoError(t, err)
	assert.False(t, active, "versioning takes precedence over soft delete")

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))

	// The version is retained; nothing lands in the soft-deleted store.
	vers, err := m.ListBlobVersions(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, vers.Versions, 1)

	del, err := m.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, del.Blobs)
}

// TestSoftDeletePersistRoundTrip confirms a soft-deleted blob survives a
// Snapshot/Restore and can still be undeleted afterward.
func TestSoftDeletePersistRoundTrip(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	enableSoftDelete(t, m, 5)
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("hello"), "text/plain", nil))
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))

	snap, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	m2, _ := newMock()
	require.NoError(t, m2.Restore(ctx, snap))

	del, err := m2.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, del.Blobs, 1)
	assert.Equal(t, "k1", del.Blobs[0].Info.Key)

	require.NoError(t, m2.UndeleteBlob(ctx, "c1", "k1"))
	got, err := m2.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got.Data))
}

// TestSoftDeleteConcurrent exercises concurrent deletes and undeletes across
// distinct blobs under -race to prove the soft-delete store is write-safe.
func TestSoftDeleteConcurrent(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	enableSoftDelete(t, m, 7)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	const n = 20

	keys := make([]string, n)
	for i := range keys {
		keys[i] = "k" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		require.NoError(t, m.PutObject(ctx, "c1", keys[i], []byte("v"), "text/plain", nil))
	}

	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_ = m.DeleteObject(ctx, "c1", key)
			_ = m.UndeleteBlob(ctx, "c1", key)
		}(k)
	}
	wg.Wait()

	// Every blob is active again; none is left soft-deleted.
	del, err := m.ListDeletedBlobs(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, del.Blobs)

	live, err := m.ListObjects(ctx, "c1", driver.ListOptions{MaxKeys: n})
	require.NoError(t, err)
	assert.Len(t, live.Objects, n)
}
