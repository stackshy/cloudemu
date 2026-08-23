package expr

import (
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ParseProjection parses a DynamoDB ProjectionExpression — a comma-separated
// list of document paths (top-level names, nested a.b, and a[n] indexes) —
// into the paths to retain. names resolves #alias steps
// (ExpressionAttributeNames). An empty expression yields a nil slice, which
// callers treat as "return the whole item".
func ParseProjection(raw string, names map[string]string) ([]*PathOperand, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	toks, err := lex(raw)
	if err != nil {
		return nil, err
	}

	p := &parser{toks: toks, names: names}
	if p.peek().kind == tEOF {
		return nil, cerrors.New(cerrors.InvalidArgument, "empty projection expression")
	}

	return parseProjectionPaths(p)
}

func parseProjectionPaths(p *parser) ([]*PathOperand, error) {
	paths := []*PathOperand{}

	for {
		path, err := p.parsePath()
		if err != nil {
			return nil, err
		}

		paths = append(paths, path)

		if p.peek().kind != tComma {
			break
		}

		p.next()
	}

	if p.peek().kind != tEOF {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unexpected token %q in projection", p.peek().text)
	}

	return paths, nil
}

// Project returns a deep copy of item containing only the given paths,
// preserving nested map structure. A path that is absent from item is skipped,
// matching DynamoDB, which omits missing projected attributes. Projected list
// indexes are compacted into a list of just those elements in index order,
// again matching DynamoDB. With no paths it returns item unchanged. The result
// shares no mutable structure with item.
func Project(item map[string]any, paths []*PathOperand) map[string]any {
	if len(paths) == 0 {
		return item
	}

	out := map[string]any{}

	for _, path := range paths {
		v, ok := resolvePath(path.Parts, item)
		if !ok {
			continue
		}

		setProjectedPath(out, path.Parts, v)
	}

	return finalizeMap(out)
}

// setProjectedPath writes val into dst at parts, creating intermediate maps
// (name steps) and sparse lists (index steps) and merging with structure
// placed by earlier projected paths. A path always begins with a name step.
func setProjectedPath(dst map[string]any, parts []PathPart, val any) {
	head := parts[0]

	if len(parts) == 1 {
		dst[head.Name] = val
		return
	}

	dst[head.Name] = mergeProjected(dst[head.Name], parts[1:], val)
}

func mergeProjected(existing any, parts []PathPart, val any) any {
	part := parts[0]

	if part.IsIndex {
		sl := asSparse(existing)
		sl.byIndex[part.Index] = leafOrMerge(sl.byIndex[part.Index], parts, val)

		return sl
	}

	m := asMap(existing)
	m[part.Name] = leafOrMerge(m[part.Name], parts, val)

	return m
}

// leafOrMerge returns val when parts has a single remaining step, otherwise it
// merges val deeper along parts[1:].
func leafOrMerge(existing any, parts []PathPart, val any) any {
	if len(parts) == 1 {
		return val
	}

	return mergeProjected(existing, parts[1:], val)
}

// sparseList accumulates projected list elements by their source index while a
// projection is being built. finalize converts it to a compacted list ordered
// by index — DynamoDB returns only the projected elements, dropping the gaps
// between them.
type sparseList struct {
	byIndex map[int]any
}

func asSparse(v any) *sparseList {
	if sl, ok := v.(*sparseList); ok {
		return sl
	}

	return &sparseList{byIndex: map[int]any{}}
}

func asMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}

	return m
}

// finalize materializes the projection scaffold into plain values: sparse
// lists become compacted slices, maps and slices are deep-copied so the result
// shares no mutable structure with the source item, and scalars pass through.
func finalize(v any) any {
	switch x := v.(type) {
	case *sparseList:
		return finalizeSparse(x)
	case map[string]any:
		return finalizeMap(x)
	case []any:
		return finalizeSlice(x)
	default:
		return v
	}
}

func finalizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, e := range m {
		out[k] = finalize(e)
	}

	return out
}

func finalizeSlice(s []any) []any {
	out := make([]any, 0, len(s))
	for _, e := range s {
		out = append(out, finalize(e))
	}

	return out
}

func finalizeSparse(s *sparseList) []any {
	idxs := make([]int, 0, len(s.byIndex))
	for i := range s.byIndex {
		idxs = append(idxs, i)
	}

	sort.Ints(idxs)

	out := make([]any, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, finalize(s.byIndex[i]))
	}

	return out
}
