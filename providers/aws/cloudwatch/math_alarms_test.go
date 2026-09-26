package cloudwatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const mathNS = "M/App"

func boolRef(b bool) *bool { return &b }

// rateAlarm watches err/req*100 > 20 over one 60s period.
func rateAlarm(name string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, ComparisonOperator: "GreaterThanThreshold", Threshold: 20, EvaluationPeriods: 1,
		Metrics: []driver.MetricDataQuery{
			{ID: "err", ReturnData: boolRef(false), MetricStat: &driver.MetricStat{Namespace: mathNS, MetricName: "Errors", Period: 60, Stat: "Sum"}},
			{ID: "req", ReturnData: boolRef(false), MetricStat: &driver.MetricStat{Namespace: mathNS, MetricName: "Requests", Period: 60, Stat: "Sum"}},
			{ID: "rate", Expression: "err/req*100", Label: "ErrorRate", ReturnData: boolRef(true)},
		},
	}
}

func putMath(t *testing.T, m *Mock, fc *config.FakeClock, name string, v float64) {
	t.Helper()

	requireNoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: mathNS, MetricName: name, Value: v, Timestamp: fc.Now()},
	}))
}

// A math alarm evaluates its expression: 30 errors over 100 requests is a 30%
// rate, above the 20 threshold. Before CW-12a it stayed INSUFFICIENT_DATA.
func TestMathAlarmEvaluatesExpression(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putMath(t, m, fc, "Errors", 30)
	putMath(t, m, fc, "Requests", 100)
	fc.Advance(time.Second)

	requireNoError(t, m.CreateAlarm(ctx, rateAlarm("rate")))
	assertEqual(t, stateAlarm, stateOf(t, m, "rate"))

	alarms, err := m.DescribeAlarms(ctx, []string{"rate"})
	requireNoError(t, err)
	assertEqual(t, 3, len(alarms[0].Metrics))
	assertEqual(t, "err/req*100", alarms[0].Metrics[2].Expression)
	assertEqual(t, 0, alarms[0].Period)
}

// New data on an input metric re-evaluates the math alarm right away.
func TestMathAlarmPutMetricDataReevaluates(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putMath(t, m, fc, "Requests", 100)
	putMath(t, m, fc, "Errors", 5)
	fc.Advance(time.Second)

	requireNoError(t, m.CreateAlarm(ctx, rateAlarm("rate")))
	assertEqual(t, stateOK, stateOf(t, m, "rate"))

	// Still inside the evaluation interval, so only the PutMetricData trigger
	// can move the alarm.
	putMath(t, m, fc, "Errors", 40)
	fc.Advance(time.Second)

	m.alarmMu.Lock()
	a, _ := m.alarms.Get("rate")
	state := a.State
	m.alarmMu.Unlock()

	assertEqual(t, stateAlarm, state)
}

// An expression outside the supported syntax evaluates to no data.
func TestMathAlarmUnsupportedExpression(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	putMath(t, m, fc, "Errors", 30)

	cfg := rateAlarm("fill")
	cfg.Metrics[2].Expression = "FILL(err, 0)"

	requireNoError(t, m.CreateAlarm(ctx, cfg))
	assertEqual(t, stateInsufficientData, stateOf(t, m, "fill"))
}

// The state change event lists the math alarm's queries.
func TestMathAlarmEventMetrics(t *testing.T) {
	m, bus, fc := newEventMock()
	ctx := context.Background()

	putMath(t, m, fc, "Errors", 30)
	putMath(t, m, fc, "Requests", 100)
	fc.Advance(time.Second)

	requireNoError(t, m.CreateAlarm(ctx, rateAlarm("rate")))

	events := bus.ofType(eventAlarmStateChange)
	if len(events) != 1 {
		t.Fatalf("state events = %d, want 1", len(events))
	}

	cfg, _ := events[0].detail["configuration"].(map[string]any)
	metrics, _ := cfg["metrics"].([]any)
	assertEqual(t, 3, len(metrics))

	rate, _ := metrics[2].(map[string]any)
	assertEqual(t, "err/req*100", rate["expression"])
	assertEqual(t, true, rate["returnData"])

	if _, ok := rate["metricStat"]; ok {
		t.Fatalf("expression entry has a metricStat: %v", rate)
	}

	errQ, _ := metrics[0].(map[string]any)
	assertEqual(t, false, errQ["returnData"])
}

// The stored query list is a copy, so a caller changing its config later
// does not change the alarm.
func TestMathAlarmStoresCopy(t *testing.T) {
	m, _, _ := newClockMock()
	ctx := context.Background()

	cfg := rateAlarm("rate")
	requireNoError(t, m.CreateAlarm(ctx, cfg))

	cfg.Metrics[2].Expression = "changed"
	*cfg.Metrics[0].ReturnData = true

	alarms, err := m.DescribeAlarms(ctx, []string{"rate"})
	requireNoError(t, err)
	assertEqual(t, "err/req*100", alarms[0].Metrics[2].Expression)
	assertEqual(t, false, *alarms[0].Metrics[0].ReturnData)
}

// A snapshot keeps the Metrics list and ThresholdMetricID.
func TestMathAlarmSnapshot(t *testing.T) {
	ctx := context.Background()
	src, _, _ := newClockMock()

	cfg := rateAlarm("rate")
	cfg.ThresholdMetricID = "err"
	requireNoError(t, src.CreateAlarm(ctx, cfg))

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst, _, _ := newClockMock()
	requireNoError(t, dst.Restore(ctx, data))

	alarms, err := dst.DescribeAlarms(ctx, []string{"rate"})
	requireNoError(t, err)
	assertEqual(t, 3, len(alarms[0].Metrics))
	assertEqual(t, "err", alarms[0].ThresholdMetricID)
	assertEqual(t, "Requests", alarms[0].Metrics[1].MetricStat.MetricName)
}

// Math alarms survive concurrent writes and reads. Run with -race.
func TestMathAlarmConcurrency(t *testing.T) {
	m, fc, _ := newClockMock()
	ctx := context.Background()

	var wg sync.WaitGroup

	for i := range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			name := "rate"
			if i%2 == 0 {
				name = "rate2"
			}

			for range 20 {
				_ = m.PutMetricData(ctx, []driver.MetricDatum{{Namespace: mathNS, MetricName: "Errors", Value: 1, Timestamp: fc.Now()}})
				_ = m.CreateAlarm(ctx, rateAlarm(name))
				_, _ = m.DescribeAlarms(ctx, nil)
				_ = m.DeleteAlarm(ctx, name)
			}
		}()
	}

	wg.Wait()
}
