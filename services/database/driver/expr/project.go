package expr

import (
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

// Project returns a copy of item containing only the given paths, preserving
// nested map and list structure. A path that is absent from item is skipped,
// matching DynamoDB, which omits missing projected attributes. With no paths
// it returns item unchanged.
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

	return out
}

// setProjectedPath writes val into dst at parts, creating intermediate maps
// (name steps) and slices (index steps) and merging with structure placed by
// earlier projected paths. A path always begins with a name step. Index steps
// only occur for indexes that exist in the source item, so slice growth is
// bounded by the source document.
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
		arr := growSlice(asSlice(existing), part.Index)
		arr[part.Index] = leafOrMerge(arr[part.Index], parts, val)

		return arr
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

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}

	return m
}

// growSlice returns s extended so index idx is addressable, filling new gaps
// with nil.
func growSlice(s []any, idx int) []any {
	for len(s) <= idx {
		s = append(s, nil)
	}

	return s
}
