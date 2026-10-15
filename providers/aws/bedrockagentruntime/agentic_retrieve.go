package bedrockagentruntime

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// Agentic retrieval steps and statuses (AgenticRetrieveStep, -Status).
const (
	stepPlanning     = "Planning"
	stepRetrieval    = "Retrieval"
	statusSucceeded  = "SUCCEEDED"
	agenticPageLimit = 10
)

// AgenticRetrieveStream searches the given knowledge bases for the last user
// message and, when asked, answers from the results. Planning is a single
// step: every knowledge base is searched once.
func (m *Mock) AgenticRetrieveStream(ctx context.Context, in driver.AgenticRetrieveInput) (*driver.AgenticRetrieveResult, error) {
	var v violations

	v.required("messages", len(in.Messages) == 0)
	v.required("retrievers", len(in.KnowledgeBaseIDs) == 0)

	if err := v.err(); err != nil {
		return nil, err
	}

	query := in.Messages[len(in.Messages)-1]
	out := &driver.AgenticRetrieveResult{Steps: []driver.AgenticRetrieveStep{
		{ID: "1", Step: stepPlanning, Status: statusSucceeded, Message: "Search every knowledge base for: " + query},
	}}

	var found []driver.RetrievalResult

	for _, id := range in.KnowledgeBaseIDs {
		res, err := m.Retrieve(ctx, driver.RetrieveInput{KnowledgeBaseID: id, QueryText: query})
		if err != nil {
			return nil, err
		}

		found = append(found, res.Results...)
	}

	results, next, err := paginate(found, driver.Page{NextToken: in.NextToken}, agenticPageLimit)
	if err != nil {
		return nil, err
	}

	out.Steps = append(out.Steps, driver.AgenticRetrieveStep{
		ID: "2", Step: stepRetrieval, Status: statusSucceeded,
		Message: "Searched " + strings.Join(in.KnowledgeBaseIDs, ", "),
	})
	out.Results, out.NextToken = results, next

	if in.GenerateResponse {
		out.Answer = agenticAnswer(query, results)
	}

	return out, nil
}

// agenticAnswer answers from the best result; with no results it says so.
func agenticAnswer(query string, results []driver.RetrievalResult) string {
	if len(results) == 0 {
		return "No results were found for: " + query
	}

	return "Based on the retrieved results: " + results[0].Text
}
