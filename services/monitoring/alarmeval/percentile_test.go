package alarmeval_test

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oneToN returns the values 1..n.
func oneToN(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i + 1)
	}

	return out
}

func TestParseExtendedStatistic(t *testing.T) {
	valid := []string{
		"p0", "p0.0", "p50", "p99.9", "p99.9999999999", "p100",
		"tm90", "wm98", "tc90", "ts99.5",
		"IQM",
		"TM(10%:90%)", "TM(:95%)", "TM(10%:)", "TM(150:1000)", "WM(10%:90%)",
		"TC(0.005:0.030)", "TS(80%:)", "PR(:300)", "PR(100:2000)", "PR(10:)",
	}

	for _, s := range valid {
		_, err := alarmeval.ParseExtendedStatistic(s)
		assert.NoError(t, err, s)
	}

	invalid := []string{
		"", "p", "p101", "p-1", "P99", "p99.12345678901", "px",
		"tm0", "tm101", "TM90", "tm(10%:90%)",
		"TM(:)", "TM(90%:10%)", "TM(10%:900)", "TM(10%:190%)", "TM(500:100)",
		"PR(10%:90%)", "IQM(1:2)", "iqm", "Average", "Sum", "XX(1:2)",
	}

	for _, s := range invalid {
		_, err := alarmeval.ParseExtendedStatistic(s)
		assert.Error(t, err, s)
	}
}

func TestExtendedStatisticCompute(t *testing.T) {
	hundred := oneToN(100)

	tests := []struct {
		stat   string
		values []float64
		want   float64
	}{
		{"p50", hundred, 50},
		{"p90", hundred, 90},
		{"p99", hundred, 99},
		{"p99.9", hundred, 100},
		{"p0", hundred, 1},
		{"p100", hundred, 100},
		{"tm90", hundred, 45.5},
		{"TM(10%:90%)", hundred, 50.5},
		{"IQM", hundred, 50.5},
		{"TM(150:1000)", []float64{100, 150, 200, 1000, 2000}, 600},
		{"tc90", hundred, 90},
		{"TC(10:20)", hundred, 10},
		{"ts90", hundred, 4095},
		{"wm98", hundred, 50.47},
		{"WM(10%:90%)", hundred, 50.4},
		{"PR(10:20)", hundred, 10},
		{"PR(:300)", []float64{100, 300, 301, 500}, 50},
	}

	for _, tc := range tests {
		t.Run(tc.stat, func(t *testing.T) {
			e, err := alarmeval.ParseExtendedStatistic(tc.stat)
			require.NoError(t, err)

			got, ok := e.Compute(tc.values)
			require.True(t, ok)
			assert.InDelta(t, tc.want, got, 1e-9)
		})
	}
}

// Percentiles need raw, non-negative data.
func TestExtendedStatisticUnavailable(t *testing.T) {
	p90, err := alarmeval.ParseExtendedStatistic("p90")
	require.NoError(t, err)

	_, ok := p90.Compute([]float64{1, -2, 3})
	assert.False(t, ok, "a negative value makes percentiles unavailable")

	_, ok = p90.Compute(nil)
	assert.False(t, ok, "no data")

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	set := []driver.MetricDatum{{Timestamp: now, StatisticValues: &driver.StatisticSet{
		SampleCount: 10, Sum: 55, Minimum: 1, Maximum: 10,
	}}}

	_, ok = alarmeval.StatValue(set, "p90")
	assert.False(t, ok, "a statistic set with SampleCount > 1 and Min != Max")

	flat := []driver.MetricDatum{{Timestamp: now, StatisticValues: &driver.StatisticSet{
		SampleCount: 10, Sum: 70, Minimum: 7, Maximum: 7,
	}}}

	v, ok := alarmeval.StatValue(flat, "p90")
	require.True(t, ok, "Min == Max is usable")
	assert.InDelta(t, 7, v, 1e-9)

	single := []driver.MetricDatum{{Timestamp: now, StatisticValues: &driver.StatisticSet{
		SampleCount: 1, Sum: 4, Minimum: 4, Maximum: 4,
	}}}

	v, ok = alarmeval.StatValue(single, "p50")
	require.True(t, ok, "SampleCount 1 is usable")
	assert.InDelta(t, 4, v, 1e-9)
}

// Values/Counts arrays are weighted observations.
func TestStatValueWeighted(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	data := []driver.MetricDatum{{Timestamp: now, Values: []float64{1, 100}, Counts: []float64{98, 2}}}

	v, ok := alarmeval.StatValue(data, "p98")
	require.True(t, ok)
	assert.InDelta(t, 1, v, 1e-9)

	v, ok = alarmeval.StatValue(data, "p99")
	require.True(t, ok)
	assert.InDelta(t, 100, v, 1e-9)

	v, ok = alarmeval.StatValue(data, "Average")
	require.True(t, ok)
	assert.InDelta(t, 2.98, v, 1e-9)

	_, ok = alarmeval.StatValue(nil, "Average")
	assert.False(t, ok)
}

func TestLowSample(t *testing.T) {
	assert.True(t, alarmeval.LowSample(0.99, 999))
	assert.False(t, alarmeval.LowSample(0.99, 1000))
	assert.True(t, alarmeval.LowSample(0.5, 19))
	assert.False(t, alarmeval.LowSample(0.5, 20))
	assert.True(t, alarmeval.LowSample(0.1, 99))
	assert.False(t, alarmeval.LowSample(0.1, 100))
	assert.False(t, alarmeval.LowSample(0, 1), "p0 has no low-sample rule")
	assert.False(t, alarmeval.LowSample(1, 1), "p100 has no low-sample rule")
}

// datumsAt returns one plain datum per value, all at ts.
func datumsAt(ts time.Time, values []float64) []driver.MetricDatum {
	out := make([]driver.MetricDatum, 0, len(values))
	for _, v := range values {
		out = append(out, driver.MetricDatum{Value: v, Timestamp: ts})
	}

	return out
}

// An ExtendedStatistic alarm evaluates the percentile, not the average.
func TestEvaluateWindowExtendedStatistic(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	data := datumsAt(now.Add(-10*time.Second), oneToN(100))

	p := alarmeval.Params{
		Period: 60, EvaluationPeriods: 1, ExtendedStatistic: "p99",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 95, ExtendedRange: true,
	}

	assert.Equal(t, alarmeval.StateAlarm, alarmeval.EvaluateWindow(data, &p, now).State)
	assert.Equal(t, []float64{99}, alarmeval.RecentDatapoints(data, &p, now))

	p.ExtendedStatistic = "p50"
	assert.Equal(t, alarmeval.StateOK, alarmeval.EvaluateWindow(data, &p, now).State)
}

// EvaluateLowSampleCountPercentile=ignore keeps the state while too few
// samples are present. evaluate (the default) evaluates anyway.
func TestEvaluateWindowLowSampleCount(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	data := datumsAt(now.Add(-10*time.Second), oneToN(50))

	p := alarmeval.Params{
		Period: 60, EvaluationPeriods: 1, ExtendedStatistic: "p99",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 40, ExtendedRange: true,
		LowSampleIgnore: true,
	}

	assert.Equal(t, alarmeval.Outcome{Retain: true}, alarmeval.EvaluateWindow(data, &p, now))

	p.LowSampleIgnore = false
	assert.Equal(t, alarmeval.StateAlarm, alarmeval.EvaluateWindow(data, &p, now).State)

	// p50 needs only 20 samples, so 50 is enough even with ignore.
	p.LowSampleIgnore = true
	p.ExtendedStatistic = "p50"
	p.Threshold = 20
	assert.Equal(t, alarmeval.StateAlarm, alarmeval.EvaluateWindow(data, &p, now).State)
}

// A period whose percentile is unavailable counts as missing.
func TestEvaluateWindowUnavailablePercentileIsMissing(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	data := datumsAt(now.Add(-10*time.Second), []float64{-1, 5})

	p := alarmeval.Params{
		Period: 60, EvaluationPeriods: 1, ExtendedStatistic: "p90",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 1, ExtendedRange: true,
	}

	assert.Equal(t, alarmeval.StateInsufficientData, alarmeval.EvaluateWindow(data, &p, now).State)
}
