// Package driver defines the interface for monitoring service implementations.
package driver

import (
	"context"
	"time"
)

// MetricDatum is a single metric data point.
type MetricDatum struct {
	Namespace  string
	MetricName string
	Value      float64
	Unit       string
	Dimensions map[string]string
	Timestamp  time.Time
}

// GetMetricInput configures a metric retrieval operation.
type GetMetricInput struct {
	Namespace  string
	MetricName string
	Dimensions map[string]string
	StartTime  time.Time
	EndTime    time.Time
	Period     int // seconds
	Stat       string // "Average", "Sum", "Minimum", "Maximum", "SampleCount"
}

// MetricDataResult is a set of metric data points.
type MetricDataResult struct {
	Timestamps []time.Time
	Values     []float64
}

// AlarmConfig describes an alarm to create.
type AlarmConfig struct {
	Name               string
	Namespace          string
	MetricName         string
	Dimensions         map[string]string
	ComparisonOperator string // "GreaterThanThreshold", "LessThanThreshold", etc.
	Threshold          float64
	Period             int
	EvaluationPeriods  int
	Stat               string
}

// AlarmInfo describes an alarm.
type AlarmInfo struct {
	Name               string
	Namespace          string
	MetricName         string
	State              string // "OK", "ALARM", "INSUFFICIENT_DATA"
	ComparisonOperator string
	Threshold          float64
}

// Monitoring is the interface that monitoring provider implementations must satisfy.
type Monitoring interface {
	PutMetricData(ctx context.Context, data []MetricDatum) error
	GetMetricData(ctx context.Context, input GetMetricInput) (*MetricDataResult, error)
	ListMetrics(ctx context.Context, namespace string) ([]string, error)

	CreateAlarm(ctx context.Context, config AlarmConfig) error
	DeleteAlarm(ctx context.Context, name string) error
	DescribeAlarms(ctx context.Context, names []string) ([]AlarmInfo, error)
	SetAlarmState(ctx context.Context, name, state, reason string) error
}
