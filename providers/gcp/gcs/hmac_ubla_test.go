package gcs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProject = "test-project"
	testSA      = "svc@test-project.iam.gserviceaccount.com"
)

func decodeHMACMeta(t *testing.T, raw []byte) hmacMetaJSON {
	t.Helper()

	var m hmacMetaJSON
	require.NoError(t, json.Unmarshal(raw, &m))

	return m
}

// TestHMACKeyCRUD walks create -> get -> update -> delete and the delete guard.
func TestHMACKeyCRUD(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	metaRaw, secret, err := m.CreateHMACKeyGCS(ctx, testProject, testSA)
	require.NoError(t, err)
	assert.NotEmpty(t, secret)

	meta := decodeHMACMeta(t, metaRaw)
	assert.NotEmpty(t, meta.AccessID)
	assert.Equal(t, hmacStateActive, meta.State)
	assert.Equal(t, testSA, meta.ServiceAccountEmail)
	assert.Equal(t, testProject, meta.ProjectID)

	// Get returns metadata (no secret in the metadata contract).
	gotRaw, err := m.GetHMACKeyGCS(ctx, testProject, meta.AccessID)
	require.NoError(t, err)
	assert.Equal(t, meta.AccessID, decodeHMACMeta(t, gotRaw).AccessID)

	// A key under a different project is invisible.
	_, err = m.GetHMACKeyGCS(ctx, "other-project", meta.AccessID)
	assert.True(t, cerrors.IsNotFound(err))

	// Deleting an ACTIVE key is rejected.
	err = m.DeleteHMACKeyGCS(ctx, testProject, meta.AccessID)
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))

	// Deactivate, then delete succeeds.
	updRaw, err := m.UpdateHMACKeyStateGCS(ctx, testProject, meta.AccessID, hmacStateInactive)
	require.NoError(t, err)
	assert.Equal(t, hmacStateInactive, decodeHMACMeta(t, updRaw).State)

	require.NoError(t, m.DeleteHMACKeyGCS(ctx, testProject, meta.AccessID))

	_, err = m.GetHMACKeyGCS(ctx, testProject, meta.AccessID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestHMACKeyValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _, err := m.CreateHMACKeyGCS(ctx, testProject, "")
	assert.True(t, cerrors.IsInvalidArgument(err))

	meta, _, err := m.CreateHMACKeyGCS(ctx, testProject, testSA)
	require.NoError(t, err)

	accessID := decodeHMACMeta(t, meta).AccessID
	_, err = m.UpdateHMACKeyStateGCS(ctx, testProject, accessID, "BOGUS")
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestHMACKeyListFiltersByProjectAndAccount(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _, err := m.CreateHMACKeyGCS(ctx, testProject, testSA)
	require.NoError(t, err)
	_, _, err = m.CreateHMACKeyGCS(ctx, testProject, "other@test-project.iam.gserviceaccount.com")
	require.NoError(t, err)
	_, _, err = m.CreateHMACKeyGCS(ctx, "proj-2", testSA)
	require.NoError(t, err)

	raw, err := m.ListHMACKeysGCS(ctx, testProject, "", false)
	require.NoError(t, err)

	var all []hmacMetaJSON
	require.NoError(t, json.Unmarshal(raw, &all))
	assert.Len(t, all, 2)

	raw, err = m.ListHMACKeysGCS(ctx, testProject, testSA, false)
	require.NoError(t, err)

	var filtered []hmacMetaJSON
	require.NoError(t, json.Unmarshal(raw, &filtered))
	require.Len(t, filtered, 1)
	assert.Equal(t, testSA, filtered[0].ServiceAccountEmail)
}

// TestBucketIAMConfigRoundTrip proves UBLA + PAP persist and that enabling UBLA
// stamps a lockedTime that clears on disable.
func TestBucketIAMConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "b1"))

	enabled, locked, pap, err := m.BucketIAMConfigGCS(ctx, "b1")
	require.NoError(t, err)
	assert.False(t, enabled)
	assert.Empty(t, locked)
	assert.Empty(t, pap)

	on := true
	require.NoError(t, m.SetBucketIAMConfigGCS(ctx, "b1", &on, "enforced"))

	enabled, locked, pap, err = m.BucketIAMConfigGCS(ctx, "b1")
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.NotEmpty(t, locked)
	assert.Equal(t, "enforced", pap)

	off := false
	require.NoError(t, m.SetBucketIAMConfigGCS(ctx, "b1", &off, ""))

	enabled, locked, pap, err = m.BucketIAMConfigGCS(ctx, "b1")
	require.NoError(t, err)
	assert.False(t, enabled)
	assert.Empty(t, locked)
	// PAP is a separate field and is untouched by clearing UBLA.
	assert.Equal(t, "enforced", pap)
}

func TestBucketIAMConfigNotFound(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _, _, err := m.BucketIAMConfigGCS(ctx, "nope")
	assert.True(t, cerrors.IsNotFound(err))

	on := true
	err = m.SetBucketIAMConfigGCS(ctx, "nope", &on, "")
	assert.True(t, cerrors.IsNotFound(err))
}

// TestHMACConcurrentCreate hammers concurrent creates to prove the store stays
// consistent under -race.
func TestHMACConcurrentCreate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	const n = 50

	var wg sync.WaitGroup

	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()

			_, _, err := m.CreateHMACKeyGCS(ctx, testProject, testSA)
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	raw, err := m.ListHMACKeysGCS(ctx, testProject, "", false)
	require.NoError(t, err)

	var keys []hmacMetaJSON
	require.NoError(t, json.Unmarshal(raw, &keys))
	assert.Len(t, keys, n)
}

// TestHMACKeySurvivesSnapshot proves HMAC keys and bucket iamConfiguration
// round-trip through a snapshot/restore.
func TestHMACKeySurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	on := true
	require.NoError(t, m.SetBucketIAMConfigGCS(ctx, "b1", &on, "enforced"))

	metaRaw, _, err := m.CreateHMACKeyGCS(ctx, testProject, testSA)
	require.NoError(t, err)
	accessID := decodeHMACMeta(t, metaRaw).AccessID

	data, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, data))

	gotRaw, err := restored.GetHMACKeyGCS(ctx, testProject, accessID)
	require.NoError(t, err)
	assert.Equal(t, accessID, decodeHMACMeta(t, gotRaw).AccessID)

	enabled, locked, pap, err := restored.BucketIAMConfigGCS(ctx, "b1")
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.NotEmpty(t, locked)
	assert.Equal(t, "enforced", pap)
}
