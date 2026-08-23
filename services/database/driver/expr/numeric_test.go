package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNumericCoercionAcrossTypes covers the int64/float64 coercion that lets
// the Firestore int64 model and the DynamoDB float64 model share the evaluator.
func TestNumericCoercionAcrossTypes(t *testing.T) {
	eval := func(raw string, val, itemVal any) bool {
		node, err := ParseCondition(raw, nil, map[string]any{":v": val})
		require.NoError(t, err)

		ok, err := Eval(node, map[string]any{"n": itemVal})
		require.NoError(t, err)

		return ok
	}

	assert.True(t, eval("n = :v", int64(5), int64(5)), "int64 equals int64")
	assert.True(t, eval("n = :v", float64(2), int64(2)), "float64 equals int64 numerically")
	assert.True(t, eval("n = :v", int64(2), float64(2)), "int64 equals float64 numerically")
	assert.True(t, eval("n > :v", float64(1.5), int64(2)), "int64 orders against float64")
	assert.True(t, eval("n <= :v", int64(3), float64(3.0)), "mixed-type ordering is numeric")
	assert.False(t, eval("n = :v", "5", int64(5)), "a number never equals a string")
}
