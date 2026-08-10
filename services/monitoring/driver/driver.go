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
}

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
	Period     int    // seconds
	Stat       string // "Average", "Sum", "Minimum", "Maximum", "SampleCount"
}

// MetricDataResult is a set of metric data points.
type MetricDataResult struct {
	Timestamps []time.Time
	Values     []float64
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
	Stat                    string
	AlarmActions            []string // channel IDs to notify on ALARM
	OKActions               []string // channel IDs to notify on OK
	InsufficientDataActions []string // channel IDs to notify on INSUFFICIENT_DATA
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

// OCIMetric identifies a compartment-scoped metric series. Timestamps and
// Values are populated only by a summarize query.
type OCIMetric struct {
	CompartmentID string
	Namespace     string
	ResourceGroup string
	Name          string
	Dimensions    map[string]string
	Resolution    string
	Timestamps    []time.Time
	Values        []float64
}

// OCIMetricFilter narrows a metric listing. Empty fields match anything.
type OCIMetricFilter struct {
	Namespace     string
	ResourceGroup string
	Name          string
	Dimensions    map[string]string
}

// OCIMetricQuery selects the series to aggregate. Query is OCI's metric query
// language, e.g. CpuUtilization[1m].mean().
type OCIMetricQuery struct {
	Namespace     string
	ResourceGroup string
	Query         string
	Resolution    string
	Dimensions    map[string]string
	StartTime     time.Time
	EndTime       time.Time
}

// OCIAlarmSpec describes an OCI alarm, whose condition is a single query string
// rather than the portable metric/threshold pair.
type OCIAlarmSpec struct {
	DisplayName                string
	CompartmentID              string
	MetricCompartmentID        string
	Namespace                  string
	ResourceGroup              string
	Query                      string
	Resolution                 string
	PendingDuration            string
	Severity                   string
	Body                       string
	MessageFormat              string
	RepeatNotificationDuration string
	Destinations               []string
	FreeformTags               map[string]string
	DefinedTags                map[string]map[string]any
	IsEnabled                  bool
}

// OCIAlarm is a stored alarm with its generated identity and current status.
type OCIAlarm struct {
	ID             string
	Spec           OCIAlarmSpec
	Status         string // "FIRING", "OK" or "SUSPENDED"
	LifecycleState string
	TimeCreated    time.Time
	TimeUpdated    time.Time
	TimeTriggered  time.Time
}

// OCIMonitoring is an OPTIONAL capability, discovered by type assertion. OCI
// scopes every metric and alarm to a compartment and identifies alarms by OCID,
// neither of which the portable model carries; drivers for other clouds do not
// implement it.
type OCIMonitoring interface {
	PostMetricData(ctx context.Context, compartmentID, resourceGroup string, data []MetricDatum) error
	ListOCIMetrics(ctx context.Context, compartmentID string, filter OCIMetricFilter) ([]OCIMetric, error)
	SummarizeOCIMetrics(ctx context.Context, compartmentID string, query OCIMetricQuery) ([]OCIMetric, error)

	CreateOCIAlarm(ctx context.Context, spec OCIAlarmSpec) (*OCIAlarm, error)
	GetOCIAlarm(ctx context.Context, id string) (*OCIAlarm, error)
	ListOCIAlarms(ctx context.Context, compartmentID string) ([]*OCIAlarm, error)
	UpdateOCIAlarm(ctx context.Context, id string, spec OCIAlarmSpec) (*OCIAlarm, error)
	DeleteOCIAlarm(ctx context.Context, id string) error
	OCIAlarmHistory(ctx context.Context, id string, limit int) ([]AlarmHistoryEntry, error)
}
