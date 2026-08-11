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
	// minResolution is the finest interval real OCI aggregates at; a finer one
	// is a query it answers with a 400 rather than an empty result.
	minResolution   = time.Minute
	defaultLookback = 3 * time.Hour
)

// Metric shape limits real OCI enforces on PostMetricData, in characters.
const (
	maxNamespaceLength = 255
	maxNameLength      = 255
	maxDimensionLength = 256
)

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
		if err := validateDatum(&data[i]); err != nil {
			return err
		}
	}

	for i := range data {
		m.appendPoint(compartmentID, resourceGroup, &data[i])
	}

	m.evaluateAlarms(compartmentID)

	return nil
}

// ListOCIMetrics returns the metric identities recorded in a compartment.
func (m *Mock) ListOCIMetrics(
	_ context.Context, compartmentID string, filter OCIMetricFilter,
) ([]OCIMetric, error) {
	if compartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	selected := m.selectSeries(compartmentID, filter, nil)
	out := make([]OCIMetric, 0, len(selected))

	for _, s := range selected {
		out = append(out, OCIMetric{
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
	_ context.Context, compartmentID string, query OCIMetricQuery,
) ([]OCIMetric, error) {
	if compartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	sel, err := parseSelector(query.Query)
	if err != nil {
		return nil, err
	}

	step, err := resolutionOf(sel.interval, query.Resolution)
	if err != nil {
		return nil, err
	}

	filter := OCIMetricFilter{
		Namespace:     query.Namespace,
		ResourceGroup: query.ResourceGroup,
		Name:          sel.metricName,
		Dimensions:    query.Dimensions,
	}

	start, end := m.window(query.StartTime, query.EndTime)
	selected := m.selectSeries(compartmentID, filter, sel.dimensions)
	out := make([]OCIMetric, 0, len(selected))

	for _, s := range selected {
		stamps, values := aggregate(m.pointsOf(s), start, end, step, sel.stat)
		if len(stamps) == 0 {
			continue
		}

		out = append(out, OCIMetric{
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

// selectSeries returns the series in a compartment matching the filter and
// every dimension predicate a query's selector carried.
func (m *Mock) selectSeries(
	compartmentID string, filter OCIMetricFilter, preds []dimensionPredicate,
) []*metricSeries {
	want := scope.Scope{Compartment: compartmentID}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []*metricSeries

	for _, s := range m.series.SortedValues() {
		if !s.place.Matches(want) || !s.matches(filter, preds) {
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

func (s *metricSeries) matches(filter OCIMetricFilter, preds []dimensionPredicate) bool {
	switch {
	case filter.Namespace != "" && s.namespace != filter.Namespace:
		return false
	case filter.ResourceGroup != "" && s.resourceGroup != filter.ResourceGroup:
		return false
	case filter.Name != "" && s.name != filter.Name:
		return false
	}

	return s.matchesDimensions(filter.Dimensions, preds)
}

// matchesDimensions tests a series against a filter's equality dimensions and
// a selector's predicates, both of which must hold.
func (s *metricSeries) matchesDimensions(dimensions map[string]string, preds []dimensionPredicate) bool {
	for k, v := range dimensions {
		if s.dimensions[k] != v {
			return false
		}
	}

	for i := range preds {
		if !preds[i].matches(s.dimensions) {
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
// minResolution is a query real OCI refuses, so this one refuses it too.
func resolutionOf(interval time.Duration, resolution string) (time.Duration, error) {
	step := interval
	if resolution != "" {
		step = alarmDuration(resolution)
	}

	if step < minResolution {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"resolution %s is finer than OCI's %s minimum", step, resolutionLabel(minResolution))
	}

	return step, nil
}

// validateDatum rejects a data point real OCI would reject. Namespace and
// dimension shapes are checked; the metadata, per-request datapoint cap and
// ingestion time window are not.
func validateDatum(d *driver.MetricDatum) error {
	switch {
	case d.Namespace == "":
		return cerrors.New(cerrors.InvalidArgument, "namespace is required")
	case len(d.Namespace) > maxNamespaceLength:
		return cerrors.Newf(cerrors.InvalidArgument, "namespace exceeds %d characters", maxNamespaceLength)
	case !validNamespace(d.Namespace):
		return cerrors.Newf(cerrors.InvalidArgument,
			"namespace %q must start with a letter and hold only letters, digits and underscores", d.Namespace)
	case reservedNamespace(d.Namespace):
		return cerrors.Newf(cerrors.InvalidArgument, "namespace %q uses a prefix Oracle reserves", d.Namespace)
	case d.MetricName == "":
		return cerrors.New(cerrors.InvalidArgument, "metric name is required")
	case len(d.MetricName) > maxNameLength:
		return cerrors.Newf(cerrors.InvalidArgument, "metric name exceeds %d characters", maxNameLength)
	}

	return validateDimensions(d.Dimensions)
}

// validateDimensions enforces OCI's dimension key and value shapes. A key
// excludes periods and spaces; neither key nor value may be empty.
func validateDimensions(dimensions map[string]string) error {
	for k, v := range dimensions {
		switch {
		case k == "" || v == "":
			return cerrors.New(cerrors.InvalidArgument, "dimension keys and values must not be empty")
		case len(k) > maxDimensionLength || len(v) > maxDimensionLength:
			return cerrors.Newf(cerrors.InvalidArgument, "dimension %q exceeds %d characters", k, maxDimensionLength)
		case strings.ContainsAny(k, ". "):
			return cerrors.Newf(cerrors.InvalidArgument, "dimension key %q must not hold a period or a space", k)
		}
	}

	return nil
}

// validNamespace reports OCI's namespace shape: a letter, then letters, digits
// or underscores.
func validNamespace(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r == '_' || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}

	return true
}

// reservedNamespace reports the prefixes OCI keeps for its own service metrics.
func reservedNamespace(s string) bool {
	lower := strings.ToLower(s)

	return strings.HasPrefix(lower, "oci_") || strings.HasPrefix(lower, "oracle_")
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
func mergeSeries(metrics []OCIMetric) *driver.MetricDataResult {
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
