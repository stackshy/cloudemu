// Package driver defines the interface for logging service implementations.
package driver

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// LogGroupConfig describes a log group to create.
type LogGroupConfig struct {
	Name          string
	RetentionDays int
	Tags          map[string]string

	// Scope records where the resource lives (Azure subscription/resource
	// group, GCP project). Zero for AWS and unscoped portable callers.
	Scope scope.Scope
}

// LogGroupInfo describes a log group.
type LogGroupInfo struct {
	Name          string
	ResourceID    string
	RetentionDays int
	CreatedAt     string
	StoredBytes   int64
	Tags          map[string]string
	Scope         scope.Scope
}

// LogStreamInfo describes a log stream within a log group.
type LogStreamInfo struct {
	Name       string
	CreatedAt  string
	FirstEvent string
	LastEvent  string
}

// LogEvent represents a single log entry.
type LogEvent struct {
	Timestamp time.Time
	// IngestionTime is the wall-clock time the event was received by the
	// service (set at PutLogEvents), distinct from the caller-supplied
	// Timestamp. Zero when unknown.
	IngestionTime time.Time
	Message       string
}

// LogQueryInput configures a log query operation.
type LogQueryInput struct {
	LogGroup  string
	LogStream string // empty means all streams
	StartTime time.Time
	EndTime   time.Time
	Pattern   string // filter pattern, empty means all
	Limit     int
}

// FilterLogEventsInput configures a filter log events operation.
type FilterLogEventsInput struct {
	LogGroup      string
	LogStream     string
	FilterPattern string
	StartTime     time.Time
	EndTime       time.Time
	Limit         int
}

// FilteredLogEvent represents a log event returned by FilterLogEvents.
type FilteredLogEvent struct {
	LogStream string
	Timestamp time.Time
	// IngestionTime is the wall-clock time the event was received by the
	// service, distinct from the caller-supplied Timestamp. Zero when unknown.
	IngestionTime time.Time
	Message       string
}

// MetricFilterConfig describes a metric filter to create.
type MetricFilterConfig struct {
	Name            string
	LogGroup        string
	FilterPattern   string
	MetricName      string
	MetricNamespace string
	MetricValue     string
	// DefaultValue is emitted for the metric when a log event does not match
	// the filter pattern. Nil means no default (the metric is simply not
	// published for non-matching events).
	DefaultValue *float64
	// Unit is the CloudWatch unit of the emitted metric (e.g. "Count").
	Unit string
	// Dimensions are the dimension name→value pairs attached to the metric.
	Dimensions map[string]string
}

// MetricFilterInfo describes a metric filter.
type MetricFilterInfo struct {
	Name            string
	LogGroup        string
	FilterPattern   string
	MetricName      string
	MetricNamespace string
	MetricValue     string
	// DefaultValue, Unit, and Dimensions mirror the MetricTransformation fields
	// (see MetricFilterConfig); zero/nil when the filter did not set them.
	DefaultValue *float64
	Unit         string
	Dimensions   map[string]string
	CreatedAt    time.Time
}

// SubscriptionFilterConfig describes a subscription filter to create or update.
// A subscription filter streams matching log events (as they are ingested via
// PutLogEvents) to a destination — a Lambda function, Kinesis stream, or
// Firehose delivery stream identified by DestinationARN.
type SubscriptionFilterConfig struct {
	Name           string
	LogGroup       string
	FilterPattern  string
	DestinationARN string
	RoleARN        string
	// Distribution controls how Kinesis destinations spread data across shards
	// ("Random" or "ByLogStream"); ignored for other destination types.
	Distribution string
}

// SubscriptionFilterInfo describes a subscription filter.
type SubscriptionFilterInfo struct {
	Name           string
	LogGroup       string
	FilterPattern  string
	DestinationARN string
	RoleARN        string
	Distribution   string
	CreatedAt      time.Time
}

// Logging is the interface that logging provider implementations must satisfy.
type Logging interface {
	CreateLogGroup(ctx context.Context, config LogGroupConfig) (*LogGroupInfo, error)

	// UpdateLogGroup replaces the mutable fields (retention, tags) of an
	// existing log group, mirroring ARM CreateOrUpdate-on-existing.
	UpdateLogGroup(ctx context.Context, config LogGroupConfig) (*LogGroupInfo, error)
	DeleteLogGroup(ctx context.Context, name string) error
	GetLogGroup(ctx context.Context, name string) (*LogGroupInfo, error)
	ListLogGroups(ctx context.Context, filter scope.Scope) ([]LogGroupInfo, error)

	CreateLogStream(ctx context.Context, logGroup, streamName string) (*LogStreamInfo, error)
	DeleteLogStream(ctx context.Context, logGroup, streamName string) error
	ListLogStreams(ctx context.Context, logGroup string) ([]LogStreamInfo, error)

	PutLogEvents(ctx context.Context, logGroup, streamName string, events []LogEvent) error
	GetLogEvents(ctx context.Context, input *LogQueryInput) ([]LogEvent, error)

	FilterLogEvents(ctx context.Context, input *FilterLogEventsInput) ([]FilteredLogEvent, error)
	PutMetricFilter(ctx context.Context, config *MetricFilterConfig) error
	DeleteMetricFilter(ctx context.Context, logGroup, filterName string) error
	DescribeMetricFilters(ctx context.Context, logGroup string) ([]MetricFilterInfo, error)

	PutSubscriptionFilter(ctx context.Context, config *SubscriptionFilterConfig) error
	DeleteSubscriptionFilter(ctx context.Context, logGroup, filterName string) error
	DescribeSubscriptionFilters(ctx context.Context, logGroup string) ([]SubscriptionFilterInfo, error)
}
