package artifactregistry

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRoundTripArtifactRegistry proves the mock serializes every
// repository (with its images) and restores it into a fresh mock
// identity-preservingly: re-snapshotting yields byte-identical JSON and an image
// pushed before the snapshot is readable after the restore.
func TestSnapshotRoundTripArtifactRegistry(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	_, err := src.CreateRepository(ctx, driver.RepositoryConfig{Name: "my-repo", Tags: map[string]string{"env": "prod"}})
	require.NoError(t, err)

	_, err = src.PutImage(ctx, &driver.ImageManifest{Repository: "my-repo", Tag: "v1.0", SizeBytes: 1024})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2), "snapshot must be stable across restore")

	repo, err := dst.GetRepository(ctx, "my-repo")
	require.NoError(t, err)
	assert.Contains(t, repo.Name, "my-repo")

	images, err := dst.ListImages(ctx, "my-repo")
	require.NoError(t, err)
	require.Len(t, images, 1)
}

// TestSnapshotEmptyArtifactRegistry confirms a fresh mock snapshots and restores cleanly.
func TestSnapshotEmptyArtifactRegistry(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))
}
