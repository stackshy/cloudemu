package cloudmonitoring

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
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithProjectID("test-project"))

	return New(opts), clk
}

func TestPutMetricData(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	tests := []struct {
		name      string
		data      []driver.MetricDatum
		wantErr   bool
		errSubstr string
	}{
		{name: "empty data", data: []driver.MetricDatum{}, wantErr: true, errSubstr: "required"},
		{name: "single datum", data: []driver.MetricDatum{
			{Namespace: "custom", MetricName: "cpu", Value: 42.5, Timestamp: clk.Now()},
		}},
		{name: "multiple data", data: []driver.MetricDatum{
			{Namespace: "custom", MetricName: "cpu", Value: 10, Timestamp: clk.Now()},
			{Namespace: "custom", MetricName: "mem", Value: 80, Timestamp: clk.Now()},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.PutMetricData(ctx, tt.data)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestCreateAndDescribeAlarms(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	tests := []struct {
		name      string
		cfg       driver.AlarmConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "success", cfg: driver.AlarmConfig{
			Name: "high-cpu", Namespace: "custom", MetricName: "cpu",
			ComparisonOperator: "GreaterThanThreshold", Threshold: 80,
			Period: 60, EvaluationPeriods: 1, Stat: "Average",
		}},
		{name: "empty name", cfg: driver.AlarmConfig{}, wantErr: true, errSubstr: "required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.CreateAlarm(ctx, tt.cfg)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}

	t.Run("describe all alarms", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, nil)
		require.NoError(t, err)
		require.Len(t, alarms, 1)
		assert.Equal(t, "high-cpu", alarms[0].Name)
		assert.Equal(t, "INSUFFICIENT_DATA", alarms[0].State)
	})

	t.Run("describe by name", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, []string{"high-cpu"})
		require.NoError(t, err)
		require.Len(t, alarms, 1)
	})

	t.Run("describe nonexistent", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, []string{"nope"})
		require.NoError(t, err)
		assert.Empty(t, alarms)
	})
}

func TestDeleteAlarms(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{Name: "a1", Namespace: "ns", MetricName: "m1"}))

	tests := []struct {
		name      string
		alarm     string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", alarm: "a1"},
		{name: "not found", alarm: "missing", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.DeleteAlarm(ctx, tt.alarm)
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestGetMetricData(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	now := clk.Now()

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "ns", MetricName: "cpu", Value: 10, Timestamp: now},
		{Namespace: "ns", MetricName: "cpu", Value: 30, Timestamp: now.Add(30 * time.Second)},
		{Namespace: "ns", MetricName: "cpu", Value: 20, Timestamp: now.Add(90 * time.Second)},
	}))

	tests := []struct {
		name       string
		input      driver.GetMetricInput
		wantCount  int
		wantValues []float64
	}{
		{
			name: "average over 60s periods",
			input: driver.GetMetricInput{
				Namespace: "ns", MetricName: "cpu",
				StartTime: now, EndTime: now.Add(2 * time.Minute),
				Period: 60, Stat: "Average",
			},
			wantCount:  2,
			wantValues: []float64{20, 20}, // (10+30)/2=20, 20/1=20
		},
		{
			name: "sum stat",
			input: driver.GetMetricInput{
				Namespace: "ns", MetricName: "cpu",
				StartTime: now, EndTime: now.Add(2 * time.Minute),
				Period: 60, Stat: "Sum",
			},
			wantCount:  2,
			wantValues: []float64{40, 20},
		},
		{
			name: "no data for namespace",
			input: driver.GetMetricInput{
				Namespace: "other", MetricName: "cpu",
				StartTime: now, EndTime: now.Add(2 * time.Minute),
				Period: 60, Stat: "Average",
			},
			wantCount: 0,
		},
		{
			name: "with dimensions filter",
			input: driver.GetMetricInput{
				Namespace: "ns", MetricName: "cpu",
				Dimensions: map[string]string{"host": "web1"},
				StartTime:  now, EndTime: now.Add(2 * time.Minute),
				Period: 60, Stat: "Average",
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := m.GetMetricData(ctx, tt.input)
			require.NoError(t, err)
			assert.Len(t, result.Timestamps, tt.wantCount)
			assert.Len(t, result.Values, tt.wantCount)

			for i, v := range tt.wantValues {
				assert.InDelta(t, v, result.Values[i], 0.01)
			}
		})
	}
}

func TestEvaluateAlarms(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	now := clk.Now()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{
		Name: "high-cpu", Namespace: "ns", MetricName: "cpu",
		ComparisonOperator: "GreaterThanThreshold", Threshold: 50,
		Period: 300, EvaluationPeriods: 1, Stat: "Average",
	}))

	t.Run("alarm stays INSUFFICIENT_DATA with no data", func(t *testing.T) {
		alarms, err := m.DescribeAlarms(ctx, []string{"high-cpu"})
		require.NoError(t, err)
		assert.Equal(t, "INSUFFICIENT_DATA", alarms[0].State)
	})

	t.Run("alarm transitions to ALARM", func(t *testing.T) {
		require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
			{Namespace: "ns", MetricName: "cpu", Value: 80, Timestamp: now},
		}))
		alarms, err := m.DescribeAlarms(ctx, []string{"high-cpu"})
		require.NoError(t, err)
		assert.Equal(t, "ALARM", alarms[0].State)
	})

	t.Run("alarm transitions to OK", func(t *testing.T) {
		require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
			{Namespace: "ns", MetricName: "cpu", Value: 10, Timestamp: now},
		}))
		alarms, err := m.DescribeAlarms(ctx, []string{"high-cpu"})
		require.NoError(t, err)
		assert.Equal(t, "OK", alarms[0].State)
	})
}

func TestEvaluateComparison(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		operator string
		thresh   float64
		want     bool
	}{
		{name: "greater than - true", value: 90, operator: "GreaterThanThreshold", thresh: 80, want: true},
		{name: "greater than - false", value: 70, operator: "GreaterThanThreshold", thresh: 80, want: false},
		{name: "greater or equal - true", value: 80, operator: "GreaterThanOrEqualToThreshold", thresh: 80, want: true},
		{name: "less than - true", value: 10, operator: "LessThanThreshold", thresh: 50, want: true},
		{name: "less than - false", value: 60, operator: "LessThanThreshold", thresh: 50, want: false},
		{name: "less or equal - true", value: 50, operator: "LessThanOrEqualToThreshold", thresh: 50, want: true},
		{name: "unknown operator", value: 50, operator: "Unknown", thresh: 50, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evaluateComparison(tt.value, tt.operator, tt.thresh))
		})
	}
}

func TestSetAlarmState(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	require.NoError(t, m.CreateAlarm(ctx, driver.AlarmConfig{Name: "a1", Namespace: "ns", MetricName: "m"}))

	tests := []struct {
		name      string
		alarm     string
		state     string
		wantErr   bool
		errSubstr string
	}{
		{name: "success", alarm: "a1", state: "OK"},
		{name: "not found", alarm: "missing", state: "OK", wantErr: true, errSubstr: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.SetAlarmState(ctx, tt.alarm, tt.state, "manual")
			switch {
			case tt.wantErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			default:
				require.NoError(t, err)
				alarms, descErr := m.DescribeAlarms(ctx, []string{tt.alarm})
				require.NoError(t, descErr)
				assert.Equal(t, tt.state, alarms[0].State)
			}
		})
	}
}

func TestListMetrics(t *testing.T) {
	ctx := context.Background()
	m, clk := newTestMock()

	require.NoError(t, m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "ns", MetricName: "cpu", Value: 10, Timestamp: clk.Now()},
		{Namespace: "ns", MetricName: "mem", Value: 80, Timestamp: clk.Now()},
		{Namespace: "other", MetricName: "disk", Value: 50, Timestamp: clk.Now()},
	}))

	metrics, err := m.ListMetrics(ctx, "ns")
	require.NoError(t, err)
	assert.Len(t, metrics, 2)
	assert.Contains(t, metrics, "cpu")
	assert.Contains(t, metrics, "mem")

	metrics, err = m.ListMetrics(ctx, "empty")
	require.NoError(t, err)
	assert.Empty(t, metrics)
}
