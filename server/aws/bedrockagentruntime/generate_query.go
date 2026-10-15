package bedrockagentruntime

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type generateQueryRequest struct {
	QueryGenerationInput struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"queryGenerationInput"`
	TransformationConfiguration struct {
		Mode                   string `json:"mode"`
		TextToSQLConfiguration *struct {
			Type                       string `json:"type"`
			KnowledgeBaseConfiguration *struct {
				KnowledgeBaseArn string `json:"knowledgeBaseArn"`
			} `json:"knowledgeBaseConfiguration"`
		} `json:"textToSqlConfiguration"`
	} `json:"transformationConfiguration"`
}

// serveGenerateQuery handles GenerateQuery: POST /generateQuery.
func serveGenerateQuery(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	withCapability(h, w, "GenerateQuery", func(c driver.QueryGenerator) {
		var in generateQueryRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.TransformationConfiguration.Mode != "TEXT_TO_SQL" {
			writeError(w, http.StatusBadRequest, "ValidationException",
				"1 validation error detected: Value '"+in.TransformationConfiguration.Mode+
					"' at 'transformationConfiguration.mode' failed to satisfy constraint: Member must satisfy enum value set: [TEXT_TO_SQL]")

			return
		}

		arn := ""
		if t := in.TransformationConfiguration.TextToSQLConfiguration; t != nil && t.KnowledgeBaseConfiguration != nil {
			arn = t.KnowledgeBaseConfiguration.KnowledgeBaseArn
		}

		queries, err := c.GenerateQuery(r.Context(), in.QueryGenerationInput.Text, arn)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]map[string]string, 0, len(queries))
		for _, q := range queries {
			out = append(out, map[string]string{"type": q.Type, "sql": q.SQL})
		}

		writeJSON(w, map[string]any{"queries": out})
	})
}
