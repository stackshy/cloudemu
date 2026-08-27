package ecr

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRoundTripECR proves a snapshot/restore round-trip preserves a
// repository, its pushed image, and its settings.
func TestSnapshotRoundTripECR(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	_, err := src.CreateRepository(ctx, driver.RepositoryConfig{Name: "app", ImageTagMutability: "IMMUTABLE"})
	require.NoError(t, err)

	_, err = src.PutImage(ctx, &driver.ImageManifest{
		Repository: "app", Tag: "v1", Digest: "sha256:abc", Layers: []driver.LayerInfo{{Digest: "sha256:l1"}},
	})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	repo, err := dst.GetRepository(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "IMMUTABLE", repo.ImageTagMutability)

	imgs, err := dst.ListImages(ctx, "app")
	require.NoError(t, err)
	require.Len(t, imgs, 1)
	assert.Equal(t, "sha256:abc", imgs[0].Digest)
}
