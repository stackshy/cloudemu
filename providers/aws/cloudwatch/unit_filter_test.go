package cloudwatch

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func unitAlarm(name, unit string) driver.AlarmConfig {
	return driver.AlarmConfig{
		Name: name, Namespace: "U/App", MetricName: "Cpu", Stat: "Average", Unit: unit,
		Period: 60, EvaluationPeriods: 1, Threshold: 50, ComparisonOperator: "GreaterThanThreshold",
	}
}

// TestAlarmUnitFilter checks that an alarm only evaluates data put with its
// unit. An alarm with the wrong unit stays in INSUFFICIENT_DATA, as AWS
// documents for PutMetricAlarm.
func TestAlarmUnitFilter(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	requireNoError(t, m.CreateAlarm(ctx, unitAlarm("bytes", "Bytes")))
	requireNoError(t, m.CreateAlarm(ctx, unitAlarm("percent", "Percent")))
	requireNoError(t, m.CreateAlarm(ctx, unitAlarm("any", "")))

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "U/App", MetricName: "Cpu", Value: 90, Unit: "Percent", Timestamp: m.opts.Clock.Now()},
	}))

	alarms, err := m.DescribeAlarms(ctx, []string{"bytes", "percent", "any"})
	requireNoError(t, err)
	assertEqual(t, 3, len(alarms))
	assertEqual(t, stateInsufficientData, alarms[0].State)
	assertEqual(t, stateAlarm, alarms[1].State)
	assertEqual(t, stateAlarm, alarms[2].State)
}

// TestGetMetricDataUnitFilter checks the Unit field of GetMetricInput and the
// None unit of a datum put without one.
func TestGetMetricDataUnitFilter(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	now := m.opts.Clock.Now()

	requireNoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "U/App", MetricName: "Cpu", Value: 90, Unit: "Percent", Timestamp: now},
		{Namespace: "U/App", MetricName: "Cpu", Value: 10, Timestamp: now},
	}))

	get := func(unit string) *driver.MetricDataResult {
		t.Helper()

		res, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace: "U/App", MetricName: "Cpu", Stat: "Sum", Period: 60, Unit: unit,
			StartTime: now.Add(-time.Minute), EndTime: now.Add(time.Minute),
		})
		requireNoError(t, err)

		return res
	}

	assertEqual(t, 0, len(get("Bytes").Values))

	percent := get("Percent")
	assertEqual(t, 1, len(percent.Values))
	assertEqual(t, 90.0, percent.Values[0])
	assertEqual(t, "Percent", percent.Unit)

	none := get("None")
	assertEqual(t, 1, len(none.Values))
	assertEqual(t, 10.0, none.Values[0])
	assertEqual(t, "None", none.Unit)

	assertEqual(t, 100.0, get("").Values[0])

	units := m.MetricUnits(ctx, &driver.GetMetricInput{
		Namespace: "U/App", MetricName: "Cpu", Unit: "Bytes",
		StartTime: now.Add(-time.Minute), EndTime: now.Add(time.Minute),
	})
	assertEqual(t, 2, len(units))
	assertEqual(t, "None", units[0])
	assertEqual(t, "Percent", units[1])
}
