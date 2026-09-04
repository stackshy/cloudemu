package blobstorage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

func TestImmutabilityPolicyBlocksDeleteAndOverwrite(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	until := clk.Now().Add(24 * time.Hour)
	got, err := m.SetBlobImmutabilityPolicy(ctx, "c1", "k1",
		driver.BlobImmutabilityPolicy{ExpiryTime: until, Mode: driver.BlobImmutabilityUnlocked})
	require.NoError(t, err)
	assert.Equal(t, driver.BlobImmutabilityUnlocked, got.Mode)
	assert.True(t, got.ExpiryTime.Equal(until))

	// Within the window: delete and overwrite are blocked.
	assert.Error(t, m.DeleteObject(ctx, "c1", "k1"))
	_, err = m.PutBlockBlob(ctx, "c1", "k1", []byte("v2"), nil, nil)
	assert.Error(t, err)

	// Original content survives.
	obj, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "v1", string(obj.Data))

	// After the window elapses, both are allowed.
	clk.Advance(25 * time.Hour)
	_, err = m.PutBlockBlob(ctx, "c1", "k1", []byte("v2"), nil, nil)
	require.NoError(t, err)
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))
}

func TestImmutabilityPolicyPastDateRejected(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	_, err := m.SetBlobImmutabilityPolicy(ctx, "c1", "k1",
		driver.BlobImmutabilityPolicy{ExpiryTime: clk.Now().Add(-time.Hour), Mode: driver.BlobImmutabilityUnlocked})
	assert.Error(t, err)
}

func TestImmutabilityLockedOnlyExtends(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	base := clk.Now().Add(48 * time.Hour)
	_, err := m.SetBlobImmutabilityPolicy(ctx, "c1", "k1",
		driver.BlobImmutabilityPolicy{ExpiryTime: base, Mode: driver.BlobImmutabilityLocked})
	require.NoError(t, err)

	// Shorten -> rejected.
	_, err = m.SetBlobImmutabilityPolicy(ctx, "c1", "k1",
		driver.BlobImmutabilityPolicy{ExpiryTime: clk.Now().Add(time.Hour), Mode: driver.BlobImmutabilityLocked})
	assert.Error(t, err)

	// Revert to unlocked -> rejected.
	_, err = m.SetBlobImmutabilityPolicy(ctx, "c1", "k1",
		driver.BlobImmutabilityPolicy{ExpiryTime: clk.Now().Add(96 * time.Hour), Mode: driver.BlobImmutabilityUnlocked})
	assert.Error(t, err)

	// Delete of a locked policy -> rejected.
	assert.Error(t, m.DeleteBlobImmutabilityPolicy(ctx, "c1", "k1"))

	// Extend -> allowed.
	_, err = m.SetBlobImmutabilityPolicy(ctx, "c1", "k1",
		driver.BlobImmutabilityPolicy{ExpiryTime: clk.Now().Add(96 * time.Hour), Mode: driver.BlobImmutabilityLocked})
	require.NoError(t, err)
}

func TestLegalHoldBlocksIndependentOfPolicy(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	require.NoError(t, m.SetBlobLegalHold(ctx, "c1", "k1", true))

	assert.Error(t, m.DeleteObject(ctx, "c1", "k1"))
	_, err := m.PutBlockBlob(ctx, "c1", "k1", []byte("v2"), nil, nil)
	assert.Error(t, err)

	policy, hold, err := m.BlobImmutability(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.True(t, hold)
	assert.Empty(t, policy.Mode)

	require.NoError(t, m.SetBlobLegalHold(ctx, "c1", "k1", false))
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))
}

// protect sets an unlocked immutability policy expiring in 24h on an existing
// blob and returns the expiry, so a test can later advance the clock past it.
func protectWithPolicy(t *testing.T, m *Mock, clk *config.FakeClock, container, blob string) time.Time {
	t.Helper()
	until := clk.Now().Add(24 * time.Hour)
	_, err := m.SetBlobImmutabilityPolicy(context.Background(), container, blob,
		driver.BlobImmutabilityPolicy{ExpiryTime: until, Mode: driver.BlobImmutabilityUnlocked})
	require.NoError(t, err)
	return until
}

// TestPageBlobPathsRespectImmutability proves none of the page-blob write paths
// (CreatePageBlob overwrite, PutPage, ClearPage) can destroy or overwrite a
// blob protected by a policy or a legal hold, and that each succeeds once the
// protection lifts.
func TestPageBlobPathsRespectImmutability(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	// A protected page blob with one written page.
	_, err := m.CreatePageBlob(ctx, "c1", "pb", 1024, nil, nil)
	require.NoError(t, err)
	_, err = m.PutPage(ctx, "c1", "pb", 0, 511, make([]byte, 512))
	require.NoError(t, err)
	protectWithPolicy(t, m, clk, "c1", "pb")

	// CreatePageBlob over the protected key is blocked; the blob survives.
	_, err = m.CreatePageBlob(ctx, "c1", "pb", 2048, nil, nil)
	assert.Error(t, err)
	info, err := m.HeadObject(ctx, "c1", "pb")
	require.NoError(t, err)
	assert.Equal(t, int64(1024), info.Size)

	// PutPage / ClearPage on the protected page blob are blocked.
	_, err = m.PutPage(ctx, "c1", "pb", 512, 1023, make([]byte, 512))
	assert.Error(t, err)
	_, err = m.ClearPage(ctx, "c1", "pb", 0, 511)
	assert.Error(t, err)

	// After the window elapses, page writes proceed.
	clk.Advance(25 * time.Hour)
	_, err = m.PutPage(ctx, "c1", "pb", 512, 1023, make([]byte, 512))
	require.NoError(t, err)
	_, err = m.ClearPage(ctx, "c1", "pb", 0, 511)
	require.NoError(t, err)
	_, err = m.CreatePageBlob(ctx, "c1", "pb", 2048, nil, nil)
	require.NoError(t, err)
}

// TestPageAndAppendCreateBlockedByLegalHold proves a legal hold (no time window)
// blocks the create-overwrite and page-write paths, independent of any policy.
func TestPageAndAppendCreateBlockedByLegalHold(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	// A protected page blob under legal hold.
	_, err := m.CreatePageBlob(ctx, "c1", "pb", 1024, nil, nil)
	require.NoError(t, err)
	require.NoError(t, m.SetBlobLegalHold(ctx, "c1", "pb", true))

	_, err = m.CreatePageBlob(ctx, "c1", "pb", 2048, nil, nil)
	assert.Error(t, err)
	_, err = m.PutPage(ctx, "c1", "pb", 0, 511, make([]byte, 512))
	assert.Error(t, err)
	_, err = m.ClearPage(ctx, "c1", "pb", 0, 511)
	assert.Error(t, err)

	// Clearing the hold restores mutability.
	require.NoError(t, m.SetBlobLegalHold(ctx, "c1", "pb", false))
	_, err = m.PutPage(ctx, "c1", "pb", 0, 511, make([]byte, 512))
	require.NoError(t, err)

	// A protected block blob cannot be replaced by an append blob create.
	require.NoError(t, m.PutObject(ctx, "c1", "ab", []byte("original"), "text/plain", nil))
	require.NoError(t, m.SetBlobLegalHold(ctx, "c1", "ab", true))
	_, err = m.CreateAppendBlob(ctx, "c1", "ab", "text/plain", nil)
	assert.Error(t, err)
	obj, err := m.GetObject(ctx, "c1", "ab")
	require.NoError(t, err)
	assert.Equal(t, "original", string(obj.Data))

	require.NoError(t, m.SetBlobLegalHold(ctx, "c1", "ab", false))
	_, err = m.CreateAppendBlob(ctx, "c1", "ab", "text/plain", nil)
	require.NoError(t, err)
}

// TestAppendBlobCreateBlockedByPolicy proves CreateAppendBlob over a
// policy-protected blob is blocked and succeeds once the window elapses.
func TestAppendBlobCreateBlockedByPolicy(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "ab", []byte("original"), "text/plain", nil))
	protectWithPolicy(t, m, clk, "c1", "ab")

	_, err := m.CreateAppendBlob(ctx, "c1", "ab", "text/plain", nil)
	assert.Error(t, err)
	obj, err := m.GetObject(ctx, "c1", "ab")
	require.NoError(t, err)
	assert.Equal(t, "original", string(obj.Data))

	clk.Advance(25 * time.Hour)
	_, err = m.CreateAppendBlob(ctx, "c1", "ab", "text/plain", nil)
	require.NoError(t, err)
}

func TestUnlockedPolicyDeletable(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	_, err := m.SetBlobImmutabilityPolicy(ctx, "c1", "k1",
		driver.BlobImmutabilityPolicy{ExpiryTime: clk.Now().Add(24 * time.Hour), Mode: driver.BlobImmutabilityUnlocked})
	require.NoError(t, err)

	require.NoError(t, m.DeleteBlobImmutabilityPolicy(ctx, "c1", "k1"))

	// Policy gone -> delete allowed within what was the window.
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))
}
