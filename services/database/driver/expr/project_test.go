package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProjectionEmpty(t *testing.T) {
	paths, err := ParseProjection("", nil)
	require.NoError(t, err)
	assert.Nil(t, paths, "an empty projection yields no paths")

	paths, err = ParseProjection("   ", nil)
	require.NoError(t, err)
	assert.Nil(t, paths)
}

func TestParseProjectionPaths(t *testing.T) {
	paths, err := ParseProjection("a, b.c, d[0]", nil)
	require.NoError(t, err)
	require.Len(t, paths, 3)

	assert.Equal(t, []PathPart{{Name: "a"}}, paths[0].Parts)
	assert.Equal(t, []PathPart{{Name: "b"}, {Name: "c"}}, paths[1].Parts)
	assert.Equal(t, []PathPart{{Name: "d"}, {Index: 0, IsIndex: true}}, paths[2].Parts)
}

func TestParseProjectionAlias(t *testing.T) {
	paths, err := ParseProjection("#n, #a.city", map[string]string{"#n": "name", "#a": "address"})
	require.NoError(t, err)
	require.Len(t, paths, 2)

	assert.Equal(t, "name", paths[0].Parts[0].Name)
	assert.Equal(t, "address", paths[1].Parts[0].Name)
	assert.Equal(t, "city", paths[1].Parts[1].Name)
}

func TestParseProjectionErrors(t *testing.T) {
	for _, raw := range []string{",a", "a b", "a,", "#missing"} {
		_, err := ParseProjection(raw, nil)
		assert.Error(t, err, "projection %q should not parse", raw)
	}
}

func TestProjectSubsetAndAbsent(t *testing.T) {
	item := map[string]any{"id": "u1", "name": "alice", "age": float64(30)}

	paths, err := ParseProjection("id, name, missing", nil)
	require.NoError(t, err)

	got := Project(item, paths)
	assert.Equal(t, map[string]any{"id": "u1", "name": "alice"}, got,
		"only present projected paths are kept; missing is omitted")
}

func TestProjectNestedMap(t *testing.T) {
	item := map[string]any{
		"id": "u1",
		"address": map[string]any{
			"city": "Paris",
			"zip":  "75001",
		},
	}

	paths, err := ParseProjection("address.city", nil)
	require.NoError(t, err)

	got := Project(item, paths)
	assert.Equal(t, map[string]any{"address": map[string]any{"city": "Paris"}}, got,
		"the nested structure is rebuilt with only the projected sub-path")
}

func TestProjectListIndex(t *testing.T) {
	item := map[string]any{
		"tags": []any{"a", "b", "c"},
	}

	paths, err := ParseProjection("tags[1]", nil)
	require.NoError(t, err)

	got := Project(item, paths)
	require.Contains(t, got, "tags")

	arr, ok := got["tags"].([]any)
	require.True(t, ok)
	require.Len(t, arr, 2, "the slice grows to reach the projected index; earlier gaps are nil")
	assert.Nil(t, arr[0])
	assert.Equal(t, "b", arr[1])
}

func TestProjectNoPathsReturnsWholeItem(t *testing.T) {
	item := map[string]any{"a": 1, "b": 2}
	assert.Equal(t, item, Project(item, nil))
}
