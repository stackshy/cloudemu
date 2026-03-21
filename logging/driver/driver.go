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
	GetLogEvents(ctx context.Context, input LogQueryInput) ([]LogEvent, error)
}
