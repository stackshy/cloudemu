package cloudwatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const extNS = "Ext/App"

// putLatencies puts the values 1..n of extNS/lazyMetric at ts, one datum each.
func putLatencies(t *testing.T, m *Mock, ts time.Time, n int) {
	t.Helper()

	data := make([]driver.MetricDatum, 0, n)
	for i := 1; i <= n; i++ {
		data = append(data, driver.MetricDatum{Namespace: extNS, MetricName: lazyMetric, Value: float64(i), Timestamp: ts})
	}

	requireNoError(t, m.PutMetricData(context.Background(), data))
}

func percentileAlarm(name, ext, lowSample string, threshold float64) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, Namespace: extNS, MetricName: lazyMetric,
		ComparisonOperator: "GreaterThanThreshold", Threshold: threshold,
		Period: 60, EvaluationPeriods: 1, ExtendedStatistic: ext, EvaluateLowSampleCountPercentile: lowSample,
	}
}

// A p99 alarm over 1..100 breaches 95. Before CW-11 the alarm evaluated the
// average, 50.5, and went to OK.
func TestPercentileAlarmEvaluatesPercentile(t *testing.T) {
	m, fc, _ := newClockMock()
	putLatencies(t, m, fc.Now(), 100)

	requireNoError(t, m.CreateAlarm(context.Background(), percentileAlarm("p99", "p99", "", 95)))

	assertEqual(t, stateAlarm, stateOf(t, m, "p99"))

	alarms, err := m.DescribeAlarms(context.Background(), []string{"p99"})
	requireNoError(t, err)

	var reason struct {
		Statistic        string    `json:"statistic"`
		RecentDatapoints []float64 `json:"recentDatapoints"`
	}

	requireNoError(t, json.Unmarshal([]byte(alarms[0].StateReasonData), &reason))
	assertEqual(t, "p99", reason.Statistic)
	assertEqual(t, 1, len(reason.RecentDatapoints))
	assertEqual(t, 99.0, reason.RecentDatapoints[0])
}

// With ignore, 50 samples are too few for p99 and the alarm keeps its state.
// evaluate, the default, evaluates them.
func TestPercentileAlarmLowSampleCount(t *testing.T) {
	m, fc, _ := newClockMock()
	putLatencies(t, m, fc.Now(), 50)

	requireNoError(t, m.CreateAlarm(context.Background(), percentileAlarm("ignore", "p99", "ignore", 10)))
	requireNoError(t, m.CreateAlarm(context.Background(), percentileAlarm("evaluate", "p99", "evaluate", 10)))

	assertEqual(t, stateInsufficientData, stateOf(t, m, "ignore"))
	assertEqual(t, stateAlarm, stateOf(t, m, "evaluate"))

	alarms, err := m.DescribeAlarms(context.Background(), []string{"ignore"})
	requireNoError(t, err)
	assertEqual(t, "ignore", alarms[0].EvaluateLowSampleCountPercentile)
}

// GetMetricData reads a percentile Stat as the percentile, and leaves out a
// period where it is not available.
func TestGetMetricDataExtendedStat(t *testing.T) {
	m, fc, _ := newClockMock()
	putLatencies(t, m, fc.Now(), 100)

	in := driver.GetMetricInput{
		Namespace: extNS, MetricName: lazyMetric, Period: 60, Stat: "p90",
		StartTime: fc.Now().Add(-time.Minute), EndTime: fc.Now().Add(time.Minute),
	}

	res, err := m.GetMetricData(context.Background(), in)
	requireNoError(t, err)
	assertEqual(t, 1, len(res.Values))
	assertEqual(t, 90.0, res.Values[0])

	requireNoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: extNS, MetricName: "Neg", Value: -1, Timestamp: fc.Now()},
	}))

	in.MetricName = "Neg"

	res, err = m.GetMetricData(context.Background(), in)
	requireNoError(t, err)
	assertEqual(t, 0, len(res.Values))
}

// A wall clock alarm sees a point only once its clock period has ended.
func TestWallClockAlarm(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 12, 6, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc)))

	cfg := lazyAlarm("wc", 300, "")
	cfg.EvaluationWindow = &driver.EvaluationWindow{WallClock: true, Timezone: "UTC"}
	requireNoError(t, m.CreateAlarm(context.Background(), cfg))

	putValue(t, m, fc, lazyNS, 10)

	fc.Advance(time.Minute) // 12:07, the 12:05 period is still open
	assertEqual(t, stateInsufficientData, stateOf(t, m, "wc"))

	fc.Advance(3 * time.Minute) // 12:10, the 12:05 period has ended
	assertEqual(t, stateAlarm, stateOf(t, m, "wc"))

	alarms, err := m.DescribeAlarms(context.Background(), []string{"wc"})
	requireNoError(t, err)
	assertEqual(t, driver.EvaluationWindow{WallClock: true, Timezone: "UTC"}, *alarms[0].EvaluationWindow)
}

// ListMetricsActive keeps only series with recent data, and the receipt
// times survive a snapshot.
func TestListMetricsActive(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putValue(t, m, fc, "Old/App", 1)
	fc.Advance(4 * time.Hour)
	putValue(t, m, fc, "New/App", 1)

	active, err := m.ListMetricsActive(ctx, 3*time.Hour)
	requireNoError(t, err)
	assertEqual(t, 1, len(active))
	assertEqual(t, "New/App", active[0].Namespace)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := New(config.NewOptions(config.WithClock(fc)))
	requireNoError(t, restored.Restore(ctx, snap))

	active, err = restored.ListMetricsActive(ctx, 3*time.Hour)
	requireNoError(t, err)
	assertEqual(t, 1, len(active))
}

// A snapshot taken before receipt times were kept restores every series as
// received at restore time, so it stays listed.
func TestRestoreWithoutReceiptTimes(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putValue(t, m, fc, "Old/App", 1)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	var raw map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(snap, &raw))
	delete(raw, "lastReceived")

	legacy, err := json.Marshal(raw)
	requireNoError(t, err)

	fc.Advance(30 * 24 * time.Hour)

	restored := New(config.NewOptions(config.WithClock(fc)))
	requireNoError(t, restored.Restore(ctx, legacy))

	active, err := restored.ListMetricsActive(ctx, time.Hour)
	requireNoError(t, err)
	assertEqual(t, 1, len(active))
}
