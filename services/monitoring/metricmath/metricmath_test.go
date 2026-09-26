package metricmath_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

var base = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test fixture

func at(minutes ...int) []time.Time {
	out := make([]time.Time, 0, len(minutes))
	for _, m := range minutes {
		out = append(out, base.Add(time.Duration(m)*time.Minute))
	}

	return out
}

func stat(name string, period int) *driver.MetricStat {
	return &driver.MetricStat{Namespace: "App", MetricName: name, Period: period, Stat: "Sum"}
}

func boolPtr(b bool) *bool { return &b }

// fixedFetcher serves canned series by metric name and records each fetch.
type fixedFetcher struct {
	series  map[string]metricmath.Series
	periods map[string][]int
}

func (f *fixedFetcher) fetch(ms *driver.MetricStat, period int) (metricmath.Series, error) {
	if f.periods == nil {
		f.periods = map[string][]int{}
	}

	f.periods[ms.MetricName] = append(f.periods[ms.MetricName], period)

	return f.series[ms.MetricName], nil
}

func TestEvaluateExpressions(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{
		"A": {Timestamps: at(0, 1, 2), Values: []float64{10, 20, 30}},
		"B": {Timestamps: at(0, 1, 2), Values: []float64{2, 4, 5}},
	}}

	tests := []struct {
		name string
		expr string
		want []float64
	}{
		{"scale", "m1*2", []float64{20, 40, 60}},
		{"precedence", "m1+m2*2", []float64{14, 28, 40}},
		{"parens", "(m1+m2)*2", []float64{24, 48, 70}},
		{"ratio percent", "m1/m2*100", []float64{500, 500, 600}},
		{"unary minus", "-m1", []float64{-10, -20, -30}},
		{"scalar left", "100-m1", []float64{90, 80, 70}},
		{"divide by zero", "m1/0", []float64{0, 0, 0}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			queries := []driver.MetricDataQuery{
				{ID: "m1", MetricStat: stat("A", 60), ReturnData: boolPtr(false)},
				{ID: "m2", MetricStat: stat("B", 60), ReturnData: boolPtr(false)},
				{ID: "e1", Expression: tc.expr},
			}

			s, err := metricmath.New(queries, f.fetch).Resolve("e1")
			require.NoError(t, err)
			assert.Equal(t, tc.want, s.Values)
			assert.Equal(t, at(0, 1, 2), s.Timestamps)
		})
	}
}

// TestSeriesAlignByTimestamp: a gap in one input drops that point and does
// not shift the later points onto the wrong partner.
func TestSeriesAlignByTimestamp(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{
		"Errors":   {Timestamps: at(0, 2), Values: []float64{5, 9}},
		"Requests": {Timestamps: at(0, 1, 2), Values: []float64{10, 20, 30}},
	}}

	queries := []driver.MetricDataQuery{
		{ID: "err", MetricStat: stat("Errors", 60)},
		{ID: "req", MetricStat: stat("Requests", 60)},
		{ID: "rate", Expression: "err/req*100"},
	}

	s, err := metricmath.New(queries, f.fetch).Resolve("rate")
	require.NoError(t, err)
	assert.Equal(t, at(0, 2), s.Timestamps)
	assert.Equal(t, []float64{50, 30}, s.Values)
}

func TestEmptyResults(t *testing.T) {
	f := &fixedFetcher{}

	tests := []struct {
		name    string
		queries []driver.MetricDataQuery
		id      string
	}{
		{"unknown id", nil, "nope"},
		{"unsupported syntax", []driver.MetricDataQuery{{ID: "e1", Expression: "FILL(m1, 0)"}}, "e1"},
		{"cycle", []driver.MetricDataQuery{{ID: "a", Expression: "b+1"}, {ID: "b", Expression: "a+1"}}, "a"},
		{"missing reference", []driver.MetricDataQuery{{ID: "e1", Expression: "m9*2"}}, "e1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := metricmath.New(tc.queries, f.fetch).Resolve(tc.id)
			require.NoError(t, err)
			assert.Empty(t, s.Values)
		})
	}
}

func TestConstantExpression(t *testing.T) {
	s, err := metricmath.New([]driver.MetricDataQuery{{ID: "c", Expression: "2*21"}}, (&fixedFetcher{}).fetch).Resolve("c")
	require.NoError(t, err)
	assert.Equal(t, []float64{42}, s.Values)
}

func TestFetchErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	fetch := func(*driver.MetricStat, int) (metricmath.Series, error) { return metricmath.Series{}, boom }

	queries := []driver.MetricDataQuery{{ID: "m1", MetricStat: stat("A", 60)}, {ID: "e1", Expression: "m1+1"}}

	_, err := metricmath.New(queries, fetch).Resolve("e1")
	assert.ErrorIs(t, err, boom)
}

// TestExpressionPeriodSetsInputPeriod: an expression with its own Period reads
// its inputs at that Period. Without one, each input keeps its own period.
func TestExpressionPeriodSetsInputPeriod(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{"A": {Timestamps: at(0), Values: []float64{1}}}}

	queries := []driver.MetricDataQuery{
		{ID: "m1", MetricStat: stat("A", 60)},
		{ID: "fast", Expression: "m1*2"},
		{ID: "slow", Expression: "m1*2", Period: 300},
		{ID: "nested", Expression: "fast+1", Period: 120},
	}

	ev := metricmath.New(queries, f.fetch)

	for _, id := range []string{"m1", "fast", "slow", "nested"} {
		_, err := ev.Resolve(id)
		require.NoError(t, err)
	}

	// m1 at its own 60 is fetched once and shared by "fast". "slow" and
	// "nested" read it again at 300 and 120.
	assert.Equal(t, []int{60, 300, 120}, f.periods["A"])
}

func TestReferences(t *testing.T) {
	ids, ok := metricmath.References("(m1 + m2) / m1 * 100")
	require.True(t, ok)
	assert.Equal(t, []string{"m1", "m2", "m1"}, ids)

	ids, ok = metricmath.References("ANOMALY_DETECTION_BAND(m1, 2)")
	require.True(t, ok)
	assert.Equal(t, []string{"m1"}, ids)

	_, ok = metricmath.References("FILL(m1, 0)")
	assert.False(t, ok)

	_, ok = metricmath.References("m1 +")
	assert.False(t, ok)
}

func TestWatchedAndPeriod(t *testing.T) {
	queries := []driver.MetricDataQuery{
		{ID: "m1", MetricStat: stat("A", 300), ReturnData: boolPtr(true)},
		{ID: "ad1", Expression: "ANOMALY_DETECTION_BAND(m1, 2)"},
		{ID: "m2", MetricStat: stat("B", 60), ReturnData: boolPtr(false)},
		{ID: "e1", Expression: "m1*2", ReturnData: boolPtr(false)},
	}

	watched := metricmath.Watched(queries, "ad1")
	require.Len(t, watched, 1)
	assert.Equal(t, "m1", watched[0].ID)

	assert.Len(t, metricmath.Watched(queries, ""), 2, "nil ReturnData counts as true")

	assert.Equal(t, 300, metricmath.Period(queries, &queries[0]))
	assert.Equal(t, 300, metricmath.Period(queries, &queries[3]), "an expression falls back to the first metric period")
	assert.Equal(t, 120, metricmath.Period(queries, &driver.MetricDataQuery{Expression: "m1", Period: 120}))
	assert.Equal(t, 0, metricmath.Period(nil, &driver.MetricDataQuery{Expression: "1"}))
}

// TestPeriodMixedInputs pins the fallback: an expression with no Period uses
// the largest period of the metrics it reads, not the first in the list.
func TestPeriodMixedInputs(t *testing.T) {
	queries := []driver.MetricDataQuery{
		{ID: "other", MetricStat: stat("C", 30)},
		{ID: "m1", MetricStat: stat("A", 60)},
		{ID: "m2", MetricStat: stat("B", 300)},
		{ID: "e1", Expression: "m1+m2"},
		{ID: "e2", Expression: "e1*2"},
	}

	assert.Equal(t, 300, metricmath.Period(queries, &queries[3]))
	assert.Equal(t, 300, metricmath.Period(queries, &queries[4]), "found through another expression")
}

func TestCycle(t *testing.T) {
	id, ok := metricmath.Cycle([]driver.MetricDataQuery{
		{ID: "m1", MetricStat: stat("A", 60)},
		{ID: "e1", Expression: "e2+m1"},
		{ID: "e2", Expression: "e1*2"},
	})
	require.True(t, ok)
	assert.Equal(t, "e1", id)

	_, ok = metricmath.Cycle([]driver.MetricDataQuery{
		{ID: "m1", MetricStat: stat("A", 60)},
		{ID: "e1", Expression: "m1+m1"},
		{ID: "e2", Expression: "e1*m1"},
	})
	assert.False(t, ok)

	_, ok = metricmath.Cycle([]driver.MetricDataQuery{{ID: "e1", Expression: "e1"}})
	assert.True(t, ok, "self reference")
}

func TestClone(t *testing.T) {
	in := []driver.MetricDataQuery{{ID: "m1", ReturnData: boolPtr(true), MetricStat: &driver.MetricStat{Dimensions: map[string]string{"k": "v"}}}}
	out := metricmath.Clone(in)

	*out[0].ReturnData = false
	out[0].MetricStat.Dimensions["k"] = "changed"

	assert.True(t, *in[0].ReturnData)
	assert.Equal(t, "v", in[0].MetricStat.Dimensions["k"])
	assert.Nil(t, metricmath.Clone(nil))
}
