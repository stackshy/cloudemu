package acr

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshotRoundTripACR(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	_, _, err := src.CreateOrUpdateRegistry(ctx, "rg-1", "MyReg", driver.AzureRegistryConfig{
		Location: "eastus", AdminUserEnabled: true,
	})
	require.NoError(t, err)

	_, err = src.CreateRepository(ctx, driver.RepositoryConfig{Name: "app"})
	require.NoError(t, err)

	_, err = src.PutImage(ctx, &driver.ImageManifest{Repository: "app", Tag: "v1", SizeBytes: 1024})
	require.NoError(t, err)

	require.NoError(t, src.PutLifecyclePolicy(ctx, "app", driver.LifecyclePolicy{}))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	reg, err := dst.GetRegistry(ctx, "rg-1", "MyReg")
	require.NoError(t, err)
	assert.Equal(t, "MyReg", reg.Name)

	repo, err := dst.GetRepository(ctx, "app")
	require.NoError(t, err)
	assert.Contains(t, repo.Name, "app")

	images, err := dst.ListImages(ctx, "app")
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, "app", images[0].Repository)
	assert.Contains(t, images[0].Tags, "v1")

	_, err = dst.GetLifecyclePolicy(ctx, "app")
	require.NoError(t, err)
}
