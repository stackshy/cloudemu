package acr

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func falsePtr() *bool {
	f := false

	return &f
}

func truePtr() *bool {
	t := true

	return &t
}

func TestRepositoryAttributesRoundTrip(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")

	attrs, err := m.GetRepositoryAttributes(ctx, "app")
	require.NoError(t, err)
	assert.True(t, *attrs.DeleteEnabled)
	assert.True(t, *attrs.WriteEnabled)
	assert.True(t, *attrs.ListEnabled)
	assert.True(t, *attrs.ReadEnabled)

	updated, err := m.UpdateRepositoryAttributes(ctx, "app", driver.AzureChangeableAttributes{DeleteEnabled: falsePtr()})
	require.NoError(t, err)
	assert.False(t, *updated.DeleteEnabled)
	assert.True(t, *updated.WriteEnabled, "unspecified fields must be left unchanged")

	_, err = m.GetRepositoryAttributes(ctx, "ghost")
	require.Error(t, err)

	_, err = m.UpdateRepositoryAttributes(ctx, "ghost", driver.AzureChangeableAttributes{})
	require.Error(t, err)
}

func TestRepositoryDeleteEnabledBlocksDeleteRepository(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "locked")

	_, err := m.UpdateRepositoryAttributes(ctx, "locked", driver.AzureChangeableAttributes{DeleteEnabled: falsePtr()})
	require.NoError(t, err)

	err = m.DeleteRepository(ctx, "locked", true)
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err))

	_, err = m.UpdateRepositoryAttributes(ctx, "locked", driver.AzureChangeableAttributes{DeleteEnabled: truePtr()})
	require.NoError(t, err)

	require.NoError(t, m.DeleteRepository(ctx, "locked", true))
}

func TestRepositoryWriteEnabledBlocksPutImage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")

	_, err := m.UpdateRepositoryAttributes(ctx, "app", driver.AzureChangeableAttributes{WriteEnabled: falsePtr()})
	require.NoError(t, err)

	_, err = m.PutImage(ctx, &driver.ImageManifest{Repository: "app", Tag: "v1"})
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err))
}

func TestRepositoryListEnabledHidesFromListRepositories(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "hidden")
	createTestRepo(t, m, "visible")

	_, err := m.UpdateRepositoryAttributes(ctx, "hidden", driver.AzureChangeableAttributes{ListEnabled: falsePtr()})
	require.NoError(t, err)

	repos, err := m.ListRepositories(ctx)
	require.NoError(t, err)

	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}

	hasSuffix := func(names []string, suffix string) bool {
		for _, n := range names {
			if strings.HasSuffix(n, "/"+suffix) {
				return true
			}
		}

		return false
	}

	assert.False(t, hasSuffix(names, "hidden"))
	assert.True(t, hasSuffix(names, "visible"))

	// Direct fetch still works: listEnabled only hides from listing.
	_, err = m.GetRepository(ctx, "hidden")
	require.NoError(t, err)
}

func TestTagAttributesRoundTrip(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	pushTestImage(t, m, "app", "v1")

	attrs, err := m.GetTagAttributes(ctx, "app", "v1")
	require.NoError(t, err)
	assert.True(t, *attrs.DeleteEnabled)

	updated, err := m.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{WriteEnabled: falsePtr()})
	require.NoError(t, err)
	assert.False(t, *updated.WriteEnabled)
	assert.True(t, *updated.DeleteEnabled)

	_, err = m.GetTagAttributes(ctx, "app", "ghost")
	require.Error(t, err)

	_, err = m.UpdateTagAttributes(ctx, "app", "ghost", driver.AzureChangeableAttributes{})
	require.Error(t, err)

	_, err = m.UpdateTagAttributes(ctx, "ghost-repo", "v1", driver.AzureChangeableAttributes{})
	require.Error(t, err)
}

func TestTagDeleteEnabledBlocksDeleteTag(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	pushTestImage(t, m, "app", "v1")

	_, err := m.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{DeleteEnabled: falsePtr()})
	require.NoError(t, err)

	err = m.DeleteTag(ctx, "app", "v1")
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err))

	_, err = m.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{DeleteEnabled: truePtr()})
	require.NoError(t, err)

	require.NoError(t, m.DeleteTag(ctx, "app", "v1"))
}

func TestTagWriteEnabledBlocksOverwriteAndRetag(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	pushTestImage(t, m, "app", "v1")
	pushTestImage(t, m, "app", "v2")

	_, err := m.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{WriteEnabled: falsePtr()})
	require.NoError(t, err)

	t.Run("PutImage overwrite blocked", func(t *testing.T) {
		_, err := m.PutImage(ctx, &driver.ImageManifest{Repository: "app", Tag: "v1"})
		require.Error(t, err)
		assert.True(t, errors.IsFailedPrecondition(err))
	})

	t.Run("TagImage retag blocked", func(t *testing.T) {
		err := m.TagImage(ctx, "app", "v2", "v1")
		require.Error(t, err)
		assert.True(t, errors.IsFailedPrecondition(err))
	})

	t.Run("new tag unaffected", func(t *testing.T) {
		require.NoError(t, m.TagImage(ctx, "app", "v2", "latest"))
	})
}

func TestTagListEnabledHidesFromListImagesTags(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	pushTestImage(t, m, "app", "v1")
	pushTestImage(t, m, "app", "v2")

	_, err := m.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{ListEnabled: falsePtr()})
	require.NoError(t, err)

	// ListImages is manifest-scoped, not tag-scoped: a tag-level list lock
	// leaves the manifest (and its other tags) fully listed.
	images, err := m.ListImages(ctx, "app")
	require.NoError(t, err)
	assert.Len(t, images, 2)
}

func TestForgetTagResetsAttributesOnRecreate(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	pushTestImage(t, m, "app", "v1")

	_, err := m.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{DeleteEnabled: falsePtr(), WriteEnabled: falsePtr()})
	require.NoError(t, err)

	_, err = m.UpdateTagAttributes(ctx, "app", "v1", driver.AzureChangeableAttributes{DeleteEnabled: truePtr()})
	require.NoError(t, err)
	require.NoError(t, m.DeleteTag(ctx, "app", "v1"))

	// A brand new tag pushed under the same name is a new resource: it must
	// not inherit the deleted tag's writeEnabled=false lock.
	pushTestImage(t, m, "app", "v1")

	attrs, err := m.GetTagAttributes(ctx, "app", "v1")
	require.NoError(t, err)
	assert.True(t, *attrs.WriteEnabled)
}

func TestManifestAttributesRoundTrip(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	detail := pushTestImage(t, m, "app", "v1")

	attrs, err := m.GetManifestAttributes(ctx, "app", detail.Digest)
	require.NoError(t, err)
	assert.True(t, *attrs.DeleteEnabled)

	updated, err := m.UpdateManifestAttributes(ctx, "app", detail.Digest, driver.AzureChangeableAttributes{ListEnabled: falsePtr()})
	require.NoError(t, err)
	assert.False(t, *updated.ListEnabled)

	_, err = m.GetManifestAttributes(ctx, "app", "sha256:ghost")
	require.Error(t, err)

	_, err = m.UpdateManifestAttributes(ctx, "app", "sha256:ghost", driver.AzureChangeableAttributes{})
	require.Error(t, err)
}

func TestManifestDeleteEnabledBlocksDeleteImage(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	detail := pushTestImage(t, m, "app", "v1")

	_, err := m.UpdateManifestAttributes(ctx, "app", detail.Digest, driver.AzureChangeableAttributes{DeleteEnabled: falsePtr()})
	require.NoError(t, err)

	err = m.DeleteImage(ctx, "app", detail.Digest)
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err))

	_, err = m.UpdateManifestAttributes(ctx, "app", detail.Digest, driver.AzureChangeableAttributes{DeleteEnabled: truePtr()})
	require.NoError(t, err)

	require.NoError(t, m.DeleteImage(ctx, "app", detail.Digest))
}

func TestManifestListEnabledHidesFromListImages(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	hidden := pushTestImage(t, m, "app", "v1")
	pushTestImage(t, m, "app", "v2")

	_, err := m.UpdateManifestAttributes(ctx, "app", hidden.Digest, driver.AzureChangeableAttributes{ListEnabled: falsePtr()})
	require.NoError(t, err)

	images, err := m.ListImages(ctx, "app")
	require.NoError(t, err)
	assert.Len(t, images, 1)
	assert.Equal(t, "v2", images[0].Tags[0])

	// Direct fetch still works: listEnabled only hides from listing.
	_, err = m.GetImage(ctx, "app", hidden.Digest)
	require.NoError(t, err)
}

func TestManifestAttributesPreservedAcrossRepush(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")
	detail := pushTestImage(t, m, "app", "v1")

	_, err := m.UpdateManifestAttributes(ctx, "app", detail.Digest, driver.AzureChangeableAttributes{ListEnabled: falsePtr()})
	require.NoError(t, err)

	// Re-pushing the exact same digest (e.g. re-tagging it) must not reset the
	// manifest's changeableAttributes back to fully enabled.
	_, err = m.PutImage(ctx, &driver.ImageManifest{Repository: "app", Digest: detail.Digest, Tag: "v2"})
	require.NoError(t, err)

	attrs, err := m.GetManifestAttributes(ctx, "app", detail.Digest)
	require.NoError(t, err)
	assert.False(t, *attrs.ListEnabled)
}
