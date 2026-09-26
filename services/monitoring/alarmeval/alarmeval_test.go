package alarmeval_test

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
)

func TestMatchDimensions(t *testing.T) {
	tests := []struct {
		name   string
		data   map[string]string
		filter map[string]string
		want   bool
	}{
		{name: "both empty", want: true},
		{name: "exact single", data: map[string]string{"a": "1"}, filter: map[string]string{"a": "1"}, want: true},
		{name: "no-dim query excludes dimensioned", data: map[string]string{"a": "1"}, filter: nil, want: false},
		{name: "subset query excludes superset", data: map[string]string{"a": "1", "b": "2"}, filter: map[string]string{"a": "1"}, want: false},
		{name: "value mismatch", data: map[string]string{"a": "1"}, filter: map[string]string{"a": "2"}, want: false},
		{name: "same size different key", data: map[string]string{"a": "1"}, filter: map[string]string{"b": "1"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, alarmeval.MatchDimensions(tc.data, tc.filter))
		})
	}
}

func TestEvaluateComparison(t *testing.T) {
	assert.True(t, alarmeval.EvaluateComparison(80, "GreaterThanThreshold", 50))
	assert.False(t, alarmeval.EvaluateComparison(30, "GreaterThanThreshold", 50))
	assert.True(t, alarmeval.EvaluateComparison(50, "GreaterThanOrEqualToThreshold", 50))
	assert.True(t, alarmeval.EvaluateComparison(30, "LessThanThreshold", 50))
	assert.True(t, alarmeval.EvaluateComparison(50, "LessThanOrEqualToThreshold", 50))
	assert.False(t, alarmeval.EvaluateComparison(50, "Unknown", 50))
}

func TestStatOf(t *testing.T) {
	datums := []driver.MetricDatum{
		{Value: 10}, {Value: 20}, {Value: 30},
	}
	assert.Equal(t, 60.0, alarmeval.StatOf(datums, "Sum"))
	assert.Equal(t, 20.0, alarmeval.StatOf(datums, "Average"))
	assert.Equal(t, 10.0, alarmeval.StatOf(datums, "Minimum"))
	assert.Equal(t, 30.0, alarmeval.StatOf(datums, "Maximum"))
	assert.Equal(t, 3.0, alarmeval.StatOf(datums, "SampleCount"))
	assert.Equal(t, 0.0, alarmeval.StatOf(nil, "Average"))

	// StatisticValues and Values/Counts fold into the same accumulator.
	agg := []driver.MetricDatum{
		{StatisticValues: &driver.StatisticSet{SampleCount: 2, Sum: 30, Minimum: 10, Maximum: 20}},
		{Values: []float64{5}, Counts: []float64{4}},
	}
	assert.Equal(t, 50.0, alarmeval.StatOf(agg, "Sum"))
	assert.Equal(t, 6, int(alarmeval.StatOf(agg, "SampleCount")))
}

// TestEvaluateWindowMOfN exercises the M-of-N rule: only the most recent of three
// periods breaches, so a 3-of-3 alarm stays OK; all three breaching gives ALARM.
func TestEvaluateWindowMOfN(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	p := alarmeval.Params{
		Period: 60, EvaluationPeriods: 3, DatapointsToAlarm: 3,
		Stat: "Average", ComparisonOperator: "GreaterThanThreshold", Threshold: 10,
	}

	mk := func(vals ...float64) []driver.MetricDatum {
		out := make([]driver.MetricDatum, 0, len(vals))
		for i, v := range vals {
			age := time.Duration(len(vals)-1-i) * 60 * time.Second
			out = append(out, driver.MetricDatum{Value: v, Timestamp: now.Add(-age)})
		}

		return out
	}

	out := alarmeval.EvaluateWindow(mk(0, 0, 100), &p, now)
	assert.False(t, out.Retain)
	assert.Equal(t, alarmeval.StateOK, out.State, "1 of 3 periods breaching stays OK")

	out = alarmeval.EvaluateWindow(mk(100, 100, 100), &p, now)
	assert.Equal(t, alarmeval.StateAlarm, out.State, "3 of 3 periods breaching alarms")

	out = alarmeval.EvaluateWindow(mk(0, 0, 0), &p, now)
	assert.Equal(t, alarmeval.StateOK, out.State, "recovery to OK")
}

// TestRecentDatapoints: one value per non-empty period, oldest first.
func TestRecentDatapoints(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	p := alarmeval.Params{Period: 60, EvaluationPeriods: 3, Stat: "Sum"}

	datums := []driver.MetricDatum{
		{Value: 3, Timestamp: now},
		{Value: 1, Timestamp: now.Add(-120 * time.Second)},
		{Value: 1, Timestamp: now.Add(-130 * time.Second)},
		{Value: 9, Timestamp: now.Add(-10 * time.Minute)}, // outside the window
	}

	assert.Equal(t, []float64{2, 3}, alarmeval.RecentDatapoints(datums, &p, now))
	assert.Empty(t, alarmeval.RecentDatapoints(nil, &p, now))
}

func TestValidState(t *testing.T) {
	for _, s := range []string{"OK", "ALARM", "INSUFFICIENT_DATA"} {
		assert.True(t, alarmeval.ValidState(s), s)
	}

	for _, s := range []string{"", "BOGUS", "ok"} {
		assert.False(t, alarmeval.ValidState(s), s)
	}
}

// TestEvaluateWindowTreatMissingData checks the empty-period policies.
func TestEvaluateWindowTreatMissingData(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	base := alarmeval.Params{
		Period: 60, EvaluationPeriods: 3, DatapointsToAlarm: 3,
		Stat: "Average", ComparisonOperator: "GreaterThanThreshold", Threshold: 10,
	}

	// One breaching datapoint in the most recent period; the other two are missing.
	one := []driver.MetricDatum{{Value: 100, Timestamp: now}}

	// Default "missing": the two empty periods aren't counted, 1 < 3 -> OK.
	out := alarmeval.EvaluateWindow(one, &base, now)
	assert.False(t, out.Retain)
	assert.Equal(t, alarmeval.StateOK, out.State)

	// "breaching": empty periods count as breaching, 3 of 3 -> ALARM.
	breaching := base
	breaching.TreatMissingData = "breaching"
	assert.Equal(t, alarmeval.StateAlarm, alarmeval.EvaluateWindow(one, &breaching, now).State)
}

func TestWindowStart(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	p := alarmeval.Params{Period: 60, EvaluationPeriods: 5}
	assert.Equal(t, now.Add(-5*time.Minute), p.WindowStart(now))

	// Defaults: period 60, evalPeriods 1.
	def := alarmeval.Params{}
	assert.Equal(t, now.Add(-time.Minute), def.WindowStart(now))
}
