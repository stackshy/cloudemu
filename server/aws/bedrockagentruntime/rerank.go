package bedrockagentruntime

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type rerankDocumentJSON struct {
	Type         string `json:"type"`
	TextDocument *struct {
		Text string `json:"text"`
	} `json:"textDocument,omitempty"`
	JSONDocument json.RawMessage `json:"jsonDocument,omitempty"`
}

type rerankRequest struct {
	Queries []struct {
		TextQuery struct {
			Text string `json:"text"`
		} `json:"textQuery"`
	} `json:"queries"`
	Sources []struct {
		InlineDocumentSource rerankDocumentJSON `json:"inlineDocumentSource"`
	} `json:"sources"`
	RerankingConfiguration struct {
		BedrockRerankingConfiguration struct {
			NumberOfResults    *int32 `json:"numberOfResults"`
			ModelConfiguration struct {
				ModelArn string `json:"modelArn"`
			} `json:"modelConfiguration"`
		} `json:"bedrockRerankingConfiguration"`
	} `json:"rerankingConfiguration"`
	NextToken string `json:"nextToken"`
}

// serveRerank handles Rerank: POST /rerank.
func serveRerank(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	withCapability(h, w, "Rerank", func(c driver.Reranker) {
		var in rerankRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		cfg := in.RerankingConfiguration.BedrockRerankingConfiguration
		req := driver.RerankInput{ModelARN: cfg.ModelConfiguration.ModelArn, NumberOfResults: cfg.NumberOfResults, NextToken: in.NextToken}

		if len(in.Queries) > 0 {
			req.Query = in.Queries[0].TextQuery.Text
		}

		for _, s := range in.Sources {
			src := driver.RerankSource{JSON: s.InlineDocumentSource.JSONDocument}
			if s.InlineDocumentSource.TextDocument != nil {
				src.Text = s.InlineDocumentSource.TextDocument.Text
			}

			req.Sources = append(req.Sources, src)
		}

		results, next, err := c.Rerank(r.Context(), req)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]map[string]any, 0, len(results))
		for _, res := range results {
			out = append(out, map[string]any{
				"index": res.Index, "relevanceScore": res.Score, "document": in.Sources[res.Index].InlineDocumentSource,
			})
		}

		writeJSON(w, map[string]any{"results": out, "nextToken": omitEmpty(next)})
	})
}
