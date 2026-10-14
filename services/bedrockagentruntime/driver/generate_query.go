package driver

import "context"

// GeneratedQuery is a query generated from natural language (REDSHIFT_SQL).
type GeneratedQuery struct {
	Type string
	SQL  string
}

// QueryGenerator turns natural language into SQL (TEXT_TO_SQL).
//
// The query targets a structured (SQL) knowledge base.
type QueryGenerator interface {
	GenerateQuery(ctx context.Context, text, knowledgeBaseARN string) ([]GeneratedQuery, error)
}
