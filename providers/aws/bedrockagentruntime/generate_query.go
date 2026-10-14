package bedrockagentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Query generation constants.
const (
	generatedQueryType  = "REDSHIFT_SQL"
	knowledgeBaseSQL    = "SQL"
	maxQueryInputLength = 20000
)

// GenerateQuery turns a question into SQL for a structured (SQL) knowledge
// base. There is no model to translate with, so the query is a fixed SELECT
// over the knowledge base with the question as a comment.
func (m *Mock) GenerateQuery(ctx context.Context, text, knowledgeBaseARN string) ([]driver.GeneratedQuery, error) {
	var v violations

	v.required("queryGenerationInput.text", text == "")
	v.maxLen("queryGenerationInput.text", text, maxQueryInputLength)
	v.required("transformationConfiguration.textToSqlConfiguration.knowledgeBaseConfiguration.knowledgeBaseArn",
		knowledgeBaseARN == "")

	if err := v.err(); err != nil {
		return nil, err
	}

	table := "knowledge_base"

	if m.cp != nil {
		kb, err := m.cp.GetKnowledgeBase(ctx, knowledgeBaseID(knowledgeBaseARN))
		if err != nil {
			return nil, err
		}

		var cfg struct {
			Type string `json:"type"`
		}

		_ = json.Unmarshal(kb.KnowledgeBaseConfiguration, &cfg)

		if cfg.Type != knowledgeBaseSQL {
			return nil, errors.Newf(errors.InvalidArgument,
				"Knowledge base %s is of type %s. GenerateQuery needs a knowledge base of type SQL.", kb.ID, cfg.Type)
		}

		table = kb.Name
	}

	sql := fmt.Sprintf("-- %s\nSELECT * FROM %q LIMIT 100;", strings.ReplaceAll(text, "\n", " "), table)

	return []driver.GeneratedQuery{{Type: generatedQueryType, SQL: sql}}, nil
}
