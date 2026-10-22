package driver

import (
	"context"
	"time"
)

// Log group classes CloudWatch Logs accepts on CreateLogGroup. A group's class
// is fixed at creation; no API changes it afterwards.
const (
	LogGroupClassStandard         = "STANDARD"
	LogGroupClassInfrequentAccess = "INFREQUENT_ACCESS"
	LogGroupClassDelivery         = "DELIVERY"
)

// AWSLogGroupOptions carries the CreateLogGroup fields that only CloudWatch
// Logs has: the KMS key that encrypts ingested events and the log group class.
type AWSLogGroupOptions struct {
	KmsKeyID      string
	LogGroupClass string
}

// AWSLogGroup is a log group with its CloudWatch Logs-only attributes.
type AWSLogGroup struct {
	LogGroupInfo
	KmsKeyID      string
	LogGroupClass string
}

// LogGroupTagFilter matches log groups that carry Key with any of Values (or
// any value when Values is empty).
type LogGroupTagFilter struct {
	Key    string
	Values []string
}

// ListLogGroupsFilter narrows ListAWSLogGroups. Every non-zero field must
// match. NamePattern uses the ListLogGroups logGroupNamePattern syntax.
type ListLogGroupsFilter struct {
	NamePattern   string
	LogGroupClass string
	Tags          []LogGroupTagFilter
}

// RejectedLogEventsInfo reports which events of a PutLogEvents batch were
// dropped. Each index is nil when no event fell in that category. The "end"
// indexes are exclusive ends of a rejected prefix; TooNewLogEventStartIndex is
// the first index of a rejected suffix.
type RejectedLogEventsInfo struct {
	TooOldLogEventEndIndex   *int
	ExpiredLogEventEndIndex  *int
	TooNewLogEventStartIndex *int
}

// PutLogEventsResult is the CloudWatch Logs PutLogEvents response.
type PutLogEventsResult struct {
	NextSequenceToken string
	// Rejected is nil when every event in the batch was accepted.
	Rejected *RejectedLogEventsInfo
}

// MetricFilterMatch is one TestMetricFilter result: a sample message the
// pattern matched, its 0-based position, and the values the pattern extracts.
type MetricFilterMatch struct {
	EventNumber     int64
	EventMessage    string
	ExtractedValues map[string]string
}

// DescribeMetricFiltersInput narrows DescribeAWSMetricFilters. LogGroup is
// optional (empty lists every log group); FilterNamePrefix only applies with
// a LogGroup; MetricName and MetricNamespace must be given together.
type DescribeMetricFiltersInput struct {
	LogGroup         string
	FilterNamePrefix string
	MetricName       string
	MetricNamespace  string
}

// Logs Insights query statuses.
const (
	QueryStatusRunning   = "Running"
	QueryStatusComplete  = "Complete"
	QueryStatusCancelled = "Cancelled" //nolint:misspell // the API's status value is spelled this way.
	QueryStatusTimeout   = "Timeout"
)

// StartQueryInput is a Logs Insights StartQuery request. Exactly one of
// LogGroupName, LogGroupNames or LogGroupIdentifiers (names or ARNs) is set.
type StartQueryInput struct {
	LogGroupName        string
	LogGroupNames       []string
	LogGroupIdentifiers []string
	StartTime, EndTime  time.Time
	QueryString         string
	QueryLanguage       string
	// Limit caps the rows returned; zero means the default.
	Limit int
}

// QueryResultField is one field/value pair of a query result row.
type QueryResultField struct{ Field, Value string }

// QueryStatistics are a query's scan and match counts.
type QueryStatistics struct {
	RecordsMatched   float64
	RecordsScanned   float64
	BytesScanned     float64
	LogGroupsScanned float64
}

// QueryResults is a GetQueryResults response.
type QueryResults struct {
	Status        string
	QueryLanguage string
	EncryptionKey string
	Results       [][]QueryResultField
	Statistics    QueryStatistics
}

// QueryInfo describes a query for DescribeQueries.
type QueryInfo struct {
	QueryID       string
	QueryString   string
	QueryLanguage string
	Status        string
	LogGroupName  string
	CreateTime    time.Time
	// QueryDuration is how long the query ran, in milliseconds.
	QueryDuration int64
	BytesScanned  float64
}

// DescribeQueriesInput narrows DescribeQueries; empty fields match all.
type DescribeQueriesInput struct {
	LogGroupName, Status, QueryLanguage string
}

// LogGroupField is a field GetLogGroupFields found and the share of events
// (0-100) that carry it.
type LogGroupField struct {
	Name    string
	Percent int
}

// MalformedQueryError is a query string that does not compile. Start and End
// are the character offsets of the offending text.
type MalformedQueryError struct {
	Message    string
	Start, End int
}

func (e *MalformedQueryError) Error() string { return e.Message }

// AWSLogging is an optional interface implemented by the AWS CloudWatch Logs
// backend for operations with no cross-provider equivalent. The CloudWatch
// Logs wire handler type-asserts for it; Azure, GCP and OCI backends do not
// implement it.
type AWSLogging interface {
	CreateAWSLogGroup(ctx context.Context, cfg *LogGroupConfig, opts AWSLogGroupOptions) (*AWSLogGroup, error)
	ListAWSLogGroups(ctx context.Context, filter ListLogGroupsFilter) ([]AWSLogGroup, error)

	PutRetentionPolicy(ctx context.Context, logGroup string, retentionDays int) error
	DeleteRetentionPolicy(ctx context.Context, logGroup string) error

	PutLogEventsWithResult(ctx context.Context, logGroup, stream string, events []LogEvent) (*PutLogEventsResult, error)

	// AssociateKmsKey and DisassociateKmsKey target exactly one of a log group
	// name or a resource identifier (a log group ARN, or the account's
	// query-result ARN).
	AssociateKmsKey(ctx context.Context, logGroup, resourceIdentifier, kmsKeyID string) error
	DisassociateKmsKey(ctx context.Context, logGroup, resourceIdentifier string) error

	// DescribeAWSMetricFilters lists metric filters, sorted by filter name.
	DescribeAWSMetricFilters(ctx context.Context, input DescribeMetricFiltersInput) ([]MetricFilterInfo, error)

	// Logs Insights. A query is Running until its first GetQueryResults,
	// which runs it. StartQuery returns a *MalformedQueryError for a query
	// string that does not compile.
	StartQuery(ctx context.Context, input *StartQueryInput) (string, error)
	GetQueryResults(ctx context.Context, queryID string) (*QueryResults, error)
	StopQuery(ctx context.Context, queryID string) error
	DescribeQueries(ctx context.Context, input DescribeQueriesInput) ([]QueryInfo, error)
	GetLogRecord(ctx context.Context, logRecordPointer string) (map[string]string, error)
	// GetLogGroupFields samples the 8 minutes either side of at, or the last
	// 15 minutes when at is zero.
	GetLogGroupFields(ctx context.Context, logGroup string, at time.Time) ([]LogGroupField, error)

	// TestMetricFilter runs a filter pattern over sample log messages.
	TestMetricFilter(ctx context.Context, filterPattern string, logEventMessages []string) ([]MetricFilterMatch, error)
}
