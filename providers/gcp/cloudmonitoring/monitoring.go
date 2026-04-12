// Package cloudmonitoring provides an in-memory mock implementation of GCP Cloud Monitoring.
package cloudmonitoring

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/monitoring/driver"
)

// Compile-time check that Mock implements driver.Monitoring.
var _ driver.Monitoring = (*Mock)(nil)

// metricKey uniquely identifies a metric series by namespace and metric name.
type metricKey struct {
	Namespace  string
	MetricName string
}

// alarmData holds internal state for a single alarm (alert policy).
type alarmData struct {
	Name                    string
	Namespace               string
	MetricName              string
	Dimensions              map[string]string
	ComparisonOperator      string
	Threshold               float64
	Period                  int
	EvaluationPeriods       int
	Stat                    string
	State                   string
	StateReason             string
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
}

// Mock is an in-memory mock implementation of the GCP Cloud Monitoring service.
type Mock struct {
	mu       sync.RWMutex
	metrics  map[metricKey][]driver.MetricDatum
	alarms   *memstore.Store[*alarmData]
	channels *memstore.Store[*driver.NotificationChannelInfo]
	history  []driver.AlarmHistoryEntry
	opts     *config.Options
}

// New creates a new Cloud Monitoring mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		metrics:  make(map[metricKey][]driver.MetricDatum),
		alarms:   memstore.New[*alarmData](),
		channels: memstore.New[*driver.NotificationChannelInfo](),
		opts:     opts,
	}
}

// PutMetricData stores metric data points (time series data) and evaluates any matching alarms.
func (m *Mock) PutMetricData(_ context.Context, data []driver.MetricDatum) error {
	if len(data) == 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "metric data is required")
	}

	m.mu.Lock()
	for _, d := range data {
		key := metricKey{
			Namespace:  d.Namespace,
			MetricName: d.MetricName,
		}
		m.metrics[key] = append(m.metrics[key], d)
	}
	m.mu.Unlock()

	// Evaluate alarms for each unique namespace/metric pair that was updated.
	seen := make(map[metricKey]bool)

	for _, d := range data {
		mk := metricKey{Namespace: d.Namespace, MetricName: d.MetricName}
		if !seen[mk] {
			seen[mk] = true

			m.evaluateAlarms(d.Namespace, d.MetricName)
		}
	}

	return nil
}

func evaluateComparison(value float64, operator string, threshold float64) bool {
	switch operator {
	case "GreaterThanThreshold":
		return value > threshold
	case "GreaterThanOrEqualToThreshold":
		return value >= threshold
	case "LessThanThreshold":
		return value < threshold
	case "LessThanOrEqualToThreshold":
		return value <= threshold
	default:
		return false
	}
}

func (m *Mock) evaluateAlarms(namespace, metricName string) {
	allAlarms := m.alarms.All()

	for _, alarm := range allAlarms {
		if alarm.Namespace != namespace || alarm.MetricName != metricName {
			continue
		}

		m.evaluateSingleAlarm(alarm, namespace, metricName)
	}
}

func (m *Mock) evaluateSingleAlarm(alarm *alarmData, namespace, metricName string) {
	period := alarm.Period
	if period <= 0 {
		period = 60
	}

	evalPeriods := alarm.EvaluationPeriods
	if evalPeriods <= 0 {
		evalPeriods = 1
	}

	now := m.opts.Clock.Now()
	windowDur := time.Duration(period*evalPeriods) * time.Second
	windowStart := now.Add(-windowDur)

	filtered := m.collectFilteredValues(namespace, metricName, alarm.Dimensions, windowStart, now)

	if len(filtered) == 0 {
		return
	}

	stat := computeStat(filtered, alarm.Stat)

	var newState, reason string
	if evaluateComparison(stat, alarm.ComparisonOperator, alarm.Threshold) {
		newState = "ALARM"
		reason = "Threshold crossed"
	} else {
		newState = "OK"
		reason = "Threshold not crossed"
	}

	if alarm.State != newState {
		m.mu.Lock()
		m.history = append(m.history, driver.AlarmHistoryEntry{
			AlarmName: alarm.Name,
			Timestamp: now,
			OldState:  alarm.State,
			NewState:  newState,
			Reason:    fmt.Sprintf("Transition from %s to %s: %s", alarm.State, newState, reason),
		})
		m.mu.Unlock()
	}

	alarm.State = newState
	alarm.StateReason = reason
}

func (m *Mock) collectFilteredValues(namespace, metricName string, dims map[string]string, windowStart, now time.Time) []float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := metricKey{Namespace: namespace, MetricName: metricName}
	dataPoints := m.metrics[key]

	var filtered []float64

	for _, d := range dataPoints {
		if d.Timestamp.Before(windowStart) || d.Timestamp.After(now) {
			continue
		}

		if !matchDimensions(d.Dimensions, dims) {
			continue
		}

		filtered = append(filtered, d.Value)
	}

	return filtered
}

// GetMetricData retrieves metric data for the given query, filtering by time range
// and computing the requested statistic (aligner).
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) GetMetricData(_ context.Context, input driver.GetMetricInput) (*driver.MetricDataResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := metricKey{
		Namespace:  input.Namespace,
		MetricName: input.MetricName,
	}

	dataPoints := m.metrics[key]
	filtered := filterByTimeAndDimensions(dataPoints, input.StartTime, input.EndTime, input.Dimensions)

	// Sort by timestamp.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.Before(filtered[j].Timestamp)
	})

	period := input.Period
	if period <= 0 {
		period = 60
	}

	return buildMetricResult(filtered, input.StartTime, input.EndTime, period, input.Stat), nil
}

func filterByTimeAndDimensions(dataPoints []driver.MetricDatum, startTime, endTime time.Time, dims map[string]string) []driver.MetricDatum {
	var filtered []driver.MetricDatum

	for _, d := range dataPoints {
		if d.Timestamp.Before(startTime) || !d.Timestamp.Before(endTime) {
			continue
		}

		if !matchDimensions(d.Dimensions, dims) {
			continue
		}

		filtered = append(filtered, d)
	}

	return filtered
}

func buildMetricResult(filtered []driver.MetricDatum, startTime, endTime time.Time, period int, stat string) *driver.MetricDataResult {
	result := &driver.MetricDataResult{}

	if len(filtered) == 0 {
		result.Timestamps = []time.Time{}
		result.Values = []float64{}

		return result
	}

	periodDur := time.Duration(period) * time.Second

	// Walk through alignment periods from StartTime to EndTime.
	for periodStart := startTime; periodStart.Before(endTime); periodStart = periodStart.Add(periodDur) {
		periodEnd := periodStart.Add(periodDur)
		periodValues := collectPeriodValues(filtered, periodStart, periodEnd)

		if len(periodValues) == 0 {
			continue
		}

		s := computeStat(periodValues, stat)

		result.Timestamps = append(result.Timestamps, periodStart)
		result.Values = append(result.Values, s)
	}

	if result.Timestamps == nil {
		result.Timestamps = []time.Time{}
		result.Values = []float64{}
	}

	return result
}

func collectPeriodValues(filtered []driver.MetricDatum, periodStart, periodEnd time.Time) []float64 {
	var values []float64

	for _, d := range filtered {
		if !d.Timestamp.Before(periodStart) && d.Timestamp.Before(periodEnd) {
			values = append(values, d.Value)
		}
	}

	return values
}

// ListMetrics returns unique metric names (metric descriptors) for the given namespace (project/metric type prefix).
func (m *Mock) ListMetrics(_ context.Context, namespace string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]bool)

	for key := range m.metrics {
		if key.Namespace == namespace {
			seen[key.MetricName] = true
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}

	sort.Strings(names)

	return names, nil
}

// CreateAlarm creates or updates an alert policy with the given configuration.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateAlarm(_ context.Context, cfg driver.AlarmConfig) error {
	if cfg.Name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "alert policy name is required")
	}

	dims := make(map[string]string, len(cfg.Dimensions))
	for k, v := range cfg.Dimensions {
		dims[k] = v
	}

	alarm := &alarmData{
		Name:                    cfg.Name,
		Namespace:               cfg.Namespace,
		MetricName:              cfg.MetricName,
		Dimensions:              dims,
		ComparisonOperator:      cfg.ComparisonOperator,
		Threshold:               cfg.Threshold,
		Period:                  cfg.Period,
		EvaluationPeriods:       cfg.EvaluationPeriods,
		Stat:                    cfg.Stat,
		State:                   "INSUFFICIENT_DATA",
		AlarmActions:            append([]string{}, cfg.AlarmActions...),
		OKActions:               append([]string{}, cfg.OKActions...),
		InsufficientDataActions: append([]string{}, cfg.InsufficientDataActions...),
	}

	m.alarms.Set(cfg.Name, alarm)

	return nil
}

// DeleteAlarm deletes the alert policy with the given name.
func (m *Mock) DeleteAlarm(_ context.Context, name string) error {
	if !m.alarms.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "alert policy %q not found", name)
	}

	return nil
}

// DescribeAlarms returns alert policies matching the given names, or all policies if names is empty.
func (m *Mock) DescribeAlarms(_ context.Context, names []string) ([]driver.AlarmInfo, error) {
	if len(names) == 0 {
		all := m.alarms.All()
		result := make([]driver.AlarmInfo, 0, len(all))

		for _, a := range all {
			result = append(result, toAlarmInfo(a))
		}

		return result, nil
	}

	result := make([]driver.AlarmInfo, 0, len(names))

	for _, name := range names {
		a, ok := m.alarms.Get(name)
		if !ok {
			continue
		}

		result = append(result, toAlarmInfo(a))
	}

	return result, nil
}

// SetAlarmState manually sets the state of an alert policy.
func (m *Mock) SetAlarmState(_ context.Context, name, state, reason string) error {
	a, ok := m.alarms.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "alert policy %q not found", name)
	}

	a.State = state
	a.StateReason = reason

	return nil
}

// CreateNotificationChannel creates a new notification channel and returns its info.
func (m *Mock) CreateNotificationChannel(
	_ context.Context, cfg driver.NotificationChannelConfig,
) (*driver.NotificationChannelInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "channel name is required")
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	ch := &driver.NotificationChannelInfo{
		ID:       idgen.GenerateID("chan-"),
		Name:     cfg.Name,
		Type:     cfg.Type,
		Endpoint: cfg.Endpoint,
		Tags:     tags,
	}

	m.channels.Set(ch.ID, ch)

	return ch, nil
}

// DeleteNotificationChannel deletes the notification channel with the given ID.
func (m *Mock) DeleteNotificationChannel(_ context.Context, id string) error {
	if !m.channels.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "notification channel %q not found", id)
	}

	return nil
}

// GetNotificationChannel returns the notification channel with the given ID.
func (m *Mock) GetNotificationChannel(_ context.Context, id string) (*driver.NotificationChannelInfo, error) {
	ch, ok := m.channels.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "notification channel %q not found", id)
	}

	return ch, nil
}

// ListNotificationChannels returns all notification channels.
func (m *Mock) ListNotificationChannels(_ context.Context) ([]driver.NotificationChannelInfo, error) {
	all := m.channels.All()
	result := make([]driver.NotificationChannelInfo, 0, len(all))

	for _, ch := range all {
		result = append(result, *ch)
	}

	return result, nil
}

// GetAlarmHistory returns alarm history entries filtered by alarm name, limited by limit.
func (m *Mock) GetAlarmHistory(_ context.Context, alarmName string, limit int) ([]driver.AlarmHistoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []driver.AlarmHistoryEntry

	for _, entry := range m.history {
		if entry.AlarmName == alarmName {
			filtered = append(filtered, entry)
		}
	}

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	return filtered, nil
}

// matchDimensions returns true if the data point dimensions (labels) contain all of the
// requested filter dimensions.
func matchDimensions(dataDims, filterDims map[string]string) bool {
	for k, v := range filterDims {
		if dataDims[k] != v {
			return false
		}
	}

	return true
}

// computeStat computes the requested statistic (aligner) over a slice of values.
func computeStat(values []float64, stat string) float64 {
	if len(values) == 0 {
		return 0
	}

	switch stat {
	case "Sum":
		return sumValues(values)
	case "Min", "Minimum":
		return minValue(values)
	case "Max", "Maximum":
		return maxValue(values)
	case "SampleCount":
		return float64(len(values))
	default: // "Average" or unspecified
		return sumValues(values) / float64(len(values))
	}
}

func sumValues(values []float64) float64 {
	sum := 0.0

	for _, v := range values {
		sum += v
	}

	return sum
}

func minValue(values []float64) float64 {
	result := math.MaxFloat64

	for _, v := range values {
		if v < result {
			result = v
		}
	}

	return result
}

func maxValue(values []float64) float64 {
	result := -math.MaxFloat64

	for _, v := range values {
		if v > result {
			result = v
		}
	}

	return result
}

func toAlarmInfo(a *alarmData) driver.AlarmInfo {
	return driver.AlarmInfo{
		Name:               a.Name,
		Namespace:          a.Namespace,
		MetricName:         a.MetricName,
		State:              a.State,
		ComparisonOperator: a.ComparisonOperator,
		Threshold:          a.Threshold,
	}
}
