package driver

import (
	"context"
	"encoding/json"
)

// Document data-source types (ContentDataSourceType).
const (
	DocumentSourceCustom = "CUSTOM"
	DocumentSourceS3     = "S3"
)

// Document status values (DocumentStatus) the emulator reports.
const (
	DocumentIndexed  = "INDEXED"
	DocumentDeleting = "DELETING"
	DocumentNotFound = "NOT_FOUND"
)

// DocumentIdentifier names a document in a data source: CustomID for a CUSTOM
// data source, S3URI for an S3 one.
type DocumentIdentifier struct {
	DataSourceType string
	CustomID       string
	S3URI          string
}

// KnowledgeBaseDocument is a document to ingest. Content is the DocumentContent
// union and Metadata the optional DocumentMetadata, both kept verbatim.
type KnowledgeBaseDocument struct {
	Content  json.RawMessage
	Metadata json.RawMessage
}

// DocumentDetail is the status of one document in a data source.
type DocumentDetail struct {
	KnowledgeBaseID string
	DataSourceID    string
	Identifier      DocumentIdentifier
	Status          string
	StatusReason    string
	UpdatedAt       string
}

// KnowledgeBaseDocuments manages the documents of a data source directly.
//
// Only CUSTOM and S3 data sources qualify; no ingestion job is needed. Each call takes
// 1 to 10 documents. A document the data source does not hold reports
// NOT_FOUND.
type KnowledgeBaseDocuments interface {
	IngestKnowledgeBaseDocuments(ctx context.Context, kbID, dsID string, docs []KnowledgeBaseDocument) ([]DocumentDetail, error)
	GetKnowledgeBaseDocuments(ctx context.Context, kbID, dsID string, ids []DocumentIdentifier) ([]DocumentDetail, error)
	ListKnowledgeBaseDocuments(ctx context.Context, kbID, dsID string, page Page) ([]DocumentDetail, string, error)
	DeleteKnowledgeBaseDocuments(ctx context.Context, kbID, dsID string, ids []DocumentIdentifier) ([]DocumentDetail, error)
}
