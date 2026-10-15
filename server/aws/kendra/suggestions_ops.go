package kendra

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// registerSuggestionRoutes wires the query suggestions operations.
func (h *Handler) registerSuggestionRoutes(d driver.QuerySuggestions) {
	h.routes["DescribeQuerySuggestionsConfig"] = handle(h,
		func(ctx context.Context, req *indexOnlyRequest) (describeSuggestionsConfigResponse, error) {
			c, err := d.DescribeQuerySuggestionsConfig(ctx, req.IndexID)
			if err != nil {
				return describeSuggestionsConfigResponse{}, err
			}

			return suggestionsConfigToWire(c), nil
		})
	h.routes["UpdateQuerySuggestionsConfig"] = handle(h,
		func(ctx context.Context, req *updateSuggestionsConfigRequest) (struct{}, error) {
			return ack(d.UpdateQuerySuggestionsConfig(ctx, &driver.UpdateSuggestionsConfigInput{
				IndexID: req.IndexID, Mode: req.Mode, QueryLogLookBackWindowInDays: req.QueryLogLookBackWindowInDays,
				IncludeQueriesWithoutUserInformation: req.IncludeQueriesWithoutUserInformation,
				MinimumNumberOfQueryingUsers:         req.MinimumNumberOfQueryingUsers, MinimumQueryCount: req.MinimumQueryCount,
				AttributeSuggestionsConfig: req.AttributeSuggestionsConfig,
			}))
		})
	h.routes["ClearQuerySuggestions"] = handle(h, func(ctx context.Context, req *indexOnlyRequest) (struct{}, error) {
		return ack(d.ClearQuerySuggestions(ctx, req.IndexID))
	})
	h.routes["GetQuerySuggestions"] = handle(h, func(ctx context.Context, req *getSuggestionsRequest) (getSuggestionsResponse, error) {
		res, err := d.GetQuerySuggestions(ctx, &driver.GetSuggestionsInput{
			IndexID: req.IndexID, QueryText: req.QueryText, MaxSuggestionsCount: req.MaxSuggestionsCount,
			SuggestionTypes: req.SuggestionTypes,
		})
		if err != nil {
			return getSuggestionsResponse{}, err
		}

		return suggestionsToWire(res), nil
	})
}

func suggestionsConfigToWire(c *driver.SuggestionsConfig) describeSuggestionsConfigResponse {
	out := describeSuggestionsConfigResponse{
		Mode: c.Mode, Status: c.Status, QueryLogLookBackWindowInDays: c.QueryLogLookBackWindowInDays,
		IncludeQueriesWithoutUserInformation: c.IncludeQueriesWithoutUserInformation,
		MinimumNumberOfQueryingUsers:         c.MinimumNumberOfQueryingUsers, MinimumQueryCount: c.MinimumQueryCount,
		AttributeSuggestionsConfig: c.AttributeSuggestionsConfig, TotalSuggestionsCount: c.TotalSuggestionsCount,
	}

	if !c.LastSuggestionsBuildTime.IsZero() {
		out.LastSuggestionsBuildTime = epochSeconds(c.LastSuggestionsBuildTime)
	}

	if !c.LastClearTime.IsZero() {
		out.LastClearTime = epochSeconds(c.LastClearTime)
	}

	return out
}

func suggestionsToWire(res *driver.SuggestionsResult) getSuggestionsResponse {
	out := getSuggestionsResponse{QuerySuggestionsID: res.QuerySuggestionsID, Suggestions: make([]suggestionJSON, len(res.Suggestions))}

	for i, s := range res.Suggestions {
		var sj suggestionJSON

		sj.ID = s.ID
		sj.Value.Text.Text = s.Text
		sj.Value.Text.Highlights = make([]suggestionHighlightJSON, len(s.Highlights))

		for j, hl := range s.Highlights {
			sj.Value.Text.Highlights[j] = suggestionHighlightJSON{BeginOffset: hl.BeginOffset, EndOffset: hl.EndOffset}
		}

		out.Suggestions[i] = sj
	}

	return out
}

type getSuggestionsResponse struct {
	QuerySuggestionsID string           `json:"QuerySuggestionsId"`
	Suggestions        []suggestionJSON `json:"Suggestions"`
}

type indexOnlyRequest struct {
	IndexID string `json:"IndexId"`
}

type describeSuggestionsConfigResponse struct {
	Mode                                 string  `json:"Mode"`
	Status                               string  `json:"Status"`
	QueryLogLookBackWindowInDays         int32   `json:"QueryLogLookBackWindowInDays"`
	IncludeQueriesWithoutUserInformation bool    `json:"IncludeQueriesWithoutUserInformation"`
	MinimumNumberOfQueryingUsers         int32   `json:"MinimumNumberOfQueryingUsers"`
	MinimumQueryCount                    int32   `json:"MinimumQueryCount"`
	AttributeSuggestionsConfig           rawJSON `json:"AttributeSuggestionsConfig,omitempty"`
	LastSuggestionsBuildTime             int64   `json:"LastSuggestionsBuildTime,omitempty"`
	LastClearTime                        int64   `json:"LastClearTime,omitempty"`
	TotalSuggestionsCount                int32   `json:"TotalSuggestionsCount"`
}

type updateSuggestionsConfigRequest struct {
	IndexID                              string  `json:"IndexId"`
	Mode                                 *string `json:"Mode"`
	QueryLogLookBackWindowInDays         *int32  `json:"QueryLogLookBackWindowInDays"`
	IncludeQueriesWithoutUserInformation *bool   `json:"IncludeQueriesWithoutUserInformation"`
	MinimumNumberOfQueryingUsers         *int32  `json:"MinimumNumberOfQueryingUsers"`
	MinimumQueryCount                    *int32  `json:"MinimumQueryCount"`
	AttributeSuggestionsConfig           rawJSON `json:"AttributeSuggestionsConfig"`
}

type getSuggestionsRequest struct {
	IndexID             string   `json:"IndexId"`
	QueryText           string   `json:"QueryText"`
	MaxSuggestionsCount int32    `json:"MaxSuggestionsCount"`
	SuggestionTypes     []string `json:"SuggestionTypes"`
}

type suggestionHighlightJSON struct {
	BeginOffset int32 `json:"BeginOffset"`
	EndOffset   int32 `json:"EndOffset"`
}

type suggestionJSON struct {
	ID    string `json:"Id"`
	Value struct {
		Text struct {
			Text       string                    `json:"Text"`
			Highlights []suggestionHighlightJSON `json:"Highlights"`
		} `json:"Text"`
	} `json:"Value"`
}
