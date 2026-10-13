package driver

import "context"

// IngestionJobStatistics are the document counters of an ingestion job.
type IngestionJobStatistics struct {
	NumberOfDocumentsScanned          int64
	NumberOfMetadataDocumentsScanned  int64
	NumberOfNewDocumentsIndexed       int64
	NumberOfModifiedDocumentsIndexed  int64
	NumberOfMetadataDocumentsModified int64
	NumberOfDocumentsDeleted          int64
	NumberOfDocumentsFailed           int64
}

// IngestionJobFilter keeps the jobs whose Attribute (STATUS) matches one of
// Values under Operator (EQ).
type IngestionJobFilter struct {
	Attribute string
	Operator  string
	Values    []string
}

// IngestionJobSort orders ListIngestionJobs by Attribute (STATUS or
// STARTED_AT) in Order (ASCENDING or DESCENDING).
type IngestionJobSort struct {
	Attribute string
	Order     string
}

// ListIngestionJobsInput selects one page of a data source's ingestion jobs.
type ListIngestionJobsInput struct {
	KnowledgeBaseID string
	DataSourceID    string
	Filters         []IngestionJobFilter
	SortBy          *IngestionJobSort
	Page            Page
}

// IngestionJobs reads, lists and stops data-source ingestion jobs.
//
// StartIngestionJob starts them. A job runs until it settles (immediately unless async settling is on)
// and can be stopped only while it runs.
type IngestionJobs interface {
	GetIngestionJob(ctx context.Context, kbID, dsID, jobID string) (*IngestionJob, error)
	ListIngestionJobs(ctx context.Context, in ListIngestionJobsInput) ([]IngestionJob, string, error)
	StopIngestionJob(ctx context.Context, kbID, dsID, jobID string) (*IngestionJob, error)
}
