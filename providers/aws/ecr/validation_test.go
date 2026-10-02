package ecr

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRepositoryNameRules(t *testing.T) {
	bad := []string{"Invalid_Repo_UPPER", "a", strings.Repeat("a", 257), "-lead", "trail-", "a//b", "a..b"}
	good := []string{"ab", "my-repo", "team/app", "a__b", "a---b", "ns/sub.name_x", strings.Repeat("a", 256)}

	m, _ := newTestMock()
	ctx := context.Background()

	for _, name := range bad {
		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{Name: name})
		assert.True(t, cerrors.IsInvalidArgument(err), "name %q: err = %v", name, err)
	}

	for _, name := range good {
		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{Name: name})
		assert.NoError(t, err, "name %q", name)
	}
}

func TestPutImageDigestMustMatchManifest(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	createTestRepo(t, m, "repo")

	_, err := m.PutImage(ctx, &driver.ImageManifest{
		Repository: "repo", Tag: "v1", Manifest: `{}`,
		Digest: "sha256:" + strings.Repeat("0", 64),
	})

	var ex interface{ ECRException() string }
	require.True(t, stderrors.As(err, &ex), "err = %v", err)
	assert.Equal(t, "ImageDigestDoesNotMatchException", ex.ECRException())
	assert.True(t, cerrors.IsInvalidArgument(err))

	img, err := m.PutImage(ctx, &driver.ImageManifest{
		Repository: "repo", Tag: "v1", Manifest: `{}`, Digest: manifestDigest(`{}`),
	})
	require.NoError(t, err)
	assert.Equal(t, manifestDigest(`{}`), img.Digest)

	// A digest with no manifest is still taken as given.
	_, err = m.PutImage(ctx, &driver.ImageManifest{Repository: "repo", Tag: "v2", Digest: "sha256:abc"})
	require.NoError(t, err)
}
