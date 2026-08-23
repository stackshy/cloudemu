package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustUpdate(t *testing.T, item map[string]any, raw string, values map[string]any) map[string]any {
	t.Helper()

	prog, err := ParseUpdate(raw, nil, values)
	require.NoError(t, err, "parse %q", raw)

	out, err := ApplyUpdate(item, prog)
	require.NoError(t, err, "apply %q", raw)

	return out
}

func TestUpdateSetLiteralAndArithmetic(t *testing.T) {
	out := mustUpdate(t, map[string]any{"a": float64(10)}, "SET a = a + :n, b = :b",
		map[string]any{":n": float64(5), ":b": "new"})

	assert.Equal(t, float64(15), out["a"], "a + :n")
	assert.Equal(t, "new", out["b"])

	out = mustUpdate(t, map[string]any{"a": float64(10)}, "SET a = a - :n", map[string]any{":n": float64(3)})
	assert.Equal(t, float64(7), out["a"], "a - :n")
}

func TestUpdateIfNotExists(t *testing.T) {
	out := mustUpdate(t, map[string]any{}, "SET a = if_not_exists(a, :d)", map[string]any{":d": float64(1)})
	assert.Equal(t, float64(1), out["a"], "absent → default")

	out = mustUpdate(t, map[string]any{"a": float64(9)}, "SET a = if_not_exists(a, :d)", map[string]any{":d": float64(1)})
	assert.Equal(t, float64(9), out["a"], "present → keeps existing")
}

func TestUpdateListAppend(t *testing.T) {
	out := mustUpdate(t, map[string]any{"l": []any{"a", "b"}}, "SET l = list_append(l, :new)",
		map[string]any{":new": []any{"c"}})
	assert.Equal(t, []any{"a", "b", "c"}, out["l"])
}

func TestUpdateRemove(t *testing.T) {
	out := mustUpdate(t, map[string]any{"a": 1, "b": 2, "l": []any{"x", "y", "z"}}, "REMOVE a, l[1]", nil)

	_, hasA := out["a"]
	assert.False(t, hasA, "a removed")
	assert.Equal(t, []any{"x", "z"}, out["l"], "list element removed and shifted")
}

func TestUpdateAddNumber(t *testing.T) {
	out := mustUpdate(t, map[string]any{}, "ADD n :inc", map[string]any{":inc": float64(3)})
	assert.Equal(t, float64(3), out["n"], "ADD creates the attribute at the value")

	out = mustUpdate(t, map[string]any{"n": float64(10)}, "ADD n :inc", map[string]any{":inc": float64(3)})
	assert.Equal(t, float64(13), out["n"], "ADD increments")
}

func TestUpdateAddSet(t *testing.T) {
	out := mustUpdate(t, map[string]any{"colors": StringSet{"red", "blue"}}, "ADD colors :c",
		map[string]any{":c": StringSet{"blue", "green"}})

	cs, ok := out["colors"].(StringSet)
	require.True(t, ok)
	assert.ElementsMatch(t, StringSet{"red", "blue", "green"}, cs, "union, deduplicated")

	out = mustUpdate(t, map[string]any{}, "ADD colors :c", map[string]any{":c": StringSet{"red"}})
	assert.Equal(t, StringSet{"red"}, out["colors"], "ADD creates the set")
}

func TestUpdateAddNumberSet(t *testing.T) {
	out := mustUpdate(t, map[string]any{"nums": NumberSet{1, 2}}, "ADD nums :n",
		map[string]any{":n": NumberSet{2, 3}})

	ns, ok := out["nums"].(NumberSet)
	require.True(t, ok)
	assert.ElementsMatch(t, NumberSet{1, 2, 3}, ns)
}

func TestUpdateDeleteSet(t *testing.T) {
	out := mustUpdate(t, map[string]any{"colors": StringSet{"red", "blue", "green"}}, "DELETE colors :c",
		map[string]any{":c": StringSet{"blue"}})

	assert.ElementsMatch(t, StringSet{"red", "green"}, out["colors"].(StringSet))
}

func TestUpdateDeleteSetEmptiesRemovesAttr(t *testing.T) {
	out := mustUpdate(t, map[string]any{"colors": StringSet{"red"}}, "DELETE colors :c",
		map[string]any{":c": StringSet{"red"}})

	_, has := out["colors"]
	assert.False(t, has, "an emptied set attribute is removed")
}

func TestUpdateNestedSet(t *testing.T) {
	out := mustUpdate(t, map[string]any{"addr": map[string]any{"city": "Paris"}}, "SET addr.city = :c",
		map[string]any{":c": "London"})

	addr, ok := out["addr"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "London", addr["city"])
}

func TestUpdateCopyOnWriteDoesNotMutateSource(t *testing.T) {
	inner := map[string]any{"city": "Paris"}
	// A shallow item copy (as the providers make) shares the nested map.
	shallowCopy := map[string]any{"addr": inner}

	_ = mustUpdate(t, shallowCopy, "SET addr.city = :c", map[string]any{":c": "London"})
	assert.Equal(t, "Paris", inner["city"], "the shared nested map must not be mutated")
}

func TestUpdateMultiClause(t *testing.T) {
	item := map[string]any{"a": float64(1), "old": "x", "tags": StringSet{"t1"}}
	out := mustUpdate(t, item, "SET b = :b REMOVE old ADD a :n DELETE tags :t",
		map[string]any{":b": "y", ":n": float64(4), ":t": StringSet{"t1"}})

	assert.Equal(t, "y", out["b"])
	assert.Equal(t, float64(5), out["a"])

	_, hasOld := out["old"]
	assert.False(t, hasOld)

	_, hasTags := out["tags"]
	assert.False(t, hasTags, "tags emptied by DELETE")
}

func TestUpdateApplyErrors(t *testing.T) {
	cases := []struct {
		raw    string
		item   map[string]any
		values map[string]any
	}{
		{"SET a = b", map[string]any{}, nil},                                             // operand path missing
		{"ADD s :n", map[string]any{"s": "hi"}, map[string]any{":n": float64(1)}},        // ADD to non-number
		{"DELETE s :c", map[string]any{"s": "hi"}, map[string]any{":c": StringSet{"x"}}}, // DELETE on non-set
	}

	for _, c := range cases {
		prog, err := ParseUpdate(c.raw, nil, c.values)
		require.NoError(t, err, "parse %q", c.raw)

		_, aerr := ApplyUpdate(c.item, prog)
		assert.Error(t, aerr, "apply %q should error", c.raw)
	}
}

func TestUpdateParseErrors(t *testing.T) {
	for _, raw := range []string{"", "SET", "SET a", "SET a =", "FOO a = :v", "ADD a", "SET a > :v"} {
		_, err := ParseUpdate(raw, nil, map[string]any{":v": float64(1)})
		assert.Error(t, err, "%q should fail to parse", raw)
	}
}
