package blobstorage

import (
	"context"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateBucketAttributes_SeedsDefaultsWhenAbsent verifies a PATCH-style
// update on a never-created account starts from the real-Azure baseline
// (Standard_LRS/StorageV2/Hot), matching BucketAttributes' own default.
func TestUpdateBucketAttributes_SeedsDefaultsWhenAbsent(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	updated, err := m.UpdateBucketAttributes(ctx, "never-seeded", func(a driver.AccountAttributes) driver.AccountAttributes {
		a.AccessTier = "Cool"
		return a
	})
	require.NoError(t, err)

	assert.Equal(t, "Standard_LRS", updated.SKU)
	assert.Equal(t, "StorageV2", updated.Kind)
	assert.Equal(t, "Cool", updated.AccessTier)
}

// TestUpdateBucketAttributes_PreservesUntouchedFields verifies a partial
// update via fn only changes what fn changes, leaving every other seeded
// field intact — the non-destructive PATCH semantics the ARM handler relies
// on.
func TestUpdateBucketAttributes_PreservesUntouchedFields(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	m.SetBucketAttributes("acct1", driver.AccountAttributes{
		SKU: "Premium_LRS", Kind: "BlockBlobStorage", AccessTier: "Hot",
		Location: "westus2", Tags: map[string]string{"env": "prod"},
	})

	updated, err := m.UpdateBucketAttributes(ctx, "acct1", func(a driver.AccountAttributes) driver.AccountAttributes {
		a.AccessTier = "Cool"
		return a
	})
	require.NoError(t, err)

	assert.Equal(t, "Cool", updated.AccessTier)
	assert.Equal(t, "Premium_LRS", updated.SKU)
	assert.Equal(t, "BlockBlobStorage", updated.Kind)
	assert.Equal(t, "westus2", updated.Location)
	assert.Equal(t, map[string]string{"env": "prod"}, updated.Tags)

	// The store itself, not just the returned value, reflects the update.
	stored, err := m.BucketAttributes(ctx, "acct1")
	require.NoError(t, err)
	assert.Equal(t, "Cool", stored.AccessTier)
}

// TestUpdateBucketAttributes_ConcurrentNoLostUpdates drives many concurrent
// updates through UpdateBucketAttributes and asserts every one of them is
// reflected in the end state — proving the atomic Update-based
// read-modify-write doesn't lose writes the way a Get-then-Set pair would
// under concurrent access.
func TestUpdateBucketAttributes_ConcurrentNoLostUpdates(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const n = 100

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_, err := m.UpdateBucketAttributes(ctx, "acct-concurrent", func(a driver.AccountAttributes) driver.AccountAttributes {
				if a.Tags == nil {
					a.Tags = map[string]string{}
				}

				a.Tags[keyFor(i)] = "set"

				return a
			})
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	final, err := m.BucketAttributes(ctx, "acct-concurrent")
	require.NoError(t, err)
	assert.Len(t, final.Tags, n, "every concurrent update must be reflected — none lost")
}

func keyFor(i int) string {
	return "k" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

// TestBlobServiceProperties_DefaultsToZeroValue verifies an account that
// never had blob service properties set reports all-disabled defaults,
// matching a freshly created real Azure account.
func TestBlobServiceProperties_DefaultsToZeroValue(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	props, err := m.BlobServiceProperties(ctx, "never-seeded")
	require.NoError(t, err)

	assert.False(t, props.IsVersioningEnabled)
	assert.False(t, props.ChangeFeedEnabled)
	assert.False(t, props.DeleteRetentionEnabled)
	assert.Empty(t, props.CORS)
}

// TestBlobServiceProperties_RoundTrip verifies a full Set survives an
// independent Get.
func TestBlobServiceProperties_RoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	want := driver.BlobServiceProperties{
		IsVersioningEnabled:     true,
		ChangeFeedEnabled:       true,
		ChangeFeedRetentionDays: 30,
		DeleteRetentionEnabled:  true,
		DeleteRetentionDays:     7,
		CORS: []driver.CORSRule{{
			AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, MaxAgeSeconds: 3600,
		}},
	}

	require.NoError(t, m.SetBlobServiceProperties(ctx, "acct1", want))

	got, err := m.BlobServiceProperties(ctx, "acct1")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestBlobServiceProperties_SetIsFullReplace verifies a second Set call
// wholesale replaces the first — Azure's Set Blob Service Properties takes a
// complete document each call, not a merge patch.
func TestBlobServiceProperties_SetIsFullReplace(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.SetBlobServiceProperties(ctx, "acct1", driver.BlobServiceProperties{
		IsVersioningEnabled: true, DeleteRetentionEnabled: true, DeleteRetentionDays: 7,
	}))
	require.NoError(t, m.SetBlobServiceProperties(ctx, "acct1", driver.BlobServiceProperties{
		ChangeFeedEnabled: true,
	}))

	got, err := m.BlobServiceProperties(ctx, "acct1")
	require.NoError(t, err)

	assert.False(t, got.IsVersioningEnabled, "second Set must clear fields it omits")
	assert.False(t, got.DeleteRetentionEnabled)
	assert.True(t, got.ChangeFeedEnabled)
}
