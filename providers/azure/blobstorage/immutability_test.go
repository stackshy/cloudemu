package blobstorage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
