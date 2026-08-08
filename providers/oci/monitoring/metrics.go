package monitoring

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Aggregation defaults, in seconds.
const (
	defaultPeriod   = 60
	secondsPerHour  = 3600
	secondsPerMin   = 60
	defaultLookback = 3 * time.Hour
)

// metricPoint is one recorded sample.
type metricPoint struct {
	timestamp time.Time
	value     float64
}

// metricSeries is every sample posted for one metric identity: a compartment,
// namespace, resource group, name and dimension set.
type metricSeries struct {
	place         scope.Scope
	namespace     string
	resourceGroup string
	name          string
	dimensions    map[string]string
	points        []metricPoint
}

// PostMetricData records metric data points against a compartment.
func (m *Mock) PostMetricData(_ context.Context, compartmentID, resourceGroup string, data []driver.MetricDatum) error {
	if compartmentID == "" {
		return cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	if len(data) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "metric data is required")
	}

	for i := range data {
		m.appendPoint(compartmentID, resourceGroup, &data[i])
	}

	m.evaluateAlarms(compartmentID)

	return nil
}

// ListOCIMetrics returns the metric identities recorded in a compartment.
func (m *Mock) ListOCIMetrics(
	_ context.Context, compartmentID string, filter driver.OCIMetricFilter,
) ([]driver.OCIMetric, error) {
	if compartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	selected := m.selectSeries(compartmentID, filter)
	out := make([]driver.OCIMetric, 0, len(selected))

	for _, s := range selected {
		out = append(out, driver.OCIMetric{
			CompartmentID: compartmentID,
			Namespace:     s.namespace,
			ResourceGroup: s.resourceGroup,
			Name:          s.name,
			Dimensions:    copyTags(s.dimensions),
		})
	}

	return out, nil
}

// SummarizeOCIMetrics aggregates each series matching the query into datapoints
// spaced by the query's resolution.
//
//nolint:gocritic // hugeParam: query is the request in full.
func (m *Mock) SummarizeOCIMetrics(
	_ context.Context, compartmentID string, query driver.OCIMetricQuery,
) ([]driver.OCIMetric, error) {
	if compartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	sel, ok := parseSelector(query.Query)
	if !ok {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "malformed query %q", query.Query)
	}

	filter := driver.OCIMetricFilter{
		Namespace:     query.Namespace,
		ResourceGroup: query.ResourceGroup,
		Name:          sel.metricName,
		Dimensions:    query.Dimensions,
	}

	period := int(sel.interval.Seconds())
	if query.Resolution != "" {
		period = int(alarmDuration(query.Resolution).Seconds())
	}

	start, end := m.window(query.StartTime, query.EndTime)
	selected := m.selectSeries(compartmentID, filter)
	out := make([]driver.OCIMetric, 0, len(selected))

	for _, s := range selected {
		stamps, values := aggregate(m.pointsOf(s), start, end, period, sel.stat)
		if len(stamps) == 0 {
			continue
		}

		out = append(out, driver.OCIMetric{
			CompartmentID: compartmentID,
			Namespace:     s.namespace,
			ResourceGroup: s.resourceGroup,
			Name:          s.name,
			Dimensions:    copyTags(s.dimensions),
			Resolution:    resolutionLabel(period),
			Timestamps:    stamps,
			Values:        values,
		})
	}

	return out, nil
}

// appendPoint adds one sample to its series, creating the series on first sight.
func (m *Mock) appendPoint(compartmentID, resourceGroup string, d *driver.MetricDatum) {
	key := seriesKey(compartmentID, resourceGroup, d)

	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.series.Get(key)
	if !ok {
		s = &metricSeries{
			place:         scope.Scope{Compartment: compartmentID},
			namespace:     d.Namespace,
			resourceGroup: resourceGroup,
			name:          d.MetricName,
			dimensions:    copyTags(d.Dimensions),
		}
		m.series.Set(key, s)
	}

	ts := d.Timestamp
	if ts.IsZero() {
		ts = m.opts.Clock.Now().UTC()
	}

	s.points = append(s.points, metricPoint{timestamp: ts, value: d.Value})
}

// selectSeries returns the series in a compartment matching the filter.
func (m *Mock) selectSeries(compartmentID string, filter driver.OCIMetricFilter) []*metricSeries {
	want := scope.Scope{Compartment: compartmentID}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []*metricSeries

	for _, s := range m.series.SortedValues() {
		if !s.place.Matches(want) || !s.matches(filter) {
			continue
		}

		out = append(out, s)
	}

	return out
}

// pointsOf copies a series' samples so callers read them without the lock.
func (m *Mock) pointsOf(s *metricSeries) []metricPoint {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return append([]metricPoint(nil), s.points...)
}

// window fills in the time range OCI defaults to when a caller omits one.
func (m *Mock) window(start, end time.Time) (from, to time.Time) {
	if end.IsZero() {
		end = m.opts.Clock.Now().UTC()
	}

	if start.IsZero() {
		start = end.Add(-defaultLookback)
	}

	return start, end
}

func (s *metricSeries) matches(filter driver.OCIMetricFilter) bool {
	if filter.Namespace != "" && s.namespace != filter.Namespace {
		return false
	}

	if filter.ResourceGroup != "" && s.resourceGroup != filter.ResourceGroup {
		return false
	}

	if filter.Name != "" && s.name != filter.Name {
		return false
	}

	for k, v := range filter.Dimensions {
		if s.dimensions[k] != v {
			return false
		}
	}

	return true
}

// seriesKey identifies a metric series. Dimensions are sorted so the same
// dimension set always lands on the same series.
func seriesKey(compartmentID, resourceGroup string, d *driver.MetricDatum) string {
	keys := make([]string, 0, len(d.Dimensions))
	for k := range d.Dimensions {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+d.Dimensions[k])
	}

	return strings.Join([]string{compartmentID, d.Namespace, resourceGroup, d.MetricName, strings.Join(parts, ",")}, "|")
}

// aggregate buckets samples into fixed periods and reduces each with stat.
func aggregate(points []metricPoint, start, end time.Time, period int, stat string) (stamps []time.Time, values []float64) {
	sort.Slice(points, func(i, j int) bool { return points[i].timestamp.Before(points[j].timestamp) })

	step := time.Duration(period) * time.Second
	stamps = make([]time.Time, 0)
	values = make([]float64, 0)

	for bucket := start; bucket.Before(end); bucket = bucket.Add(step) {
		in := valuesIn(points, bucket, bucket.Add(step))
		if len(in) == 0 {
			continue
		}

		stamps = append(stamps, bucket.UTC())
		values = append(values, computeStat(in, stat))
	}

	return stamps, values
}

func valuesIn(points []metricPoint, from, to time.Time) []float64 {
	var out []float64

	for _, p := range points {
		if !p.timestamp.Before(from) && p.timestamp.Before(to) {
			out = append(out, p.value)
		}
	}

	return out
}

// mergeSeries flattens summarized series into the portable single result.
func mergeSeries(metrics []driver.OCIMetric) *driver.MetricDataResult {
	result := &driver.MetricDataResult{Timestamps: []time.Time{}, Values: []float64{}}

	for i := range metrics {
		result.Timestamps = append(result.Timestamps, metrics[i].Timestamps...)
		result.Values = append(result.Values, metrics[i].Values...)
	}

	return result
}

// computeStat reduces a bucket of values with the requested statistic.
func computeStat(values []float64, stat string) float64 {
	if len(values) == 0 {
		return 0
	}

	switch stat {
	case statSum:
		return sumValues(values)
	case statMinimum:
		return reduce(values, func(cur, candidate float64) bool { return candidate < cur })
	case statMaximum:
		return reduce(values, func(cur, candidate float64) bool { return candidate > cur })
	case statCount:
		return float64(len(values))
	default: // Average or unspecified
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

// reduce returns the value for which better reports every other value worse.
func reduce(values []float64, better func(cur, candidate float64) bool) float64 {
	out := values[0]

	for _, v := range values[1:] {
		if better(out, v) {
			out = v
		}
	}

	return out
}

// resolutionLabel renders a period in seconds as OCI's interval notation.
func resolutionLabel(period int) string {
	switch {
	case period%secondsPerHour == 0:
		return fmt.Sprintf("%dh", period/secondsPerHour)
	case period%secondsPerMin == 0:
		return fmt.Sprintf("%dm", period/secondsPerMin)
	default:
		return fmt.Sprintf("%ds", period)
	}
}
