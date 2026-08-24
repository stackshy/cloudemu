// Package cloudwatch provides an in-memory mock implementation of AWS CloudWatch.
package cloudwatch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time check that Mock implements driver.Monitoring.
var _ driver.Monitoring = (*Mock)(nil)

// metricKey uniquely identifies a metric series by namespace, name, and dimensions.
type metricKey struct {
	Namespace  string
	MetricName string
}

// Mock is an in-memory mock implementation of the AWS CloudWatch service.
type Mock struct {
	mu       sync.RWMutex
	metrics  map[metricKey][]driver.MetricDatum
	alarms   *memstore.Store[*alarmData]
	channels *memstore.Store[*driver.NotificationChannelInfo]
	history  []driver.AlarmHistoryEntry
	opts     *config.Options
}

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
	StateUpdatedTimestamp   time.Time
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	AlarmDescription        string
	ActionsEnabled          bool
	AlarmArn                string
	Tags                    map[string]string
}

// New creates a new CloudWatch mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		metrics:  make(map[metricKey][]driver.MetricDatum),
		alarms:   memstore.New[*alarmData](),
		channels: memstore.New[*driver.NotificationChannelInfo](),
		opts:     opts,
	}
}

// PutMetricData stores metric data points and evaluates any matching alarms.
func (m *Mock) PutMetricData(_ context.Context, data []driver.MetricDatum) error {
	if len(data) == 0 {
		return errors.Newf(errors.InvalidArgument, "metric data is required")
	}

	m.mu.Lock()
	for i := range data {
		key := metricKey{
			Namespace:  data[i].Namespace,
			MetricName: data[i].MetricName,
		}
		m.metrics[key] = append(m.metrics[key], data[i])
	}
	m.mu.Unlock()

	// Evaluate alarms for each unique namespace/metric pair that was updated.
	seen := make(map[metricKey]bool)

	for i := range data {
		mk := metricKey{Namespace: data[i].Namespace, MetricName: data[i].MetricName}
		if !seen[mk] {
			seen[mk] = true

			m.evaluateAlarms(data[i].Namespace, data[i].MetricName)
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

	filtered := m.collectFilteredDatums(namespace, metricName, alarm.Dimensions, windowStart, now)

	if len(filtered) == 0 {
		return
	}

	stat := aggregateDatums(filtered).stat(alarm.Stat)

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

		alarm.StateUpdatedTimestamp = now
	}

	alarm.State = newState
	alarm.StateReason = reason
}

func (m *Mock) collectFilteredDatums(
	namespace, metricName string, dims map[string]string, windowStart, now time.Time,
) []driver.MetricDatum {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := metricKey{Namespace: namespace, MetricName: metricName}
	dataPoints := m.metrics[key]

	var filtered []driver.MetricDatum

	for i := range dataPoints {
		d := &dataPoints[i]
		if d.Timestamp.Before(windowStart) || d.Timestamp.After(now) {
			continue
		}

		if !matchDimensions(d.Dimensions, dims) {
			continue
		}

		filtered = append(filtered, *d)
	}

	return filtered
}

// GetMetricData retrieves metric data for the given query, filtering by time range and
// computing the requested statistic.
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

	for i := range dataPoints {
		d := &dataPoints[i]
		if d.Timestamp.Before(startTime) || !d.Timestamp.Before(endTime) {
			continue
		}

		if !matchDimensions(d.Dimensions, dims) {
			continue
		}

		filtered = append(filtered, *d)
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

	// Carry the stored unit so the wire layer can echo the real unit (e.g.
	// "Percent" for CPUUtilization) instead of hardcoding "Count".
	result.Unit = unitOf(filtered)

	periodDur := time.Duration(period) * time.Second

	// Walk through periods from StartTime to EndTime.
	for periodStart := startTime; periodStart.Before(endTime); periodStart = periodStart.Add(periodDur) {
		periodEnd := periodStart.Add(periodDur)
		periodDatums := collectPeriodDatums(filtered, periodStart, periodEnd)

		if len(periodDatums) == 0 {
			continue
		}

		s := aggregateDatums(periodDatums).stat(stat)

		result.Timestamps = append(result.Timestamps, periodStart)
		result.Values = append(result.Values, s)
	}

	if result.Timestamps == nil {
		result.Timestamps = []time.Time{}
		result.Values = []float64{}
	}

	return result
}

// unitOf returns the first non-empty unit among the data points, or "" if none
// carry a unit.
func unitOf(data []driver.MetricDatum) string {
	for i := range data {
		if data[i].Unit != "" {
			return data[i].Unit
		}
	}

	return ""
}

func collectPeriodDatums(filtered []driver.MetricDatum, periodStart, periodEnd time.Time) []driver.MetricDatum {
	var datums []driver.MetricDatum

	for i := range filtered {
		if !filtered[i].Timestamp.Before(periodStart) && filtered[i].Timestamp.Before(periodEnd) {
			datums = append(datums, filtered[i])
		}
	}

	return datums
}

// ListMetrics returns unique metric names for the given namespace.
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

// ListMetricsDetailed returns every stored metric as a (namespace, name) pair.
// ListMetrics filters by an exact namespace, so a namespace-less "list all"
// call needs this to return real metrics tagged with their true namespace.
func (m *Mock) ListMetricsDetailed(_ context.Context) ([]driver.MetricIdentifier, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// AWS lists one entry per unique (namespace, name, dimension-set), so walk
	// every stored datum and dedupe on a canonical dimension signature.
	seen := make(map[string]bool)
	out := make([]driver.MetricIdentifier, 0, len(m.metrics))

	for key, data := range m.metrics {
		for i := range data {
			sig := key.Namespace + "\x00" + key.MetricName + "\x00" + canonicalDims(data[i].Dimensions)
			if seen[sig] {
				continue
			}

			seen[sig] = true

			out = append(out, driver.MetricIdentifier{
				Namespace:  key.Namespace,
				MetricName: key.MetricName,
				Dimensions: copyDims(data[i].Dimensions),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}

		if out[i].MetricName != out[j].MetricName {
			return out[i].MetricName < out[j].MetricName
		}

		return canonicalDims(out[i].Dimensions) < canonicalDims(out[j].Dimensions)
	})

	return out, nil
}

// canonicalDims renders a dimension map as a stable, order-independent string.
func canonicalDims(dims map[string]string) string {
	if len(dims) == 0 {
		return ""
	}

	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(dims[k])
		b.WriteByte(';')
	}

	return b.String()
}

func copyDims(dims map[string]string) map[string]string {
	if len(dims) == 0 {
		return nil
	}

	out := make(map[string]string, len(dims))
	for k, v := range dims {
		out[k] = v
	}

	return out
}

// CreateAlarm creates or updates an alarm with the given configuration.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateAlarm(_ context.Context, cfg driver.AlarmConfig) error {
	if cfg.Name == "" {
		return errors.Newf(errors.InvalidArgument, "alarm name is required")
	}

	dims := make(map[string]string, len(cfg.Dimensions))
	for k, v := range cfg.Dimensions {
		dims[k] = v
	}

	actionsEnabled := true
	if cfg.ActionsEnabled != nil {
		actionsEnabled = *cfg.ActionsEnabled
	}

	// When PutMetricAlarm updates an existing alarm, its state is left unchanged and
	// tags supplied in this operation are ignored (real AWS API_PutMetricAlarm semantics).
	// The rest of the configuration is completely overwritten.
	state := "INSUFFICIENT_DATA"
	stateReason := ""
	stateUpdated := m.opts.Clock.Now()

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	if existing, ok := m.alarms.Get(cfg.Name); ok {
		state = existing.State
		stateReason = existing.StateReason
		stateUpdated = existing.StateUpdatedTimestamp
		tags = existing.Tags
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
		State:                   state,
		StateReason:             stateReason,
		StateUpdatedTimestamp:   stateUpdated,
		AlarmActions:            append([]string{}, cfg.AlarmActions...),
		OKActions:               append([]string{}, cfg.OKActions...),
		InsufficientDataActions: append([]string{}, cfg.InsufficientDataActions...),
		AlarmDescription:        cfg.AlarmDescription,
		ActionsEnabled:          actionsEnabled,
		AlarmArn:                idgen.AWSARN("cloudwatch", m.opts.Region, m.opts.AccountID, "alarm:"+cfg.Name),
		Tags:                    tags,
	}

	m.alarms.Set(cfg.Name, alarm)

	return nil
}

// DeleteAlarm deletes the alarm with the given name.
func (m *Mock) DeleteAlarm(_ context.Context, name string) error {
	if !m.alarms.Delete(name) {
		return errors.Newf(errors.NotFound, "alarm %q not found", name)
	}

	return nil
}

// DescribeAlarms returns alarms matching the given names, or all alarms if names is empty.
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

// SetAlarmState manually sets the state of an alarm.
func (m *Mock) SetAlarmState(_ context.Context, name, state, reason string) error {
	a, ok := m.alarms.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "alarm %q not found", name)
	}

	a.State = state
	a.StateReason = reason
	a.StateUpdatedTimestamp = m.opts.Clock.Now()

	return nil
}

// CreateNotificationChannel creates a new notification channel and returns its info.
func (m *Mock) CreateNotificationChannel(
	_ context.Context, cfg driver.NotificationChannelConfig,
) (*driver.NotificationChannelInfo, error) {
	if cfg.Name == "" {
		return nil, errors.Newf(errors.InvalidArgument, "channel name is required")
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
		return errors.Newf(errors.NotFound, "notification channel %q not found", id)
	}

	return nil
}

// GetNotificationChannel returns the notification channel with the given ID.
func (m *Mock) GetNotificationChannel(_ context.Context, id string) (*driver.NotificationChannelInfo, error) {
	ch, ok := m.channels.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "notification channel %q not found", id)
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

// SetAlarmActionsEnabled toggles ActionsEnabled for the named alarms. It backs
// the AWS-local EnableAlarmActions / DisableAlarmActions wire operations.
func (m *Mock) SetAlarmActionsEnabled(_ context.Context, names []string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, name := range names {
		a, ok := m.alarms.Get(name)
		if !ok {
			return errors.Newf(errors.NotFound, "alarm %q not found", name)
		}

		a.ActionsEnabled = enabled
	}

	return nil
}

// AddAlarmTags merges tags onto the named alarm, backing TagResource.
func (m *Mock) AddAlarmTags(_ context.Context, alarmName string, tags map[string]string) error {
	a, ok := m.alarms.Get(alarmName)
	if !ok {
		return errors.Newf(errors.NotFound, "alarm %q not found", alarmName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if a.Tags == nil {
		a.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		a.Tags[k] = v
	}

	return nil
}

// RemoveAlarmTags deletes the given tag keys from the named alarm, backing
// UntagResource.
func (m *Mock) RemoveAlarmTags(_ context.Context, alarmName string, keys []string) error {
	a, ok := m.alarms.Get(alarmName)
	if !ok {
		return errors.Newf(errors.NotFound, "alarm %q not found", alarmName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range keys {
		delete(a.Tags, k)
	}

	return nil
}

// AlarmTags returns a copy of the named alarm's tags, backing
// ListTagsForResource.
func (m *Mock) AlarmTags(_ context.Context, alarmName string) (map[string]string, error) {
	a, ok := m.alarms.Get(alarmName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "alarm %q not found", alarmName)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return copyDims(a.Tags), nil
}

// matchDimensions returns true if the data point dimensions contain all of the
// requested filter dimensions.
func matchDimensions(dataDims, filterDims map[string]string) bool {
	for k, v := range filterDims {
		if dataDims[k] != v {
			return false
		}
	}

	return true
}

// statAgg accumulates SampleCount / Sum / Minimum / Maximum across a set of
// metric datums so any requested statistic can be derived. It treats a plain
// Value, a pre-aggregated StatisticValues set, and paired Values/Counts arrays
// uniformly, matching how real CloudWatch folds all three into one series.
type statAgg struct {
	count float64
	sum   float64
	min   float64
	max   float64
	seen  bool
}

// add folds one observation (or sub-aggregate) into the accumulator: count
// samples summing to sum, whose smallest and largest observed values are low
// and high. Non-positive counts contribute nothing, matching AWS.
func (a *statAgg) add(count, sum, low, high float64) {
	if count <= 0 {
		return
	}

	a.count += count
	a.sum += sum

	if !a.seen || low < a.min {
		a.min = low
	}

	if !a.seen || high > a.max {
		a.max = high
	}

	a.seen = true
}

// stat returns the requested statistic, or 0 when no data was accumulated.
func (a statAgg) stat(stat string) float64 {
	if !a.seen {
		return 0
	}

	switch stat {
	case "Sum":
		return a.sum
	case "Min", "Minimum":
		return a.min
	case "Max", "Maximum":
		return a.max
	case "SampleCount":
		return a.count
	default: // "Average" or unspecified
		return a.sum / a.count
	}
}

// aggregateDatums folds every datum — plain Value, StatisticValues set, or
// Values/Counts arrays — into a single accumulator.
func aggregateDatums(datums []driver.MetricDatum) statAgg {
	var a statAgg

	for i := range datums {
		d := &datums[i]

		switch {
		case d.StatisticValues != nil:
			s := d.StatisticValues
			a.add(s.SampleCount, s.Sum, s.Minimum, s.Maximum)
		case len(d.Values) > 0:
			for j, v := range d.Values {
				count := 1.0
				if j < len(d.Counts) {
					count = d.Counts[j]
				}

				a.add(count, v*count, v, v)
			}
		default:
			a.add(1, d.Value, d.Value, d.Value)
		}
	}

	return a
}

func toAlarmInfo(a *alarmData) driver.AlarmInfo {
	dims := make(map[string]string, len(a.Dimensions))
	for k, v := range a.Dimensions {
		dims[k] = v
	}

	return driver.AlarmInfo{
		Name:                    a.Name,
		Namespace:               a.Namespace,
		MetricName:              a.MetricName,
		State:                   a.State,
		ComparisonOperator:      a.ComparisonOperator,
		Threshold:               a.Threshold,
		StateReason:             a.StateReason,
		StateUpdatedTimestamp:   a.StateUpdatedTimestamp,
		Period:                  a.Period,
		EvaluationPeriods:       a.EvaluationPeriods,
		Statistic:               a.Stat,
		ActionsEnabled:          a.ActionsEnabled,
		AlarmActions:            append([]string{}, a.AlarmActions...),
		OKActions:               append([]string{}, a.OKActions...),
		InsufficientDataActions: append([]string{}, a.InsufficientDataActions...),
		AlarmDescription:        a.AlarmDescription,
		AlarmArn:                a.AlarmArn,
		Dimensions:              dims,
	}
}
