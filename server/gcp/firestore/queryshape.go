package firestore

import (
	"sort"
	"strings"

	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// docName is the field carrying the document id, addressable in a query as the
// special __name__ path.
const (
	docName    = "id"
	fieldName  = "__name__"
	descending = "DESCENDING"
)

// sortKey is one resolved ORDER BY term.
type sortKey struct {
	path string
	desc bool
}

// shapeResults applies the non-WHERE parts of a StructuredQuery to the matched
// documents in order: ORDER BY, startAt/endAt cursors, offset, limit, then the
// select projection.
func shapeResults(items []map[string]any, q *structuredQuery) []map[string]any {
	keys := effectiveSortKeys(q.OrderBy)

	applyOrderBy(items, keys)

	items = applyCursor(items, keys, q.StartAt, true)
	items = applyCursor(items, keys, q.EndAt, false)
	items = applyOffsetLimit(items, q.Offset, q.Limit)

	return applySelect(items, q.Select)
}

// effectiveSortKeys returns the ORDER BY terms plus the implicit __name__
// tiebreaker Firestore appends (inheriting the last term's direction). When no
// ORDER BY is given the result is a single ascending __name__ key, which
// matches the store's default document-id order.
func effectiveSortKeys(orderBy []orderByClause) []sortKey {
	keys := make([]sortKey, 0, len(orderBy)+1)
	hasName := false

	for _, ob := range orderBy {
		keys = append(keys, sortKey{path: ob.Field.FieldPath, desc: isDescending(ob.Direction)})

		if ob.Field.FieldPath == fieldName {
			hasName = true
		}
	}

	if !hasName {
		desc := false
		if len(orderBy) > 0 {
			desc = isDescending(orderBy[len(orderBy)-1].Direction)
		}

		keys = append(keys, sortKey{path: fieldName, desc: desc})
	}

	return keys
}

func isDescending(dir direction) bool { return strings.EqualFold(string(dir), descending) }

func applyOrderBy(items []map[string]any, keys []sortKey) {
	sort.SliceStable(items, func(i, j int) bool {
		return compareByKeys(items[i], items[j], keys) < 0
	})
}

func compareByKeys(a, b map[string]any, keys []sortKey) int {
	for _, k := range keys {
		c := dbdriver.CompareValues(resolveField(a, k.path), resolveField(b, k.path))
		if k.desc {
			c = -c
		}

		if c != 0 {
			return c
		}
	}

	return 0
}

// applyCursor keeps the documents on the requested side of a startAt/endAt
// boundary. isStart distinguishes startAt (lower bound) from endAt (upper).
func applyCursor(items []map[string]any, keys []sortKey, cur *queryCursor, isStart bool) []map[string]any {
	if cur == nil {
		return items
	}

	vals := make([]any, len(cur.Values))
	for i, v := range cur.Values {
		vals[i] = firestoreValueToGo(v)
	}

	out := make([]map[string]any, 0, len(items))

	for _, item := range items {
		if cursorKeeps(compareToCursor(item, keys, vals), cur.Before, isStart) {
			out = append(out, item)
		}
	}

	return out
}

// compareToCursor compares a document's sort tuple against the cursor values,
// over as many positions as the cursor supplies.
func compareToCursor(item map[string]any, keys []sortKey, vals []any) int {
	n := min(len(keys), len(vals))

	for i := range n {
		c := dbdriver.CompareValues(resolveField(item, keys[i].path), vals[i])
		if keys[i].desc {
			c = -c
		}

		if c != 0 {
			return c
		}
	}

	return 0
}

// cursorKeeps applies the four boundary cases: startAt/before=true keeps k>=c
// (StartAt), startAt/before=false keeps k>c (StartAfter), endAt/before=true
// keeps k<c (EndBefore), endAt/before=false keeps k<=c (EndAt).
func cursorKeeps(c int, before, isStart bool) bool {
	switch {
	case isStart && before:
		return c >= 0
	case isStart:
		return c > 0
	case before:
		return c < 0
	default:
		return c <= 0
	}
}

func applyOffsetLimit(items []map[string]any, offset, limit int) []map[string]any {
	if offset > 0 {
		if offset >= len(items) {
			return nil
		}

		items = items[offset:]
	}

	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}

	return items
}

// applySelect projects each document to the selected field paths, always
// retaining the document id (its name). An absent or empty select returns the
// documents unchanged.
func applySelect(items []map[string]any, sel *selectClause) []map[string]any {
	if sel == nil || len(sel.Fields) == 0 {
		return items
	}

	paths := make([]*expr.PathOperand, 0, len(sel.Fields))

	for _, f := range sel.Fields {
		if f.FieldPath == fieldName {
			continue // the name is always retained below
		}

		paths = append(paths, fieldPathToOperand(f.FieldPath))
	}

	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, selectDoc(item, paths))
	}

	return out
}

func selectDoc(item map[string]any, paths []*expr.PathOperand) map[string]any {
	var projected map[string]any
	if len(paths) == 0 {
		projected = map[string]any{}
	} else {
		projected = expr.Project(item, paths)
	}

	if id, ok := item[docName]; ok {
		projected[docName] = id
	}

	// Carry the reserved commit-timestamp keys through the projection so a
	// selected document still reports stable createTime/updateTime.
	for _, k := range []string{fieldCreateTime, fieldUpdateTime} {
		if v, ok := item[k]; ok {
			projected[k] = v
		}
	}

	return projected
}

// resolveField resolves a document field path (including the special __name__)
// to its value, walking nested maps. A missing path yields nil.
func resolveField(item map[string]any, path string) any {
	if path == fieldName {
		return item[docName]
	}

	var cur any = item

	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}

		cur = m[seg]
	}

	return cur
}
