package expr

import cerrors "github.com/stackshy/cloudemu/v2/errors"

// ApplyUpdate applies a parsed UpdateProgram to item, mutating it in place and
// returning it. item is expected to be a caller-owned copy. Clauses apply in
// SET, REMOVE, ADD, DELETE order, and every operand is resolved against the
// pre-update image of the item — DynamoDB evaluates all clauses against the
// item as it was before the update. Because writes are copy-on-write, a shallow
// snapshot stays pristine as item mutates. A SET operand that resolves to a
// missing attribute, an ADD/DELETE type mismatch, or an invalid document path
// is a cerrors.InvalidArgument.
func ApplyUpdate(item map[string]any, prog *UpdateProgram) (map[string]any, error) {
	orig := cloneMap(item)

	for _, s := range prog.sets {
		if err := applySet(item, orig, s); err != nil {
			return nil, err
		}
	}

	for _, p := range prog.removes {
		if err := removePath(item, p.Parts); err != nil {
			return nil, err
		}
	}

	for _, a := range prog.adds {
		if err := applyAdd(item, orig, a); err != nil {
			return nil, err
		}
	}

	for _, d := range prog.deletes {
		if err := applyDelete(item, orig, d); err != nil {
			return nil, err
		}
	}

	return item, nil
}

func applySet(item, orig map[string]any, s setItem) error {
	val, ok := evalUpdOperand(s.rhs, orig)
	if !ok {
		return cerrors.New(cerrors.InvalidArgument, "SET operand refers to a missing attribute")
	}

	return assignPath(item, s.path.Parts, val)
}

func applyAdd(item, orig map[string]any, a pathValue) error {
	cur, exists := resolvePath(a.path.Parts, orig)

	newVal, err := addValue(cur, exists, a.value.Value)
	if err != nil {
		return err
	}

	return assignPath(item, a.path.Parts, newVal)
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

func applyDelete(item, orig map[string]any, d pathValue) error {
	cur, exists := resolvePath(d.path.Parts, orig)
	if !exists {
		return nil
	}

	res, ok := DifferenceSets(cur, d.value.Value)
	if !ok {
		return cerrors.New(cerrors.InvalidArgument, "DELETE requires a set attribute and a matching set value")
	}

	if SetIsEmpty(res) {
		return removePath(item, d.path.Parts)
	}

	return assignPath(item, d.path.Parts, res)
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
// document path that traverses an attribute of the wrong container type (e.g.
// SET a.b where a is a scalar) is rejected, matching DynamoDB.
func assignPath(item map[string]any, parts []PathPart, value any) error {
	head := parts[0]

	if len(parts) == 1 {
		item[head.Name] = value
		return nil
	}

	child, err := mergeAssign(item[head.Name], parts[1:], value)
	if err != nil {
		return err
	}

	item[head.Name] = child

	return nil
}

func mergeAssign(cur any, parts []PathPart, value any) (any, error) {
	part := parts[0]
	last := len(parts) == 1

	if part.IsIndex {
		return assignIndex(cur, part.Index, parts, last, value)
	}

	m, ok := cur.(map[string]any)
	if !ok {
		return nil, invalidPath()
	}

	m = cloneMap(m)

	if last {
		m[part.Name] = value
		return m, nil
	}

	child, err := mergeAssign(m[part.Name], parts[1:], value)
	if err != nil {
		return nil, err
	}

	m[part.Name] = child

	return m, nil
}

func assignIndex(cur any, idx int, parts []PathPart, last bool, value any) (any, error) {
	arr, ok := cur.([]any)
	if !ok {
		return nil, invalidPath()
	}

	arr = cloneSlice(arr)

	if last {
		return setListElem(arr, idx, value), nil
	}

	if idx < 0 || idx >= len(arr) {
		return nil, invalidPath()
	}

	child, err := mergeAssign(arr[idx], parts[1:], value)
	if err != nil {
		return nil, err
	}

	arr[idx] = child

	return arr, nil
}

// setListElem sets arr[idx] = value, appending when idx is at or beyond the
// slice length (DynamoDB appends rather than creating gaps).
func setListElem(arr []any, idx int, value any) []any {
	if idx >= 0 && idx < len(arr) {
		arr[idx] = value
		return arr
	}

	return append(arr, value)
}

// removePath deletes the value at parts within item, mutating the owned top map
// in place and cloning nested containers copy-on-write. A list index shifts
// later elements down. A missing path is a no-op; a path that traverses the
// wrong container type is rejected.
func removePath(item map[string]any, parts []PathPart) error {
	head := parts[0]

	if len(parts) == 1 {
		delete(item, head.Name)
		return nil
	}

	child, ok := item[head.Name]
	if !ok {
		return nil
	}

	newChild, err := mergeRemove(child, parts[1:])
	if err != nil {
		return err
	}

	item[head.Name] = newChild

	return nil
}

func mergeRemove(cur any, parts []PathPart) (any, error) {
	part := parts[0]
	last := len(parts) == 1

	if part.IsIndex {
		return removeIndex(cur, part.Index, parts, last)
	}

	m, ok := cur.(map[string]any)
	if !ok {
		return nil, invalidPath()
	}

	m = cloneMap(m)

	if last {
		delete(m, part.Name)
		return m, nil
	}

	child, present := m[part.Name]
	if !present {
		return m, nil
	}

	nc, err := mergeRemove(child, parts[1:])
	if err != nil {
		return nil, err
	}

	m[part.Name] = nc

	return m, nil
}

func removeIndex(cur any, idx int, parts []PathPart, last bool) (any, error) {
	arr, ok := cur.([]any)
	if !ok {
		return nil, invalidPath()
	}

	arr = cloneSlice(arr)

	if idx < 0 || idx >= len(arr) {
		return arr, nil
	}

	if last {
		return append(arr[:idx:idx], arr[idx+1:]...), nil
	}

	child, err := mergeRemove(arr[idx], parts[1:])
	if err != nil {
		return nil, err
	}

	arr[idx] = child

	return arr, nil
}

func invalidPath() error {
	return cerrors.New(cerrors.InvalidArgument, "invalid document path for update")
}

func cloneMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, e := range src {
		out[k] = e
	}

	return out
}

func cloneSlice(src []any) []any {
	out := make([]any, len(src))
	copy(out, src)

	return out
}
