package monitoring

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

const lazyMetricType = "custom.googleapis.com/errors"

func lazyPolicy(name, treat string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, Namespace: "compute.googleapis.com", MetricName: lazyMetricType,
		ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
		Period: 60, EvaluationPeriods: 1, Stat: "Sum", TreatMissingData: treat,
	}
}

func putErrors(t *testing.T, m *Mock, clk *config.FakeClock, v float64) {
	t.Helper()

	require.NoError(t, m.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: "compute.googleapis.com", MetricName: lazyMetricType, Value: v, Timestamp: clk.Now()},
	}))
}

func policyState(t *testing.T, m *Mock, name string) string {
	t.Helper()

	a, err := m.DescribeAlarms(context.Background(), []string{name})
	require.NoError(t, err)
	require.Len(t, a, 1)

	return a[0].State
}

func TestLazyEvalPolicyCreatedAfterData(t *testing.T) {
	m, clk := newTestMock()
	putErrors(t, m, clk, 10)

	require.NoError(t, m.CreateAlarm(context.Background(), lazyPolicy("late", "")))

	assert.Equal(t, "ALARM", policyState(t, m, "late"))
}

func TestLazyEvalPolicyFallsBackToInsufficientData(t *testing.T) {
	m, clk := newTestMock()
	putErrors(t, m, clk, 10)
	require.NoError(t, m.CreateAlarm(context.Background(), lazyPolicy("stale", "")))

	clk.Advance(2 * time.Minute)

	assert.Equal(t, "INSUFFICIENT_DATA", policyState(t, m, "stale"))

	h, err := m.GetAlarmHistory(context.Background(), "stale", 0)
	require.NoError(t, err)
	require.Len(t, h, 2)
	assert.Equal(t, "INSUFFICIENT_DATA", h[0].NewState)
}

func TestLazyEvalPolicyBreachingWithNoData(t *testing.T) {
	m, _ := newTestMock()

	require.NoError(t, m.CreateAlarm(context.Background(), lazyPolicy("bad", "breaching")))

	assert.Equal(t, "ALARM", policyState(t, m, "bad"))
}

func TestLazyEvalPolicySetStateReverts(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()
	require.NoError(t, m.CreateAlarm(ctx, lazyPolicy("forced", "")))
	require.NoError(t, m.SetAlarmState(ctx, "forced", "ALARM", "test"))

	assert.Equal(t, "ALARM", policyState(t, m, "forced"))

	clk.Advance(time.Minute)
	assert.True(t, m.Tick(clk.Now()))
	assert.Equal(t, "INSUFFICIENT_DATA", policyState(t, m, "forced"))
}

func TestLazyEvalPolicyConcurrency(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()
	require.NoError(t, m.CreateAlarm(ctx, lazyPolicy("race", "")))

	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range 20 {
				_ = m.PutMetricData(ctx, []driver.MetricDatum{
					{Namespace: "compute.googleapis.com", MetricName: lazyMetricType, Value: float64(i), Timestamp: clk.Now()},
				})
				_, _ = m.DescribeAlarms(ctx, nil)
				_ = m.SetAlarmActions(ctx, "race", []string{"c"})
				_ = m.SetAlarmState(ctx, "race", "OK", "race")
				_, _ = m.Snapshot(ctx, false)

				clk.Advance(time.Second)
			}
		}()
	}

	wg.Wait()
}
