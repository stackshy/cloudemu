package driver

import (
	"context"
	"encoding/json"
	"time"
)

// Document status values reported by BatchGetDocumentStatus.
const (
	DocStatusNotFound   = "NOT_FOUND"
	DocStatusProcessing = "PROCESSING"
	DocStatusIndexed    = "INDEXED"
	DocStatusUpdated    = "UPDATED"
	DocStatusFailed     = "FAILED"
	DocStatusDeleting   = "DELETING"
)

// Failure / error codes carried by per-document failures.
const (
	ErrCodeInvalidRequest = "InvalidRequest"
	ErrCodeInternalError  = "InternalError"
)

// Query result types and score confidence buckets.
const (
	ResultTypeDocument       = "DOCUMENT"
	ResultTypeQuestionAnswer = "QUESTION_ANSWER"
	ResultTypeAnswer         = "ANSWER"

	ScoreVeryHigh     = "VERY_HIGH"
	ScoreHigh         = "HIGH"
	ScoreMedium       = "MEDIUM"
	ScoreLow          = "LOW"
	ScoreNotAvailable = "NOT_AVAILABLE"
)

// S3Path locates an object in S3.
type S3Path struct {
	Bucket string
	Key    string
}

// DocumentAttributeValue is a typed document attribute value; exactly one member
// is set.
type DocumentAttributeValue struct {
	StringValue     *string
	StringListValue []string
	LongValue       *int64
	DateValue       *time.Time
}

// DocumentAttribute is a key/value pair attached to a document.
type DocumentAttribute struct {
	Key   string
	Value DocumentAttributeValue
}

// PutDocument is one document of a BatchPutDocument call. The access-control
// blocks round-trip as raw JSON.
type PutDocument struct {
	ID                            string
	Title                         string
	Blob                          []byte
	S3Path                        *S3Path
	ContentType                   string
	Attributes                    []DocumentAttribute
	AccessControlConfigurationID  string
	AccessControlList             json.RawMessage
	HierarchicalAccessControlList json.RawMessage
}

// PutDocumentsInput is the input to BatchPutDocument.
type PutDocumentsInput struct {
	IndexID                               string
	RoleArn                               string
	Documents                             []PutDocument
	CustomDocumentEnrichmentConfiguration json.RawMessage
}

// FailedDocument is a document a batch operation could not process.
type FailedDocument struct {
	ID           string
	DataSourceID string
	ErrorCode    string
	ErrorMessage string
}

// SyncJobMetricTarget maps a deletion to a data source sync job's metrics.
type SyncJobMetricTarget struct {
	DataSourceID        string
	DataSourceSyncJobID string
}

// DeleteDocumentsInput is the input to BatchDeleteDocument.
type DeleteDocumentsInput struct {
	IndexID      string
	DocumentIDs  []string
	MetricTarget *SyncJobMetricTarget
}

// DocumentInfo identifies a document whose status BatchGetDocumentStatus reports.
type DocumentInfo struct {
	DocumentID string
	Attributes []DocumentAttribute
}

// DocumentStatus is the indexing status of one document.
type DocumentStatus struct {
	DocumentID    string
	Status        string
	FailureCode   string
	FailureReason string
}

// DocumentStatusError is a document whose status could not be read.
type DocumentStatusError struct {
	DocumentID   string
	DataSourceID string
	ErrorCode    string
	ErrorMessage string
}

// DocumentStatusResult is the BatchGetDocumentStatus result.
type DocumentStatusResult struct {
	Statuses []DocumentStatus
	Errors   []DocumentStatusError
}

// AttributeFilter filters documents by attribute. Exactly one member is set. The
// comparison members take the single attribute they compare against.
type AttributeFilter struct {
	AndAll              []AttributeFilter
	OrAll               []AttributeFilter
	Not                 *AttributeFilter
	EqualsTo            *DocumentAttribute
	ContainsAll         *DocumentAttribute
	ContainsAny         *DocumentAttribute
	GreaterThan         *DocumentAttribute
	GreaterThanOrEquals *DocumentAttribute
	LessThan            *DocumentAttribute
	LessThanOrEquals    *DocumentAttribute
}

// Facet asks Query for value counts of one attribute.
type Facet struct {
	DocumentAttributeKey string
	MaxResults           int32
}

// SortingConfig sorts results by an attribute (SortOrder is ASC or DESC).
type SortingConfig struct {
	DocumentAttributeKey string
	SortOrder            string
}

// QueryInput is the input to Query.
type QueryInput struct {
	IndexID             string
	QueryText           string
	AttributeFilter     *AttributeFilter
	Facets              []Facet
	PageNumber          int32
	PageSize            int32
	RequestedAttributes []string
	ResultTypeFilter    string
	Sorting             []SortingConfig
}

// Highlight marks a matched span of text.
type Highlight struct {
	BeginOffset int32
	EndOffset   int32
	TopAnswer   bool
	Type        string
}

// TextWithHighlights is an excerpt with its highlighted spans.
type TextWithHighlights struct {
	Text       string
	Highlights []Highlight
}

// QueryResultItem is one Query result.
type QueryResultItem struct {
	ID         string
	DocumentID string
	Type       string
	Format     string
	Title      TextWithHighlights
	Excerpt    TextWithHighlights
	URI        string
	Attributes []DocumentAttribute
	Score      string
}

// FacetValueCount is the number of matching documents with one attribute value.
type FacetValueCount struct {
	Value DocumentAttributeValue
	Count int32
}

// FacetResult holds the counts for one requested facet.
type FacetResult struct {
	Key       string
	ValueType string
	Counts    []FacetValueCount
}

// QueryOutput is the Query result.
type QueryOutput struct {
	QueryID string
	Total   int32
	Items   []QueryResultItem
	Facets  []FacetResult
}

// RetrieveInput is the input to Retrieve.
type RetrieveInput struct {
	IndexID             string
	QueryText           string
	AttributeFilter     *AttributeFilter
	PageNumber          int32
	PageSize            int32
	RequestedAttributes []string
}

// RetrieveItem is one retrieved passage.
type RetrieveItem struct {
	ID         string
	DocumentID string
	Title      string
	URI        string
	Content    string
	Attributes []DocumentAttribute
	Score      string
}

// RetrieveOutput is the Retrieve result.
type RetrieveOutput struct {
	QueryID string
	Items   []RetrieveItem
}

// Documents is the optional document-ingestion and search capability: documents
// are held in the index, indexed synchronously, and searched with a simple
// term-matching engine (there is no semantic ranking).
type Documents interface {
	BatchPutDocument(ctx context.Context, in *PutDocumentsInput) ([]FailedDocument, error)
	BatchDeleteDocument(ctx context.Context, in *DeleteDocumentsInput) ([]FailedDocument, error)
	BatchGetDocumentStatus(ctx context.Context, indexID string, docs []DocumentInfo) (*DocumentStatusResult, error)
	Query(ctx context.Context, in *QueryInput) (*QueryOutput, error)
	Retrieve(ctx context.Context, in *RetrieveInput) (*RetrieveOutput, error)
}
