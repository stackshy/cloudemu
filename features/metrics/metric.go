// Package metrics provides in-memory metrics collection for cloudemu services.
package metrics

import "time"

// MetricType identifies the type of metric.
type MetricType int

const (
	CounterType MetricType = iota
	GaugeType
	HistogramType
)

// Metric is a recorded metric data point.
type Metric struct {
	Name      string
	Type      MetricType
	Value     float64
	Labels    map[string]string
	Timestamp time.Time
}
