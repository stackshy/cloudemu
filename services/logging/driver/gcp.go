package driver

import (
	"context"
	"time"
)

// LogSink is a Cloud Logging export sink (logging.projects.sinks). It selects
// entries with a filter and forwards them to a destination (a GCS bucket, BigQuery
// dataset, or Pub/Sub topic). It has no cross-provider equivalent, so it lives on
// the optional GCPLogging interface rather than the portable Logging interface.
type LogSink struct {
	Name            string
	Destination     string
	Filter          string
	Description     string
	Disabled        bool
	WriterIdentity  string
	IncludeChildren bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// LogBasedMetric is a Cloud Logging log-based metric (logging.projects.metrics):
// a counter/distribution metric whose value is derived from matching log entries.
type LogBasedMetric struct {
	Name           string
	Description    string
	Filter         string
	ValueExtractor string
	MetricKind     string
	ValueType      string
	Unit           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// GCPLogging is an optional interface implemented by the GCP logging backend for
// Cloud Logging resource surfaces (export sinks, log-based metrics) that have no
// portable, cross-provider equivalent. The Cloud Logging wire handler type-asserts
// for it; AWS and Azure backends do not implement it.
type GCPLogging interface {
	CreateSink(ctx context.Context, project string, sink *LogSink) (*LogSink, error)
	GetSink(ctx context.Context, project, name string) (*LogSink, error)
	ListSinks(ctx context.Context, project string) ([]LogSink, error)
	UpdateSink(ctx context.Context, project string, sink *LogSink) (*LogSink, error)
	DeleteSink(ctx context.Context, project, name string) error

	CreateLogMetric(ctx context.Context, project string, metric *LogBasedMetric) (*LogBasedMetric, error)
	GetLogMetric(ctx context.Context, project, name string) (*LogBasedMetric, error)
	ListLogMetrics(ctx context.Context, project string) ([]LogBasedMetric, error)
	UpdateLogMetric(ctx context.Context, project string, metric *LogBasedMetric) (*LogBasedMetric, error)
	DeleteLogMetric(ctx context.Context, project, name string) error
}
