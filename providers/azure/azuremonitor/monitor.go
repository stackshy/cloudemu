// Package azuremonitor provides an in-memory mock implementation of Azure Monitor.
package azuremonitor

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/NitinKumar004/cloudemu/config"
	cerrors "github.com/NitinKumar004/cloudemu/errors"
	"github.com/NitinKumar004/cloudemu/internal/memstore"
	"github.com/NitinKumar004/cloudemu/monitoring/driver"
)

// Compile-time check that Mock implements driver.Monitoring.
var _ driver.Monitoring = (*Mock)(nil)

// metricKey uniquely identifies a metric series by namespace and name.
type metricKey struct {
	Namespace  string
	MetricName string
}

// alarmData holds the internal state of an Azure Monitor alert rule.
type alarmData struct {
	Name               string
	Namespace          string
	MetricName         string
	Dimensions         map[string]string
	ComparisonOperator string
	Threshold          float64
	Period             int
	EvaluationPeriods  int
	Stat               string
	State              string
	StateReason        string
}

// Mock is an in-memory mock implementation of the Azure Monitor service.
type Mock struct {
	mu      sync.RWMutex
	metrics map[metricKey][]driver.MetricDatum
	alarms  *memstore.Store[*alarmData]
	opts    *config.Options
}

// New creates a new Azure Monitor mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		metrics: make(map[metricKey][]driver.MetricDatum),
		alarms:  memstore.New[*alarmData](),
		opts:    opts,
	}
}

// PutMetricData stores metric data points.
func (m *Mock) PutMetricData(_ context.Context, data []driver.MetricDatum) error {
	if len(data) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "metric data is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range data {
		key := metricKey{
			Namespace:  d.Namespace,
			MetricName: d.MetricName,
		}
		m.metrics[key] = append(m.metrics[key], d)
	}

	return nil
}

// GetMetricData retrieves metric data for the given query, filtering by time range and
// computing the requested statistic.
func (m *Mock) GetMetricData(_ context.Context, input driver.GetMetricInput) (*driver.MetricDataResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := metricKey{
		Namespace:  input.Namespace,
		MetricName: input.MetricName,
	}

	dataPoints := m.metrics[key]

	// Filter by time range and dimensions.
	var filtered []driver.MetricDatum
	for _, d := range dataPoints {
		if d.Timestamp.Before(input.StartTime) || !d.Timestamp.Before(input.EndTime) {
			continue
		}
		if !matchDimensions(d.Dimensions, input.Dimensions) {
			continue
		}
		filtered = append(filtered, d)
	}

	// Sort by timestamp.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.Before(filtered[j].Timestamp)
	})

	// Group by period and compute stat.
	if input.Period <= 0 {
		input.Period = 60
	}

	periodDur := time.Duration(input.Period) * time.Second
	result := &driver.MetricDataResult{}

	if len(filtered) == 0 {
		result.Timestamps = []time.Time{}
		result.Values = []float64{}
		return result, nil
	}

	// Walk through periods from StartTime to EndTime.
	for periodStart := input.StartTime; periodStart.Before(input.EndTime); periodStart = periodStart.Add(periodDur) {
		periodEnd := periodStart.Add(periodDur)

		var periodValues []float64
		for _, d := range filtered {
			if !d.Timestamp.Before(periodStart) && d.Timestamp.Before(periodEnd) {
				periodValues = append(periodValues, d.Value)
			}
		}

		if len(periodValues) == 0 {
			continue
		}

		stat := computeStat(periodValues, input.Stat)
		result.Timestamps = append(result.Timestamps, periodStart)
		result.Values = append(result.Values, stat)
	}

	if result.Timestamps == nil {
		result.Timestamps = []time.Time{}
		result.Values = []float64{}
	}

	return result, nil
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

// CreateAlarm creates or updates a metric alert rule with the given configuration.
func (m *Mock) CreateAlarm(_ context.Context, cfg driver.AlarmConfig) error {
	if cfg.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "alarm name is required")
	}

	dims := make(map[string]string, len(cfg.Dimensions))
	for k, v := range cfg.Dimensions {
		dims[k] = v
	}

	alarm := &alarmData{
		Name:               cfg.Name,
		Namespace:          cfg.Namespace,
		MetricName:         cfg.MetricName,
		Dimensions:         dims,
		ComparisonOperator: cfg.ComparisonOperator,
		Threshold:          cfg.Threshold,
		Period:             cfg.Period,
		EvaluationPeriods:  cfg.EvaluationPeriods,
		Stat:               cfg.Stat,
		State:              "INSUFFICIENT_DATA",
	}

	m.alarms.Set(cfg.Name, alarm)
	return nil
}

// DeleteAlarm deletes the metric alert rule with the given name.
func (m *Mock) DeleteAlarm(_ context.Context, name string) error {
	if !m.alarms.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "alarm %q not found", name)
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

// SetAlarmState manually sets the state of a metric alert rule.
func (m *Mock) SetAlarmState(_ context.Context, name, state, reason string) error {
	a, ok := m.alarms.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "alarm %q not found", name)
	}

	a.State = state
	a.StateReason = reason
	return nil
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

// computeStat computes the requested statistic over a slice of values.
func computeStat(values []float64, stat string) float64 {
	if len(values) == 0 {
		return 0
	}

	switch stat {
	case "Sum":
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum
	case "Min", "Minimum":
		min := math.MaxFloat64
		for _, v := range values {
			if v < min {
				min = v
			}
		}
		return min
	case "Max", "Maximum":
		max := -math.MaxFloat64
		for _, v := range values {
			if v > max {
				max = v
			}
		}
		return max
	case "SampleCount":
		return float64(len(values))
	default: // "Average" or unspecified
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum / float64(len(values))
	}
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
