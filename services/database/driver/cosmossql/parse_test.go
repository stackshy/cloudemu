package cosmossql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

func TestParseSelectStar(t *testing.T) {
	s, err := Parse("SELECT * FROM c", nil)
	require.NoError(t, err)
	assert.Equal(t, ProjStar, s.Proj.Kind)
	assert.Nil(t, s.Where)
	assert.Equal(t, -1, s.Limit)
}

func TestParseWhereEval(t *testing.T) {
	s, err := Parse("SELECT * FROM c WHERE c.age >= @min AND c.status = 'active'",
		map[string]any{"@min": float64(18)})
	require.NoError(t, err)
	require.NotNil(t, s.Where)

	match, err := expr.Eval(s.Where, map[string]any{"age": float64(20), "status": "active"})
	require.NoError(t, err)
	assert.True(t, match)

	match, _ = expr.Eval(s.Where, map[string]any{"age": float64(10), "status": "active"})
	assert.False(t, match, "age below @min")

	match, _ = expr.Eval(s.Where, map[string]any{"age": float64(20), "status": "archived"})
	assert.False(t, match, "wrong status")
}

func TestParseInBetweenOrNot(t *testing.T) {
	s, err := Parse("SELECT * FROM c WHERE c.n BETWEEN 1 AND 10 AND c.t IN ('a','b') AND NOT c.hidden = true", nil)
	require.NoError(t, err)

	match, _ := expr.Eval(s.Where, map[string]any{"n": float64(5), "t": "a", "hidden": false})
	assert.True(t, match)

	match, _ = expr.Eval(s.Where, map[string]any{"n": float64(50), "t": "a", "hidden": false})
	assert.False(t, match, "n out of BETWEEN range")

	match, _ = expr.Eval(s.Where, map[string]any{"n": float64(5), "t": "z", "hidden": false})
	assert.False(t, match, "t not IN list")

	match, _ = expr.Eval(s.Where, map[string]any{"n": float64(5), "t": "a", "hidden": true})
	assert.False(t, match, "NOT hidden=true")
}

func TestParseFunctions(t *testing.T) {
	item := map[string]any{
		"name": "hello",
		"tags": []any{"x", "y"},
		"opt":  nil,
	}

	cases := map[string]bool{
		"SELECT * FROM c WHERE STARTSWITH(c.name, 'he')":    true,
		"SELECT * FROM c WHERE STARTSWITH(c.name, 'zz')":    false,
		"SELECT * FROM c WHERE CONTAINS(c.name, 'ell')":     true,
		"SELECT * FROM c WHERE ARRAY_CONTAINS(c.tags, 'x')": true,
		"SELECT * FROM c WHERE ARRAY_CONTAINS(c.name, 'e')": false, // scalar, not array
		"SELECT * FROM c WHERE IS_DEFINED(c.name)":          true,
		"SELECT * FROM c WHERE IS_DEFINED(c.missing)":       false,
		"SELECT * FROM c WHERE IS_NULL(c.opt)":              true,
		"SELECT * FROM c WHERE NOT IS_DEFINED(c.missing)":   true,
	}

	for q, want := range cases {
		s, err := Parse(q, nil)
		require.NoError(t, err, q)

		got, err := expr.Eval(s.Where, item)
		require.NoError(t, err, q)
		assert.Equal(t, want, got, q)
	}
}

func TestParseOrderByPaging(t *testing.T) {
	s, err := Parse("SELECT * FROM c WHERE c.a = 1 ORDER BY c.age DESC OFFSET 2 LIMIT 5", nil)
	require.NoError(t, err)

	require.Len(t, s.OrderBy, 1)
	assert.Equal(t, []string{"age"}, s.OrderBy[0].Path)
	assert.True(t, s.OrderBy[0].Desc)
	assert.Equal(t, 2, s.Offset)
	assert.Equal(t, 5, s.Limit)
}

func TestParseProjectionFields(t *testing.T) {
	s, err := Parse("SELECT c.a, c.b AS x FROM c", nil)
	require.NoError(t, err)
	require.Equal(t, ProjFields, s.Proj.Kind)
	require.Len(t, s.Proj.Fields, 2)
	assert.Equal(t, []string{"a"}, s.Proj.Fields[0].Path)
	assert.Equal(t, "x", s.Proj.Fields[1].Alias)
}

func TestParseValueAndAggregate(t *testing.T) {
	s, err := Parse("SELECT VALUE c.name FROM c", nil)
	require.NoError(t, err)
	assert.Equal(t, ProjValue, s.Proj.Kind)
	assert.Equal(t, []string{"name"}, s.Proj.ValuePath)

	s, err = Parse("SELECT VALUE COUNT(1) FROM c", nil)
	require.NoError(t, err)
	require.Equal(t, ProjAggregate, s.Proj.Kind)
	assert.Equal(t, "COUNT", s.Proj.Aggregate.Func)
	assert.False(t, s.Proj.Bare)

	s, err = Parse("SELECT DISTINCT TOP 3 c.city FROM c", nil)
	require.NoError(t, err)
	assert.True(t, s.Distinct)
	assert.Equal(t, 3, s.Top)
}

func TestParseErrors(t *testing.T) {
	for _, q := range []string{
		"", "SELECT c.a", "SELECT * FROM", "SELECT * FROM c WHERE",
		"SELECT * FROM c WHERE c.a", "SELECT * FROM c ORDER c.a",
		"SELECT * FROM c OFFSET 2", "SELECT * FROM c WHERE c.a = @p",
	} {
		_, err := Parse(q, nil)
		assert.Error(t, err, "%q should fail to parse", q)
	}
}

func TestParseGuardedNot(t *testing.T) {
	// NOT over a single-field predicate excludes documents missing that field
	// (Cosmos three-valued logic; the two-valued evaluator is guarded).
	cases := []string{
		"SELECT * FROM c WHERE NOT (c.status = 'deleted')",
		"SELECT * FROM c WHERE NOT STARTSWITH(c.name, 'x')",
		"SELECT * FROM c WHERE NOT ARRAY_CONTAINS(c.tags, 'x')",
		"SELECT * FROM c WHERE NOT c.n IN (1, 2)",
	}

	present := map[string]any{"status": "active", "name": "apple", "tags": []any{"a"}, "n": float64(9)}

	for _, q := range cases {
		s, err := Parse(q, nil)
		require.NoError(t, err, q)

		got, err := expr.Eval(s.Where, present)
		require.NoError(t, err, q)
		assert.True(t, got, "present field should match: %s", q)

		got, err = expr.Eval(s.Where, map[string]any{})
		require.NoError(t, err, q)
		assert.False(t, got, "absent field must be excluded: %s", q)
	}

	// NOT IS_DEFINED is intentionally NOT guarded: it matches absent fields.
	s, err := Parse("SELECT * FROM c WHERE NOT IS_DEFINED(c.x)", nil)
	require.NoError(t, err)

	got, _ := expr.Eval(s.Where, map[string]any{})
	assert.True(t, got, "NOT IS_DEFINED matches an absent field")

	got, _ = expr.Eval(s.Where, map[string]any{"x": float64(1)})
	assert.False(t, got, "NOT IS_DEFINED excludes a present field")
}
