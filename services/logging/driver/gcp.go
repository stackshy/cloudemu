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

// LogBucket is a Cloud Logging bucket (logging.projects.locations.buckets): a
// retention container that log entries are stored in, either directly or via a
// sink's destination. Every project auto-provisions two special buckets in the
// "global" location — _Default and _Required — which can never be deleted;
// _Required additionally can never be modified at all. A bucket may be locked,
// which is a one-way transition: once locked, its retention period can only be
// increased (never reduced) and it cannot be deleted or unlocked. It has no
// cross-provider equivalent, so it lives on the optional GCPLogging interface
// rather than the portable Logging interface.
type LogBucket struct {
	Name           string
	Location       string
	Description    string
	RetentionDays  int32
	Locked         bool
	LifecycleState string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// BucketUpdate describes a partial update (PATCH) to a log bucket's mutable
// fields. Each Set* flag mirrors whether the wire layer's updateMask (or, for a
// mask-less caller, a presence heuristic) named that field, so a field the
// caller did not intend to touch is left unchanged rather than reset to its
// Go zero value.
type BucketUpdate struct {
	Description      string
	SetDescription   bool
	RetentionDays    int32
	SetRetentionDays bool
	Locked           bool
	SetLocked        bool
}

// GCPLogging is an optional interface implemented by the GCP logging backend for
// Cloud Logging resource surfaces (export sinks, log-based metrics, buckets)
// that have no portable, cross-provider equivalent. The Cloud Logging wire
// handler type-asserts for it; AWS and Azure backends do not implement it.
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

	CreateBucket(ctx context.Context, project, location string, bucket *LogBucket) (*LogBucket, error)
	GetBucket(ctx context.Context, project, location, name string) (*LogBucket, error)
	ListBuckets(ctx context.Context, project, location string) ([]LogBucket, error)
	UpdateBucket(ctx context.Context, project, location, name string, update BucketUpdate) (*LogBucket, error)
	DeleteBucket(ctx context.Context, project, location, name string) error
}
