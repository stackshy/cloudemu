package blobstorage

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindBlobsByTags(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.CreateBucket(ctx, "c2"))

	put := func(container, key string, tags map[string]string) {
		require.NoError(t, m.PutObject(ctx, container, key, []byte("x"), "text/plain", nil))
		if tags != nil {
			require.NoError(t, m.PutObjectTagging(ctx, container, key, tags))
		}
	}

	put("c1", "a", map[string]string{"env": "prod", "team": "red"})
	put("c1", "b", map[string]string{"env": "dev"})
	put("c1", "notag", nil)
	put("c2", "c", map[string]string{"env": "prod"})

	refs := func(container string, match map[string]string) []string {
		res, err := m.FindBlobsByTags(ctx, container, match)
		require.NoError(t, err)

		out := make([]string, 0, len(res))
		for _, b := range res {
			out = append(out, b.Container+"/"+b.Name)
		}

		sort.Strings(out)

		return out
	}

	// Account-wide env=prod spans both containers.
	assert.Equal(t, []string{"c1/a", "c2/c"}, refs("", map[string]string{"env": "prod"}))

	// Multi-term AND narrows to one blob.
	assert.Equal(t, []string{"c1/a"}, refs("", map[string]string{"env": "prod", "team": "red"}))

	// Container-scoped search.
	assert.Equal(t, []string{"c1/a"}, refs("c1", map[string]string{"env": "prod"}))

	// An untagged blob never matches an empty query.
	all := refs("c1", map[string]string{})
	assert.NotContains(t, all, "c1/notag")
	assert.ElementsMatch(t, []string{"c1/a", "c1/b"}, all)

	// Unknown container errors.
	_, err := m.FindBlobsByTags(ctx, "missing", map[string]string{"env": "prod"})
	require.Error(t, err)
}
