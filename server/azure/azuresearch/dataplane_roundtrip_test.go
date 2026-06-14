package azuresearch_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndexAndDocumentSearch(t *testing.T) {
	url := newServer(t)

	// Create an index.
	idx := do(t, http.MethodPut, url+"/indexes/products", map[string]any{
		"name": "products",
		"fields": []any{
			map[string]any{"name": "id", "type": "Edm.String", "key": true, "retrievable": true},
			map[string]any{"name": "name", "type": "Edm.String", "searchable": true, "retrievable": true},
		},
	})
	assert.Equal(t, "products", idx["name"])

	list := do(t, http.MethodGet, url+"/indexes", nil)
	assert.Len(t, list["value"], 1)

	// Upload documents.
	up := do(t, http.MethodPost, url+"/indexes/products/docs/index", map[string]any{
		"value": []any{
			map[string]any{"@search.action": "upload", "id": "1", "name": "red widget"},
			map[string]any{"@search.action": "upload", "id": "2", "name": "blue gadget"},
		},
	})
	assert.Len(t, up["value"], 2)

	// Count.
	cnt := do(t, http.MethodGet, url+"/indexes/products/docs/$count", nil)
	_ = cnt // count returns a bare number; just assert the call succeeded (200)

	// Search with count.
	res := do(t, http.MethodPost, url+"/indexes/products/docs/search", map[string]any{
		"search": "widget", "count": true,
	})
	require.Len(t, res["value"], 1)
	assert.EqualValues(t, 1, res["@odata.count"])
	assert.Equal(t, "red widget", res["value"].([]any)[0].(map[string]any)["name"])

	// Search all.
	all := do(t, http.MethodPost, url+"/indexes/products/docs/search", map[string]any{"search": "*", "count": true})
	assert.EqualValues(t, 2, all["@odata.count"])

	// Get document by key.
	doc := do(t, http.MethodGet, url+"/indexes/products/docs/2", nil)
	assert.Equal(t, "blue gadget", doc["name"])

	// Suggest / autocomplete.
	sug := do(t, http.MethodPost, url+"/indexes/products/docs/suggest", map[string]any{"search": "blue", "suggesterName": "sg"})
	assert.Len(t, sug["value"], 1)

	// Delete a doc via merge action then verify count drops.
	do(t, http.MethodPost, url+"/indexes/products/docs/index", map[string]any{
		"value": []any{map[string]any{"@search.action": "delete", "id": "1"}},
	})
	after := do(t, http.MethodPost, url+"/indexes/products/docs/search", map[string]any{"search": "*", "count": true})
	assert.EqualValues(t, 1, after["@odata.count"])
}

func TestIndexersDataSourcesSkillsetsSynonymsAliases(t *testing.T) {
	url := newServer(t)
	do(t, http.MethodPut, url+"/indexes/products", map[string]any{"name": "products"})

	ds := do(t, http.MethodPut, url+"/datasources/blob1", map[string]any{
		"name": "blob1", "type": "azureblob", "container": map[string]any{"name": "docs"},
		"credentials": map[string]any{"connectionString": "x"},
	})
	assert.Equal(t, "blob1", ds["name"])

	ss := do(t, http.MethodPut, url+"/skillsets/enrich", map[string]any{
		"name": "enrich", "skills": []any{map[string]any{"x": 1}},
	})
	assert.Equal(t, "enrich", ss["name"])

	ix := do(t, http.MethodPut, url+"/indexers/idxr", map[string]any{
		"name": "idxr", "dataSourceName": "blob1", "targetIndexName": "products", "skillsetName": "enrich",
	})
	assert.Equal(t, "idxr", ix["name"])

	do(t, http.MethodPost, url+"/indexers/idxr/run", map[string]any{})
	st := do(t, http.MethodGet, url+"/indexers/idxr/status", nil)
	assert.Equal(t, "running", st["status"])

	sm := do(t, http.MethodPut, url+"/synonymmaps/syn", map[string]any{
		"name": "syn", "format": "solr", "synonyms": "usa, united states",
	})
	assert.Equal(t, "syn", sm["name"])

	al := do(t, http.MethodPut, url+"/aliases/prod-alias", map[string]any{
		"name": "prod-alias", "indexes": []any{"products"},
	})
	assert.Equal(t, "prod-alias", al["name"])

	assert.Len(t, do(t, http.MethodGet, url+"/indexers", nil)["value"], 1)
	assert.Len(t, do(t, http.MethodGet, url+"/datasources", nil)["value"], 1)
	assert.Len(t, do(t, http.MethodGet, url+"/skillsets", nil)["value"], 1)
	assert.Len(t, do(t, http.MethodGet, url+"/synonymmaps", nil)["value"], 1)
	assert.Len(t, do(t, http.MethodGet, url+"/aliases", nil)["value"], 1)

	stats := do(t, http.MethodGet, url+"/servicestats", nil)
	counters := stats["counters"].(map[string]any)
	assert.EqualValues(t, 1, counters["indexesCount"].(map[string]any)["usage"])
}
