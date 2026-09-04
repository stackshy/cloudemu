package driver

import "time"

// MetricStreamFilter names a metric namespace to include in, or exclude from,
// a metric stream, optionally narrowed to specific metric names within it. An
// empty MetricNames means "every metric in Namespace".
type MetricStreamFilter struct {
	Namespace   string
	MetricNames []string
}

// MetricStreamStatisticsMetric identifies one metric (by namespace + name) that
// an entry in MetricStreamConfig.StatisticsConfigurations applies to.
type MetricStreamStatisticsMetric struct {
	Namespace  string
	MetricName string
}

// MetricStreamStatisticsConfig requests extra statistics — beyond the stream's
// always-sent MAX/MIN/SUM/SAMPLECOUNT — for a set of metrics.
type MetricStreamStatisticsConfig struct {
	IncludeMetrics       []MetricStreamStatisticsMetric
	AdditionalStatistics []string
}

// MetricStreamConfig describes a metric stream to create or update.
// IncludeFilters and ExcludeFilters are mutually exclusive: at most one may be
// set, matching the real PutMetricStream validation. Tags apply only when
// creating a new stream; PutMetricStream ignores Tags on an update (use
// TagResource/UntagResource for an existing stream, matching real CloudWatch).
type MetricStreamConfig struct {
	Name                         string
	FirehoseARN                  string
	RoleARN                      string
	OutputFormat                 string // "json", "opentelemetry1.0", "opentelemetry0.7"
	IncludeFilters               []MetricStreamFilter
	ExcludeFilters               []MetricStreamFilter
	StatisticsConfigurations     []MetricStreamStatisticsConfig
	IncludeLinkedAccountsMetrics bool
	Tags                         map[string]string
}

// MetricStreamInfo describes a stored metric stream, as returned by
// GetMetricStream.
type MetricStreamInfo struct {
	Name                         string
	ARN                          string
	FirehoseARN                  string
	RoleARN                      string
	OutputFormat                 string
	State                        string // "running" or "stopped"
	IncludeFilters               []MetricStreamFilter
	ExcludeFilters               []MetricStreamFilter
	StatisticsConfigurations     []MetricStreamStatisticsConfig
	IncludeLinkedAccountsMetrics bool
	CreationDate                 time.Time
	LastUpdateDate               time.Time
}

// MetricStreamEntry is a ListMetricStreams summary row — a metric stream's
// identity and state, without its filters or statistics configuration.
type MetricStreamEntry struct {
	Name           string
	ARN            string
	FirehoseARN    string
	OutputFormat   string
	State          string
	CreationDate   time.Time
	LastUpdateDate time.Time
}
