package sagemaker

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

type wireSearchFilter struct {
	Name     string `json:"Name"`
	Operator string `json:"Operator"`
	Value    string `json:"Value"`
}

type wireSearchNested struct {
	NestedPropertyName string             `json:"NestedPropertyName"`
	Filters            []wireSearchFilter `json:"Filters"`
}

type wireSearchExpression struct {
	Filters        []wireSearchFilter     `json:"Filters"`
	NestedFilters  []wireSearchNested     `json:"NestedFilters"`
	SubExpressions []wireSearchExpression `json:"SubExpressions"`
	Operator       string                 `json:"Operator"`
}

func toSearchExpression(in *wireSearchExpression) *driver.SearchExpression {
	if in == nil {
		return nil
	}

	out := &driver.SearchExpression{Operator: in.Operator}

	for _, f := range in.Filters {
		out.Filters = append(out.Filters, driver.SearchFilter{Name: f.Name, Operator: f.Operator, Value: f.Value})
	}

	for _, n := range in.NestedFilters {
		nested := driver.SearchNested{PropertyName: n.NestedPropertyName}
		for _, f := range n.Filters {
			nested.Filters = append(nested.Filters, driver.SearchFilter{Name: f.Name, Operator: f.Operator, Value: f.Value})
		}

		out.NestedFilters = append(out.NestedFilters, nested)
	}

	for i := range in.SubExpressions {
		out.SubExpressions = append(out.SubExpressions, *toSearchExpression(&in.SubExpressions[i]))
	}

	return out
}

// searchRecordKeys maps a search Resource to its SearchRecord member.
var searchRecordKeys = map[string]string{ //nolint:gochecknoglobals // static table
	driver.SearchExperimentTrial: "Trial", driver.SearchExperimentTrialComponent: "TrialComponent",
}

// timestampsToEpoch converts the RFC3339 string values of members whose name
// ends in "Time" into epoch seconds, recursively.
func timestampsToEpoch(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))

		for k, val := range t {
			if s, ok := val.(string); ok && strings.HasSuffix(k, "Time") {
				out[k] = epoch(s)

				continue
			}

			out[k] = timestampsToEpoch(val)
		}

		return out
	default:
		return v
	}
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Resource         string                `json:"Resource"`
		SearchExpression *wireSearchExpression `json:"SearchExpression"`
		SortBy           string                `json:"SortBy"`
		SortOrder        string                `json:"SortOrder"`
		MaxResults       *int                  `json:"MaxResults"`
		NextToken        string                `json:"NextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	sr := driver.SearchRequest{
		Resource: req.Resource, Expression: toSearchExpression(req.SearchExpression), SortBy: req.SortBy,
		SortOrder: req.SortOrder, NextToken: req.NextToken,
	}

	if req.MaxResults != nil {
		if *req.MaxResults < 1 {
			sr.MaxResults = -1 // rejected by the provider's range check
		} else {
			sr.MaxResults = *req.MaxResults
		}
	}

	res, err := h.svc.Search(r.Context(), sr)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	results := make([]map[string]any, 0, len(res.Hits))

	for _, hit := range res.Hits {
		key := hit.Resource
		if k, ok := searchRecordKeys[key]; ok {
			key = k
		}

		results = append(results, map[string]any{key: timestampsToEpoch(hit.Record)})
	}

	resp := map[string]any{"Results": results, "TotalHits": map[string]any{"Value": res.TotalHits, "Relation": "EqualTo"}}
	if res.NextToken != "" {
		resp["NextToken"] = res.NextToken
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) getSearchSuggestions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Resource        string `json:"Resource"`
		SuggestionQuery *struct {
			PropertyNameQuery *struct {
				PropertyNameHint string `json:"PropertyNameHint"`
			} `json:"PropertyNameQuery"`
		} `json:"SuggestionQuery"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	hint := ""
	if req.SuggestionQuery != nil && req.SuggestionQuery.PropertyNameQuery != nil {
		hint = req.SuggestionQuery.PropertyNameQuery.PropertyNameHint
	}

	names, err := h.svc.GetSearchSuggestions(r.Context(), req.Resource, hint)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	suggestions := make([]map[string]any, 0, len(names))
	for _, n := range names {
		suggestions = append(suggestions, map[string]any{"PropertyName": n})
	}

	wire.WriteJSON(w, map[string]any{"PropertyNameSuggestions": suggestions})
}
