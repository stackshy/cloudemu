// Package driver defines the interface for monitoring service implementations.
package driver

import (
	"context"
	"time"
)

// MetricIdentifier names one metric by namespace + name. It's used by the
// AWS-local detailed metric listing that backs a namespace-less ListMetrics
// ("list all") so each returned metric keeps its real namespace.
type MetricIdentifier struct {
	Namespace  string
	MetricName string
	Dimensions map[string]string
}

// StatisticSet is a pre-aggregated metric sample: the SampleCount/Sum/Minimum/
// Maximum a caller supplies via PutMetricData's StatisticValues instead of a
// single Value. It lets the series answer every statistic without the raw
// observations.
type StatisticSet struct {
	SampleCount float64
	Sum         float64
	Minimum     float64
	Maximum     float64
}

// MetricDatum is a single metric data point. A datum carries either a single
// Value, a pre-aggregated StatisticValues set, or paired Values/Counts arrays
// (each Values[i] observed Counts[i] times); the later two let a caller publish
// aggregated data without the individual observations.
type MetricDatum struct {
	Namespace       string
	MetricName      string
	Value           float64
	Unit            string
	Dimensions      map[string]string
	Timestamp       time.Time
	StatisticValues *StatisticSet
	Values          []float64
	Counts          []float64
}

// GetMetricInput configures a metric retrieval operation.
type GetMetricInput struct {
	Namespace  string
	MetricName string
	Dimensions map[string]string
	StartTime  time.Time
	EndTime    time.Time
	Period     int    // seconds
	Stat       string // "Average", "Sum", "Minimum", "Maximum", "SampleCount"
}

// MetricDataResult is a set of metric data points.
type MetricDataResult struct {
	Timestamps []time.Time
	Values     []float64
	Unit       string // the unit stored with the underlying datapoints, if any
}

// AlarmConfig describes an alarm to create.
type AlarmConfig struct {
	Name                    string
	Namespace               string
	MetricName              string
	Dimensions              map[string]string
	ComparisonOperator      string // "GreaterThanThreshold", "LessThanThreshold", etc.
	Threshold               float64
	Period                  int
	EvaluationPeriods       int
	DatapointsToAlarm       int    // M in the M-of-N rule; 0 defaults to EvaluationPeriods
	Stat                    string
	ExtendedStatistic       string // percentile statistic (e.g. "p95"); alternative to Stat
	Unit                    string // the metric unit the alarm watches
	TreatMissingData        string // "missing" (default), "notBreaching", "breaching", "ignore"
	AlarmActions            []string // channel IDs to notify on ALARM
	OKActions               []string // channel IDs to notify on OK
	InsufficientDataActions []string // channel IDs to notify on INSUFFICIENT_DATA
	AlarmDescription        string
	ActionsEnabled          *bool // nil defaults to true (AWS semantics)
	Tags                    map[string]string
}

// AlarmInfo describes an alarm.
type AlarmInfo struct {
	Name                    string
	Namespace               string
	MetricName              string
	State                   string // "OK", "ALARM", "INSUFFICIENT_DATA"
	ComparisonOperator      string
	Threshold               float64
	StateReason             string
	StateUpdatedTimestamp   time.Time
	Period                  int
	EvaluationPeriods       int
	DatapointsToAlarm       int
	Statistic               string
	ExtendedStatistic       string
	Unit                    string
	TreatMissingData        string
	ActionsEnabled          bool
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	AlarmDescription        string
	AlarmArn                string
	Dimensions              map[string]string
}

// NotificationChannelConfig describes a notification channel.
type NotificationChannelConfig struct {
	Name     string
	Type     string // "email", "webhook", "queue", "function"
	Endpoint string
	Tags     map[string]string
}

// NotificationChannelInfo describes a notification channel.
type NotificationChannelInfo struct {
	ID       string
	Name     string
	Type     string
	Endpoint string
	Tags     map[string]string
}

// AlarmHistoryEntry describes a state change or action in alarm history.
type AlarmHistoryEntry struct {
	AlarmName string
	Timestamp time.Time
	OldState  string
	NewState  string
	Reason    string
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

	// Notification Channels
	CreateNotificationChannel(ctx context.Context, config NotificationChannelConfig) (*NotificationChannelInfo, error)
	DeleteNotificationChannel(ctx context.Context, id string) error
	GetNotificationChannel(ctx context.Context, id string) (*NotificationChannelInfo, error)
	ListNotificationChannels(ctx context.Context) ([]NotificationChannelInfo, error)

	// Alarm History
	GetAlarmHistory(ctx context.Context, alarmName string, limit int) ([]AlarmHistoryEntry, error)
}
