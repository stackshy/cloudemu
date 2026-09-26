// Package cloudwatch provides an in-memory mock implementation of AWS CloudWatch.
package cloudwatch

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time check that Mock implements driver.Monitoring.
var _ driver.Monitoring = (*Mock)(nil)

// snsTopicARNPrefix identifies an alarm action that targets an SNS topic. Only
// these actions are delivered to a notification publisher; other action ARNs
// (Auto Scaling, EC2, etc.) are recorded but not fired.
const snsTopicARNPrefix = "arn:aws:sns:"

// Alarm states, matching the CloudWatch StateValue enum.
const (
	stateAlarm            = "ALARM"
	stateOK               = "OK"
	stateInsufficientData = "INSUFFICIENT_DATA"
)

// reasonDataTimeFormat is the timestamp layout CloudWatch uses in stateReasonData.
const reasonDataTimeFormat = "2006-01-02T15:04:05.000-0700"

// historyStateUpdate is the HistoryItemType stamped on a recorded state change.
const historyStateUpdate = "StateUpdate"

// ActionPublisher publishes an alarm-state-change notification to an SNS topic
// by ARN. It is satisfied by the SNS backend's PublishExternal, mirroring the
// S3 -> SNS notification wiring, so an alarm transition fans a message out to
// the topic's subscribers.
type ActionPublisher interface {
	PublishExternal(ctx context.Context, topicARN, message string) error
}

// metricKey uniquely identifies a metric series by namespace, name, and dimensions.
type metricKey struct {
	Namespace  string
	MetricName string
}

// Mock is an in-memory mock implementation of the AWS CloudWatch service.
type Mock struct {
	// alarmMu guards every alarm field and is taken before mu. SNS actions and
	// EventBridge events are published only after both are released.
	alarmMu         sync.Mutex
	mu              sync.RWMutex
	metrics         map[metricKey][]driver.MetricDatum
	alarms          *memstore.Store[*alarmData]
	compositeAlarms *memstore.Store[*compositeAlarmData]
	dashboards      *memstore.Store[*storedDashboard]
	metricStreams   *memstore.Store[*storedMetricStream]
	channels        *memstore.Store[*driver.NotificationChannelInfo]
	history         []driver.AlarmHistoryEntry
	opts            *config.Options
	sns             ActionPublisher
	events          awsevents.Emitter
}

// SetSNSPublisher wires the SNS backend so an alarm state transition delivers
// its configured actions (AlarmActions / OKActions / InsufficientDataActions)
// to the SNS topics they name. Nil (the default) leaves actions un-fired.
func (m *Mock) SetSNSPublisher(p ActionPublisher) {
	m.sns = p
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
	DatapointsToAlarm       int
	Stat                    string
	ExtendedStatistic       string
	Unit                    string
	TreatMissingData        string
	State                   string
	StateReason             string
	StateReasonData         string
	StateUpdatedTimestamp   time.Time
	AlarmActions            []string
	OKActions               []string
	InsufficientDataActions []string
	AlarmDescription        string
	ActionsEnabled          bool
	AlarmArn                string
	Tags                    map[string]string
	// StateTransitionedTimestamp is when State last changed.
	StateTransitionedTimestamp time.Time
	// LastEvaluatedAt is when the alarm was last evaluated or had its state
	// set. The next lazy evaluation is due one EvaluationInterval later.
	LastEvaluatedAt time.Time
	// MetricQueryID is the id of the alarm's metric query in its state change
	// events. A new configuration gets a new id, as on AWS.
	MetricQueryID string
}

// New creates a new CloudWatch mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		metrics:         make(map[metricKey][]driver.MetricDatum),
		alarms:          memstore.New[*alarmData](),
		compositeAlarms: memstore.New[*compositeAlarmData](),
		dashboards:      memstore.New[*storedDashboard](),
		metricStreams:   memstore.New[*storedMetricStream](),
		channels:        memstore.New[*driver.NotificationChannelInfo](),
		opts:            opts,
	}
}

// PutMetricData stores metric data points and evaluates any matching alarms.
func (m *Mock) PutMetricData(ctx context.Context, data []driver.MetricDatum) error {
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

	// New data re-evaluates the alarms on each updated metric right away.
	seen := make(map[metricKey]bool)

	for i := range data {
		seen[metricKey{Namespace: data[i].Namespace, MetricName: data[i].MetricName}] = true
	}

	m.evaluateMetricAlarms(ctx, seen)

	return nil
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

	filtered := filterDatums(m.metrics[key], &input)

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

// filterDatums keeps the datums inside the query's time range that match its
// dimensions and unit.
func filterDatums(dataPoints []driver.MetricDatum, in *driver.GetMetricInput) []driver.MetricDatum {
	var filtered []driver.MetricDatum

	for i := range dataPoints {
		d := &dataPoints[i]
		if d.Timestamp.Before(in.StartTime) || !d.Timestamp.Before(in.EndTime) {
			continue
		}

		if !alarmeval.MatchDimensions(d.Dimensions, in.Dimensions) || !alarmeval.MatchUnit(d.Unit, in.Unit) {
			continue
		}

		filtered = append(filtered, *d)
	}

	return filtered
}

// MetricUnits returns the sorted distinct units of the data a query would
// read, with a unit-less datum counted as None. Unit in the query is ignored.
// GetMetricStatistics uses it to return one datapoint per unit, as AWS does
// when the caller omits Unit.
func (m *Mock) MetricUnits(_ context.Context, in *driver.GetMetricInput) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	anyUnit := *in
	anyUnit.Unit = ""

	seen := map[string]bool{}
	units := []string{}

	data := filterDatums(m.metrics[metricKey{Namespace: in.Namespace, MetricName: in.MetricName}], &anyUnit)
	for i := range data {
		u := alarmeval.EffectiveUnit(data[i].Unit)
		if seen[u] {
			continue
		}

		seen[u] = true

		units = append(units, u)
	}

	sort.Strings(units)

	return units
}

func buildMetricResult(filtered []driver.MetricDatum, startTime, endTime time.Time, period int, stat string) *driver.MetricDataResult {
	result := &driver.MetricDataResult{}

	if len(filtered) == 0 {
		result.Timestamps = []time.Time{}
		result.Values = []float64{}

		return result
	}

	// Carry the stored unit so the wire layer can echo it. Data put without a
	// unit reads back as None, like on AWS.
	result.Unit = alarmeval.EffectiveUnit(unitOf(filtered))

	periodDur := time.Duration(period) * time.Second

	// Walk through periods from StartTime to EndTime.
	for periodStart := startTime; periodStart.Before(endTime); periodStart = periodStart.Add(periodDur) {
		periodEnd := periodStart.Add(periodDur)
		periodDatums := collectPeriodDatums(filtered, periodStart, periodEnd)

		if len(periodDatums) == 0 {
			continue
		}

		s := alarmeval.StatOf(periodDatums, stat)

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

// GetAlarmHistory returns an alarm's history entries newest-first (CloudWatch's
// default TimestampDescending order). When limit > 0 it keeps the newest limit
// entries. Passing limit <= 0 returns the full history so a caller can apply its
// own filters before truncating. Alarms that are due are evaluated first.
func (m *Mock) GetAlarmHistory(ctx context.Context, alarmName string, limit int) ([]driver.AlarmHistoryEntry, error) {
	m.evaluateDue(ctx, m.opts.Clock.Now())

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

// SetAlarmActionsEnabled toggles ActionsEnabled for the named alarms. It backs
// the AWS-local EnableAlarmActions / DisableAlarmActions wire operations.
func (m *Mock) SetAlarmActionsEnabled(_ context.Context, names []string, enabled bool) error {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	for _, name := range names {
		a, ok := m.alarms.Get(name)
		if !ok {
			return errors.Newf(errors.NotFound, "alarm %q not found", name)
		}

		a.ActionsEnabled = enabled
	}

	return nil
}

// alarmTagsOf resolves an alarm name to its tag map. Metric and composite
// alarms share the same ARN shape (arn:...:alarm:NAME), so a TagResource /
// ListTagsForResource call carries no hint of which store holds the alarm; this
// looks in both, matching real CloudWatch where one tagging API serves both
// alarm types. When ensure is true a nil map is initialized in place (through
// the store's struct pointer) so the returned map is safe to write to.
func (m *Mock) alarmTagsOf(name string, ensure bool) (map[string]string, bool) {
	if a, ok := m.alarms.Get(name); ok {
		if a.Tags == nil && ensure {
			a.Tags = map[string]string{}
		}

		return a.Tags, true
	}

	if c, ok := m.compositeAlarms.Get(name); ok {
		if c.Tags == nil && ensure {
			c.Tags = map[string]string{}
		}

		return c.Tags, true
	}

	return nil, false
}

// AddAlarmTags merges tags onto the named alarm (metric or composite), backing
// TagResource.
func (m *Mock) AddAlarmTags(_ context.Context, alarmName string, tags map[string]string) error {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	target, ok := m.alarmTagsOf(alarmName, true)
	if !ok {
		return errors.Newf(errors.NotFound, "alarm %q not found", alarmName)
	}

	for k, v := range tags {
		target[k] = v
	}

	return nil
}

// RemoveAlarmTags deletes the given tag keys from the named alarm (metric or
// composite), backing UntagResource.
func (m *Mock) RemoveAlarmTags(_ context.Context, alarmName string, keys []string) error {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	target, ok := m.alarmTagsOf(alarmName, false)
	if !ok {
		return errors.Newf(errors.NotFound, "alarm %q not found", alarmName)
	}

	for _, k := range keys {
		delete(target, k)
	}

	return nil
}

// AlarmTags returns a copy of the named alarm's tags (metric or composite),
// backing ListTagsForResource.
func (m *Mock) AlarmTags(_ context.Context, alarmName string) (map[string]string, error) {
	m.alarmMu.Lock()
	defer m.alarmMu.Unlock()

	target, ok := m.alarmTagsOf(alarmName, false)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "alarm %q not found", alarmName)
	}

	return copyDims(target), nil
}

func toAlarmInfo(a *alarmData) driver.AlarmInfo {
	dims := make(map[string]string, len(a.Dimensions))
	for k, v := range a.Dimensions {
		dims[k] = v
	}

	tags := make(map[string]string, len(a.Tags))
	for k, v := range a.Tags {
		tags[k] = v
	}

	return driver.AlarmInfo{
		Name:                       a.Name,
		Namespace:                  a.Namespace,
		MetricName:                 a.MetricName,
		State:                      a.State,
		ComparisonOperator:         a.ComparisonOperator,
		Threshold:                  a.Threshold,
		StateReason:                a.StateReason,
		StateReasonData:            a.StateReasonData,
		StateUpdatedTimestamp:      a.StateUpdatedTimestamp,
		StateTransitionedTimestamp: a.StateTransitionedTimestamp,
		Period:                     a.Period,
		EvaluationPeriods:          a.EvaluationPeriods,
		DatapointsToAlarm:          a.DatapointsToAlarm,
		Statistic:                  a.Stat,
		ExtendedStatistic:          a.ExtendedStatistic,
		Unit:                       a.Unit,
		TreatMissingData:           a.TreatMissingData,
		ActionsEnabled:             a.ActionsEnabled,
		AlarmActions:               append([]string{}, a.AlarmActions...),
		OKActions:                  append([]string{}, a.OKActions...),
		InsufficientDataActions:    append([]string{}, a.InsufficientDataActions...),
		AlarmDescription:           a.AlarmDescription,
		AlarmArn:                   a.AlarmArn,
		Dimensions:                 dims,
		Tags:                       tags,
	}
}
