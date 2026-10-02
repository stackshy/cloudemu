package cloudwatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func createReasonDataAlarm(t *testing.T, m *Mock) {
	t.Helper()

	requireNoError(t, m.CreateAlarm(context.Background(), driver.AlarmConfig{
		Name: "a1", Namespace: "NS", MetricName: "M", ComparisonOperator: "GreaterThanThreshold",
		Threshold: 50, Period: 60, EvaluationPeriods: 1, Stat: "Average",
	}))
}

func TestSetAlarmStateWithData(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createReasonDataAlarm(t, m)

	requireNoError(t, m.SetAlarmStateWithData(ctx, "a1", "ALARM", "manual", `{"k":1}`))

	alarms, _ := m.DescribeAlarms(ctx, []string{"a1"})
	assertEqual(t, `{"k":1}`, alarms[0].StateReasonData)

	history, _ := m.GetAlarmHistory(ctx, "a1", 0)
	assertEqual(t, 1, len(history))
	assertEqual(t, "", history[0].OldStateReasonData)
	assertEqual(t, `{"k":1}`, history[0].NewStateReasonData)

	// A plain SetAlarmState clears the data. The history keeps the old value.
	requireNoError(t, m.SetAlarmState(ctx, "a1", "OK", "manual"))

	alarms, _ = m.DescribeAlarms(ctx, []string{"a1"})
	assertEqual(t, "", alarms[0].StateReasonData)

	history, _ = m.GetAlarmHistory(ctx, "a1", 0)
	assertEqual(t, 2, len(history))

	var oldData string
	for _, h := range history {
		if h.NewState == "OK" {
			oldData = h.OldStateReasonData
		}
	}

	assertEqual(t, `{"k":1}`, oldData)
}

func TestEvaluationSetsStateReasonData(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createReasonDataAlarm(t, m)

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{{
		Namespace: "NS", MetricName: "M", Value: 75, Timestamp: m.opts.Clock.Now(),
	}}))

	alarms, _ := m.DescribeAlarms(ctx, []string{"a1"})
	assertEqual(t, "ALARM", alarms[0].State)

	var got struct {
		Version          string    `json:"version"`
		QueryDate        string    `json:"queryDate"`
		StartDate        string    `json:"startDate"`
		Statistic        string    `json:"statistic"`
		Period           int       `json:"period"`
		RecentDatapoints []float64 `json:"recentDatapoints"`
		Threshold        float64   `json:"threshold"`
	}

	requireNoError(t, json.Unmarshal([]byte(alarms[0].StateReasonData), &got))
	assertEqual(t, "1.0", got.Version)
	assertEqual(t, "Average", got.Statistic)
	assertEqual(t, 60, got.Period)
	assertEqual(t, 50.0, got.Threshold)
	assertEqual(t, 1, len(got.RecentDatapoints))
	assertEqual(t, 75.0, got.RecentDatapoints[0])

	if _, err := time.Parse(reasonDataTimeFormat, got.QueryDate); err != nil {
		t.Fatalf("queryDate %q: %v", got.QueryDate, err)
	}

	history, _ := m.GetAlarmHistory(ctx, "a1", 0)
	assertEqual(t, alarms[0].StateReasonData, history[0].NewStateReasonData)
}

func TestSnapshotKeepsStateReasonData(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	createReasonDataAlarm(t, src)
	requireNoError(t, src.SetAlarmStateWithData(ctx, "a1", "ALARM", "manual", `{"k":1}`))

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	alarms, _ := dst.DescribeAlarms(ctx, []string{"a1"})
	assertEqual(t, `{"k":1}`, alarms[0].StateReasonData)

	history, _ := dst.GetAlarmHistory(ctx, "a1", 0)
	assertEqual(t, `{"k":1}`, history[0].NewStateReasonData)
}
