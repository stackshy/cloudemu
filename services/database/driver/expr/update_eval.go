package expr

import cerrors "github.com/stackshy/cloudemu/v2/errors"

// ApplyUpdate applies a parsed UpdateProgram to item, mutating it in place and
// returning it. item is expected to be a caller-owned copy. Clauses apply in
// SET, REMOVE, ADD, DELETE order. A SET operand that resolves to a missing
// attribute, or an ADD/DELETE type mismatch, is a cerrors.InvalidArgument.
func ApplyUpdate(item map[string]any, prog *UpdateProgram) (map[string]any, error) {
	for _, s := range prog.sets {
		if err := applySet(item, s); err != nil {
			return nil, err
		}
	}

	for _, p := range prog.removes {
		removePath(item, p.Parts)
	}

	for _, a := range prog.adds {
		if err := applyAdd(item, a); err != nil {
			return nil, err
		}
	}

	for _, d := range prog.deletes {
		if err := applyDelete(item, d); err != nil {
			return nil, err
		}
	}

	return item, nil
}

func applySet(item map[string]any, s setItem) error {
	val, ok := evalUpdOperand(s.rhs, item)
	if !ok {
		return cerrors.New(cerrors.InvalidArgument, "SET operand refers to a missing attribute")
	}

	assignPath(item, s.path.Parts, val)

	return nil
}

func applyAdd(item map[string]any, a pathValue) error {
	cur, exists := resolvePath(a.path.Parts, item)

	newVal, err := addValue(cur, exists, a.value.Value)
	if err != nil {
		return err
	}

	assignPath(item, a.path.Parts, newVal)

	return nil
}

// addValue computes the result of ADD: numeric addition (creating the
// attribute at the value when absent), or set union.
func addValue(cur any, exists bool, val any) (any, error) {
	switch v := val.(type) {
	case float64:
		if !exists {
			return v, nil
		}

		base, ok := cur.(float64)
		if !ok {
			return nil, cerrors.New(cerrors.InvalidArgument, "ADD to a non-numeric attribute")
		}

		return base + v, nil
	case StringSet, NumberSet, BinarySet:
		if !exists {
			return val, nil
		}

		res, ok := UnionSets(cur, val)
		if !ok {
			return nil, cerrors.New(cerrors.InvalidArgument, "ADD set type does not match the attribute")
		}

		return res, nil
	default:
		return nil, cerrors.New(cerrors.InvalidArgument, "ADD supports only Number and Set attributes")
	}
}

func applyDelete(item map[string]any, d pathValue) error {
	cur, exists := resolvePath(d.path.Parts, item)
	if !exists {
		return nil
	}

	res, ok := DifferenceSets(cur, d.value.Value)
	if !ok {
		return cerrors.New(cerrors.InvalidArgument, "DELETE requires a set attribute and a matching set value")
	}

	if SetIsEmpty(res) {
		removePath(item, d.path.Parts)
		return nil
	}

	assignPath(item, d.path.Parts, res)

	return nil
}

func evalUpdOperand(op updOperand, item map[string]any) (any, bool) {
	switch o := op.(type) {
	case *updLeaf:
		return resolveOperand(o.operand, item)
	case *updArith:
		return evalArith(o, item)
	case *updIfNotExists:
		return evalIfNotExists(o, item)
	case *updListAppend:
		return evalListAppend(o, item)
	default:
		return nil, false
	}
}

func evalArith(o *updArith, item map[string]any) (any, bool) {
	lv, lok := evalUpdOperand(o.left, item)
	rv, rok := evalUpdOperand(o.right, item)

	if !lok || !rok {
		return nil, false
	}

	lf, ok1 := lv.(float64)
	rf, ok2 := rv.(float64)

	if !ok1 || !ok2 {
		return nil, false
	}

	if o.op == "+" {
		return lf + rf, true
	}

	return lf - rf, true
}

func evalIfNotExists(o *updIfNotExists, item map[string]any) (any, bool) {
	if v, ok := resolvePath(o.path.Parts, item); ok {
		return v, true
	}

	return evalUpdOperand(o.def, item)
}

func evalListAppend(o *updListAppend, item map[string]any) (any, bool) {
	lv, lok := evalUpdOperand(o.left, item)
	rv, rok := evalUpdOperand(o.right, item)

	if !lok || !rok {
		return nil, false
	}

	ll, ok1 := lv.([]any)
	rl, ok2 := rv.([]any)

	if !ok1 || !ok2 {
		return nil, false
	}

	out := make([]any, 0, len(ll)+len(rl))
	out = append(out, ll...)
	out = append(out, rl...)

	return out, true
}

// assignPath places value at parts within item. The top-level map is the
// caller-owned copy and is mutated in place; nested containers are cloned
// copy-on-write so structure shared with the stored item is never mutated. A
// list index at or beyond the slice length appends, matching DynamoDB.
func assignPath(item map[string]any, parts []PathPart, value any) {
	head := parts[0]

	if len(parts) == 1 {
		item[head.Name] = value
		return
	}

	item[head.Name] = mergeAssign(item[head.Name], parts[1:], value)
}

func mergeAssign(cur any, parts []PathPart, value any) any {
	part := parts[0]
	last := len(parts) == 1

	if part.IsIndex {
		arr := cloneSlice(cur)

		if last {
			return setListElem(arr, part.Index, value)
		}

		if part.Index >= 0 && part.Index < len(arr) {
			arr[part.Index] = mergeAssign(arr[part.Index], parts[1:], value)
		}

		return arr
	}

	m := cloneMap(cur)

	if last {
		m[part.Name] = value
	} else {
		m[part.Name] = mergeAssign(m[part.Name], parts[1:], value)
	}

	return m
}

func setListElem(arr []any, idx int, value any) []any {
	if idx >= 0 && idx < len(arr) {
		arr[idx] = value
		return arr
	}

	return append(arr, value)
}

// removePath deletes the value at parts within item, mutating the owned top map
// in place and cloning nested containers copy-on-write. A list index shifts
// later elements down; missing paths are a no-op.
func removePath(item map[string]any, parts []PathPart) {
	head := parts[0]

	if len(parts) == 1 {
		delete(item, head.Name)
		return
	}

	if child, ok := item[head.Name]; ok {
		item[head.Name] = mergeRemove(child, parts[1:])
	}
}

func mergeRemove(cur any, parts []PathPart) any {
	part := parts[0]
	last := len(parts) == 1

	if part.IsIndex {
		return removeListElem(cur, part.Index, parts, last)
	}

	m := cloneMap(cur)

	if last {
		delete(m, part.Name)
	} else if child, present := m[part.Name]; present {
		m[part.Name] = mergeRemove(child, parts[1:])
	}

	return m
}

func removeListElem(cur any, idx int, parts []PathPart, last bool) any {
	arr := cloneSlice(cur)
	if idx < 0 || idx >= len(arr) {
		return arr
	}

	if last {
		return append(arr[:idx:idx], arr[idx+1:]...)
	}

	arr[idx] = mergeRemove(arr[idx], parts[1:])

	return arr
}

func cloneMap(v any) map[string]any {
	src, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}

	out := make(map[string]any, len(src))
	for k, e := range src {
		out[k] = e
	}

	return out
}

func cloneSlice(v any) []any {
	src, _ := v.([]any)
	out := make([]any, len(src))
	copy(out, src)

	return out
}
