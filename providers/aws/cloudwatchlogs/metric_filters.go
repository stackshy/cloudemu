package cloudwatchlogs

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/filterpattern"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// PutMetricFilter limits, per the PutMetricFilter and MetricTransformation
// API references.
const (
	maxMetricFiltersPerGroup = 100
	maxFilterNameLength      = 512
	maxMetricNameLength      = 255
	maxMetricValueLength     = 100
	maxMetricDimensions      = 3
	maxDimensionLength       = 255
	// unitNone is the unit AWS assigns when a transformation omits one.
	unitNone = "None"
)

// validMetricUnits is the MetricTransformation unit enum.
//
//nolint:gochecknoglobals // fixed lookup table for a closed enum.
var validMetricUnits = map[string]bool{
	"Seconds": true, "Microseconds": true, "Milliseconds": true, "Bytes": true, "Kilobytes": true,
	"Megabytes": true, "Gigabytes": true, "Terabytes": true, "Bits": true, "Kilobits": true,
	"Megabits": true, "Gigabits": true, "Terabits": true, "Percent": true, "Count": true,
	"Bytes/Second": true, "Kilobytes/Second": true, "Megabytes/Second": true, "Gigabytes/Second": true,
	"Terabytes/Second": true, "Bits/Second": true, "Kilobits/Second": true, "Megabits/Second": true,
	"Gigabits/Second": true, "Terabits/Second": true, "Count/Second": true, unitNone: true,
}

// PutMetricFilter creates or updates a metric filter for a log group. The
// filter, its pattern and its single metric transformation are validated
// the way CloudWatch Logs validates them before anything is stored.
func (m *Mock) PutMetricFilter(_ context.Context, cfg *driver.MetricFilterConfig) error {
	g, ok := m.groups.Get(cfg.LogGroup)
	if !ok {
		return errors.Newf(errors.NotFound, "log group %q not found", cfg.LogGroup)
	}

	if err := validateFilterName(cfg.Name); err != nil {
		return err
	}

	if err := validateFilterPattern(g, metricFilterKind, cfg.Name, cfg.FilterPattern); err != nil {
		return err
	}

	pattern, err := filterpattern.Parse(cfg.FilterPattern)
	if err != nil {
		return err
	}

	if err := validateTransformation(cfg, pattern); err != nil {
		return err
	}

	if !g.metricFilters.Has(cfg.Name) && g.metricFilters.Len() >= maxMetricFiltersPerGroup {
		return errors.Newf(errors.ResourceExhausted,
			"log group %q already has the maximum of %d metric filters", cfg.LogGroup, maxMetricFiltersPerGroup)
	}

	unit := cfg.Unit
	if unit == "" {
		unit = unitNone
	}

	g.metricFilters.Set(cfg.Name, &driver.MetricFilterInfo{
		Name:            cfg.Name,
		LogGroup:        cfg.LogGroup,
		FilterPattern:   cfg.FilterPattern,
		MetricName:      cfg.MetricName,
		MetricNamespace: cfg.MetricNamespace,
		MetricValue:     cfg.MetricValue,
		DefaultValue:    cfg.DefaultValue,
		Unit:            unit,
		Dimensions:      maps.Clone(cfg.Dimensions),
		CreatedAt:       m.opts.Clock.Now().UTC(),
	})

	return nil
}

func validateFilterName(name string) error {
	if name == "" || len(name) > maxFilterNameLength || strings.ContainsAny(name, ":*") {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'filterName' failed to satisfy constraint: "+
				"Member must have length between 1 and %d and satisfy regular expression pattern: [^:*]*",
			name, maxFilterNameLength)
	}

	return nil
}

// validateTransformation applies the MetricTransformation rules: a metric
// name and namespace, a metric value that is a number or a field the pattern
// extracts, at most three dimensions taken from pattern fields, no default
// value alongside dimensions, and a known unit.
func validateTransformation(cfg *driver.MetricFilterConfig, pattern *filterpattern.Pattern) error {
	for field, v := range map[string]string{"metricName": cfg.MetricName, "metricNamespace": cfg.MetricNamespace} {
		if v == "" || len(v) > maxMetricNameLength || strings.ContainsAny(v, ":*$") {
			return errors.Newf(errors.InvalidArgument,
				"1 validation error detected: Value '%s' at 'metricTransformations.1.member.%s' failed to satisfy constraint: "+
					"Member must have length between 1 and %d and satisfy regular expression pattern: [^:*$]*",
				v, field, maxMetricNameLength)
		}
	}

	if err := validateMetricValue(cfg.MetricValue, pattern); err != nil {
		return err
	}

	if cfg.Unit != "" && !validMetricUnits[cfg.Unit] {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'metricTransformations.1.member.unit' failed to satisfy "+
				"constraint: Member must satisfy enum value set", cfg.Unit)
	}

	return validateDimensions(cfg, pattern)
}

func validateMetricValue(value string, pattern *filterpattern.Pattern) error {
	if value == "" || len(value) > maxMetricValueLength {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'metricTransformations.1.member.metricValue' failed to "+
				"satisfy constraint: Member must have length between 1 and %d", value, maxMetricValueLength)
	}

	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return nil
	}

	if strings.HasPrefix(value, "$") && pattern.ValidRef(value) {
		return nil
	}

	return errors.Newf(errors.InvalidArgument,
		"Invalid metric value '%s': use a number or a field the filter pattern selects", value)
}

func validateDimensions(cfg *driver.MetricFilterConfig, pattern *filterpattern.Pattern) error {
	if len(cfg.Dimensions) == 0 {
		return nil
	}

	switch {
	case len(cfg.Dimensions) > maxMetricDimensions:
		return errors.Newf(errors.InvalidArgument,
			"A metric filter can have at most %d dimensions", maxMetricDimensions)
	case cfg.DefaultValue != nil:
		return errors.New(errors.InvalidArgument,
			"A metric filter with dimensions cannot have a default value")
	case !pattern.HasFields():
		return errors.New(errors.InvalidArgument,
			"Dimensions are only supported for JSON and space-delimited filter patterns")
	}

	for k, v := range cfg.Dimensions {
		if k == "" || len(k) > maxDimensionLength || len(v) > maxDimensionLength || !pattern.ValidRef(v) {
			return errors.Newf(errors.InvalidArgument,
				"Invalid dimension %s=%s: the value must be a field the filter pattern selects", k, v)
		}
	}

	return nil
}

// DescribeAWSMetricFilters lists metric filters, ASCII-sorted by filter name.
// Without a log group it lists every group's filters, and the name prefix is
// ignored, as CloudWatch Logs documents.
func (m *Mock) DescribeAWSMetricFilters(
	_ context.Context, in driver.DescribeMetricFiltersInput,
) ([]driver.MetricFilterInfo, error) {
	if (in.MetricName == "") != (in.MetricNamespace == "") {
		return nil, errors.New(errors.InvalidArgument,
			"metricName and metricNamespace must be specified together")
	}

	groups := m.groups.SortedValues()

	if in.LogGroup != "" {
		g, ok := m.groups.Get(in.LogGroup)
		if !ok {
			return nil, errors.Newf(errors.NotFound, "log group %q not found", in.LogGroup)
		}

		groups = []*logGroup{g}
	}

	var out []driver.MetricFilterInfo

	for _, g := range groups {
		for _, mf := range g.metricFilters.SortedValues() {
			if metricFilterSelected(mf, in) {
				out = append(out, *mf)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

func metricFilterSelected(mf *driver.MetricFilterInfo, in driver.DescribeMetricFiltersInput) bool {
	if in.LogGroup != "" && in.FilterNamePrefix != "" && !strings.HasPrefix(mf.Name, in.FilterNamePrefix) {
		return false
	}

	return in.MetricName == "" || (mf.MetricName == in.MetricName && mf.MetricNamespace == in.MetricNamespace)
}

// DescribeMetricFilters lists a log group's metric filters, sorted by name.
func (m *Mock) DescribeMetricFilters(ctx context.Context, logGroup string) ([]driver.MetricFilterInfo, error) {
	out, err := m.DescribeAWSMetricFilters(ctx, driver.DescribeMetricFiltersInput{LogGroup: logGroup})
	if out == nil && err == nil {
		out = []driver.MetricFilterInfo{}
	}

	return out, err
}

// evaluateMetricFilters turns the events of one PutLogEvents batch into
// CloudWatch metric data, one PutMetricData call per metric filter.
func (m *Mock) evaluateMetricFilters(g *logGroup, events []driver.LogEvent) {
	if m.monitoring == nil {
		return
	}

	now := m.opts.Clock.Now()

	for _, mf := range g.metricFilters.SortedValues() {
		if data := metricFilterData(mf, events, now); len(data) > 0 {
			_ = m.monitoring.PutMetricData(context.Background(), data)
		}
	}
}

// metricFilterData sums the metric value of every matching event, one datum
// per distinct set of dimension values, with the filter's unit. When no event
// in the batch matches, a filter with a default value emits that instead.
// A matching event whose metric value or dimension fields cannot be resolved
// contributes nothing.
func metricFilterData(mf *driver.MetricFilterInfo, events []driver.LogEvent, now time.Time) []mondriver.MetricDatum {
	// Patterns are validated when the filter is put; one that no longer
	// parses (restored from an older snapshot) emits nothing.
	pattern, err := filterpattern.Parse(mf.FilterPattern)
	if err != nil {
		return nil
	}

	sums := map[string]*mondriver.MetricDatum{}
	matched := false

	for i := range events {
		msg := events[i].Message
		if !pattern.Match(msg) {
			continue
		}

		matched = true

		v, ok := metricFilterValue(mf.MetricValue, pattern, msg)
		if !ok {
			continue
		}

		dims, ok := metricDimensions(mf.Dimensions, pattern, msg)
		if !ok {
			continue
		}

		key := dimensionKey(dims)
		if d, seen := sums[key]; seen {
			d.Value += v
			continue
		}

		sums[key] = newMetricDatum(mf, v, dims, now)
	}

	if !matched && mf.DefaultValue != nil {
		return []mondriver.MetricDatum{*newMetricDatum(mf, *mf.DefaultValue, nil, now)}
	}

	out := make([]mondriver.MetricDatum, 0, len(sums))
	for _, k := range slices.Sorted(maps.Keys(sums)) {
		out = append(out, *sums[k])
	}

	return out
}

func newMetricDatum(mf *driver.MetricFilterInfo, v float64, dims map[string]string, now time.Time) *mondriver.MetricDatum {
	unit := mf.Unit
	if unit == "" {
		unit = unitNone
	}

	return &mondriver.MetricDatum{
		Namespace: mf.MetricNamespace, MetricName: mf.MetricName, Value: v, Unit: unit,
		Dimensions: dims, Timestamp: now,
	}
}

// metricDimensions resolves each dimension's field reference in message.
func metricDimensions(spec map[string]string, pattern *filterpattern.Pattern, message string) (map[string]string, bool) {
	if len(spec) == 0 {
		return nil, true
	}

	dims := make(map[string]string, len(spec))

	for name, ref := range spec {
		v, ok := pattern.Value(message, ref)
		if !ok || v == "" {
			return nil, false
		}

		dims[name] = v
	}

	return dims, true
}

func dimensionKey(dims map[string]string) string {
	var b strings.Builder

	for _, k := range slices.Sorted(maps.Keys(dims)) {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(dims[k])
		b.WriteByte(0)
	}

	return b.String()
}

// metricFilterValue resolves a metric filter's metricValue to the number one
// matching event contributes. A literal number is used directly; a "$field"
// reference ("$.latency", "$size", "$3") takes the value the pattern extracts
// from the event, and an event whose referenced value is missing or not a
// number contributes nothing.
func metricFilterValue(raw string, pattern *filterpattern.Pattern, message string) (float64, bool) {
	if v, err := strconv.ParseFloat(raw, 64); err == nil {
		return v, true
	}

	field, ok := pattern.Value(message, raw)
	if !ok {
		return 0, false
	}

	v, err := strconv.ParseFloat(field, 64)

	return v, err == nil
}
