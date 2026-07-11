package azuresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/azuresearch/driver"
	"github.com/stackshy/cloudemu/errors"
)

// maxSearchTop bounds the number of documents a single search returns so an
// unvalidated request can't trigger an unbounded response.
const maxSearchTop = 1000

// --- Indexes ---

func cloneIndex(i *driver.Index) *driver.Index {
	out := *i
	out.Fields = append([]driver.Field(nil), i.Fields...)

	return &out
}

func (m *Mock) CreateOrUpdateIndex(_ context.Context, service string, idx driver.Index) (*driver.Index, error) {
	if idx.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "index name is required")
	}

	stored := cloneIndex(&idx)
	stored.ETag = m.etag()
	m.indexes.Set(key(service, idx.Name), stored)
	m.emitMetric("index/count", 1, map[string]string{"service": service})

	return cloneIndex(stored), nil
}

func (m *Mock) GetIndex(_ context.Context, service, name string) (*driver.Index, error) {
	i, ok := m.indexes.Get(key(service, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "index %q not found", name)
	}

	return cloneIndex(i), nil
}

func (m *Mock) ListIndexes(_ context.Context, service string) ([]driver.Index, error) {
	prefix := service + "/"
	out := make([]driver.Index, 0)

	for k, i := range m.indexes.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *cloneIndex(i))
		}
	}

	return out, nil
}

func (m *Mock) DeleteIndex(_ context.Context, service, name string) error {
	if !m.indexes.Delete(key(service, name)) {
		return errors.Newf(errors.NotFound, "index %q not found", name)
	}

	// Drop the index's documents too.
	docPrefix := key(service, name) + "/"
	for k := range m.documents.All() {
		if strings.HasPrefix(k, docPrefix) {
			m.documents.Delete(k)
		}
	}

	return nil
}

// keyField returns the name of the index's key field (defaults to "id").
func (m *Mock) keyField(service, index string) string {
	if idx, ok := m.indexes.Get(key(service, index)); ok {
		for _, f := range idx.Fields {
			if f.Key {
				return f.Name
			}
		}
	}

	return "id"
}

// --- Documents ---

func (m *Mock) IndexDocuments(_ context.Context, service, index string, actions []driver.IndexAction) ([]driver.IndexResult, error) {
	if !m.indexes.Has(key(service, index)) {
		return nil, errors.Newf(errors.NotFound, "index %q not found", index)
	}

	kf := m.keyField(service, index)
	results := make([]driver.IndexResult, 0, len(actions))

	for _, act := range actions {
		kv, hasKey := act.Document[kf]
		docKey := fmt.Sprintf("%v", kv)

		if !hasKey || docKey == "" {
			results = append(results, driver.IndexResult{
				Key: docKey, Status: false, StatusCode: 400, ErrorMsg: "document key field " + kf + " is missing or empty",
			})

			continue
		}

		storeKey := key(service, index, docKey)

		switch act.Action {
		case "delete":
			// Delete is idempotent in Azure AI Search: removing an absent key returns 200.
			m.documents.Delete(storeKey)
		case "merge":
			if !m.mergeInto(storeKey, act.Document) {
				results = append(results, driver.IndexResult{
					Key: docKey, Status: false, StatusCode: 404, ErrorMsg: "document not found for merge",
				})

				continue
			}
		case "mergeOrUpload":
			// Merge onto an existing doc, preserving unspecified fields; insert otherwise.
			if !m.mergeInto(storeKey, act.Document) {
				m.documents.Set(storeKey, copyDoc(act.Document))
			}
		default: // upload — full replace.
			m.documents.Set(storeKey, copyDoc(act.Document))
		}

		results = append(results, driver.IndexResult{Key: docKey, Status: true, StatusCode: 200})
	}

	m.emitMetric("document/indexed", float64(len(actions)), map[string]string{"index": index})

	return results, nil
}

// mergeInto merges src onto the stored doc at storeKey, preserving fields absent
// from src. Returns false if no doc exists at storeKey.
func (m *Mock) mergeInto(storeKey string, src map[string]any) bool {
	existing, ok := m.documents.Get(storeKey)
	if !ok {
		return false
	}

	merged := copyDoc(existing)
	for k, v := range src {
		merged[k] = v
	}

	m.documents.Set(storeKey, merged)

	return true
}

func copyDoc(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

//nolint:gocritic // req matches the driver signature; copied on entry.
func (m *Mock) SearchDocuments(_ context.Context, service, index string, req driver.SearchRequest) (*driver.SearchResponse, error) {
	if !m.indexes.Has(key(service, index)) {
		return nil, errors.Newf(errors.NotFound, "index %q not found", index)
	}

	filter, err := parseFilter(req.Filter)
	if err != nil {
		return nil, err
	}

	docs := m.indexDocs(service, index)

	// Naive full-text match: keep docs whose any string field contains the
	// (case-insensitive) search term; "" or "*" matches everything. The optional
	// $filter comparison is applied on top.
	term := strings.ToLower(strings.TrimSpace(req.Search))
	matched := make([]driver.SearchResult, 0, len(docs))

	for _, d := range docs {
		if term != "" && term != "*" && !docContains(d, term) {
			continue
		}

		if !filter.match(d) {
			continue
		}

		matched = append(matched, driver.SearchResult{Score: 1.0, Document: d})
	}

	orderResults(matched, req.OrderBy, m.keyField(service, index))

	resp := &driver.SearchResponse{Count: -1}
	if req.Count {
		resp.Count = int64(len(matched))
	}

	// Paginate first, then project $select onto the returned page.
	page := paginate(matched, req.Skip, req.Top)
	for i := range page {
		page[i].Document = applySelect(page[i].Document, req.Select)
	}

	resp.Results = page

	m.emitMetric("query/count", 1, map[string]string{"index": index})

	return resp, nil
}

func (m *Mock) indexDocs(service, index string) []map[string]any {
	prefix := key(service, index) + "/"
	out := make([]map[string]any, 0)

	for k, d := range m.documents.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, copyDoc(d))
		}
	}

	return out
}

func docContains(d map[string]any, term string) bool {
	for _, v := range d {
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), term) {
			return true
		}
	}

	return false
}

func applySelect(d map[string]any, sel []string) map[string]any {
	if len(sel) == 0 {
		return copyDoc(d)
	}

	out := make(map[string]any, len(sel))

	for _, f := range sel {
		if v, ok := d[f]; ok {
			out[f] = v
		}
	}

	return out
}

// orderResults sorts rs by the $orderby clause ("field [asc|desc]"), falling
// back to the index key field (ascending) when no clause is given. Only the
// first clause is honored.
func orderResults(rs []driver.SearchResult, orderBy, keyField string) {
	field, desc := keyField, false

	if ob := strings.TrimSpace(orderBy); ob != "" {
		firstClause, _, _ := strings.Cut(ob, ",")

		clause := strings.Fields(firstClause)
		if len(clause) > 0 {
			field = clause[0]
		}

		if len(clause) > 1 && strings.EqualFold(clause[1], "desc") {
			desc = true
		}
	}

	sort.SliceStable(rs, func(i, j int) bool {
		c := compareAny(rs[i].Document[field], rs[j].Document[field])
		if desc {
			return c > 0
		}

		return c < 0
	})
}

// compareAny orders two document values numerically when both are numbers and
// lexically otherwise. Returns -1, 0, or 1.
func compareAny(a, b any) int {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)

	if aok && bok {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}

	return strings.Compare(fmt.Sprintf("%v", a), fmt.Sprintf("%v", b))
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// docFilter is a single OData comparison clause: field op value.
type docFilter struct {
	field string
	op    string
	value any // float64 or string
}

// filterOps maps each supported OData comparison operator to a predicate over
// the result of compareAny (-1, 0, or 1).
//
//nolint:gochecknoglobals // immutable operator table
var filterOps = map[string]func(int) bool{
	"eq": func(c int) bool { return c == 0 },
	"ne": func(c int) bool { return c != 0 },
	"gt": func(c int) bool { return c > 0 },
	"ge": func(c int) bool { return c >= 0 },
	"lt": func(c int) bool { return c < 0 },
	"le": func(c int) bool { return c <= 0 },
}

// parseFilter parses a single OData comparison ("price gt 100", "name eq 'blue'").
// Boolean combinations and functions are unsupported and rejected rather than
// silently ignored. An empty filter yields a nil (match-everything) filter.
func parseFilter(expr string) (*docFilter, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil //nolint:nilnil // nil filter means "match everything".
	}

	toks := strings.Fields(expr)

	const minTokens = 3
	if len(toks) < minTokens {
		return nil, errors.Newf(errors.InvalidArgument, "unsupported filter expression %q", expr)
	}

	field, op := toks[0], strings.ToLower(toks[1])
	if _, ok := filterOps[op]; !ok {
		return nil, errors.Newf(errors.InvalidArgument, "unsupported filter operator %q", op)
	}

	f := &docFilter{field: field, op: op}
	rawVal := strings.Join(toks[2:], " ")

	switch {
	case strings.HasPrefix(rawVal, "'"):
		if !strings.HasSuffix(rawVal, "'") || len(rawVal) < 2 || strings.Contains(rawVal[1:len(rawVal)-1], "'") {
			return nil, errors.Newf(errors.InvalidArgument, "unsupported filter expression %q", expr)
		}

		f.value = rawVal[1 : len(rawVal)-1]
	case len(toks) > minTokens:
		return nil, errors.Newf(errors.InvalidArgument, "only single comparisons are supported: %q", expr)
	default:
		if n, err := strconv.ParseFloat(rawVal, 64); err == nil {
			f.value = n
		} else {
			f.value = rawVal
		}
	}

	return f, nil
}

// match reports whether doc satisfies the filter; a nil filter matches all.
func (f *docFilter) match(doc map[string]any) bool {
	if f == nil {
		return true
	}

	v, ok := doc[f.field]
	if !ok {
		return false
	}

	pred, ok := filterOps[f.op]
	if !ok {
		return false
	}

	return pred(compareAny(v, f.value))
}

func paginate(rs []driver.SearchResult, skip, top int) []driver.SearchResult {
	if skip < 0 {
		skip = 0
	}

	if skip >= len(rs) {
		return []driver.SearchResult{}
	}

	rs = rs[skip:]

	if top <= 0 || top > maxSearchTop {
		top = maxSearchTop
	}

	if top < len(rs) {
		rs = rs[:top]
	}

	return rs
}

func (m *Mock) SuggestDocuments(
	_ context.Context, service, index, searchText, _ string, top int,
) ([]driver.SuggestResult, error) {
	if !m.indexes.Has(key(service, index)) {
		return nil, errors.Newf(errors.NotFound, "index %q not found", index)
	}

	term := strings.ToLower(strings.TrimSpace(searchText))
	out := make([]driver.SuggestResult, 0)

	for _, d := range m.indexDocs(service, index) {
		if text, ok := firstStringContaining(d, term); ok {
			out = append(out, driver.SuggestResult{Text: text, Document: d})
		}

		if top > 0 && len(out) >= top {
			break
		}
	}

	return out, nil
}

func (m *Mock) AutocompleteDocuments(
	_ context.Context, service, index, searchText, _ string, top int,
) ([]string, error) {
	if !m.indexes.Has(key(service, index)) {
		return nil, errors.Newf(errors.NotFound, "index %q not found", index)
	}

	term := strings.ToLower(strings.TrimSpace(searchText))
	seen := make(map[string]bool)
	out := make([]string, 0)

	for _, d := range m.indexDocs(service, index) {
		if text, ok := firstStringContaining(d, term); ok && !seen[text] {
			seen[text] = true

			out = append(out, text)
		}

		if top > 0 && len(out) >= top {
			break
		}
	}

	return out, nil
}

func firstStringContaining(d map[string]any, term string) (string, bool) {
	for _, v := range d {
		if s, ok := v.(string); ok && (term == "" || strings.Contains(strings.ToLower(s), term)) {
			return s, true
		}
	}

	return "", false
}

func (m *Mock) CountDocuments(_ context.Context, service, index string) (int64, error) {
	if !m.indexes.Has(key(service, index)) {
		return 0, errors.Newf(errors.NotFound, "index %q not found", index)
	}

	return int64(len(m.indexDocs(service, index))), nil
}

func (m *Mock) GetDocument(_ context.Context, service, index, docKey string) (map[string]any, error) {
	d, ok := m.documents.Get(key(service, index, docKey))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "document %q not found", docKey)
	}

	return copyDoc(d), nil
}
