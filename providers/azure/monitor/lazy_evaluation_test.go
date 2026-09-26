package monitor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lazyRule(name, treat string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, Namespace: "Microsoft.Compute", MetricName: "Errors",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
		Period: 60, EvaluationPeriods: 1, Stat: "Sum", TreatMissingData: treat,
	}
}

func putErrors(t *testing.T, m *Mock, clk *config.FakeClock, v float64) {
	t.Helper()

	require.NoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: "Microsoft.Compute", MetricName: "Errors", Value: v, Timestamp: clk.Now()},
	}))
}

func ruleState(t *testing.T, m *Mock, name string) string {
	t.Helper()

	a, err := m.DescribeAlarms(context.Background(), []string{name})
	require.NoError(t, err)
	require.Len(t, a, 1)

	return a[0].State
}

func TestLazyEvalRuleCreatedAfterData(t *testing.T) {
	m, clk := newTestMock()
	putErrors(t, m, clk, 10)

	require.NoError(t, m.CreateAlarm(context.Background(), lazyRule("late", "")))

	assert.Equal(t, "ALARM", ruleState(t, m, "late"))
}

func TestLazyEvalRuleFallsBackToInsufficientData(t *testing.T) {
	m, clk := newTestMock()
	putErrors(t, m, clk, 10)
	require.NoError(t, m.CreateAlarm(context.Background(), lazyRule("stale", "")))

	clk.Advance(2 * time.Minute)

	assert.Equal(t, "INSUFFICIENT_DATA", ruleState(t, m, "stale"))

	h, err := m.GetAlarmHistory(context.Background(), "stale", 0)
	require.NoError(t, err)
	require.Len(t, h, 2)
	assert.Equal(t, "INSUFFICIENT_DATA", h[0].NewState)
}

func TestLazyEvalRuleBreachingWithNoData(t *testing.T) {
	m, _ := newTestMock()

	require.NoError(t, m.CreateAlarm(context.Background(), lazyRule("bad", "breaching")))

	assert.Equal(t, "ALARM", ruleState(t, m, "bad"))
}

func TestLazyEvalRuleSetStateReverts(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()
	require.NoError(t, m.CreateAlarm(ctx, lazyRule("forced", "")))
	require.NoError(t, m.SetAlarmState(ctx, "forced", "ALARM", "test"))

	assert.Equal(t, "ALARM", ruleState(t, m, "forced"))

	clk.Advance(time.Minute)
	assert.Equal(t, "INSUFFICIENT_DATA", ruleState(t, m, "forced"))
	assert.False(t, m.Tick(clk.Now()))
}

func TestLazyEvalRuleConcurrency(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()
	require.NoError(t, m.CreateAlarm(ctx, lazyRule("race", "")))

	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range 20 {
				_ = m.PutMetricData(ctx, []driver.MetricDatum{
					{Namespace: "Microsoft.Compute", MetricName: "Errors", Value: float64(i), Timestamp: clk.Now()},
				})
				_, _ = m.DescribeAlarms(ctx, nil)
				_ = m.SetAlarmState(ctx, "race", "OK", "race")
				_, _ = m.Snapshot(ctx, false)

				clk.Advance(time.Second)
			}
		}()
	}

	wg.Wait()
}
