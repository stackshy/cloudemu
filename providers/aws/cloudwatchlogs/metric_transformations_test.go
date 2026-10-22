package cloudwatchlogs

import (
	"context"
	"fmt"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func metricFilter(name, pattern, value string) *driver.MetricFilterConfig {
	return &driver.MetricFilterConfig{
		Name: name, LogGroup: "g", FilterPattern: pattern,
		MetricName: "M", MetricNamespace: "App", MetricValue: value,
	}
}

func TestPutMetricFilterValidation(t *testing.T) {
	def := 0.0

	tests := []struct {
		name   string
		mutate func(c *driver.MetricFilterConfig)
		code   errors.Code
	}{
		{"name with colon", func(c *driver.MetricFilterConfig) { c.Name = "a:b" }, errors.InvalidArgument},
		{"missing metric name", func(c *driver.MetricFilterConfig) { c.MetricName = "" }, errors.InvalidArgument},
		{"namespace with $", func(c *driver.MetricFilterConfig) { c.MetricNamespace = "a$b" }, errors.InvalidArgument},
		{"missing metric value", func(c *driver.MetricFilterConfig) { c.MetricValue = "" }, errors.InvalidArgument},
		{"word metric value", func(c *driver.MetricFilterConfig) { c.MetricValue = "lots" }, errors.InvalidArgument},
		{"field value on unstructured pattern", func(c *driver.MetricFilterConfig) { c.MetricValue = "$size" }, errors.InvalidArgument},
		{"unknown field value", func(c *driver.MetricFilterConfig) {
			c.FilterPattern, c.MetricValue = "[ip, size]", "$bytes"
		}, errors.InvalidArgument},
		{"position past the pattern's fields", func(c *driver.MetricFilterConfig) {
			c.FilterPattern, c.MetricValue = "[ip, user, size]", "$4"
		}, errors.InvalidArgument},
		{"bad unit", func(c *driver.MetricFilterConfig) { c.Unit = "Apples" }, errors.InvalidArgument},
		{"dimensions on unstructured pattern", func(c *driver.MetricFilterConfig) {
			c.Dimensions = map[string]string{"Level": "$.level"}
		}, errors.InvalidArgument},
		{"four dimensions", func(c *driver.MetricFilterConfig) {
			c.FilterPattern = "[a, b, c, d]"
			c.Dimensions = map[string]string{"A": "$a", "B": "$b", "C": "$c", "D": "$d"}
		}, errors.InvalidArgument},
		{"dimensions with default value", func(c *driver.MetricFilterConfig) {
			c.FilterPattern, c.DefaultValue = `{ $.level = * }`, &def
			c.Dimensions = map[string]string{"Level": "$.level"}
		}, errors.InvalidArgument},
		{"dimension not a field", func(c *driver.MetricFilterConfig) {
			c.FilterPattern = `{ $.level = * }`
			c.Dimensions = map[string]string{"Level": "static"}
		}, errors.InvalidArgument},
		{"missing group", func(c *driver.MetricFilterConfig) { c.LogGroup = "nope" }, errors.NotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newWindowMock(t, 0)
			cfg := metricFilter("f", "ERROR", "1")
			tc.mutate(cfg)

			err := m.PutMetricFilter(context.Background(), cfg)
			require.Error(t, err)
			assert.Equal(t, tc.code, errors.GetCode(err), err.Error())
		})
	}
}

func TestPutMetricFilterAccepts(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)

	for i, cfg := range []*driver.MetricFilterConfig{
		metricFilter("literal", "ERROR", "2.5"),
		metricFilter("json", `{ $.latency > 0 }`, "$.latency"),
		metricFilter("delim-named", "[ip, user, size]", "$size"),
		metricFilter("delim-positional", "[..., size]", "$3"),
	} {
		cfg.Unit = "Milliseconds"
		require.NoError(t, m.PutMetricFilter(ctx, cfg), "case %d", i)
	}
}

func TestMetricFilterLimitPerGroup(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)

	for i := range maxMetricFiltersPerGroup {
		require.NoError(t, m.PutMetricFilter(ctx, metricFilter(fmt.Sprintf("f%03d", i), "ERROR", "1")))
	}

	err := m.PutMetricFilter(ctx, metricFilter("one-too-many", "ERROR", "1"))
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))

	// Updating an existing filter at the limit is fine.
	require.NoError(t, m.PutMetricFilter(ctx, metricFilter("f000", "WARN", "1")))
}

// inNamespace returns the recorded data of one namespace, leaving out the
// AWS/Logs ingestion metrics every upload emits.
func (r *recordingMonitoring) inNamespace(ns string) []mondriver.MetricDatum {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []mondriver.MetricDatum

	for _, d := range r.data {
		if d.Namespace == ns {
			out = append(out, d)
		}
	}

	return out
}

// TestMetricFilterDimensionsAndUnit publishes one datum per distinct set of
// dimension values, each with the filter's unit.
func TestMetricFilterDimensionsAndUnit(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)
	mon := &recordingMonitoring{}
	m.SetMonitoring(mon)

	cfg := metricFilter("latency", `{ $.latency > 0 }`, "$.latency")
	cfg.Unit = "Milliseconds"
	cfg.Dimensions = map[string]string{"Service": "$.service"}
	require.NoError(t, m.PutMetricFilter(ctx, cfg))

	putMessages(t, m,
		`{"service":"api","latency":10}`,
		`{"service":"api","latency":20}`,
		`{"service":"api","latency":30}`,
		`{"service":"web","latency":7}`,
		`{"latency":3}`,
	)

	data := mon.inNamespace("App")
	require.Len(t, data, 2, "the event without a service dimension is dropped")

	// Each match is its own observation: three samples, not one summed value.
	assert.Equal(t, map[string]string{"Service": "api"}, data[0].Dimensions)
	assert.Equal(t, &mondriver.StatisticSet{SampleCount: 3, Sum: 60, Minimum: 10, Maximum: 30}, data[0].StatisticValues)
	assert.Equal(t, map[string]string{"Service": "web"}, data[1].Dimensions)
	assert.Equal(t, &mondriver.StatisticSet{SampleCount: 1, Sum: 7, Minimum: 7, Maximum: 7}, data[1].StatisticValues)

	for _, d := range data {
		assert.Equal(t, "Milliseconds", d.Unit)
		assert.Equal(t, "App", d.Namespace)
	}
}

// TestMetricFilterDefaultValuePerPeriod publishes the default value at most
// once per one-minute period, and never for a period that had a match.
func TestMetricFilterDefaultValuePerPeriod(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)
	mon := &recordingMonitoring{}
	m.SetMonitoring(mon)

	def := 0.0
	cfg := metricFilter("errors", "ERROR", "1")
	cfg.DefaultValue = &def
	require.NoError(t, m.PutMetricFilter(ctx, cfg))

	// Minute 1: no match, then no match again: one default only.
	putMessages(t, m, "INFO ok")
	putMessages(t, m, "INFO fine")

	data := mon.inNamespace("App")
	require.Len(t, data, 1)
	assert.InDelta(t, 0.0, data[0].Value, 0)
	assert.Nil(t, data[0].StatisticValues)
	assert.Equal(t, "None", data[0].Unit)

	// Minute 2: a match, then a non-matching batch: no default for the period.
	advanceClock(t, m, metricPeriod)
	putMessages(t, m, "ERROR a")
	putMessages(t, m, "INFO b")

	data = mon.inNamespace("App")
	require.Len(t, data, 2)
	assert.InDelta(t, 1.0, data[1].StatisticValues.Sum, 0)

	// Minute 3: only non-matching logs, so the default is published again,
	// and it survives a snapshot round trip without repeating.
	advanceClock(t, m, metricPeriod)
	putMessages(t, m, "INFO c")
	require.Len(t, mon.inNamespace("App"), 3)

	raw, err := m.Snapshot(ctx, true)
	require.NoError(t, err)
	require.NoError(t, m.Restore(ctx, raw))

	putMessages(t, m, "INFO d")
	assert.Len(t, mon.inNamespace("App"), 3)
}

func TestDescribeAWSMetricFilters(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "other"})
	require.NoError(t, err)

	for _, cfg := range []*driver.MetricFilterConfig{
		metricFilter("b-errors", "ERROR", "1"),
		metricFilter("a-warn", "WARN", "1"),
	} {
		require.NoError(t, m.PutMetricFilter(ctx, cfg))
	}

	other := metricFilter("c-other", "ERROR", "1")
	other.LogGroup, other.MetricName = "other", "Other"
	require.NoError(t, m.PutMetricFilter(ctx, other))

	names := func(in driver.DescribeMetricFiltersInput) []string {
		got, derr := m.DescribeAWSMetricFilters(ctx, in)
		require.NoError(t, derr)

		out := make([]string, 0, len(got))
		for _, f := range got {
			out = append(out, f.Name)
		}

		return out
	}

	assert.Equal(t, []string{"a-warn", "b-errors", "c-other"}, names(driver.DescribeMetricFiltersInput{}))
	assert.Equal(t, []string{"a-warn", "b-errors"}, names(driver.DescribeMetricFiltersInput{LogGroup: "g"}))
	assert.Equal(t, []string{"b-errors"}, names(driver.DescribeMetricFiltersInput{LogGroup: "g", FilterNamePrefix: "b"}))
	assert.Equal(t, []string{"a-warn", "b-errors", "c-other"},
		names(driver.DescribeMetricFiltersInput{FilterNamePrefix: "b"}), "the prefix needs a log group")
	assert.Equal(t, []string{"c-other"},
		names(driver.DescribeMetricFiltersInput{MetricName: "Other", MetricNamespace: "App"}))

	_, err = m.DescribeAWSMetricFilters(ctx, driver.DescribeMetricFiltersInput{MetricName: "M"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.DescribeAWSMetricFilters(ctx, driver.DescribeMetricFiltersInput{LogGroup: "nope"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}
