package blobstorage

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enableVersioning turns on account-level blob versioning for the single
// account the data plane models.
func enableVersioning(t *testing.T, m *Mock) {
	t.Helper()
	require.NoError(t, m.SetBlobServiceProperties(context.Background(), AccountName,
		driver.BlobServiceProperties{IsVersioningEnabled: true}))
}

func TestVersioningDisabledMintsNoVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	enabled, err := m.VersioningEnabled(ctx)
	require.NoError(t, err)
	assert.False(t, enabled)

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	info, err := m.HeadObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Empty(t, info.VersionID, "no version id when versioning is disabled")
}

func TestVersioningWriteMintsVersions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	enableVersioning(t, m)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	enabled, err := m.VersioningEnabled(ctx)
	require.NoError(t, err)
	assert.True(t, enabled)

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v2"), "text/plain", nil))

	// Base blob returns the current (second) content.
	base, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "v2", string(base.Data))
	require.NotEmpty(t, base.Info.VersionID)

	// Two distinct versions listed, current marked latest.
	res, err := m.ListBlobVersions(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, res.Versions, 2)
	assert.NotEqual(t, res.Versions[0].VersionID, res.Versions[1].VersionID)
	assert.False(t, res.Versions[0].IsLatest, "older version sorts first and is not current")
	assert.True(t, res.Versions[1].IsLatest, "current version sorts last")
	assert.Equal(t, base.Info.VersionID, res.Versions[1].VersionID)

	firstVersion := res.Versions[0].VersionID
	currentVersion := res.Versions[1].VersionID

	// Read each version by id.
	v1, err := m.GetBlobVersion(ctx, "c1", "k1", firstVersion)
	require.NoError(t, err)
	assert.Equal(t, "v1", string(v1.Data))

	vCur, err := m.GetBlobVersion(ctx, "c1", "k1", currentVersion)
	require.NoError(t, err)
	assert.Equal(t, "v2", string(vCur.Data))

	// Head a version.
	info, err := m.HeadBlobVersion(ctx, "c1", "k1", firstVersion)
	require.NoError(t, err)
	assert.Equal(t, firstVersion, info.VersionID)
}

func TestDeleteSpecificVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	enableVersioning(t, m)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v2"), "text/plain", nil))

	res, err := m.ListBlobVersions(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, res.Versions, 2)
	older := res.Versions[0].VersionID

	// Delete the older version — it's gone, current remains.
	require.NoError(t, m.DeleteBlobVersion(ctx, "c1", "k1", older))

	_, err = m.GetBlobVersion(ctx, "c1", "k1", older)
	assert.True(t, cerrors.IsNotFound(err))

	res, err = m.ListBlobVersions(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, res.Versions, 1)
	assert.True(t, res.Versions[0].IsLatest)

	// The base blob is still readable (current version untouched).
	base, err := m.GetObject(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, "v2", string(base.Data))

	// Deleting a non-existent version is NotFound.
	err = m.DeleteBlobVersion(ctx, "c1", "k1", "nope")
	assert.True(t, cerrors.IsNotFound(err))
}

func TestDeleteBaseKeepsVersions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	enableVersioning(t, m)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v2"), "text/plain", nil))

	// Delete the base blob — with versioning on, the versions survive.
	require.NoError(t, m.DeleteObject(ctx, "c1", "k1"))

	_, err := m.GetObject(ctx, "c1", "k1")
	assert.True(t, cerrors.IsNotFound(err), "base blob no longer exists")

	res, err := m.ListBlobVersions(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, res.Versions, 2, "both versions remain listable after base delete")
	for _, v := range res.Versions {
		assert.False(t, v.IsLatest, "no current version after base delete")
	}
}

func TestDeleteCurrentVersionRemovesBase(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	enableVersioning(t, m)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	info, err := m.HeadObject(ctx, "c1", "k1")
	require.NoError(t, err)
	current := info.VersionID
	require.NotEmpty(t, current)

	require.NoError(t, m.DeleteBlobVersion(ctx, "c1", "k1", current))

	_, err = m.GetObject(ctx, "c1", "k1")
	assert.True(t, cerrors.IsNotFound(err), "deleting the current version removes the base blob")
}

func TestSetTierAndPropertiesDoNotMintVersion(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	enableVersioning(t, m)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))

	countVersions := func() int {
		res, err := m.ListBlobVersions(ctx, "c1", driver.ListOptions{})
		require.NoError(t, err)

		return len(res.Versions)
	}

	require.Equal(t, 1, countVersions())

	// Set Blob Tier is an in-place tier change — no new version.
	_, err := m.SetBlobTier(ctx, "c1", "k1", accessTierCool)
	require.NoError(t, err)
	assert.Equal(t, 1, countVersions(), "Set Blob Tier must not mint a version")

	// Set Blob Properties is an in-place property update — no new version.
	_, err = m.SetBlobProperties(ctx, "c1", "k1", &driver.BlobProperties{ContentType: "application/json"})
	require.NoError(t, err)
	assert.Equal(t, 1, countVersions(), "Set Blob Properties must not mint a version")

	// Set Blob Metadata DOES mint a version.
	_, err = m.SetBlobMetadata(ctx, "c1", "k1", map[string]string{"k": "v"})
	require.NoError(t, err)
	assert.Equal(t, 2, countVersions(), "Set Blob Metadata mints a version")

	// A subsequent overwrite (Put Blob) also mints a version.
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v2"), "text/plain", nil))
	assert.Equal(t, 3, countVersions(), "Put Blob mints a version")
}

func TestVersionsSurviveSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	enableVersioning(t, m)
	require.NoError(t, m.CreateBucket(ctx, "c1"))

	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "c1", "k1", []byte("v2"), "text/plain", nil))

	data, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, data))

	res, err := restored.ListBlobVersions(ctx, "c1", driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, res.Versions, 2)

	v1, err := restored.GetBlobVersion(ctx, "c1", "k1", res.Versions[0].VersionID)
	require.NoError(t, err)
	assert.Equal(t, "v1", string(v1.Data))
}
