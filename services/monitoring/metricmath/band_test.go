package metricmath_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

func bandQueries(expr string) []driver.MetricDataQuery {
	return []driver.MetricDataQuery{
		{ID: "m1", MetricStat: stat("A", 60)},
		{ID: "ad1", Expression: expr},
	}
}

func minutesSeries(values ...float64) metricmath.Series {
	ts := make([]int, len(values))
	for i := range ts {
		ts[i] = i
	}

	return metricmath.Series{Timestamps: at(ts...), Values: values}
}

func TestBandInput(t *testing.T) {
	tests := []struct {
		expr string
		id   string
		ok   bool
	}{
		{"ANOMALY_DETECTION_BAND(m1, 2)", "m1", true},
		{"ANOMALY_DETECTION_BAND(m1)", "m1", true},
		{"ANOMALY_DETECTION_BAND( e1 ,3.5 )", "e1", true},
		{"ANOMALY_DETECTION_BAND(m1, 2) * 2", "", false},
		{"ANOMALY_DETECTION_BAND(m1, x)", "", false},
		{"ANOMALY_DETECTION_BAND(m1 2)", "", false},
		{"anomaly_detection_band(m1)", "", false},
		{"m1*2", "", false},
	}

	for _, tc := range tests {
		id, ok := metricmath.BandInput(tc.expr)
		assert.Equal(t, tc.ok, ok, tc.expr)
		assert.Equal(t, tc.id, id, tc.expr)
	}
}

// The band at each point is mean -/+ k sd of the earlier points. The first
// points have too little history and get no band.
func TestBandValues(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{"A": minutesSeries(8, 12, 8, 12, 10)}}

	// Point 3 trains on 8, 12, 8: mean 9.33, sd 1.8856. Point 4 trains on
	// 8, 12, 8, 12: mean 10, sd 2.
	const sd3 = 1.8856180831641267

	for _, tc := range []struct {
		expr string
		k    float64
	}{{"ANOMALY_DETECTION_BAND(m1, 2)", 2}, {"ANOMALY_DETECTION_BAND(m1)", 2}, {"ANOMALY_DETECTION_BAND(m1, 1)", 1}} {
		band, ok, err := metricmath.New(bandQueries(tc.expr), f.fetch).Band("ad1")
		require.NoError(t, err)
		require.True(t, ok)

		assert.Equal(t, at(3, 4), band.Timestamps, tc.expr)
		assert.InDelta(t, 28.0/3-tc.k*sd3, band.Lower[0], 1e-9, tc.expr)
		assert.InDelta(t, 28.0/3+tc.k*sd3, band.Upper[0], 1e-9, tc.expr)
		assert.InDelta(t, 10-tc.k*2, band.Lower[1], 1e-9, tc.expr)
		assert.InDelta(t, 10+tc.k*2, band.Upper[1], 1e-9, tc.expr)
	}
}

// A non-band entry reports ok=false, and a band has no single-series value.
func TestBandNotABand(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{"A": minutesSeries(1, 2, 3, 4)}}
	ev := metricmath.New(bandQueries("ANOMALY_DETECTION_BAND(m1, 2)"), f.fetch)

	_, ok, err := ev.Band("m1")
	require.NoError(t, err)
	assert.False(t, ok)

	_, ok, err = ev.Band("nope")
	require.NoError(t, err)
	assert.False(t, ok)

	s, err := ev.Resolve("ad1")
	require.NoError(t, err)
	assert.Empty(t, s.Values)
}

// History extends training back past the evaluated range, and an excluded
// range drops the points whose period it overlaps.
func TestBandHistoryAndExclusions(t *testing.T) {
	recent := &fixedFetcher{series: map[string]metricmath.Series{"A": {Timestamps: at(10), Values: []float64{10}}}}
	history := &fixedFetcher{series: map[string]metricmath.Series{"A": {
		Timestamps: at(0, 1, 2, 3, 4, 5, 10), Values: []float64{0, 20, 0, 20, 10, 10, 10},
	}}}

	var gotInput string

	cfg := metricmath.BandConfig{History: history.fetch}
	band, _, err := metricmath.New(bandQueries("ANOMALY_DETECTION_BAND(m1, 2)"), recent.fetch).WithBand(cfg).Band("ad1")
	require.NoError(t, err)
	require.Len(t, band.Timestamps, 1)
	assert.Greater(t, band.Upper[0]-band.Lower[0], 20.0, "noisy history widens the band")

	cfg.Excluded = func(queries []driver.MetricDataQuery, inputID string) []driver.TimeRange {
		gotInput = inputID
		assert.Len(t, queries, 2)

		// Ends inside minute 3, so minute 3's period is left out too.
		return []driver.TimeRange{{StartTime: base, EndTime: base.Add(3*time.Minute + time.Second)}}
	}

	band, _, err = metricmath.New(bandQueries("ANOMALY_DETECTION_BAND(m1, 2)"), recent.fetch).WithBand(cfg).Band("ad1")
	require.NoError(t, err)
	assert.Equal(t, "m1", gotInput)
	require.Len(t, band.Timestamps, 0, "only two training points are left")
}

// A flat series gets a band that holds its own value despite rounding.
func TestBandFlatSeries(t *testing.T) {
	f := &fixedFetcher{series: map[string]metricmath.Series{"A": minutesSeries(0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1)}}

	band, _, err := metricmath.New(bandQueries("ANOMALY_DETECTION_BAND(m1, 2)"), f.fetch).Band("ad1")
	require.NoError(t, err)

	for i := range band.Timestamps {
		assert.LessOrEqual(t, band.Lower[i], 0.1)
		assert.GreaterOrEqual(t, band.Upper[i], 0.1)
	}
}

// A metric with a large mean keeps the precision of its variance.
func TestBandLargeMean(t *testing.T) {
	const n = 20160

	values := make([]float64, n+1)
	ts := make([]time.Time, n+1)

	for i := range values {
		values[i] = 1e9 + float64(1-2*(i%2)) // 1e9 +/- 1, sd 1
		ts[i] = base.Add(time.Duration(i) * time.Minute)
	}

	f := &fixedFetcher{series: map[string]metricmath.Series{"A": {Timestamps: ts, Values: values}}}

	band, _, err := metricmath.New(bandQueries("ANOMALY_DETECTION_BAND(m1, 2)"), f.fetch).Band("ad1")
	require.NoError(t, err)

	last := len(band.Timestamps) - 1
	width := band.Upper[last] - band.Lower[last]
	assert.InDelta(t, 4, width, 0.04, "width %v", width)
	assert.False(t, math.IsNaN(width))
}

func BenchmarkBandAt(b *testing.B) {
	const n = 20160

	values := make([]float64, n)
	ts := make([]time.Time, n)

	for i := range values {
		values[i] = float64(i % 7)
		ts[i] = base.Add(time.Duration(i) * time.Minute)
	}

	series := metricmath.Series{Timestamps: ts, Values: values}
	fetch := func(*driver.MetricStat, int) (metricmath.Series, error) { return series, nil }

	b.ResetTimer()

	for range b.N {
		if _, _, err := metricmath.New(bandQueries("ANOMALY_DETECTION_BAND(m1, 2)"), fetch).BandAt("ad1", 60); err != nil {
			b.Fatal(err)
		}
	}
}
