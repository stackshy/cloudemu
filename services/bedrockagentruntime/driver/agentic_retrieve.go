package driver

import "context"

// AgenticRetrieveInput is the request for AgenticRetrieveStream: the
// conversation and the knowledge bases to search.
type AgenticRetrieveInput struct {
	Messages         []string
	KnowledgeBaseIDs []string
	GenerateResponse bool
	NextToken        string
}

// AgenticRetrieveStep is one progress step the stream reports.
type AgenticRetrieveStep struct {
	ID      string
	Step    string
	Status  string
	Message string
}

// AgenticRetrieveResult is the outcome of an agentic retrieval.
type AgenticRetrieveResult struct {
	Steps     []AgenticRetrieveStep
	Results   []RetrievalResult
	Answer    string
	NextToken string
}

// AgenticRetriever plans and runs retrievals over knowledge bases.
type AgenticRetriever interface {
	AgenticRetrieveStream(ctx context.Context, in AgenticRetrieveInput) (*AgenticRetrieveResult, error)
}
