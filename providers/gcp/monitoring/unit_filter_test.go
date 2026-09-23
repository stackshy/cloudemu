package monitoring

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestUnitFilter checks that GetMetricData and alarm evaluation only use data
// stored with the requested unit.
func TestUnitFilter(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()
	now := clk.Now()

	for name, unit := range map[string]string{"bytes": "Bytes", "percent": "Percent"} {
		require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
			Name: name, Namespace: "NS", MetricName: "Cpu", Stat: "Average", Unit: unit,
			Period: 60, EvaluationPeriods: 1, Threshold: 50, ComparisonOperator: "GreaterThanThreshold",
		}))
	}

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "NS", MetricName: "Cpu", Value: 90, Unit: "Percent", Timestamp: now},
	}))

	get := func(unit string) []float64 {
		res, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace: "NS", MetricName: "Cpu", Stat: "Sum", Period: 60, Unit: unit,
			StartTime: now.Add(-time.Minute), EndTime: now.Add(time.Minute),
		})
		require.NoError(t, err)

		return res.Values
	}

	assert.Empty(t, get("Bytes"))
	assert.Equal(t, []float64{90}, get("Percent"))
	assert.Equal(t, []float64{90}, get(""))

	alarms, err := m.DescribeAlarms(ctx, []string{"bytes", "percent"})
	require.NoError(t, err)
	require.Len(t, alarms, 2)
	assert.Equal(t, "INSUFFICIENT_DATA", alarms[0].State)
	assert.Equal(t, "ALARM", alarms[1].State)
}
