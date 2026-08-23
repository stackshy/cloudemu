package expr

import (
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleItem is a representative native DynamoDB item used across tests.
func sampleItem() map[string]any {
	return map[string]any{
		"pk":       "user#1",
		"age":      float64(30),
		"score":    float64(75),
		"name":     "Alice",
		"active":   true,
		"nickname": nil,
		"blob":     []byte("hello"),
		"tags":     []any{"red", "green", "blue"},
		"nums":     []any{float64(1), float64(2), float64(3)},
		"address": map[string]any{
			"city": "Seattle",
			"zip":  "98101",
		},
		"matrix": []any{
			map[string]any{"k": "v0"},
			map[string]any{"k": "v1"},
		},
	}
}

// evalRaw parses then evaluates raw against item, requiring no error.
func evalRaw(t *testing.T, raw string, names map[string]string, values map[string]any) bool {
	t.Helper()
	node, err := ParseCondition(raw, names, values)
	require.NoError(t, err)
	got, err := Eval(node, sampleItem())
	require.NoError(t, err)
	return got
}

func TestComparators(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		values map[string]any
		want   bool
	}{
		{"num eq", "age = :v", map[string]any{":v": float64(30)}, true},
		{"num eq false", "age = :v", map[string]any{":v": float64(31)}, false},
		{"num ne", "age <> :v", map[string]any{":v": float64(31)}, true},
		{"num lt", "age < :v", map[string]any{":v": float64(40)}, true},
		{"num le eq", "age <= :v", map[string]any{":v": float64(30)}, true},
		{"num gt", "age > :v", map[string]any{":v": float64(10)}, true},
		{"num ge", "age >= :v", map[string]any{":v": float64(30)}, true},
		{"str eq", "name = :v", map[string]any{":v": "Alice"}, true},
		{"str lt", "name < :v", map[string]any{":v": "Bob"}, true},
		{"bool eq", "active = :v", map[string]any{":v": true}, true},
		{"bool ne", "active <> :v", map[string]any{":v": false}, true},
		// Cross-type: number 30 is not equal to string "30", and not orderable.
		{"num not equal str", "age = :v", map[string]any{":v": "30"}, false},
		{"num ne str is true", "age <> :v", map[string]any{":v": "30"}, true},
		{"num lt str is false", "age < :v", map[string]any{":v": "30"}, false},
		{"num gt str is false", "age > :v", map[string]any{":v": "30"}, false},
		// Bytes compare bytewise.
		{"bytes eq", "blob = :v", map[string]any{":v": []byte("hello")}, true},
		{"bytes lt", "blob < :v", map[string]any{":v": []byte("world")}, true},
		// Missing path → comparison false.
		{"missing path", "missing = :v", map[string]any{":v": "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evalRaw(t, tt.raw, nil, tt.values))
		})
	}
}

func TestNumberNotEqualStringLiteralCase(t *testing.T) {
	// The headline invariant: 25 != "25".
	assert.False(t, evalRaw(t, "score = :v", nil, map[string]any{":v": "75"}))
	assert.True(t, evalRaw(t, "score = :v", nil, map[string]any{":v": float64(75)}))
}

func TestBooleanLogicAndPrecedence(t *testing.T) {
	v := map[string]any{
		":a":     float64(30),
		":score": float64(75),
		":lo":    float64(70),
		":hi":    float64(80),
		":n":     "Alice",
	}
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"and true", "age = :a AND name = :n", true},
		{"and false", "age = :a AND name = :hi", false},
		{"or true", "age = :hi OR name = :n", true},
		{"not", "NOT age = :hi", true},
		{"not paren", "NOT (age = :a)", false},
		// NOT binds tighter than AND: NOT age=:hi AND name=:n → (NOT age=:hi) AND (name=:n).
		{"not before and", "NOT age = :hi AND name = :n", true},
		// AND binds tighter than OR: false OR (true AND true).
		{"and before or", "name = :hi OR age = :a AND score = :score", true},
		// Same shape, but the AND clause is false → whole thing false.
		{"and before or false", "name = :hi OR age = :a AND score = :lo", false},
		// Parens override precedence: (false OR true) AND false.
		{"paren override", "(name = :hi OR age = :a) AND score = :hi", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evalRaw(t, tt.raw, nil, v))
		})
	}
}

func TestInAndBetween(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		values map[string]any
		want   bool
	}{
		{"in match", "name IN (:a, :b, :c)", map[string]any{":a": "Bob", ":b": "Alice", ":c": "Eve"}, true},
		{"in no match", "name IN (:a, :b)", map[string]any{":a": "Bob", ":b": "Eve"}, false},
		{"in numeric", "age IN (:a, :b)", map[string]any{":a": float64(30), ":b": float64(40)}, true},
		{"between inside", "age BETWEEN :lo AND :hi", map[string]any{":lo": float64(20), ":hi": float64(40)}, true},
		{"between edge lo", "age BETWEEN :lo AND :hi", map[string]any{":lo": float64(30), ":hi": float64(40)}, true},
		{"between outside", "age BETWEEN :lo AND :hi", map[string]any{":lo": float64(31), ":hi": float64(40)}, false},
		{"between string", "name BETWEEN :lo AND :hi", map[string]any{":lo": "A", ":hi": "B"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evalRaw(t, tt.raw, nil, tt.values))
		})
	}
}

func TestFunctions(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		values map[string]any
		want   bool
	}{
		{"exists", "attribute_exists(name)", nil, true},
		{"exists missing", "attribute_exists(missing)", nil, false},
		// A key present with a nil value still counts as existing.
		{"exists nil value", "attribute_exists(nickname)", nil, true},
		{"not_exists true", "attribute_not_exists(missing)", nil, true},
		{"not_exists false", "attribute_not_exists(name)", nil, false},
		{"type S", "attribute_type(name, :t)", map[string]any{":t": "S"}, true},
		{"type N", "attribute_type(age, :t)", map[string]any{":t": "N"}, true},
		{"type BOOL", "attribute_type(active, :t)", map[string]any{":t": "BOOL"}, true},
		{"type B", "attribute_type(blob, :t)", map[string]any{":t": "B"}, true},
		{"type NULL", "attribute_type(nickname, :t)", map[string]any{":t": "NULL"}, true},
		{"type L", "attribute_type(tags, :t)", map[string]any{":t": "L"}, true},
		{"type M", "attribute_type(address, :t)", map[string]any{":t": "M"}, true},
		{"type mismatch", "attribute_type(age, :t)", map[string]any{":t": "S"}, false},
		{"begins_with", "begins_with(name, :p)", map[string]any{":p": "Al"}, true},
		{"begins_with false", "begins_with(name, :p)", map[string]any{":p": "Xy"}, false},
		{"begins_with bytes", "begins_with(blob, :p)", map[string]any{":p": []byte("he")}, true},
		{"contains substr", "contains(name, :s)", map[string]any{":s": "lic"}, true},
		{"contains substr false", "contains(name, :s)", map[string]any{":s": "zzz"}, false},
		{"contains list member", "contains(tags, :s)", map[string]any{":s": "green"}, true},
		{"contains list absent", "contains(tags, :s)", map[string]any{":s": "pink"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evalRaw(t, tt.raw, nil, tt.values))
		})
	}
}

func TestSizeInComparison(t *testing.T) {
	// size(tags) == 3
	assert.True(t, evalRaw(t, "size(tags) > :n", nil, map[string]any{":n": float64(2)}))
	assert.False(t, evalRaw(t, "size(tags) > :n", nil, map[string]any{":n": float64(3)}))
	assert.True(t, evalRaw(t, "size(tags) = :n", nil, map[string]any{":n": float64(3)}))
	// size of a string is its byte length; "Alice" == 5.
	assert.True(t, evalRaw(t, "size(name) = :n", nil, map[string]any{":n": float64(5)}))
	// size of a map is its entry count; address has 2 entries.
	assert.True(t, evalRaw(t, "size(address) = :n", nil, map[string]any{":n": float64(2)}))
	// size over a missing path → comparison false.
	assert.False(t, evalRaw(t, "size(missing) > :n", nil, map[string]any{":n": float64(0)}))
	// size on the right-hand side of a comparison also works.
	assert.True(t, evalRaw(t, ":n < size(tags)", nil, map[string]any{":n": float64(2)}))
}

func TestDocumentPaths(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		values map[string]any
		want   bool
	}{
		{"nested map", "address.city = :v", map[string]any{":v": "Seattle"}, true},
		{"nested map miss", "address.city = :v", map[string]any{":v": "Boston"}, false},
		{"list index", "tags[0] = :v", map[string]any{":v": "red"}, true},
		{"list index 2", "tags[2] = :v", map[string]any{":v": "blue"}, true},
		{"list index oob", "tags[9] = :v", map[string]any{":v": "red"}, false},
		{"nested list of maps", "matrix[1].k = :v", map[string]any{":v": "v1"}, true},
		{"path exists", "attribute_exists(address.zip)", nil, true},
		{"path missing exists", "attribute_exists(address.country)", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evalRaw(t, tt.raw, nil, tt.values))
		})
	}
}

func TestAliasAndValueSubstitution(t *testing.T) {
	names := map[string]string{
		"#n":   "name",
		"#a":   "address",
		"#c":   "city",
		"#age": "age",
	}
	values := map[string]any{":v": "Alice", ":city": "Seattle", ":min": float64(18)}

	assert.True(t, evalRaw(t, "#n = :v", names, values))
	assert.True(t, evalRaw(t, "#a.#c = :city", names, values))
	assert.True(t, evalRaw(t, "#age >= :min", names, values))
	assert.True(t, evalRaw(t, "attribute_exists(#n)", names, values))
}

func TestMalformedExpressions(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		names  map[string]string
		values map[string]any
	}{
		{"empty", "", nil, nil},
		{"dangling operator", "age =", nil, nil},
		{"unclosed paren", "(age = :v", nil, map[string]any{":v": float64(1)}},
		{"missing operator", "age name", nil, nil},
		{"unknown alias", "#x = :v", nil, map[string]any{":v": float64(1)}},
		{"unknown value", "age = :missing", nil, nil},
		{"empty ref", "age = :", nil, nil},
		{"bad index", "tags[x] = :v", nil, map[string]any{":v": "red"}},
		{"trailing token", "age = :v :v", nil, map[string]any{":v": float64(1)}},
		{"bad char", "age ~ :v", nil, map[string]any{":v": float64(1)}},
		{"size as condition", "size(tags)", nil, nil},
		{"in missing rparen", "name IN (:a", nil, map[string]any{":a": "x"}},
		{"between missing and", "age BETWEEN :lo :hi", nil, map[string]any{":lo": float64(1), ":hi": float64(2)}},
		{"func no args", "attribute_exists()", nil, nil},
		{"operand func misuse", "contains(tags) = :v", nil, map[string]any{":v": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCondition(tt.raw, tt.names, tt.values)
			require.Error(t, err)
			assert.True(t, cerrors.IsInvalidArgument(err), "expected InvalidArgument, got %v", err)
		})
	}
}

func TestCaseInsensitiveKeywordsAndFunctions(t *testing.T) {
	v := map[string]any{":a": float64(30), ":n": "Alice"}
	assert.True(t, evalRaw(t, "age = :a and name = :n", nil, v))
	assert.True(t, evalRaw(t, "age = :a Or name = :n", nil, v))
	assert.True(t, evalRaw(t, "Attribute_Exists(name)", nil, nil))
	assert.True(t, evalRaw(t, "BEGINS_WITH(name, :p)", nil, map[string]any{":p": "Al"}))
}

func TestTypeAndContainerEdges(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		values map[string]any
		want   bool
	}{
		// Descending into a scalar as if it were a map fails → false.
		{"scalar as map", "name.foo = :v", map[string]any{":v": "x"}, false},
		// Descending into a scalar as if it were a list fails → false.
		{"scalar as list", "name[0] = :v", map[string]any{":v": "x"}, false},
		// begins_with with a non-string prefix against a string value → false.
		{"begins_with type mismatch", "begins_with(name, :p)", map[string]any{":p": float64(1)}, false},
		// begins_with over a non-string, non-binary value → false.
		{"begins_with wrong target", "begins_with(age, :p)", map[string]any{":p": "3"}, false},
		// contains over a non-string, non-list value → false.
		{"contains wrong target", "contains(age, :s)", map[string]any{":s": "3"}, false},
		// contains substring where the value operand isn't a string → false.
		{"contains type mismatch", "contains(name, :s)", map[string]any{":s": float64(1)}, false},
		// Ordering a binary value against a number → not orderable → false.
		{"bytes vs num", "blob < :v", map[string]any{":v": float64(5)}, false},
		// bool inequality with a non-bool operand.
		{"bool vs string", "active = :v", map[string]any{":v": "true"}, false},
		// nil equality: nickname (NULL) equals a NULL value operand.
		{"nil eq nil", "nickname = :v", map[string]any{":v": nil}, true},
		{"nil ne value", "nickname = :v", map[string]any{":v": "x"}, false},
		// size() over a scalar number is not sizable → comparison false.
		{"size of number", "size(age) > :n", map[string]any{":n": float64(0)}, false},
		// contains membership over a numeric list.
		{"contains numeric list", "contains(nums, :s)", map[string]any{":s": float64(2)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evalRaw(t, tt.raw, nil, tt.values))
		})
	}
}

func TestMoreMalformed(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"size missing rparen", "size(tags = :v"},
		{"binary func missing comma", "begins_with(name :p)"},
		{"between operand error", "age BETWEEN AND :hi"},
		{"path dot no name", "address. = :v"},
		{"index no rbracket", "tags[0 = :v"},
		{"leading operator", "= :v"},
		{"not nothing", "NOT"},
		{"binary func bad arg", "attribute_type(name, )"},
		{"binary func missing value", "begins_with(name, :nope)"},
		{"size bad alias", "size(#bad) = :v"},
		{"binary func missing rparen", "contains(name, :v"},
		{"in bad member", "name IN (:nope)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCondition(tt.raw, nil, map[string]any{":v": "x", ":p": "x", ":hi": float64(1)})
			require.Error(t, err)
			assert.True(t, cerrors.IsInvalidArgument(err), "expected InvalidArgument, got %v", err)
		})
	}
}

func TestEvalUnsupportedNode(t *testing.T) {
	// Guards the defensive branch in Eval for an unknown node type.
	type bogus struct{ Node }
	_, err := Eval(bogus{}, sampleItem())
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
}
