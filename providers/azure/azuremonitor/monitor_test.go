package azuremonitor

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() (*Mock, *config.FakeClock) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))

	return New(opts), clk
}

func TestPutMetricData(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	t.Run("success", func(t *testing.T) {
		err := m.PutMetricData(ctx, []driver.MetricDatum{
			{Namespace: "Microsoft.Compute", MetricName: "CPU", Value: 42.0, Timestamp: clk.Now()},
		})
		require.NoError(t, err)
	})

	t.Run("empty data", func(t *testing.T) {
		err := m.PutMetricData(ctx, []driver.MetricDatum{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "metric data is required")
	})
}

func TestGetMetricData(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	baseTime := clk.Now()

	data := []driver.MetricDatum{
		{Namespace: "NS", MetricName: "M1", Value: 10.0, Timestamp: baseTime, Dimensions: map[string]string{"host": "a"}},
		{Namespace: "NS", MetricName: "M1", Value: 20.0, Timestamp: baseTime.Add(30 * time.Second), Dimensions: map[string]string{"host": "a"}},
		{Namespace: "NS", MetricName: "M1", Value: 30.0, Timestamp: baseTime.Add(90 * time.Second), Dimensions: map[string]string{"host": "a"}},
		{Namespace: "NS", MetricName: "M1", Value: 100.0, Timestamp: baseTime.Add(30 * time.Second), Dimensions: map[string]string{"host": "b"}},
	}

	require.NoError(t, m.PutMetricData(ctx, data))

	tests := []struct {
		name       string
		input      driver.GetMetricInput
		wantValues int
	}{
		{
			name: "average over period",
			input: driver.GetMetricInput{
				Namespace: "NS", MetricName: "M1",
				Dimensions: map[string]string{"host": "a"},
				StartTime:  baseTime, EndTime: baseTime.Add(2 * time.Minute),
				Period: 60, Stat: "Average",
			},
			wantValues: 2,
		},
		{
			name: "sum stat",
			input: driver.GetMetricInput{
				Namespace: "NS", MetricName: "M1",
				Dimensions: map[string]string{"host": "a"},
				StartTime:  baseTime, EndTime: baseTime.Add(1 * time.Minute),
				Period: 60, Stat: "Sum",
			},
			wantValues: 1,
		},
		{
			name: "filter by dimension",
			input: driver.GetMetricInput{
				Namespace: "NS", MetricName: "M1",
				Dimensions: map[string]string{"host": "b"},
				StartTime:  baseTime, EndTime: baseTime.Add(2 * time.Minute),
				Period: 60, Stat: "Average",
			},
			wantValues: 1,
		},
		{
			name: "no matching data",
			input: driver.GetMetricInput{
				Namespace: "NS", MetricName: "M1",
				Dimensions: map[string]string{"host": "c"},
				StartTime:  baseTime, EndTime: baseTime.Add(2 * time.Minute),
				Period: 60, Stat: "Average",
			},
			wantValues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.GetMetricData(ctx, tt.input)
			require.NoError(t, err)
			assert.Len(t, result.Values, tt.wantValues)
			assert.Len(t, result.Timestamps, tt.wantValues)
		})
	}
}

func TestListMetrics(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	t.Run("empty", func(t *testing.T) {
		names, err := m.ListMetrics(ctx, "NS")
		require.NoError(t, err)
		assert.Empty(t, names)
	})

	t.Run("with metrics", func(t *testing.T) {
		require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
			{Namespace: "NS", MetricName: "CPU", Value: 1.0, Timestamp: clk.Now()},
			{Namespace: "NS", MetricName: "Memory", Value: 2.0, Timestamp: clk.Now()},
			{Namespace: "Other", MetricName: "Disk", Value: 3.0, Timestamp: clk.Now()},
		}))

		names, err := m.ListMetrics(ctx, "NS")
		require.NoError(t, err)
		assert.Len(t, names, 2)
		assert.Contains(t, names, "CPU")
		assert.Contains(t, names, "Memory")
	})
}

func TestCreateAlarm(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	tests := []struct {
		name    string
		cfg     driver.AlarmConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "success",
			cfg: driver.AlarmConfig{
				Name: "high-cpu", Namespace: "NS", MetricName: "CPU",
				ComparisonOperator: "GreaterThanThreshold", Threshold: 80.0,
				Period: 60, EvaluationPeriods: 1, Stat: "Average",
			},
		},
		{
			name:    "empty name",
			cfg:     driver.AlarmConfig{Name: ""},
			wantErr: true, errMsg: "alarm name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CreateAlarm(ctx, tt.cfg)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)

				alarms, _ := m.DescribeAlarms(ctx, []string{"high-cpu"})
				require.Len(t, alarms, 1)
				assert.Equal(t, "INSUFFICIENT_DATA", alarms[0].State)
			}
		})
	}
}

func TestDescribeAlarms(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{Name: "alarm1", Namespace: "NS", MetricName: "M"}))
	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{Name: "alarm2", Namespace: "NS", MetricName: "M"}))

	t.Run("all alarms", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, nil)
		require.NoError(t, err)
		assert.Len(t, alarms, 2)
	})

	t.Run("by name", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, []string{"alarm1"})
		require.NoError(t, err)
		require.Len(t, alarms, 1)
		assert.Equal(t, "alarm1", alarms[0].Name)
	})

	t.Run("nonexistent name", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, []string{"missing"})
		require.NoError(t, err)
		assert.Empty(t, alarms)
	})
}

func TestDeleteAlarm(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{Name: "alarm1", Namespace: "NS", MetricName: "M"}))

	tests := []struct {
		name    string
		alarm   string
		wantErr bool
		errMsg  string
	}{
		{name: "success", alarm: "alarm1"},
		{name: "not found", alarm: "missing", wantErr: true, errMsg: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteAlarm(ctx, tt.alarm)

			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestSetAlarmState(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{Name: "alarm1", Namespace: "NS", MetricName: "M"}))

	t.Run("success", func(t *testing.T) {
		err := m.SetAlarmState(ctx, "alarm1", "ALARM", "manual override")
		require.NoError(t, err)

		alarms, _ := m.DescribeAlarms(ctx, []string{"alarm1"})
		require.Len(t, alarms, 1)
		assert.Equal(t, "ALARM", alarms[0].State)
	})

	t.Run("not found", func(t *testing.T) {
		err := m.SetAlarmState(ctx, "missing", "OK", "reason")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestEvaluateAlarms(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	tests := []struct {
		name      string
		alarm     driver.AlarmConfig
		data      []driver.MetricDatum
		wantState string
	}{
		{
			name: "alarm triggered - greater than threshold",
			alarm: driver.AlarmConfig{
				Name: "high-cpu", Namespace: "NS", MetricName: "CPU",
				ComparisonOperator: "GreaterThanThreshold", Threshold: 50.0,
				Period: 300, EvaluationPeriods: 1, Stat: "Average",
				Dimensions: map[string]string{"host": "a"},
			},
			data: []driver.MetricDatum{
				{Namespace: "NS", MetricName: "CPU", Value: 80.0, Timestamp: clk.Now(), Dimensions: map[string]string{"host": "a"}},
			},
			wantState: "ALARM",
		},
		{
			name: "alarm OK - below threshold",
			alarm: driver.AlarmConfig{
				Name: "low-cpu", Namespace: "NS", MetricName: "CPU",
				ComparisonOperator: "GreaterThanThreshold", Threshold: 50.0,
				Period: 300, EvaluationPeriods: 1, Stat: "Average",
				Dimensions: map[string]string{"host": "b"},
			},
			data: []driver.MetricDatum{
				{Namespace: "NS", MetricName: "CPU", Value: 20.0, Timestamp: clk.Now(), Dimensions: map[string]string{"host": "b"}},
			},
			wantState: "OK",
		},
		{
			name: "less than threshold triggered",
			alarm: driver.AlarmConfig{
				Name: "low-mem", Namespace: "NS", MetricName: "Memory",
				ComparisonOperator: "LessThanThreshold", Threshold: 30.0,
				Period: 300, EvaluationPeriods: 1, Stat: "Average",
			},
			data: []driver.MetricDatum{
				{Namespace: "NS", MetricName: "Memory", Value: 10.0, Timestamp: clk.Now()},
			},
			wantState: "ALARM",
		},
		{
			name: "greater than or equal triggered",
			alarm: driver.AlarmConfig{
				Name: "gte-alarm", Namespace: "NS", MetricName: "Disk",
				ComparisonOperator: "GreaterThanOrEqualToThreshold", Threshold: 90.0,
				Period: 300, EvaluationPeriods: 1, Stat: "Average",
			},
			data: []driver.MetricDatum{
				{Namespace: "NS", MetricName: "Disk", Value: 90.0, Timestamp: clk.Now()},
			},
			wantState: "ALARM",
		},
		{
			name: "less than or equal triggered",
			alarm: driver.AlarmConfig{
				Name: "lte-alarm", Namespace: "NS", MetricName: "Net",
				ComparisonOperator: "LessThanOrEqualToThreshold", Threshold: 5.0,
				Period: 300, EvaluationPeriods: 1, Stat: "Average",
			},
			data: []driver.MetricDatum{
				{Namespace: "NS", MetricName: "Net", Value: 5.0, Timestamp: clk.Now()},
			},
			wantState: "ALARM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, clk = newTestMock()

			require.NoError(t, m.CreateAlarm(ctx, tt.alarm))

			// Ensure metric timestamps are within the evaluation window
			for i := range tt.data {
				tt.data[i].Timestamp = clk.Now()
			}

			require.NoError(t, m.PutMetricData(ctx, tt.data))

			alarms, err := m.DescribeAlarms(ctx, []string{tt.alarm.Name})
			require.NoError(t, err)
			require.Len(t, alarms, 1)
			assert.Equal(t, tt.wantState, alarms[0].State)
		})
	}
}

func TestComputeStat(t *testing.T) {
	values := []float64{10.0, 20.0, 30.0, 40.0}

	tests := []struct {
		name string
		stat string
		want float64
	}{
		{name: "average", stat: "Average", want: 25.0},
		{name: "sum", stat: "Sum", want: 100.0},
		{name: "min", stat: "Min", want: 10.0},
		{name: "minimum", stat: "Minimum", want: 10.0},
		{name: "max", stat: "Max", want: 40.0},
		{name: "maximum", stat: "Maximum", want: 40.0},
		{name: "sample count", stat: "SampleCount", want: 4.0},
		{name: "default is average", stat: "", want: 25.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.want, computeStat(values, tt.stat), 0.001)
		})
	}

	t.Run("empty values", func(t *testing.T) {
		assert.Equal(t, 0.0, computeStat(nil, "Average"))
	})
}
