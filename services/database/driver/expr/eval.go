package expr

import (
	"bytes"
	"cmp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Eval evaluates a parsed condition against a native DynamoDB item and
// returns whether it matches. Comparisons are type-aware: numbers compare
// numerically, strings lexically, binary bytewise; values of different
// types are never equal and never ordered (any comparison yields false), and
// any operand that resolves to a missing path makes its condition false —
// matching real DynamoDB semantics.
func Eval(node Node, item map[string]any) (bool, error) {
	switch n := node.(type) {
	case *And:
		return evalAnd(n, item)
	case *Or:
		return evalOr(n, item)
	case *Not:
		v, err := Eval(n.Child, item)
		return !v, err
	case *Comparison:
		return evalComparison(n, item)
	case *Between:
		return evalBetween(n, item)
	case *In:
		return evalIn(n, item)
	default:
		return evalFunctionNode(node, item)
	}
}

// evalFunctionNode handles the function-condition nodes, keeping the primary
// Eval dispatch within the cyclomatic-complexity budget.
func evalFunctionNode(node Node, item map[string]any) (bool, error) {
	switch n := node.(type) {
	case *AttrExists:
		_, ok := resolvePath(n.Path.Parts, item)
		return ok, nil
	case *AttrNotExists:
		_, ok := resolvePath(n.Path.Parts, item)
		return !ok, nil
	case *AttrType:
		return evalAttrType(n, item), nil
	case *BeginsWith:
		return evalBeginsWith(n, item), nil
	case *Contains:
		return evalContains(n, item), nil
	default:
		return false, cerrors.New(cerrors.InvalidArgument, "unsupported condition node")
	}
}

func evalAnd(n *And, item map[string]any) (bool, error) {
	left, err := Eval(n.Left, item)
	if err != nil || !left {
		return false, err
	}

	return Eval(n.Right, item)
}

func evalOr(n *Or, item map[string]any) (bool, error) {
	left, err := Eval(n.Left, item)
	if err != nil || left {
		return left, err
	}

	return Eval(n.Right, item)
}

func evalComparison(n *Comparison, item map[string]any) (bool, error) {
	lv, lok := resolveOperand(n.Left, item)
	rv, rok := resolveOperand(n.Right, item)

	if !lok || !rok {
		return false, nil
	}

	return compareOp(n.Op, lv, rv), nil
}

func evalBetween(n *Between, item map[string]any) (bool, error) {
	v, ok := resolveOperand(n.Operand, item)
	lo, lok := resolveOperand(n.Lo, item)
	hi, hok := resolveOperand(n.Hi, item)

	if !ok || !lok || !hok {
		return false, nil
	}

	cLo, ok1 := orderedCompare(v, lo)
	cHi, ok2 := orderedCompare(v, hi)

	if !ok1 || !ok2 {
		return false, nil
	}

	return cLo >= 0 && cHi <= 0, nil
}

func evalIn(n *In, item map[string]any) (bool, error) {
	v, ok := resolveOperand(n.Operand, item)
	if !ok {
		return false, nil
	}

	for _, opnd := range n.List {
		iv, iok := resolveOperand(opnd, item)
		if iok && valuesEqual(v, iv) {
			return true, nil
		}
	}

	return false, nil
}

func evalAttrType(n *AttrType, item map[string]any) bool {
	v, ok := resolvePath(n.Path.Parts, item)
	if !ok {
		return false
	}

	tv, tok := resolveOperand(n.Type, item)
	if !tok {
		return false
	}

	ts, isStr := tv.(string)

	return isStr && dynamoType(v) == ts
}

func evalBeginsWith(n *BeginsWith, item map[string]any) bool {
	v, ok := resolvePath(n.Path.Parts, item)
	if !ok {
		return false
	}

	pv, pok := resolveOperand(n.Prefix, item)
	if !pok {
		return false
	}

	switch s := v.(type) {
	case string:
		p, isStr := pv.(string)
		return isStr && strings.HasPrefix(s, p)
	case []byte:
		p, isBytes := pv.([]byte)
		return isBytes && bytes.HasPrefix(s, p)
	}

	return false
}

func evalContains(n *Contains, item map[string]any) bool {
	v, ok := resolvePath(n.Path.Parts, item)
	if !ok {
		return false
	}

	target, tok := resolveOperand(n.Operand, item)
	if !tok {
		return false
	}

	switch s := v.(type) {
	case string:
		t, isStr := target.(string)
		return isStr && strings.Contains(s, t)
	case []any:
		return sliceContains(s, target)
	}

	return false
}

func sliceContains(s []any, target any) bool {
	for _, e := range s {
		if valuesEqual(e, target) {
			return true
		}
	}

	return false
}

// resolveOperand yields an operand's native value and whether it is present.
// A missing path (or a size() over a missing/non-sizable path) reports
// present=false so its enclosing condition evaluates to false.
func resolveOperand(op Operand, item map[string]any) (any, bool) {
	switch o := op.(type) {
	case *ValueOperand:
		return o.Value, true
	case *PathOperand:
		return resolvePath(o.Parts, item)
	case *SizeOperand:
		v, ok := resolvePath(o.Path.Parts, item)
		if !ok {
			return nil, false
		}

		return sizeOf(v)
	}

	return nil, false
}

// resolvePath walks a document path over nested maps and slices. A key that
// is present with a nil value counts as present (DynamoDB NULL).
func resolvePath(parts []PathPart, item map[string]any) (any, bool) {
	var cur any = item

	for _, part := range parts {
		if part.IsIndex {
			arr, ok := cur.([]any)
			if !ok || part.Index < 0 || part.Index >= len(arr) {
				return nil, false
			}

			cur = arr[part.Index]

			continue
		}

		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		v, ok := m[part.Name]
		if !ok {
			return nil, false
		}

		cur = v
	}

	return cur, true
}

func compareOp(op string, a, b any) bool {
	switch op {
	case "=":
		return valuesEqual(a, b)
	case "<>":
		return !valuesEqual(a, b)
	}

	c, ok := orderedCompare(a, b)
	if !ok {
		return false
	}

	switch op {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}

	return false
}

// valuesEqual reports type-aware equality: only same-typed values can be
// equal, so a number never equals a string.
func valuesEqual(a, b any) bool {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case []byte:
		bv, ok := b.([]byte)
		return ok && bytes.Equal(av, bv)
	case nil:
		return b == nil
	}

	return false
}

// orderedCompare returns -1/0/1 for orderable, same-typed values (numbers,
// strings, binary). Mixed or non-orderable types report ok=false.
func orderedCompare(a, b any) (int, bool) {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		if !ok {
			return 0, false
		}

		return cmp.Compare(av, bv), true
	case string:
		bv, ok := b.(string)
		if !ok {
			return 0, false
		}

		return cmp.Compare(av, bv), true
	case []byte:
		bv, ok := b.([]byte)
		if !ok {
			return 0, false
		}

		return bytes.Compare(av, bv), true
	}

	return 0, false
}

func sizeOf(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		return float64(len(x)), true
	case []byte:
		return float64(len(x)), true
	case []any:
		return float64(len(x)), true
	case map[string]any:
		return float64(len(x)), true
	}

	return nil, false
}

// dynamoType returns the DynamoDB attribute type code for a native value.
// Sets are indistinguishable from lists in the native model and report "L".
func dynamoType(v any) string {
	switch v.(type) {
	case string:
		return "S"
	case float64:
		return "N"
	case bool:
		return "BOOL"
	case []byte:
		return "B"
	case nil:
		return "NULL"
	case []any:
		return "L"
	case map[string]any:
		return "M"
	}

	return ""
}
