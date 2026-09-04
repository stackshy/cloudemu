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

	detail, err := src.PutImage(ctx, &driver.ImageManifest{Repository: "app", Tag: "v1", SizeBytes: 1024})
	require.NoError(t, err)

	require.NoError(t, src.PutLifecyclePolicy(ctx, "app", driver.LifecyclePolicy{}))

	no := false
	_, err = src.UpdateRepositoryAttributes(ctx, "app", driver.AzureChangeableAttributes{DeleteEnabled: &no})
	require.NoError(t, err)
	_, err = src.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{WriteEnabled: &no})
	require.NoError(t, err)
	_, err = src.UpdateManifestAttributes(ctx, "app", detail.Digest, driver.AzureChangeableAttributes{DeleteEnabled: &no})
	require.NoError(t, err)

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

	repoAttrs, err := dst.GetRepositoryAttributes(ctx, "app")
	require.NoError(t, err)
	assert.False(t, *repoAttrs.DeleteEnabled, "repository deleteEnabled=false must survive the round trip")

	tagAttrs, err := dst.GetTagAttributes(ctx, "app", "v1")
	require.NoError(t, err)
	assert.False(t, *tagAttrs.WriteEnabled, "tag writeEnabled=false must survive the round trip")

	manifestAttrs, err := dst.GetManifestAttributes(ctx, "app", detail.Digest)
	require.NoError(t, err)
	assert.False(t, *manifestAttrs.DeleteEnabled, "manifest deleteEnabled=false must survive the round trip")
}

// TestSnapshotRestoreDefaultsMissingAttributesToEnabled proves a snapshot
// taken before changeableAttributes existed (Attrs fields absent from the
// JSON) restores every resource fully enabled rather than fully locked.
func TestSnapshotRestoreDefaultsMissingAttributesToEnabled(t *testing.T) {
	ctx := context.Background()
	dst, _ := newTestMock()

	legacy := `{"repos":{"app":{"info":{"name":"app","uri":"cloudemu.azurecr.io/app"},` +
		`"images":{"sha256:legacy":{"detail":{"digest":"sha256:legacy","tags":["v1"]}}}}}}`

	require.NoError(t, dst.Restore(ctx, []byte(legacy)))

	repoAttrs, err := dst.GetRepositoryAttributes(ctx, "app")
	require.NoError(t, err)
	assert.True(t, *repoAttrs.DeleteEnabled)
	assert.True(t, *repoAttrs.WriteEnabled)
	assert.True(t, *repoAttrs.ListEnabled)
	assert.True(t, *repoAttrs.ReadEnabled)

	manifestAttrs, err := dst.GetManifestAttributes(ctx, "app", "sha256:legacy")
	require.NoError(t, err)
	assert.True(t, *manifestAttrs.DeleteEnabled)
	assert.True(t, *manifestAttrs.WriteEnabled)

	tagAttrs, err := dst.GetTagAttributes(ctx, "app", "v1")
	require.NoError(t, err)
	assert.True(t, *tagAttrs.DeleteEnabled)
}
