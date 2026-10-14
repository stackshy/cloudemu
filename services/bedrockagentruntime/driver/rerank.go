package driver

import (
	"context"
	"encoding/json"
)

// RerankSource is one document to rank: Text for a TEXT document, JSON for a
// JSON one.
type RerankSource struct {
	Text string
	JSON json.RawMessage
}

// RerankInput is the request for Rerank.
type RerankInput struct {
	Query           string
	Sources         []RerankSource
	ModelARN        string
	NumberOfResults *int32
	NextToken       string
}

// RerankResult is a ranked source: Index points into the request's sources.
type RerankResult struct {
	Index  int32
	Score  float64
	Source RerankSource
}

// Reranker orders documents by relevance to a query.
type Reranker interface {
	Rerank(ctx context.Context, in RerankInput) ([]RerankResult, string, error)
}
