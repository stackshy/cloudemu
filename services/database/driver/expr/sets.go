package expr

import "bytes"

// DynamoDB set types (SS/NS/BS) are distinct from a List (L) in the value
// model: their elements are unique and unordered. They are represented
// natively as these named slice types so the type-aware evaluator and the wire
// codec can tell a set apart from a list.

// StringSet is a DynamoDB String Set (SS).
type StringSet []string

// NumberSet is a DynamoDB Number Set (NS). Each element is kept as its exact
// decimal string (a Number), mirroring how the scalar N type is modeled, so
// large ids and high-precision decimals round-trip without float64 corruption.
// Uniqueness and membership are by numeric value (Number's exact comparison),
// so "1", "1.0" and "+1" are one element, while values that differ only beyond
// float64 precision stay distinct.
type NumberSet []Number

// BinarySet is a DynamoDB Binary Set (BS).
type BinarySet [][]byte

// stringSetHas reports whether s contains v.
func stringSetHas(s StringSet, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}

	return false
}

// numberSetHas reports whether s contains v, comparing by exact numeric value.
func numberSetHas(s NumberSet, v Number) bool {
	for _, e := range s {
		if numberEqual(e, v) {
			return true
		}
	}

	return false
}

// binarySetHas reports whether s contains v.
func binarySetHas(s BinarySet, v []byte) bool {
	for _, e := range s {
		if bytes.Equal(e, v) {
			return true
		}
	}

	return false
}

// setsEqual reports whether two same-typed sets hold the same elements,
// ignoring order.
func setsEqual(a, b any) bool {
	switch av := a.(type) {
	case StringSet:
		bv, ok := b.(StringSet)
		return ok && len(av) == len(bv) && stringSetSubset(av, bv)
	case NumberSet:
		bv, ok := b.(NumberSet)
		return ok && len(av) == len(bv) && numberSetSubset(av, bv)
	case BinarySet:
		bv, ok := b.(BinarySet)
		return ok && len(av) == len(bv) && binarySetSubset(av, bv)
	}

	return false
}

func stringSetSubset(a, b StringSet) bool {
	for _, e := range a {
		if !stringSetHas(b, e) {
			return false
		}
	}

	return true
}

func numberSetSubset(a, b NumberSet) bool {
	for _, e := range a {
		if !numberSetHas(b, e) {
			return false
		}
	}

	return true
}

func binarySetSubset(a, b BinarySet) bool {
	for _, e := range a {
		if !binarySetHas(b, e) {
			return false
		}
	}

	return true
}

// UnionSets returns the set union of dst and add for ADD on a set attribute.
// dst may be nil (ADD creates the attribute). The two must be the same set
// type; a mismatch returns dst unchanged and ok=false.
func UnionSets(dst, add any) (result any, ok bool) {
	switch a := add.(type) {
	case StringSet:
		return unionString(dst, a)
	case NumberSet:
		return unionNumber(dst, a)
	case BinarySet:
		return unionBinary(dst, a)
	}

	return dst, false
}

func unionString(dst any, add StringSet) (any, bool) {
	base, _ := dst.(StringSet)
	out := append(StringSet{}, base...)

	for _, e := range add {
		if !stringSetHas(out, e) {
			out = append(out, e)
		}
	}

	return out, dst == nil || isStringSet(dst)
}

func unionNumber(dst any, add NumberSet) (any, bool) {
	base, _ := dst.(NumberSet)
	out := append(NumberSet{}, base...)

	for _, e := range add {
		if !numberSetHas(out, e) {
			out = append(out, e)
		}
	}

	return out, dst == nil || isNumberSet(dst)
}

func unionBinary(dst any, add BinarySet) (any, bool) {
	base, _ := dst.(BinarySet)
	out := append(BinarySet{}, base...)

	for _, e := range add {
		if !binarySetHas(out, e) {
			out = append(out, e)
		}
	}

	return out, dst == nil || isBinarySet(dst)
}

// DifferenceSets returns dst with rm's elements removed, for DELETE on a set
// attribute. When the result is empty the attribute should be removed, which
// the caller detects via SetIsEmpty. ok is false on a type mismatch.
func DifferenceSets(dst, rm any) (result any, ok bool) {
	switch r := rm.(type) {
	case StringSet:
		return diffString(dst, r)
	case NumberSet:
		return diffNumber(dst, r)
	case BinarySet:
		return diffBinary(dst, r)
	}

	return dst, false
}

func diffString(dst any, rm StringSet) (any, bool) {
	base, ok := dst.(StringSet)
	if !ok {
		return dst, false
	}

	out := StringSet{}

	for _, e := range base {
		if !stringSetHas(rm, e) {
			out = append(out, e)
		}
	}

	return out, true
}

func diffNumber(dst any, rm NumberSet) (any, bool) {
	base, ok := dst.(NumberSet)
	if !ok {
		return dst, false
	}

	out := NumberSet{}

	for _, e := range base {
		if !numberSetHas(rm, e) {
			out = append(out, e)
		}
	}

	return out, true
}

func diffBinary(dst any, rm BinarySet) (any, bool) {
	base, ok := dst.(BinarySet)
	if !ok {
		return dst, false
	}

	out := BinarySet{}

	for _, e := range base {
		if !binarySetHas(rm, e) {
			out = append(out, e)
		}
	}

	return out, true
}

// SetIsEmpty reports whether v is a set type with no elements. DynamoDB removes
// an attribute whose set becomes empty after a DELETE.
func SetIsEmpty(v any) bool {
	switch s := v.(type) {
	case StringSet:
		return len(s) == 0
	case NumberSet:
		return len(s) == 0
	case BinarySet:
		return len(s) == 0
	}

	return false
}

func isStringSet(v any) bool { _, ok := v.(StringSet); return ok }
func isNumberSet(v any) bool { _, ok := v.(NumberSet); return ok }
func isBinarySet(v any) bool { _, ok := v.(BinarySet); return ok }
