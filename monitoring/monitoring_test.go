package monitoring

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/monitoring/driver"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDriver() (driver.Monitoring, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return cloudwatch.New(opts), fc
}

func newTestMonitoring(opts ...Option) (*Monitoring, *config.FakeClock) {
	d, fc := newTestDriver()
	return NewMonitoring(d, opts...), fc
}

func TestNewMonitoring(t *testing.T) {
	m, _ := newTestMonitoring()

	require.NotNil(t, m)
	require.NotNil(t, m.driver)
}

func TestPutMetricDataPortable(t *testing.T) {
	m, fc := newTestMonitoring()
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{
			Namespace:  "TestNS",
			MetricName: "CPUUtilization",
			Value:      42.0,
			Unit:       "Percent",
			Timestamp:  fc.Now(),
		},
	})
	require.NoError(t, err)
}

func TestGetMetricDataPortable(t *testing.T) {
	m, fc := newTestMonitoring()
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{
			Namespace:  "TestNS",
			MetricName: "CPUUtilization",
			Value:      42.0,
			Unit:       "Percent",
			Timestamp:  fc.Now(),
		},
	})
	require.NoError(t, err)

	result, err := m.GetMetricData(ctx, driver.GetMetricInput{
		Namespace:  "TestNS",
		MetricName: "CPUUtilization",
		StartTime:  fc.Now().Add(-1 * time.Hour),
		EndTime:    fc.Now().Add(1 * time.Hour),
		Period:     60,
		Stat:       "Average",
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Values), 1)
}

func TestListMetricsPortable(t *testing.T) {
	m, fc := newTestMonitoring()
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{
			Namespace:  "TestNS",
			MetricName: "CPUUtilization",
			Value:      42.0,
			Timestamp:  fc.Now(),
		},
	})
	require.NoError(t, err)

	metricNames, err := m.ListMetrics(ctx, "TestNS")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(metricNames), 1)
}

func TestCreateDeleteAlarmPortable(t *testing.T) {
	m, _ := newTestMonitoring()
	ctx := context.Background()

	err := m.CreateAlarm(ctx, driver.AlarmConfig{
		Name:               "test-alarm",
		Namespace:          "TestNS",
		MetricName:         "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          80.0,
		Period:             60,
		EvaluationPeriods:  1,
		Stat:               "Average",
	})
	require.NoError(t, err)

	alarms, err := m.DescribeAlarms(ctx, []string{"test-alarm"})
	require.NoError(t, err)
	assert.Equal(t, 1, len(alarms))
	assert.Equal(t, "test-alarm", alarms[0].Name)

	err = m.DeleteAlarm(ctx, "test-alarm")
	require.NoError(t, err)
}

func TestSetAlarmStatePortable(t *testing.T) {
	m, _ := newTestMonitoring()
	ctx := context.Background()

	err := m.CreateAlarm(ctx, driver.AlarmConfig{
		Name:               "state-alarm",
		Namespace:          "TestNS",
		MetricName:         "CPUUtilization",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          80.0,
		Period:             60,
		EvaluationPeriods:  1,
		Stat:               "Average",
	})
	require.NoError(t, err)

	err = m.SetAlarmState(ctx, "state-alarm", "ALARM", "testing")
	require.NoError(t, err)

	alarms, err := m.DescribeAlarms(ctx, []string{"state-alarm"})
	require.NoError(t, err)
	assert.Equal(t, "ALARM", alarms[0].State)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	m, fc := newTestMonitoring(WithRecorder(rec))
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.NoError(t, err)

	_, err = m.ListMetrics(ctx, "TestNS")
	require.NoError(t, err)

	err = m.CreateAlarm(ctx, driver.AlarmConfig{
		Name:               "rec-alarm",
		Namespace:          "TestNS",
		MetricName:         "CPU",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          50.0,
		Period:             60,
		EvaluationPeriods:  1,
		Stat:               "Average",
	})
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 3)

	putCalls := rec.CallCountFor("monitoring", "PutMetricData")
	assert.Equal(t, 1, putCalls)

	listCalls := rec.CallCountFor("monitoring", "ListMetrics")
	assert.Equal(t, 1, listCalls)

	createCalls := rec.CallCountFor("monitoring", "CreateAlarm")
	assert.Equal(t, 1, createCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	m, _ := newTestMonitoring(WithRecorder(rec))
	ctx := context.Background()

	// Delete nonexistent alarm should fail.
	_ = m.DeleteAlarm(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last, "expected a recorded call")
	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	m, fc := newTestMonitoring(WithMetrics(mc))
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.NoError(t, err)

	_, err = m.ListMetrics(ctx, "TestNS")
	require.NoError(t, err)

	err = m.CreateAlarm(ctx, driver.AlarmConfig{
		Name:               "met-alarm",
		Namespace:          "TestNS",
		MetricName:         "CPU",
		ComparisonOperator: "GreaterThanThreshold",
		Threshold:          50.0,
		Period:             60,
		EvaluationPeriods:  1,
		Stat:               "Average",
	})
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 3)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 3)
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	m, _ := newTestMonitoring(WithMetrics(mc))
	ctx := context.Background()

	_ = m.DeleteAlarm(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	m, _ := newTestMonitoring(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("monitoring", "PutMetricData", injectedErr, inject.Always{})

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0},
	})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	m, fc := newTestMonitoring(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("monitoring", "ListMetrics", injectedErr, inject.Always{})

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.NoError(t, err)

	_, err = m.ListMetrics(ctx, "TestNS")
	require.Error(t, err)

	listCalls := rec.CallsFor("monitoring", "ListMetrics")
	assert.Equal(t, 1, len(listCalls))
	assert.NotNil(t, listCalls[0].Error, "expected recorded ListMetrics call to have an error")
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	m, fc := newTestMonitoring(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("monitoring", "PutMetricData", injectedErr, inject.Always{})

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.Error(t, err)

	inj.Remove("monitoring", "PutMetricData")

	err = m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.NoError(t, err)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	d := cloudwatch.New(opts)
	limiter := ratelimit.New(1, 1, fc)
	m := NewMonitoring(d, WithRateLimiter(limiter))
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.NoError(t, err)

	_, err = m.ListMetrics(ctx, "TestNS")
	require.Error(t, err, "expected rate limit error on second call without time advance")
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	m, fc := newTestMonitoring(WithLatency(latency))
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.NoError(t, err)

	metricNames, err := m.ListMetrics(ctx, "TestNS")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(metricNames), 1)
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	m, fc := newTestMonitoring(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	err := m.PutMetricData(ctx, []driver.MetricDatum{
		{Namespace: "TestNS", MetricName: "CPU", Value: 10.0, Timestamp: fc.Now()},
	})
	require.NoError(t, err)

	_, err = m.ListMetrics(ctx, "TestNS")
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestPortableDeleteAlarmError(t *testing.T) {
	m, _ := newTestMonitoring()
	ctx := context.Background()

	err := m.DeleteAlarm(ctx, "no-alarm")
	require.Error(t, err)
}

func TestPortableSetAlarmStateError(t *testing.T) {
	m, _ := newTestMonitoring()
	ctx := context.Background()

	err := m.SetAlarmState(ctx, "no-alarm", "OK", "test")
	require.Error(t, err)
}
