package firestore

import (
	"reflect"
	"strconv"
	"time"
)

// serverValueRequestTime is the SERVER_VALUE enum the SDK sends for a
// ServerTimestamp sentinel.
const serverValueRequestTime serverValue = "REQUEST_TIME"

// applyTransforms mutates base in place, applying each field transform against
// the document's existing (pre-write) value, and returns the per-transform
// results in order for writeResults[].transformResults. serverTimestamp uses
// the commit time; numeric and array transforms read their current value from
// existing so a transform-only write layers onto the stored document.
func applyTransforms(base, existing map[string]any, transforms []fieldTransform, commit time.Time) []value {
	results := make([]value, 0, len(transforms))

	for i := range transforms {
		ft := &transforms[i]
		segs := splitFieldPath(ft.FieldPath)
		cur, _ := getNestedPath(existing, segs)

		res, ok := transformResult(ft, cur, commit)
		if !ok {
			continue
		}

		setNestedPath(base, segs, res)
		results = append(results, goValueToFirestore(res))
	}

	return results
}

// transformResult computes a single transform's result value given the current
// (existing) value, reporting false for an unrecognized transform kind.
func transformResult(ft *fieldTransform, cur any, commit time.Time) (any, bool) {
	switch {
	case ft.SetToServerValue == serverValueRequestTime:
		return commit, true
	case ft.Increment != nil:
		return applyIncrement(cur, ft.Increment), true
	case ft.Maximum != nil:
		return applyMinMax(cur, ft.Maximum, true), true
	case ft.Minimum != nil:
		return applyMinMax(cur, ft.Minimum, false), true
	case ft.AppendMissingElements != nil:
		return applyArrayUnion(cur, ft.AppendMissingElements), true
	case ft.RemoveAllFromArray != nil:
		return applyArrayRemove(cur, ft.RemoveAllFromArray), true
	default:
		return nil, false
	}
}

// applyIncrement adds the operand to the current value. An absent or
// non-numeric current value is treated as 0. The result stays an int64 when
// both operands are integers, and a float64 when either is a double — matching
// Firestore's numeric typing.
func applyIncrement(cur any, operand *value) any {
	opI, opF, opIsInt, opOk := wireNumber(operand)
	if !opOk {
		return cur
	}

	curI, curF, curIsInt, curOk := goNumber(cur)
	if !curOk {
		if opIsInt {
			return opI
		}

		return opF
	}

	if curIsInt && opIsInt {
		return curI + opI
	}

	return curF + opF
}

// applyMinMax sets the field to the max (wantMax) or min of the current value
// and the operand. A tie keeps the current value; an absent or non-numeric
// current value yields the operand.
func applyMinMax(cur any, operand *value, wantMax bool) any {
	opI, opF, opIsInt, opOk := wireNumber(operand)
	if !opOk {
		return cur
	}

	curI, curF, curIsInt, curOk := goNumber(cur)
	if !curOk {
		if opIsInt {
			return opI
		}

		return opF
	}

	takeOperand := (wantMax && opF > curF) || (!wantMax && opF < curF)
	if takeOperand {
		if opIsInt {
			return opI
		}

		return opF
	}

	if curIsInt {
		return curI
	}

	return curF
}

// applyArrayUnion appends each operand element not already present to the
// current array, comparing by value. A current value that is not an array is
// treated as empty.
func applyArrayUnion(cur any, elems *arrayValue) any {
	result := toAnyArray(cur)

	for i := range elems.Values {
		gv := firestoreValueToGo(elems.Values[i])
		if !containsValue(result, gv) {
			result = append(result, gv)
		}
	}

	return result
}

// applyArrayRemove removes every element equal to any operand element from the
// current array. A current value that is not an array yields an empty array.
func applyArrayRemove(cur any, elems *arrayValue) any {
	src := toAnyArray(cur)

	remove := make([]any, len(elems.Values))
	for i := range elems.Values {
		remove[i] = firestoreValueToGo(elems.Values[i])
	}

	out := make([]any, 0, len(src))

	for _, x := range src {
		if !containsValue(remove, x) {
			out = append(out, x)
		}
	}

	return out
}

// wireNumber extracts the int and float views of a numeric transform operand,
// reporting whether it was an integerValue and whether it was numeric at all.
func wireNumber(v *value) (i int64, f float64, isInt, ok bool) {
	switch {
	case v == nil:
		return 0, 0, false, false
	case v.IntegerValue != nil:
		n, err := strconv.ParseInt(*v.IntegerValue, 10, 64)
		if err != nil {
			return 0, 0, false, false
		}

		return n, float64(n), true, true
	case v.DoubleValue != nil:
		return int64(*v.DoubleValue), *v.DoubleValue, false, true
	default:
		return 0, 0, false, false
	}
}

// goNumber extracts the int and float views of a stored Go value, reporting
// whether it was an integer and whether it was numeric at all.
func goNumber(x any) (i int64, f float64, isInt, ok bool) {
	switch n := x.(type) {
	case int64:
		return n, float64(n), true, true
	case int:
		return int64(n), float64(n), true, true
	case int32:
		return int64(n), float64(n), true, true
	case float64:
		return int64(n), n, false, true
	default:
		return 0, 0, false, false
	}
}

// toAnyArray returns a shallow copy of x when it is a []any, or a fresh empty
// slice otherwise.
func toAnyArray(x any) []any {
	if a, ok := x.([]any); ok {
		out := make([]any, len(a))
		copy(out, a)

		return out
	}

	return []any{}
}

// containsValue reports whether list holds a value deep-equal to v.
func containsValue(list []any, v any) bool {
	for _, x := range list {
		if reflect.DeepEqual(x, v) {
			return true
		}
	}

	return false
}
