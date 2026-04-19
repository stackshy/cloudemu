package cloudwatch

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/monitoring/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	return New(opts)
}

func TestPutMetricData(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		err := m.PutMetricData(ctx, []driver.MetricDatum{
			{Namespace: "AWS/EC2", MetricName: "CPUUtilization", Value: 75.0, Timestamp: time.Now()},
		})
		requireNoError(t, err)
	})

	t.Run("empty data", func(t *testing.T) {
		err := m.PutMetricData(ctx, nil)
		assertError(t, err, true)
	})
}

func TestGetMetricData(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	_ = m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "NS", MetricName: "M1", Value: 10.0, Timestamp: baseTime, Dimensions: map[string]string{"id": "1"}},
		{Namespace: "NS", MetricName: "M1", Value: 20.0, Timestamp: baseTime.Add(30 * time.Second), Dimensions: map[string]string{"id": "1"}},
		{Namespace: "NS", MetricName: "M1", Value: 30.0, Timestamp: baseTime.Add(90 * time.Second), Dimensions: map[string]string{"id": "1"}},
	})

	t.Run("average stat", func(t *testing.T) {
		result, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace:  "NS",
			MetricName: "M1",
			Dimensions: map[string]string{"id": "1"},
			StartTime:  baseTime,
			EndTime:    baseTime.Add(2 * time.Minute),
			Period:     60,
			Stat:       "Average",
		})
		requireNoError(t, err)
		assertEqual(t, 2, len(result.Values))
		assertEqual(t, 15.0, result.Values[0]) // avg of 10, 20
		assertEqual(t, 30.0, result.Values[1]) // avg of 30
	})

	t.Run("sum stat", func(t *testing.T) {
		result, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace:  "NS",
			MetricName: "M1",
			Dimensions: map[string]string{"id": "1"},
			StartTime:  baseTime,
			EndTime:    baseTime.Add(2 * time.Minute),
			Period:     60,
			Stat:       "Sum",
		})
		requireNoError(t, err)
		assertEqual(t, 30.0, result.Values[0]) // sum of 10, 20
	})

	t.Run("min stat", func(t *testing.T) {
		result, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace:  "NS",
			MetricName: "M1",
			Dimensions: map[string]string{"id": "1"},
			StartTime:  baseTime,
			EndTime:    baseTime.Add(time.Minute),
			Period:     60,
			Stat:       "Min",
		})
		requireNoError(t, err)
		assertEqual(t, 10.0, result.Values[0])
	})

	t.Run("max stat", func(t *testing.T) {
		result, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace:  "NS",
			MetricName: "M1",
			Dimensions: map[string]string{"id": "1"},
			StartTime:  baseTime,
			EndTime:    baseTime.Add(time.Minute),
			Period:     60,
			Stat:       "Max",
		})
		requireNoError(t, err)
		assertEqual(t, 20.0, result.Values[0])
	})

	t.Run("sample count stat", func(t *testing.T) {
		result, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace:  "NS",
			MetricName: "M1",
			Dimensions: map[string]string{"id": "1"},
			StartTime:  baseTime,
			EndTime:    baseTime.Add(time.Minute),
			Period:     60,
			Stat:       "SampleCount",
		})
		requireNoError(t, err)
		assertEqual(t, 2.0, result.Values[0])
	})

	t.Run("no matching data", func(t *testing.T) {
		result, err := m.GetMetricData(ctx, driver.GetMetricInput{
			Namespace:  "NS",
			MetricName: "M1",
			Dimensions: map[string]string{"id": "999"},
			StartTime:  baseTime,
			EndTime:    baseTime.Add(time.Minute),
			Period:     60,
			Stat:       "Average",
		})
		requireNoError(t, err)
		assertEqual(t, 0, len(result.Values))
	})
}

func TestListMetrics(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_ = m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "NS", MetricName: "CPU", Value: 1, Timestamp: time.Now()},
		{Namespace: "NS", MetricName: "Memory", Value: 2, Timestamp: time.Now()},
		{Namespace: "Other", MetricName: "Disk", Value: 3, Timestamp: time.Now()},
	})

	metrics, err := m.ListMetrics(ctx, "NS")
	requireNoError(t, err)
	assertEqual(t, 2, len(metrics))

	metrics, err = m.ListMetrics(ctx, "Empty")
	requireNoError(t, err)
	assertEqual(t, 0, len(metrics))
}

func TestCreateAlarm(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.AlarmConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg: driver.AlarmConfig{
				Name:               "high-cpu",
				Namespace:          "AWS/EC2",
				MetricName:         "CPUUtilization",
				ComparisonOperator: "GreaterThanThreshold",
				Threshold:          80.0,
				Period:             60,
				EvaluationPeriods:  1,
				Stat:               "Average",
			},
		},
		{
			name:      "empty name",
			cfg:       driver.AlarmConfig{},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			err := m.CreateAlarm(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestDescribeAlarms(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_ = m.CreateAlarm(ctx, driver.AlarmConfig{Name: "alarm-1", Namespace: "NS", MetricName: "M"})
	_ = m.CreateAlarm(ctx, driver.AlarmConfig{Name: "alarm-2", Namespace: "NS", MetricName: "M"})

	t.Run("all alarms", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, nil)
		requireNoError(t, err)
		assertEqual(t, 2, len(alarms))
	})

	t.Run("by name", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, []string{"alarm-1"})
		requireNoError(t, err)
		assertEqual(t, 1, len(alarms))
		assertEqual(t, "alarm-1", alarms[0].Name)
		assertEqual(t, "INSUFFICIENT_DATA", alarms[0].State)
	})

	t.Run("nonexistent name", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, []string{"nope"})
		requireNoError(t, err)
		assertEqual(t, 0, len(alarms))
	})
}

func TestDeleteAlarm(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateAlarm(ctx, driver.AlarmConfig{Name: "alarm-1", Namespace: "NS", MetricName: "M"})

	t.Run("success", func(t *testing.T) {
		err := m.DeleteAlarm(ctx, "alarm-1")
		requireNoError(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.DeleteAlarm(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestSetAlarmState(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	_ = m.CreateAlarm(ctx, driver.AlarmConfig{Name: "a1", Namespace: "NS", MetricName: "M"})

	t.Run("success", func(t *testing.T) {
		err := m.SetAlarmState(ctx, "a1", "ALARM", "manual trigger")
		requireNoError(t, err)

		alarms, _ := m.DescribeAlarms(ctx, []string{"a1"})
		assertEqual(t, "ALARM", alarms[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.SetAlarmState(ctx, "nope", "OK", "")
		assertError(t, err, true)
	})
}

func TestAlarmAutoEvaluation(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc))
	m := New(opts)
	ctx := context.Background()

	// Create alarm: CPU > 50 triggers ALARM
	_ = m.CreateAlarm(ctx, driver.AlarmConfig{
		Name:               "high-cpu",
		Namespace:          "AWS/EC2",
		MetricName:         "CPUUtilization",
		Dimensions:         map[string]string{"InstanceId": "i-123"},
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          50.0,
		Period:             300,
		EvaluationPeriods:  1,
		Stat:               "Average",
	})

	t.Run("metric triggers ALARM", func(t *testing.T) {
		_ = m.PutMetricData(ctx, []driver.MetricDatum{
			{
				Namespace:  "AWS/EC2",
				MetricName: "CPUUtilization",
				Value:      75.0,
				Dimensions: map[string]string{"InstanceId": "i-123"},
				Timestamp:  fc.Now(),
			},
		})

		alarms, _ := m.DescribeAlarms(ctx, []string{"high-cpu"})
		assertEqual(t, "ALARM", alarms[0].State)
	})

	t.Run("metric transitions to OK", func(t *testing.T) {
		_ = m.PutMetricData(ctx, []driver.MetricDatum{
			{
				Namespace:  "AWS/EC2",
				MetricName: "CPUUtilization",
				Value:      10.0,
				Dimensions: map[string]string{"InstanceId": "i-123"},
				Timestamp:  fc.Now(),
			},
		})

		alarms, _ := m.DescribeAlarms(ctx, []string{"high-cpu"})
		assertEqual(t, "OK", alarms[0].State)
	})
}

func TestAlarmEvaluationOperators(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		value    float64
		thresh   float64
		expect   bool
	}{
		{name: "greater than - true", operator: "GreaterThanThreshold", value: 80, thresh: 50, expect: true},
		{name: "greater than - false", operator: "GreaterThanThreshold", value: 30, thresh: 50, expect: false},
		{name: "less than - true", operator: "LessThanThreshold", value: 30, thresh: 50, expect: true},
		{name: "less than - false", operator: "LessThanThreshold", value: 80, thresh: 50, expect: false},
		{name: "greater or equal - true", operator: "GreaterThanOrEqualToThreshold", value: 50, thresh: 50, expect: true},
		{name: "less or equal - true", operator: "LessThanOrEqualToThreshold", value: 50, thresh: 50, expect: true},
		{name: "unknown operator", operator: "Unknown", value: 50, thresh: 50, expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := evaluateComparison(tc.value, tc.operator, tc.thresh)
			assertEqual(t, tc.expect, result)
		})
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()
	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}
