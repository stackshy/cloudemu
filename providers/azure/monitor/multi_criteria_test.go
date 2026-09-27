package monitor

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func criterionRule(metric string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: "multi", Namespace: "Microsoft.Compute", MetricName: metric,
		ComparisonOperator: "GreaterThanThreshold", Threshold: 5,
		Period: 60, EvaluationPeriods: 1, Stat: "Sum",
	}
}

func TestCreateAlarmAllOfNeedsEveryCriterion(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	require.NoError(t, m.CreateAlarmAllOf(ctx, []driver.AlarmConfig{criterionRule("Errors"), criterionRule("Latency")}))
	assert.Equal(t, "INSUFFICIENT_DATA", ruleState(t, m, "multi"))

	putErrors(t, m, clk, 10)
	assert.NotEqual(t, "ALARM", ruleState(t, m, "multi"))

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "Microsoft.Compute", MetricName: "Latency", Value: 1, Timestamp: clk.Now()},
	}))
	assert.Equal(t, "OK", ruleState(t, m, "multi"))

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "Microsoft.Compute", MetricName: "Latency", Value: 10, Timestamp: clk.Now()},
	}))
	assert.Equal(t, "ALARM", ruleState(t, m, "multi"))
}

func TestCreateAlarmAllOfRejectsEmpty(t *testing.T) {
	m, _ := newTestMock()
	assert.Error(t, m.CreateAlarmAllOf(context.Background(), nil))
}
