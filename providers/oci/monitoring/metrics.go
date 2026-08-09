package monitoring

import (
	"context"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	// defaultPeriod is the aggregation interval, in seconds, applied when a
	// query names none.
	defaultPeriod = 60
	// minResolution is the finest interval that still forms a non-empty bucket.
	minResolution   = time.Second
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

	step, err := resolutionOf(sel.interval, query.Resolution)
	if err != nil {
		return nil, err
	}

	filter := driver.OCIMetricFilter{
		Namespace:     query.Namespace,
		ResourceGroup: query.ResourceGroup,
		Name:          sel.metricName,
		Dimensions:    mergeTags(query.Dimensions, sel.dimensions),
	}

	start, end := m.window(query.StartTime, query.EndTime)
	selected := m.selectSeries(compartmentID, filter)
	out := make([]driver.OCIMetric, 0, len(selected))

	for _, s := range selected {
		stamps, values := aggregate(m.pointsOf(s), start, end, step, sel.stat)
		if len(stamps) == 0 {
			continue
		}

		out = append(out, driver.OCIMetric{
			CompartmentID: compartmentID,
			Namespace:     s.namespace,
			ResourceGroup: s.resourceGroup,
			Name:          s.name,
			Dimensions:    copyTags(s.dimensions),
			Resolution:    resolutionLabel(step),
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

// resolutionOf returns the interval a summarize query aggregates by, preferring
// an explicit resolution over the selector's own. Anything finer than
// minResolution would collapse to a zero-width bucket, so it is refused.
func resolutionOf(interval time.Duration, resolution string) (time.Duration, error) {
	step := interval
	if resolution != "" {
		step = alarmDuration(resolution)
	}

	if step < minResolution {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"resolution %s is finer than the %s minimum", step, minResolution)
	}

	return step, nil
}

// aggregate buckets samples into fixed periods and reduces each with stat. A
// step of zero or less would never advance the bucket, so it never gets one.
func aggregate(points []metricPoint, start, end time.Time, step time.Duration, stat string) (stamps []time.Time, values []float64) {
	if step <= 0 {
		step = defaultPeriod * time.Second
	}

	sort.Slice(points, func(i, j int) bool { return points[i].timestamp.Before(points[j].timestamp) })

	stamps = make([]time.Time, 0)
	values = make([]float64, 0)

	for bucket := start; bucket.Before(end); bucket = bucket.Add(step) {
		in := valuesIn(points, bucket, bucket.Add(step), false)
		if len(in) == 0 {
			continue
		}

		stamps = append(stamps, bucket.UTC())
		values = append(values, computeStat(in, stat))
	}

	return stamps, values
}

// valuesIn returns the samples between from and to. Buckets abut, so they
// exclude their end; an alarm window ends at now and must include it, or a
// sample posted at now is missing from its own evaluation.
func valuesIn(points []metricPoint, from, to time.Time, inclusiveEnd bool) []float64 {
	var out []float64

	for _, p := range points {
		if p.timestamp.Before(from) || p.timestamp.After(to) {
			continue
		}

		if !inclusiveEnd && p.timestamp.Equal(to) {
			continue
		}

		out = append(out, p.value)
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
