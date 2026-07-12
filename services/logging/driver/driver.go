// Package driver defines the interface for logging service implementations.
package driver

import (
	"context"
	"time"
)

// LogGroupConfig describes a log group to create.
type LogGroupConfig struct {
	Name          string
	RetentionDays int
	Tags          map[string]string
}

// LogGroupInfo describes a log group.
type LogGroupInfo struct {
	Name          string
	ResourceID    string
	RetentionDays int
	CreatedAt     string
	StoredBytes   int64
	Tags          map[string]string
}

// LogStreamInfo describes a log stream within a log group.
type LogStreamInfo struct {
	Name      string
	CreatedAt string
	LastEvent string
}

// LogEvent represents a single log entry.
type LogEvent struct {
	Timestamp time.Time
	Message   string
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
	Message   string
}

// MetricFilterConfig describes a metric filter to create.
type MetricFilterConfig struct {
	Name            string
	LogGroup        string
	FilterPattern   string
	MetricName      string
	MetricNamespace string
	MetricValue     string
}

// MetricFilterInfo describes a metric filter.
type MetricFilterInfo struct {
	Name            string
	LogGroup        string
	FilterPattern   string
	MetricName      string
	MetricNamespace string
	MetricValue     string
	CreatedAt       time.Time
}

// Logging is the interface that logging provider implementations must satisfy.
type Logging interface {
	CreateLogGroup(ctx context.Context, config LogGroupConfig) (*LogGroupInfo, error)
	DeleteLogGroup(ctx context.Context, name string) error
	GetLogGroup(ctx context.Context, name string) (*LogGroupInfo, error)
	ListLogGroups(ctx context.Context) ([]LogGroupInfo, error)

	CreateLogStream(ctx context.Context, logGroup, streamName string) (*LogStreamInfo, error)
	DeleteLogStream(ctx context.Context, logGroup, streamName string) error
	ListLogStreams(ctx context.Context, logGroup string) ([]LogStreamInfo, error)

	PutLogEvents(ctx context.Context, logGroup, streamName string, events []LogEvent) error
	GetLogEvents(ctx context.Context, input *LogQueryInput) ([]LogEvent, error)

	FilterLogEvents(ctx context.Context, input *FilterLogEventsInput) ([]FilteredLogEvent, error)
	PutMetricFilter(ctx context.Context, config *MetricFilterConfig) error
	DeleteMetricFilter(ctx context.Context, logGroup, filterName string) error
	DescribeMetricFilters(ctx context.Context, logGroup string) ([]MetricFilterInfo, error)
}
