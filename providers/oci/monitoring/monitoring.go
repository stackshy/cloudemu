// Package monitoring provides an in-memory mock implementation of OCI Monitoring.
package monitoring

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time checks that Mock implements the portable driver and OCI's
// compartment-scoped capability.
var (
	_ driver.Monitoring    = (*Mock)(nil)
	_ driver.OCIMonitoring = (*Mock)(nil)
)

// Alarm statuses OCI reports for an alarm.
const (
	StatusOK        = "OK"
	StatusFiring    = "FIRING"
	StatusSuspended = "SUSPENDED"
)

// lifecycleActive is the only alarm lifecycle state a synchronous mock reaches.
const lifecycleActive = "ACTIVE"

// Portable alarm states that have no OCI equivalent of the same name.
const (
	stateAlarm            = "ALARM"
	stateInsufficientData = "INSUFFICIENT_DATA"
)

// defaultResolution is the aggregation interval OCI applies when a caller names none.
const defaultResolution = "1m"

// Alarm severities OCI accepts.
const (
	severityCritical = "CRITICAL"
	severityError    = "ERROR"
	severityWarning  = "WARNING"
	severityInfo     = "INFO"
)

// alarmRecord is a stored alarm plus its state-change history.
type alarmRecord struct {
	id             string
	place          scope.Scope
	spec           driver.OCIAlarmSpec
	status         string
	lifecycleState string
	timeCreated    time.Time
	timeUpdated    time.Time
	timeTriggered  time.Time
	// breachSince is when the query first matched, zero while it does not. The
	// alarm fires once a breach has lasted the spec's pending duration.
	breachSince time.Time
	history     []driver.AlarmHistoryEntry
}

// Mock is an in-memory mock implementation of the OCI Monitoring service.
type Mock struct {
	mu       sync.RWMutex
	series   *memstore.Store[*metricSeries]
	alarms   *memstore.Store[*alarmRecord]
	channels *memstore.Store[*driver.NotificationChannelInfo]
	opts     *config.Options
}

// New creates a new OCI Monitoring mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		series:   memstore.New[*metricSeries](),
		alarms:   memstore.New[*alarmRecord](),
		channels: memstore.New[*driver.NotificationChannelInfo](),
		opts:     opts,
	}
}

// PutMetricData stores metric data points in the default compartment.
func (m *Mock) PutMetricData(ctx context.Context, data []driver.MetricDatum) error {
	return m.PostMetricData(ctx, m.opts.CompartmentID, "", data)
}

// GetMetricData aggregates the matching series in the default compartment into
// a single result. Dimensions travel in the query struct rather than the
// selector, so a value no MQL predicate can quote still filters exactly.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) GetMetricData(ctx context.Context, input driver.GetMetricInput) (*driver.MetricDataResult, error) {
	metrics, err := m.SummarizeOCIMetrics(ctx, m.opts.CompartmentID, driver.OCIMetricQuery{
		Namespace:  input.Namespace,
		Query:      formatSelector(input.MetricName, input.Stat, input.Period, nil),
		Dimensions: input.Dimensions,
		StartTime:  input.StartTime,
		EndTime:    input.EndTime,
	})
	if err != nil {
		return nil, err
	}

	return mergeSeries(metrics), nil
}

// ListMetrics returns the metric names recorded under namespace in the default
// compartment.
func (m *Mock) ListMetrics(ctx context.Context, namespace string) ([]string, error) {
	metrics, err := m.ListOCIMetrics(ctx, m.opts.CompartmentID, driver.OCIMetricFilter{Namespace: namespace})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(metrics))
	names := make([]string, 0, len(metrics))

	for i := range metrics {
		if seen[metrics[i].Name] {
			continue
		}

		seen[metrics[i].Name] = true

		names = append(names, metrics[i].Name)
	}

	return names, nil
}

// CreateAlarm creates an alarm in the default compartment, synthesizing the
// query the portable threshold configuration describes.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateAlarm(ctx context.Context, cfg driver.AlarmConfig) error {
	query, err := formatQuery(&cfg)
	if err != nil {
		return err
	}

	_, err = m.CreateOCIAlarm(ctx, driver.OCIAlarmSpec{
		DisplayName:     cfg.Name,
		CompartmentID:   m.opts.CompartmentID,
		Namespace:       cfg.Namespace,
		Query:           query,
		Resolution:      defaultResolution,
		PendingDuration: pendingFor(cfg.Period, cfg.EvaluationPeriods),
		Destinations:    destinations(&cfg),
		IsEnabled:       true,
	})

	return err
}

// pendingFor renders the portable evaluation window as OCI's pendingDuration.
// N breaching periods means the breach lasted N-1 periods before firing, so a
// single period keeps firing on the first breaching datapoint.
func pendingFor(period, evaluationPeriods int) string {
	if evaluationPeriods <= 1 {
		return ""
	}

	if period <= 0 {
		period = defaultPeriod
	}

	return fmt.Sprintf("PT%dS", period*(evaluationPeriods-1))
}

// destinations folds the portable per-state action lists into OCI's single
// destination list, which every transition of an alarm notifies.
func destinations(cfg *driver.AlarmConfig) []string {
	out := make([]string, 0, len(cfg.AlarmActions))
	seen := make(map[string]bool, len(cfg.AlarmActions))

	for _, list := range [][]string{cfg.AlarmActions, cfg.OKActions, cfg.InsufficientDataActions} {
		for _, d := range list {
			if seen[d] {
				continue
			}

			seen[d] = true

			out = append(out, d)
		}
	}

	return out
}

// DeleteAlarm deletes the alarm with the given display name.
func (m *Mock) DeleteAlarm(ctx context.Context, name string) error {
	a, err := m.findByDisplayName(name)
	if err != nil {
		return err
	}

	return m.DeleteOCIAlarm(ctx, a.id)
}

// DescribeAlarms returns alarms matching the given display names, or every
// alarm in the default compartment when names is empty.
func (m *Mock) DescribeAlarms(_ context.Context, names []string) ([]driver.AlarmInfo, error) {
	wanted := make(map[string]bool, len(names))
	for _, n := range names {
		wanted[n] = true
	}

	out := make([]driver.AlarmInfo, 0, len(names))

	for _, rec := range m.alarms.SortedValues() {
		if rec.place.Compartment != m.opts.CompartmentID {
			continue
		}

		a := m.snapshot(rec)
		if len(wanted) > 0 && !wanted[a.Spec.DisplayName] {
			continue
		}

		out = append(out, toAlarmInfo(a))
	}

	return out, nil
}

// SetAlarmState manually sets an alarm's status and records the transition.
func (m *Mock) SetAlarmState(_ context.Context, name, state, reason string) error {
	a, err := m.findByDisplayName(name)
	if err != nil {
		return err
	}

	m.transition(a, ociStatus(state), reason)

	return nil
}

// CreateNotificationChannel creates a notification topic and returns its info.
func (m *Mock) CreateNotificationChannel(
	_ context.Context, cfg driver.NotificationChannelConfig,
) (*driver.NotificationChannelInfo, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "notification channel name is required")
	}

	ch := &driver.NotificationChannelInfo{
		ID:       idgen.OCID("onstopic", m.opts.Realm, m.opts.OCIRegion()),
		Name:     cfg.Name,
		Type:     cfg.Type,
		Endpoint: cfg.Endpoint,
		Tags:     copyTags(cfg.Tags),
	}

	m.channels.Set(ch.ID, ch)

	return ch, nil
}

// DeleteNotificationChannel deletes the notification topic with the given OCID.
func (m *Mock) DeleteNotificationChannel(_ context.Context, id string) error {
	if !m.channels.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "notification channel %q not found", id)
	}

	return nil
}

// GetNotificationChannel returns the notification topic with the given OCID.
func (m *Mock) GetNotificationChannel(_ context.Context, id string) (*driver.NotificationChannelInfo, error) {
	ch, ok := m.channels.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "notification channel %q not found", id)
	}

	return ch, nil
}

// ListNotificationChannels returns every notification topic.
func (m *Mock) ListNotificationChannels(_ context.Context) ([]driver.NotificationChannelInfo, error) {
	all := m.channels.SortedValues()
	out := make([]driver.NotificationChannelInfo, 0, len(all))

	for _, ch := range all {
		out = append(out, *ch)
	}

	return out, nil
}

// GetAlarmHistory returns the state-change history of the alarm with the given
// display name.
func (m *Mock) GetAlarmHistory(ctx context.Context, alarmName string, limit int) ([]driver.AlarmHistoryEntry, error) {
	a, err := m.findByDisplayName(alarmName)
	if err != nil {
		return nil, err
	}

	return m.OCIAlarmHistory(ctx, a.id, limit)
}

// findByDisplayName resolves an alarm by the display name the portable API
// addresses it with, within the default compartment.
func (m *Mock) findByDisplayName(name string) (*alarmRecord, error) {
	rec := m.lookupByName(m.opts.CompartmentID, name)
	if rec == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "alarm %q not found", name)
	}

	return rec, nil
}

func toAlarmInfo(a *driver.OCIAlarm) driver.AlarmInfo {
	cond, _ := parseQuery(a.Spec.Query)

	return driver.AlarmInfo{
		Name:               a.Spec.DisplayName,
		Namespace:          a.Spec.Namespace,
		MetricName:         cond.metricName,
		State:              portableState(a.Status),
		ComparisonOperator: cond.operator,
		Threshold:          cond.threshold,
	}
}

// portableState maps an OCI alarm status onto the portable alarm state.
func portableState(status string) string {
	switch status {
	case StatusFiring:
		return stateAlarm
	case StatusSuspended:
		return stateInsufficientData
	default:
		return StatusOK
	}
}

// ociStatus maps a portable alarm state onto OCI's status.
func ociStatus(state string) string {
	switch state {
	case stateAlarm:
		return StatusFiring
	case StatusOK:
		return StatusOK
	default:
		return StatusSuspended
	}
}

func copyTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
