package azuresearch

import (
	"net/http"
	"strconv"
	"strings"

	srchdriver "github.com/stackshy/cloudemu/azuresearch/driver"
)

// atoiOr parses s as an int, returning def on failure or empty input.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}

	return def
}

// serveDocs handles /indexes/{index}/docs/... actions.
func (h *DataPlaneHandler) serveDocs(w http.ResponseWriter, r *http.Request, service, index string, parts []string) {
	// parts: [indexes {index} docs {action?}]
	const actionIdx = 3
	if len(parts) <= actionIdx {
		// GET /indexes/{index}/docs  → simple search via query params.
		h.searchDocs(w, r, service, index, true)

		return
	}

	action := parts[actionIdx]

	switch action {
	case "search":
		h.searchDocs(w, r, service, index, false)
	case "index":
		h.indexDocs(w, r, service, index)
	case "suggest":
		h.suggestDocs(w, r, service, index)
	case "autocomplete":
		h.autocompleteDocs(w, r, service, index)
	case "$count":
		h.countDocs(w, r, service, index)
	default:
		// Treat any other trailing segment as a document key:
		// /indexes/{index}/docs/{key} or /docs('{key}').
		h.getDoc(w, r, service, index, docKey(action))
	}
}

func docKey(seg string) string {
	if strings.HasPrefix(seg, "('") && strings.HasSuffix(seg, "')") {
		return seg[2 : len(seg)-2]
	}

	return seg
}

func (h *DataPlaneHandler) searchDocs(w http.ResponseWriter, r *http.Request, service, index string, fromQuery bool) {
	req := srchdriver.SearchRequest{Count: false}

	if fromQuery {
		q := r.URL.Query()
		req.Search = q.Get("search")
		req.Filter = q.Get("$filter")
		req.OrderBy = q.Get("$orderby")
		req.Count = q.Get("$count") == "true"
		req.Top = atoiOr(q.Get("$top"), 0)
		req.Skip = atoiOr(q.Get("$skip"), 0)

		if sel := q.Get("$select"); sel != "" {
			req.Select = strings.Split(sel, ",")
		}
	} else {
		var body struct {
			Search  string `json:"search"`
			Filter  string `json:"filter"`
			Top     int    `json:"top"`
			Skip    int    `json:"skip"`
			Select  string `json:"select"`
			Count   bool   `json:"count"`
			OrderBy string `json:"orderby"`
		}

		if !dpDecode(w, r, &body) {
			return
		}

		req.Search, req.Filter, req.Top, req.Skip = body.Search, body.Filter, body.Top, body.Skip
		req.Count, req.OrderBy = body.Count, body.OrderBy

		if body.Select != "" {
			req.Select = strings.Split(body.Select, ",")
		}
	}

	resp, err := h.dp.SearchDocuments(r.Context(), service, index, req)
	if err != nil {
		dpCErr(w, err)

		return
	}

	value := make([]map[string]any, 0, len(resp.Results))

	for _, res := range resp.Results {
		doc := map[string]any{"@search.score": res.Score}
		for k, v := range res.Document {
			doc[k] = v
		}

		value = append(value, doc)
	}

	out := map[string]any{"value": value}
	if resp.Count >= 0 {
		out["@odata.count"] = resp.Count
	}

	dpJSON(w, out)
}

func (h *DataPlaneHandler) indexDocs(w http.ResponseWriter, r *http.Request, service, index string) {
	var body struct {
		Value []map[string]any `json:"value"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	actions := make([]srchdriver.IndexAction, 0, len(body.Value))

	for _, raw := range body.Value {
		act := "upload"
		if a, ok := raw["@search.action"].(string); ok && a != "" {
			act = a
		}

		doc := make(map[string]any, len(raw))

		for k, v := range raw {
			if k != "@search.action" {
				doc[k] = v
			}
		}

		actions = append(actions, srchdriver.IndexAction{Action: act, Document: doc})
	}

	results, err := h.dp.IndexDocuments(r.Context(), service, index, actions)
	if err != nil {
		dpCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(results))
	status := http.StatusOK

	for _, res := range results {
		if !res.Status {
			// Azure returns 207 Multi-Status when any document in the batch fails.
			status = http.StatusMultiStatus
		}

		out = append(out, map[string]any{
			"key": res.Key, "status": res.Status, "statusCode": res.StatusCode, "errorMessage": res.ErrorMsg,
		})
	}

	dpJSONStatus(w, status, map[string]any{"value": out})
}

func (h *DataPlaneHandler) suggestDocs(w http.ResponseWriter, r *http.Request, service, index string) {
	var body struct {
		Search        string `json:"search"`
		SuggesterName string `json:"suggesterName"`
		Top           int    `json:"top"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	res, err := h.dp.SuggestDocuments(r.Context(), service, index, body.Search, body.SuggesterName, body.Top)
	if err != nil {
		dpCErr(w, err)

		return
	}

	value := make([]map[string]any, 0, len(res))

	for _, s := range res {
		doc := map[string]any{"@search.text": s.Text}
		for k, v := range s.Document {
			doc[k] = v
		}

		value = append(value, doc)
	}

	dpJSON(w, map[string]any{"value": value})
}

func (h *DataPlaneHandler) autocompleteDocs(w http.ResponseWriter, r *http.Request, service, index string) {
	var body struct {
		Search        string `json:"search"`
		SuggesterName string `json:"suggesterName"`
		Top           int    `json:"top"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	res, err := h.dp.AutocompleteDocuments(r.Context(), service, index, body.Search, body.SuggesterName, body.Top)
	if err != nil {
		dpCErr(w, err)

		return
	}

	value := make([]map[string]any, 0, len(res))
	for _, t := range res {
		value = append(value, map[string]any{"text": t, "queryPlusText": t})
	}

	dpJSON(w, map[string]any{"value": value})
}

func (h *DataPlaneHandler) countDocs(w http.ResponseWriter, r *http.Request, service, index string) {
	n, err := h.dp.CountDocuments(r.Context(), service, index)
	if err != nil {
		dpCErr(w, err)

		return
	}

	dpJSON(w, n)
}

func (h *DataPlaneHandler) getDoc(w http.ResponseWriter, r *http.Request, service, index, key string) {
	doc, err := h.dp.GetDocument(r.Context(), service, index, key)
	if err != nil {
		dpCErr(w, err)

		return
	}

	dpJSON(w, doc)
}
