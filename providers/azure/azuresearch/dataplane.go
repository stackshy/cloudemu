package azuresearch

import (
	"context"
	"fmt"
	"sort"
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
		docKey := fmt.Sprintf("%v", act.Document[kf])
		storeKey := key(service, index, docKey)

		switch act.Action {
		case "delete":
			m.documents.Delete(storeKey)
		case "merge":
			existing, ok := m.documents.Get(storeKey)
			if !ok {
				results = append(results, driver.IndexResult{
					Key: docKey, Status: false, StatusCode: 404, ErrorMsg: "document not found for merge",
				})

				continue
			}

			merged := copyDoc(existing)
			for k, v := range act.Document {
				merged[k] = v
			}

			m.documents.Set(storeKey, merged)
		default: // upload | mergeOrUpload
			m.documents.Set(storeKey, copyDoc(act.Document))
		}

		results = append(results, driver.IndexResult{Key: docKey, Status: true, StatusCode: 200})
	}

	m.emitMetric("document/indexed", float64(len(actions)), map[string]string{"index": index})

	return results, nil
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

	docs := m.indexDocs(service, index)

	// Naive full-text match: keep docs whose any string field contains the
	// (case-insensitive) search term; "" or "*" matches everything.
	term := strings.ToLower(strings.TrimSpace(req.Search))
	matched := make([]driver.SearchResult, 0, len(docs))

	for _, d := range docs {
		if term == "" || term == "*" || docContains(d, term) {
			matched = append(matched, driver.SearchResult{Score: 1.0, Document: applySelect(d, req.Select)})
		}
	}

	sortByKey(matched, m.keyField(service, index))

	resp := &driver.SearchResponse{Count: -1, Results: paginate(matched, req.Skip, req.Top)}
	if req.Count {
		resp.Count = int64(len(matched))
	}

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

func sortByKey(rs []driver.SearchResult, kf string) {
	sort.SliceStable(rs, func(i, j int) bool {
		return fmt.Sprintf("%v", rs[i].Document[kf]) < fmt.Sprintf("%v", rs[j].Document[kf])
	})
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
