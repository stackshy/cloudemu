package monitoring

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExactDimensionMatching covers the exact-dimension parity fix: a metric
// published with a label is a distinct series from the label-less metric.
func TestExactDimensionMatching(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "compute.googleapis.com", MetricName: "latency", Value: 42, Timestamp: clk.Now(),
		Dimensions: map[string]string{"instance_id": "i-1"},
	}}))

	read := func(dims map[string]string) *driver.MetricDataResult {
		t.Helper()

		res, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace: "compute.googleapis.com", MetricName: "latency", Dimensions: dims,
			StartTime: clk.Now().Add(-time.Hour), EndTime: clk.Now().Add(time.Hour), Period: 60, Stat: "Sum",
		})
		require.NoError(t, err)

		return res
	}

	assert.Empty(t, read(nil).Values, "no-label query must not match a labeled metric")

	exact := read(map[string]string{"instance_id": "i-1"})
	require.Len(t, exact.Values, 1)
	assert.Equal(t, 42.0, exact.Values[0])
}

// TestAlarmMOfNAndRecovery covers the per-period M-of-N parity fix: 3 of 3
// periods must breach for ALARM (not the window average), with recovery to OK.
func TestAlarmMOfNAndRecovery(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "m-of-n", Namespace: "compute.googleapis.com", MetricName: "load",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 10,
		Period: 60, EvaluationPeriods: 3, DatapointsToAlarm: 3, Stat: "Average",
	}))

	feed := func(vals ...float64) {
		t.Helper()

		data := make([]driver.MetricDatum, 0, len(vals))
		for i, v := range vals {
			age := time.Duration(len(vals)-1-i) * 60 * time.Second
			data = append(data, driver.MetricDatum{
				Namespace: "compute.googleapis.com", MetricName: "load", Value: v, Timestamp: clk.Now().Add(-age),
			})
		}

		require.NoError(t, m.PutMetricData(ctx, data))
	}

	state := func() string {
		t.Helper()

		alarms, err := m.DescribeAlarms(ctx, []string{"m-of-n"})
		require.NoError(t, err)
		require.Len(t, alarms, 1)

		return alarms[0].State
	}

	feed(0, 0, 100)
	assert.Equal(t, "OK", state(), "only 1 of 3 periods breaches")

	clk.Advance(time.Hour)
	feed(100, 100, 100)
	assert.Equal(t, "ALARM", state(), "all 3 periods breach")

	clk.Advance(time.Hour)
	feed(0, 0, 0)
	assert.Equal(t, "OK", state(), "recovery once breaching periods age out")
}

// TestAlarmHistoryNewestFirst covers the ordering parity fix: history is
// newest-first and SetAlarmState records a transition.
func TestAlarmHistoryNewestFirst(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "hist", Namespace: "compute.googleapis.com", MetricName: "load",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 1, Period: 60, EvaluationPeriods: 1, Stat: "Average",
	}))

	for _, s := range []string{"ALARM", "OK", "ALARM"} {
		require.NoError(t, m.SetAlarmState(ctx, "hist", s, "step"))
		clk.Advance(time.Minute)
	}

	hist, err := m.GetAlarmHistory(ctx, "hist", 0)
	require.NoError(t, err)
	require.Len(t, hist, 3)

	for i := 1; i < len(hist); i++ {
		assert.Falsef(t, hist[i-1].Timestamp.Before(hist[i].Timestamp),
			"history not newest-first at %d", i)
	}
	assert.Equal(t, "ALARM", hist[0].NewState, "newest transition first")

	limited, err := m.GetAlarmHistory(ctx, "hist", 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.True(t, limited[0].Timestamp.Equal(hist[0].Timestamp), "limit keeps the newest entry")
}
