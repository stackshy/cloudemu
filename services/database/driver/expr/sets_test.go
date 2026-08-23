package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamoTypeSets(t *testing.T) {
	assert.Equal(t, "SS", dynamoType(StringSet{"a"}))
	assert.Equal(t, "NS", dynamoType(NumberSet{1}))
	assert.Equal(t, "BS", dynamoType(BinarySet{{0x1}}))
}

func TestContainsOnSets(t *testing.T) {
	item := map[string]any{
		"ss": StringSet{"red", "blue"},
		"ns": NumberSet{1, 2},
		"bs": BinarySet{{0x1}, {0x2}},
	}

	// contains(ss, :v)
	node, err := ParseCondition("contains(ss, :v)", nil, map[string]any{":v": "blue"})
	require.NoError(t, err)
	ok, err := Eval(node, item)
	require.NoError(t, err)
	assert.True(t, ok)

	node, _ = ParseCondition("contains(ns, :v)", nil, map[string]any{":v": float64(2)})
	ok, _ = Eval(node, item)
	assert.True(t, ok, "number set membership")

	node, _ = ParseCondition("contains(ss, :v)", nil, map[string]any{":v": "green"})
	ok, _ = Eval(node, item)
	assert.False(t, ok, "absent member")
}

func TestAttributeTypeSet(t *testing.T) {
	node, err := ParseCondition("attribute_type(ss, :t)", nil, map[string]any{":t": "SS"})
	require.NoError(t, err)
	ok, err := Eval(node, map[string]any{"ss": StringSet{"a"}})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestSetsEqual(t *testing.T) {
	assert.True(t, setsEqual(StringSet{"a", "b"}, StringSet{"b", "a"}), "order-independent")
	assert.False(t, setsEqual(StringSet{"a"}, StringSet{"a", "b"}))
	assert.False(t, setsEqual(StringSet{"a"}, NumberSet{1}), "different set types")
}

func TestUnionAndDifference(t *testing.T) {
	u, ok := UnionSets(StringSet{"a"}, StringSet{"a", "b"})
	require.True(t, ok)
	assert.ElementsMatch(t, StringSet{"a", "b"}, u)

	// type mismatch
	_, ok = UnionSets(StringSet{"a"}, NumberSet{1})
	assert.False(t, ok)

	d, ok := DifferenceSets(NumberSet{1, 2, 3}, NumberSet{2})
	require.True(t, ok)
	assert.ElementsMatch(t, NumberSet{1, 3}, d)

	assert.True(t, SetIsEmpty(StringSet{}))
	assert.False(t, SetIsEmpty(StringSet{"a"}))
}

func TestProjectionOverlapRejected(t *testing.T) {
	for _, raw := range []string{"a, a.b", "a.b, a", "a, a", "tags[0], tags"} {
		_, err := ParseProjection(raw, nil)
		assert.Error(t, err, "%q overlaps and must be rejected", raw)
	}

	// Non-overlapping siblings are fine.
	_, err := ParseProjection("a.b, a.c", nil)
	assert.NoError(t, err)
}
