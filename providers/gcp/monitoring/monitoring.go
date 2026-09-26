// Package monitoring provides an in-memory mock implementation of GCP Cloud Monitoring.
package monitoring

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// historyStateUpdate is the HistoryItemType stamped on a recorded state change.
const historyStateUpdate = "StateUpdate"

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
	DatapointsToAlarm       int
	Stat                    string
	ExtendedStatistic       string
	Unit                    string
	TreatMissingData        string
	State                   string
	StateReason             string
	StateUpdatedTimestamp   time.Time
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	// StateTransitionedTimestamp is when State last changed.
	StateTransitionedTimestamp time.Time
	// LastEvaluatedAt is when the alarm was last evaluated or had its state
	// set. The next lazy evaluation is due one EvaluationInterval later.
	LastEvaluatedAt time.Time
}

// Mock is an in-memory mock implementation of the GCP Cloud Monitoring service.
type Mock struct {
	// alarmMu guards every alarm field and is taken before mu. Notifications
	// are delivered only after both are released.
	alarmMu          sync.Mutex
	mu               sync.RWMutex
	metrics          map[metricKey][]driver.MetricDatum
	alarms           *memstore.Store[*alarmData]
	channels         *memstore.Store[*driver.NotificationChannelInfo]
	history          []driver.AlarmHistoryEntry
	opts             *config.Options
	webhookDeliverer WebhookDeliverer
	pubsubPublisher  PubSubPublisher
}

// New creates a new Cloud Monitoring mock with the given configuration options.
// It defaults webhookDeliverer to a real-HTTP deliverer so a breach targeting a
// webhook notification channel POSTs the incident in production without any
// wiring, mirroring azure/monitor.New.
func New(opts *config.Options) *Mock {
	return &Mock{
		metrics:          make(map[metricKey][]driver.MetricDatum),
		alarms:           memstore.New[*alarmData](),
		channels:         memstore.New[*driver.NotificationChannelInfo](),
		opts:             opts,
		webhookDeliverer: newHTTPWebhookDeliverer(),
	}
}

// PutMetricData stores metric data points (time series data) and evaluates any matching alarms.
func (m *Mock) PutMetricData(_ context.Context, data []driver.MetricDatum) error {
	if len(data) == 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "metric data is required")
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

	// New data re-evaluates the alarms on each updated metric right away.
	seen := make(map[metricKey]bool)

	for i := range data {
		seen[metricKey{Namespace: data[i].Namespace, MetricName: data[i].MetricName}] = true
	}

	m.evaluateMetricAlarms(seen)

	return nil
}

// appendHistory records one alert policy state transition in the history log.
func (m *Mock) appendHistory(name, oldState, newState, reason string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.history = append(m.history, driver.AlarmHistoryEntry{
		AlarmName:       name,
		Timestamp:       now,
		OldState:        oldState,
		NewState:        newState,
		HistoryItemType: historyStateUpdate,
		Reason:          fmt.Sprintf("Transition from %s to %s: %s", oldState, newState, reason),
	})
}

func (m *Mock) collectFilteredDatums(
	namespace, metricName string, dims map[string]string, unit string, windowStart, now time.Time,
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

		if !alarmeval.MatchAlarmDimensions(d.Dimensions, dims) || !alarmeval.MatchUnit(d.Unit, unit) {
			continue
		}

		filtered = append(filtered, *d)
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
	filtered := filterByTimeAndDimensions(dataPoints, input.StartTime, input.EndTime, input.Dimensions, input.Unit)

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

func filterByTimeAndDimensions(
	dataPoints []driver.MetricDatum, startTime, endTime time.Time, dims map[string]string, unit string,
) []driver.MetricDatum {
	var filtered []driver.MetricDatum

	for i := range dataPoints {
		d := &dataPoints[i]
		if d.Timestamp.Before(startTime) || !d.Timestamp.Before(endTime) {
			continue
		}

		if !alarmeval.MatchDimensions(d.Dimensions, dims) || !alarmeval.MatchUnit(d.Unit, unit) {
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

	for i := range filtered {
		if !filtered[i].Timestamp.Before(periodStart) && filtered[i].Timestamp.Before(periodEnd) {
			values = append(values, filtered[i].Value)
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

// GCPSeriesKeys returns the (namespace, metricName) pairs that currently hold
// metric data. It backs the GCP timeSeries.list / metricDescriptors surface,
// which enumerates metric types independently of the AWS-shaped ListMetrics.
func (m *Mock) GCPSeriesKeys() []driver.MetricIdentifier {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]driver.MetricIdentifier, 0, len(m.metrics))
	for key := range m.metrics {
		keys = append(keys, driver.MetricIdentifier{Namespace: key.Namespace, MetricName: key.MetricName})
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Namespace != keys[j].Namespace {
			return keys[i].Namespace < keys[j].Namespace
		}

		return keys[i].MetricName < keys[j].MetricName
	})

	return keys
}

// GCPRawSeries returns a copy of every raw datum stored for namespace+metricName.
// timeSeries.list groups these by their label set into individual series, which
// the aggregated GetMetricData path cannot express.
func (m *Mock) GCPRawSeries(namespace, metricName string) []driver.MetricDatum {
	m.mu.RLock()
	defer m.mu.RUnlock()

	src := m.metrics[metricKey{Namespace: namespace, MetricName: metricName}]
	out := make([]driver.MetricDatum, len(src))
	copy(out, src)

	return out
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

	now := m.opts.Clock.Now()

	alarm := &alarmData{
		Name:                       cfg.Name,
		Namespace:                  cfg.Namespace,
		MetricName:                 cfg.MetricName,
		Dimensions:                 dims,
		ComparisonOperator:         cfg.ComparisonOperator,
		Threshold:                  cfg.Threshold,
		Period:                     cfg.Period,
		EvaluationPeriods:          cfg.EvaluationPeriods,
		DatapointsToAlarm:          cfg.DatapointsToAlarm,
		Stat:                       cfg.Stat,
		ExtendedStatistic:          cfg.ExtendedStatistic,
		Unit:                       cfg.Unit,
		TreatMissingData:           cfg.TreatMissingData,
		State:                      alarmeval.StateInsufficientData,
		StateUpdatedTimestamp:      now,
		StateTransitionedTimestamp: now,
		AlarmActions:               append([]string{}, cfg.AlarmActions...),
		OKActions:                  append([]string{}, cfg.OKActions...),
		InsufficientDataActions:    append([]string{}, cfg.InsufficientDataActions...),
	}

	m.alarmMu.Lock()
	m.alarms.Set(cfg.Name, alarm)
	fire := m.evaluateLocked(alarm, now)
	m.alarmMu.Unlock()

	m.deliver([]*incidentFire{fire})

	return nil
}

// DeleteAlarm deletes the alert policy with the given name.
func (m *Mock) DeleteAlarm(_ context.Context, name string) error {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	if !m.alarms.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "alert policy %q not found", name)
	}

	return nil
}

// DescribeAlarms returns alert policies matching the given names, or all policies if names is empty.
func (m *Mock) DescribeAlarms(_ context.Context, names []string) ([]driver.AlarmInfo, error) {
	m.evaluateDue(m.opts.Clock.Now())

	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

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

// SetAlarmState manually sets the state of an alert policy. Like a metric-driven
// transition, a state change records a history entry.
func (m *Mock) SetAlarmState(_ context.Context, name, state, reason string) error {
	if !alarmeval.ValidState(state) {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid alarm state %q: must be OK, ALARM or INSUFFICIENT_DATA", state)
	}

	now := m.opts.Clock.Now()

	m.alarmMu.Lock()

	a, ok := m.alarms.Get(name)
	if !ok {
		m.alarmMu.Unlock()

		return cerrors.Newf(cerrors.NotFound, "alert policy %q not found", name)
	}

	// The forced state holds for one evaluation interval.
	a.LastEvaluatedAt = now
	fire := m.transitionLocked(a, state, reason, now)
	a.StateReason = reason
	m.alarmMu.Unlock()

	m.deliver([]*incidentFire{fire})

	return nil
}

// SetAlarmActions replaces an alert policy's notification targets (AlarmActions)
// in place, preserving the alarm's current State/StateReason/history and its
// evaluation config. alertPolicies.patch uses this to re-sync a changed
// notificationChannels list onto the backing alarm without the state reset a
// CreateAlarm would cause; a subsequent breach then delivers to the new channel
// set. actions is copied defensively.
func (m *Mock) SetAlarmActions(_ context.Context, name string, actions []string) error {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	a, ok := m.alarms.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "alert policy %q not found", name)
	}

	a.AlarmActions = append([]string{}, actions...)

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

// UpdateNotificationChannel applies apply to a copy of the stored channel and
// persists it, keeping the channel's stable ID (and thus its resource name).
// It backs the wire layer's notificationChannels.patch. Mutating a copy rather
// than the stored pointer avoids racing a concurrent Get/List that shares it.
func (m *Mock) UpdateNotificationChannel(
	_ context.Context, id string, apply func(*driver.NotificationChannelInfo),
) (*driver.NotificationChannelInfo, error) {
	existing, ok := m.channels.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "notification channel %q not found", id)
	}

	updated := *existing

	if existing.Tags != nil {
		tags := make(map[string]string, len(existing.Tags))
		for k, v := range existing.Tags {
			tags[k] = v
		}

		updated.Tags = tags
	}

	apply(&updated)
	m.channels.Set(id, &updated)

	return &updated, nil
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

// GetAlarmHistory returns an alert policy's history entries newest-first (matching
// CloudWatch's default TimestampDescending order); when limit > 0 it keeps the
// newest limit entries.
func (m *Mock) GetAlarmHistory(_ context.Context, alarmName string, limit int) ([]driver.AlarmHistoryEntry, error) {
	m.evaluateDue(m.opts.Clock.Now())

	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []driver.AlarmHistoryEntry

	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].AlarmName == alarmName {
			filtered = append(filtered, m.history[i])
		}
	}

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered, nil
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
	dims := make(map[string]string, len(a.Dimensions))
	for k, v := range a.Dimensions {
		dims[k] = v
	}

	return driver.AlarmInfo{
		Name:                       a.Name,
		Namespace:                  a.Namespace,
		MetricName:                 a.MetricName,
		State:                      a.State,
		ComparisonOperator:         a.ComparisonOperator,
		Threshold:                  a.Threshold,
		StateReason:                a.StateReason,
		StateUpdatedTimestamp:      a.StateUpdatedTimestamp,
		StateTransitionedTimestamp: a.StateTransitionedTimestamp,
		Period:                     a.Period,
		EvaluationPeriods:          a.EvaluationPeriods,
		DatapointsToAlarm:          a.DatapointsToAlarm,
		Statistic:                  a.Stat,
		ExtendedStatistic:          a.ExtendedStatistic,
		Unit:                       a.Unit,
		TreatMissingData:           a.TreatMissingData,
		AlarmActions:               append([]string{}, a.AlarmActions...),
		OKActions:                  append([]string{}, a.OKActions...),
		InsufficientDataActions:    append([]string{}, a.InsufficientDataActions...),
		Dimensions:                 dims,
	}
}
