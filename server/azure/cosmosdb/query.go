package cosmosdb

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/cosmossql"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// allResults fetches the whole container so the SQL query is evaluated with
// full fidelity in the handler.
const (
	allResults      = 1 << 30
	defaultPageSize = 100
)

type queryBody struct {
	Query      string `json:"query"`
	Parameters []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	} `json:"parameters"`
}

func (q queryBody) paramMap() map[string]any {
	m := make(map[string]any, len(q.Parameters))
	for _, p := range q.Parameters {
		m[p.Name] = p.Value
	}

	return m
}

func decodeQueryBody(w http.ResponseWriter, r *http.Request) (queryBody, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var q queryBody
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid query JSON: "+err.Error())
		return queryBody{}, false
	}

	return q, true
}

// cosmosFilter keeps the items matching node (a nil node matches all).
func cosmosFilter(items []map[string]any, node expr.Node) ([]map[string]any, error) {
	if node == nil {
		return items, nil
	}

	out := make([]map[string]any, 0, len(items))

	for _, item := range items {
		ok, err := expr.Eval(node, item)
		if err != nil {
			return nil, err
		}

		if ok {
			out = append(out, item)
		}
	}

	return out, nil
}

// shapeCosmos applies ORDER BY, projection, DISTINCT and the SQL TOP/OFFSET/
// LIMIT to the matched documents, returning the logical result set (full docs,
// projected objects, or bare scalars for SELECT VALUE / aggregates).
func shapeCosmos(matched []map[string]any, stmt *cosmossql.Statement) []any {
	if stmt.Proj.Kind == cosmossql.ProjAggregate {
		return aggregateResult(matched, stmt.Proj)
	}

	if len(stmt.OrderBy) > 0 {
		sortCosmos(matched, stmt.OrderBy)
	}

	rows := projectCosmos(matched, stmt.Proj)

	if stmt.Distinct {
		rows = distinctRows(rows)
	}

	return sqlPage(rows, stmt)
}

func sqlPage(rows []any, stmt *cosmossql.Statement) []any {
	if stmt.Offset > 0 {
		if stmt.Offset >= len(rows) {
			return nil
		}

		rows = rows[stmt.Offset:]
	}

	limit := -1
	if stmt.Top > 0 {
		limit = stmt.Top
	}

	if stmt.Limit >= 0 {
		limit = stmt.Limit
	}

	if limit >= 0 && limit < len(rows) {
		rows = rows[:limit]
	}

	return rows
}

func projectCosmos(items []map[string]any, proj cosmossql.Projection) []any {
	out := make([]any, 0, len(items))

	for _, item := range items {
		switch proj.Kind {
		case cosmossql.ProjStar:
			addSystemProps(item)
			out = append(out, item)
		case cosmossql.ProjFields:
			out = append(out, projectFields(item, proj.Fields))
		case cosmossql.ProjValue:
			if v, ok := resolveDocValue(item, proj.ValuePath); ok {
				out = append(out, v)
			}
		case cosmossql.ProjAggregate:
		}
	}

	return out
}

func projectFields(item map[string]any, fields []cosmossql.ProjField) map[string]any {
	obj := map[string]any{}

	for _, f := range fields {
		if v, ok := resolveDocValue(item, f.Path); ok {
			obj[fieldKey(f)] = v
		}
	}

	return obj
}

func fieldKey(f cosmossql.ProjField) string {
	switch {
	case f.Alias != "":
		return f.Alias
	case len(f.Path) > 0:
		return f.Path[len(f.Path)-1]
	default:
		return "$1"
	}
}

// resolveDocValue walks a name path over nested maps. An empty path (a bare
// alias) is the whole document.
func resolveDocValue(item map[string]any, segs []string) (any, bool) {
	if len(segs) == 0 {
		return item, true
	}

	var cur any = item

	for _, s := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		v, ok := m[s]
		if !ok {
			return nil, false
		}

		cur = v
	}

	return cur, true
}

func sortCosmos(items []map[string]any, order []cosmossql.OrderTerm) {
	sort.SliceStable(items, func(i, j int) bool {
		for _, t := range order {
			av, _ := resolveDocValue(items[i], t.Path)
			bv, _ := resolveDocValue(items[j], t.Path)

			c := dbdriver.CompareValues(av, bv)
			if t.Desc {
				c = -c
			}

			if c != 0 {
				return c < 0
			}
		}

		return false
	})
}

func distinctRows(rows []any) []any {
	seen := make(map[string]bool, len(rows))
	out := make([]any, 0, len(rows))

	for _, row := range rows {
		key := distinctKey(row)
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, row)
	}

	return out
}

func distinctKey(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func aggregateResult(matched []map[string]any, proj cosmossql.Projection) []any {
	val, ok := computeAggregate(matched, proj.Aggregate)
	if !ok {
		// An aggregate over no values is undefined; Cosmos returns no row.
		return []any{}
	}

	if proj.Bare {
		return []any{map[string]any{"$1": val}}
	}

	return []any{val}
}

func computeAggregate(matched []map[string]any, agg *cosmossql.Aggregate) (any, bool) {
	if agg.Func == "COUNT" {
		return float64(countDefined(matched, agg.Path)), true
	}

	nums := collectNumbers(matched, agg.Path)
	if len(nums) == 0 {
		return nil, false
	}

	return reduceNumbers(agg.Func, nums), true
}

func countDefined(matched []map[string]any, path []string) int {
	if len(path) == 0 {
		return len(matched)
	}

	n := 0

	for _, item := range matched {
		if _, ok := resolveDocValue(item, path); ok {
			n++
		}
	}

	return n
}

func collectNumbers(matched []map[string]any, path []string) []float64 {
	var nums []float64

	for _, item := range matched {
		v, ok := resolveDocValue(item, path)
		if !ok {
			continue
		}

		if f, ok := numericValue(v); ok {
			nums = append(nums, f)
		}
	}

	return nums
}

func reduceNumbers(fn string, nums []float64) any {
	switch fn {
	case "MIN":
		acc := nums[0]
		for _, n := range nums[1:] {
			acc = min(acc, n)
		}

		return acc
	case "MAX":
		acc := nums[0]
		for _, n := range nums[1:] {
			acc = max(acc, n)
		}

		return acc
	default: // SUM, AVG
		sum := 0.0
		for _, n := range nums {
			sum += n
		}

		if fn == "AVG" {
			return sum / float64(len(nums))
		}

		return sum
	}
}

func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func maxItemCount(r *http.Request) int {
	v := r.Header.Get("X-Ms-Max-Item-Count")
	if v == "" {
		return defaultPageSize
	}

	n, err := strconv.Atoi(v)
	if err != nil || n == 0 {
		return defaultPageSize
	}

	if n < 0 {
		return allResults
	}

	return n
}

func continuationOffset(tok string) int {
	if n, err := strconv.Atoi(tok); err == nil && n > 0 {
		return n
	}

	return 0
}

// pageDocs returns the page starting at start and the next continuation offset
// (0 when the page is the last).
func pageDocs(docs []any, start, pageSize int) (page []any, next int) {
	if start >= len(docs) {
		return nil, 0
	}

	end := start + pageSize
	if end >= len(docs) {
		return docs[start:], 0
	}

	return docs[start:end], end
}
