// Package cloudwatch provides an in-memory mock implementation of AWS CloudWatch.
package cloudwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
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
	mu              sync.RWMutex
	metrics         map[metricKey][]driver.MetricDatum
	alarms          *memstore.Store[*alarmData]
	compositeAlarms *memstore.Store[*compositeAlarmData]
	dashboards      *memstore.Store[*storedDashboard]
	channels        *memstore.Store[*driver.NotificationChannelInfo]
	history         []driver.AlarmHistoryEntry
	opts            *config.Options
	sns             ActionPublisher
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
		metrics:         make(map[metricKey][]driver.MetricDatum),
		alarms:          memstore.New[*alarmData](),
		compositeAlarms: memstore.New[*compositeAlarmData](),
		dashboards:      memstore.New[*storedDashboard](),
		channels:        memstore.New[*driver.NotificationChannelInfo](),
		opts:            opts,
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

func (m *Mock) evaluateAlarms(namespace, metricName string) {
	allAlarms := m.alarms.All()

	for _, alarm := range allAlarms {
		if alarm.Namespace != namespace || alarm.MetricName != metricName {
			continue
		}

		m.evaluateSingleAlarm(alarm, namespace, metricName)
	}
}

// alarmParams projects an alarm's thresholds onto the shared evaluator's Params.
func alarmParams(alarm *alarmData) alarmeval.Params {
	return alarmeval.Params{
		Period:             alarm.Period,
		EvaluationPeriods:  alarm.EvaluationPeriods,
		DatapointsToAlarm:  alarm.DatapointsToAlarm,
		Stat:               alarm.Stat,
		ComparisonOperator: alarm.ComparisonOperator,
		Threshold:          alarm.Threshold,
		TreatMissingData:   alarm.TreatMissingData,
	}
}

func (m *Mock) evaluateSingleAlarm(alarm *alarmData, namespace, metricName string) {
	now := m.opts.Clock.Now()
	params := alarmParams(alarm)

	filtered := m.collectFilteredDatums(namespace, metricName, alarm.Dimensions, params.WindowStart(now), now)
	if len(filtered) == 0 {
		return
	}

	newState, reason, ok := alarmeval.EvaluateWindow(filtered, &params, now)
	if !ok {
		return
	}

	m.transitionAlarm(alarm, newState, reason, now)
}

// transitionAlarm sets an alarm's state and — only when the state actually
// changes — records a history entry and fires the new state's actions. This
// matches CloudWatch, where both the history entry and the action invocation
// happen on a state change regardless of whether the change came from metric
// evaluation or a manual SetAlarmState. An alarm invokes its actions only when
// it changes state; it never re-fires while the state is steady.
func (m *Mock) transitionAlarm(alarm *alarmData, newState, reason string, now time.Time) {
	oldState := alarm.State

	if oldState != newState {
		m.appendHistory(alarm.Name, oldState, newState, reason, now)
		alarm.StateUpdatedTimestamp = now
	}

	alarm.State = newState
	alarm.StateReason = reason

	if oldState != newState {
		m.fireAlarmActions(alarm, oldState, newState, now)
	}
}

// appendHistory records one alarm state transition in the history log.
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

// fireAlarmActions delivers an alarm's state-change actions. Only SNS-topic
// action ARNs are published (via the wired publisher); the notification carries
// the alarm's new state so subscribers can react. It is a no-op when no
// publisher is wired or the alarm has actions disabled.
//
// The stateInsufficientData branch below is wired for completeness (and to
// keep the switch exhaustive over the alarm state enum) but is currently
// unreachable: evaluateSingleAlarm only ever assigns stateAlarm or stateOK,
// since alarm evaluation here is event-driven off incoming PutMetricData
// calls. Real CloudWatch instead transitions an alarm to INSUFFICIENT_DATA
// on a background timer when expected datapoints stop arriving — a
// timer-driven behavior this mock does not simulate.
func (m *Mock) fireAlarmActions(a *alarmData, oldState, newState string, now time.Time) {
	if m.sns == nil || !a.ActionsEnabled {
		return
	}

	var actions []string

	switch newState {
	case stateAlarm:
		actions = a.AlarmActions
	case stateOK:
		actions = a.OKActions
	case stateInsufficientData:
		actions = a.InsufficientDataActions
	}

	if len(actions) == 0 {
		return
	}

	message := m.alarmNotification(a, oldState, newState, now)

	for _, arn := range actions {
		if strings.HasPrefix(arn, snsTopicARNPrefix) {
			_ = m.sns.PublishExternal(context.Background(), arn, message)
		}
	}
}

// alarmNotification renders the JSON body CloudWatch publishes to an SNS topic
// on a state change. It mirrors the real notification's key fields so a
// subscriber (e.g. an SQS queue) receives a recognizable alarm payload.
func (m *Mock) alarmNotification(a *alarmData, oldState, newState string, now time.Time) string {
	payload := map[string]any{
		"AlarmName":        a.Name,
		"AlarmDescription": a.AlarmDescription,
		"AWSAccountId":     m.opts.AccountID,
		"Region":           m.opts.Region,
		"NewStateValue":    newState,
		"NewStateReason":   a.StateReason,
		"OldStateValue":    oldState,
		"StateChangeTime":  now.UTC().Format(time.RFC3339),
		"Trigger": map[string]any{
			"MetricName":         a.MetricName,
			"Namespace":          a.Namespace,
			"Statistic":          a.Stat,
			"ComparisonOperator": a.ComparisonOperator,
			"Threshold":          a.Threshold,
			"Period":             a.Period,
			"EvaluationPeriods":  a.EvaluationPeriods,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}

	return string(body)
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

		if !alarmeval.MatchDimensions(d.Dimensions, dims) {
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

		if !alarmeval.MatchDimensions(d.Dimensions, dims) {
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
	state := stateInsufficientData
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
		DatapointsToAlarm:       cfg.DatapointsToAlarm,
		Stat:                    cfg.Stat,
		ExtendedStatistic:       cfg.ExtendedStatistic,
		Unit:                    cfg.Unit,
		TreatMissingData:        cfg.TreatMissingData,
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

// SetAlarmState manually sets the state of an alarm. Like a metric-driven
// transition, a state change records a history entry and invokes the actions
// configured for the new state (AlarmActions / OKActions /
// InsufficientDataActions), so the documented "force ALARM to test wiring"
// workflow delivers its notifications.
func (m *Mock) SetAlarmState(_ context.Context, name, state, reason string) error {
	a, ok := m.alarms.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "alarm %q not found", name)
	}

	m.transitionAlarm(a, state, reason, m.opts.Clock.Now())

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

// GetAlarmHistory returns an alarm's history entries newest-first (CloudWatch's
// default TimestampDescending order). When limit > 0 it keeps the newest limit
// entries. Passing limit <= 0 returns the full history so a caller can apply its
// own filters before truncating.
func (m *Mock) GetAlarmHistory(_ context.Context, alarmName string, limit int) ([]driver.AlarmHistoryEntry, error) {
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
		DatapointsToAlarm:       a.DatapointsToAlarm,
		Statistic:               a.Stat,
		ExtendedStatistic:       a.ExtendedStatistic,
		Unit:                    a.Unit,
		TreatMissingData:        a.TreatMissingData,
		ActionsEnabled:          a.ActionsEnabled,
		AlarmActions:            append([]string{}, a.AlarmActions...),
		OKActions:               append([]string{}, a.OKActions...),
		InsufficientDataActions: append([]string{}, a.InsufficientDataActions...),
		AlarmDescription:        a.AlarmDescription,
		AlarmArn:                a.AlarmArn,
		Dimensions:              dims,
		Tags:                    tags,
	}
}
