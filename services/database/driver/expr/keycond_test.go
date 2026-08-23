package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseKeyConditionPartitionOnly(t *testing.T) {
	kc, err := ParseKeyCondition("id = :i", nil, map[string]any{":i": "u1"})
	require.NoError(t, err)
	assert.Equal(t, "id", kc.PartitionKey)
	assert.Equal(t, "u1", kc.PartitionVal)
	assert.Empty(t, kc.SortOp)
}

func TestParseKeyConditionSpacingTolerant(t *testing.T) {
	// No spaces around '=' or the sort operator — the lexer handles it.
	kc, err := ParseKeyCondition("id=:i AND sk>:s", nil, map[string]any{":i": "u1", ":s": float64(5)})
	require.NoError(t, err)
	assert.Equal(t, "id", kc.PartitionKey)
	assert.Equal(t, ">", kc.SortOp)
	assert.Equal(t, float64(5), kc.SortVal)
}

func TestParseKeyConditionRelational(t *testing.T) {
	for _, op := range []string{"=", "<", "<=", ">", ">="} {
		kc, err := ParseKeyCondition("pk = :p AND sk "+op+" :s", nil,
			map[string]any{":p": "x", ":s": "y"})
		require.NoError(t, err, "op %q", op)
		assert.Equal(t, op, kc.SortOp)
	}
}

func TestParseKeyConditionBetween(t *testing.T) {
	kc, err := ParseKeyCondition("pk = :p AND sk BETWEEN :lo AND :hi", nil,
		map[string]any{":p": "x", ":lo": "a", ":hi": "z"})
	require.NoError(t, err)
	assert.Equal(t, "BETWEEN", kc.SortOp)
	assert.Equal(t, "a", kc.SortVal)
	assert.Equal(t, "z", kc.SortValEnd)
}

func TestParseKeyConditionBeginsWith(t *testing.T) {
	kc, err := ParseKeyCondition("pk = :p AND begins_with(sk, :pre)", nil,
		map[string]any{":p": "x", ":pre": "2024"})
	require.NoError(t, err)
	assert.Equal(t, "BEGINS_WITH", kc.SortOp)
	assert.Equal(t, "2024", kc.SortVal)
}

func TestParseKeyConditionAlias(t *testing.T) {
	kc, err := ParseKeyCondition("#c = :c", map[string]string{"#c": "customer"},
		map[string]any{":c": "bob"})
	require.NoError(t, err)
	assert.Equal(t, "customer", kc.PartitionKey)
}

func TestParseKeyConditionErrors(t *testing.T) {
	cases := map[string]string{
		"pk not equality":    "pk > :p",
		"invalid sort op <>": "pk = :p AND sk <> :s",
		"unsupported func":   "pk = :p AND contains(sk, :s)",
		"empty":              "",
		"trailing tokens":    "pk = :p AND sk = :s AND x = :y",
		"nested key attr":    "pk.a = :p",
	}

	vals := map[string]any{":p": "x", ":s": "y", ":c": "z", ":y": "w"}

	for name, raw := range cases {
		_, err := ParseKeyCondition(raw, nil, vals)
		assert.Error(t, err, "%s: %q should fail", name, raw)
	}
}
