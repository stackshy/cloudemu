package logging

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/logging/driver"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogging(opts ...Option) *Logging {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewLogging(cloudwatchlogs.New(o), opts...)
}

func setupTestGroupAndStream(t *testing.T, l *Logging) {
	t.Helper()

	ctx := context.Background()

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "test-group"})
	require.NoError(t, err)

	_, err = l.CreateLogStream(ctx, "test-group", "test-stream")
	require.NoError(t, err)
}

func TestCreateLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		info, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		assert.Equal(t, "grp", info.Name)
		assert.NotEmpty(t, info.ResourceID)
	})

	t.Run("empty name error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: ""})
		require.Error(t, err)
	})
}

func TestDeleteLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "del"})
		require.NoError(t, err)

		err = l.DeleteLogGroup(ctx, "del")
		require.NoError(t, err)
	})

	t.Run("not found error", func(t *testing.T) {
		l := newTestLogging()

		err := l.DeleteLogGroup(ctx, "nope")
		require.Error(t, err)
	})
}

func TestGetLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g1"})
		require.NoError(t, err)

		info, err := l.GetLogGroup(ctx, "g1")
		require.NoError(t, err)

		assert.Equal(t, "g1", info.Name)
	})

	t.Run("not found error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.GetLogGroup(ctx, "missing")
		require.Error(t, err)
	})
}

func TestListLogGroups(t *testing.T) {
	ctx := context.Background()
	l := newTestLogging()

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "a"})
	require.NoError(t, err)

	_, err = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "b"})
	require.NoError(t, err)

	groups, err := l.ListLogGroups(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, len(groups))
}

func TestCreateLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		info, err := l.CreateLogStream(ctx, "grp", "s1")
		require.NoError(t, err)

		assert.Equal(t, "s1", info.Name)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogStream(ctx, "nope", "s1")
		require.Error(t, err)
	})
}

func TestDeleteLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()
		setupTestGroupAndStream(t, l)

		err := l.DeleteLogStream(ctx, "test-group", "test-stream")
		require.NoError(t, err)
	})

	t.Run("not found error", func(t *testing.T) {
		l := newTestLogging()

		err := l.DeleteLogStream(ctx, "nope", "nope")
		require.Error(t, err)
	})
}

func TestListLogStreams(t *testing.T) {
	ctx := context.Background()
	l := newTestLogging()
	setupTestGroupAndStream(t, l)

	streams, err := l.ListLogStreams(ctx, "test-group")
	require.NoError(t, err)

	assert.Equal(t, 1, len(streams))
}

func TestPutLogEvents(t *testing.T) {
	ctx := context.Background()
	baseTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()
		setupTestGroupAndStream(t, l)

		err := l.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "hello"},
		})
		require.NoError(t, err)
	})

	t.Run("nonexistent stream error", func(t *testing.T) {
		l := newTestLogging()

		err := l.PutLogEvents(ctx, "nope", "nope", nil)
		require.Error(t, err)
	})
}

func TestGetLogEvents(t *testing.T) {
	ctx := context.Background()
	baseTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()
		setupTestGroupAndStream(t, l)

		err := l.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "msg1"},
			{Timestamp: baseTime.Add(time.Second), Message: "msg2"},
		})
		require.NoError(t, err)

		events, err := l.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "test-group",
		})
		require.NoError(t, err)

		assert.Equal(t, 2, len(events))
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "nope",
		})
		require.Error(t, err)
	})
}

func TestWithRecorder(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	l := newTestLogging(WithRecorder(rec))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "rec-grp"})
	require.NoError(t, err)

	assert.Equal(t, 1, rec.CallCount())

	calls := rec.CallsFor("logging", "CreateLogGroup")
	assert.Equal(t, 1, len(calls))

	assert.Equal(t, "logging", calls[0].Service)
	assert.Equal(t, "CreateLogGroup", calls[0].Operation)
	assert.Nil(t, calls[0].Error)
}

func TestWithRecorderRecordsErrors(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	l := newTestLogging(WithRecorder(rec))

	_, err := l.GetLogGroup(ctx, "nonexistent")
	require.Error(t, err)

	calls := rec.CallsFor("logging", "GetLogGroup")
	assert.Equal(t, 1, len(calls))
	assert.NotNil(t, calls[0].Error)
}

func TestWithRecorderMultipleOps(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	l := newTestLogging(WithRecorder(rec))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
	require.NoError(t, err)

	_, err = l.CreateLogStream(ctx, "grp", "s1")
	require.NoError(t, err)

	_ = l.DeleteLogGroup(ctx, "grp")

	assert.Equal(t, 3, rec.CallCount())
	assert.Equal(t, 1, rec.CallCountFor("logging", "CreateLogGroup"))
	assert.Equal(t, 1, rec.CallCountFor("logging", "CreateLogStream"))
	assert.Equal(t, 1, rec.CallCountFor("logging", "DeleteLogGroup"))
}

func TestWithMetrics(t *testing.T) {
	ctx := context.Background()
	mc := metrics.NewCollector()
	l := newTestLogging(WithMetrics(mc))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "m-grp"})
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").
		ByLabel("service", "logging").
		ByLabel("operation", "CreateLogGroup").
		Count()
	assert.Equal(t, 1, callsCount)

	durCount := q.ByName("call_duration").
		ByLabel("service", "logging").
		ByLabel("operation", "CreateLogGroup").
		Count()
	assert.Equal(t, 1, durCount)
}

func TestWithMetricsRecordsErrors(t *testing.T) {
	ctx := context.Background()
	mc := metrics.NewCollector()
	l := newTestLogging(WithMetrics(mc))

	_ = l.DeleteLogGroup(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").
		ByLabel("service", "logging").
		ByLabel("operation", "DeleteLogGroup").
		Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	ctx := context.Background()
	inj := inject.NewInjector()
	l := newTestLogging(WithErrorInjection(inj))

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("logging", "CreateLogGroup", injectedErr, inject.Always{})

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "fail"})
	require.Error(t, err)

	assert.Equal(t, injectedErr.Error(), err.Error())
}

func TestWithErrorInjectionSelectiveOp(t *testing.T) {
	ctx := context.Background()
	inj := inject.NewInjector()
	l := newTestLogging(WithErrorInjection(inj))

	injectedErr := fmt.Errorf("injected")
	inj.Set("logging", "DeleteLogGroup", injectedErr, inject.Always{})

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "ok-grp"})
	require.NoError(t, err)

	err = l.DeleteLogGroup(ctx, "ok-grp")
	require.Error(t, err)
}

func TestWithErrorInjectionCountdown(t *testing.T) {
	ctx := context.Background()
	inj := inject.NewInjector()
	l := newTestLogging(WithErrorInjection(inj))

	injectedErr := fmt.Errorf("countdown error")
	inj.Set("logging", "ListLogGroups", injectedErr, inject.NewCountdown(1))

	_, err := l.ListLogGroups(ctx)
	require.Error(t, err)

	groups, err := l.ListLogGroups(ctx)
	require.NoError(t, err)

	assert.Equal(t, 0, len(groups))
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	inj := inject.NewInjector()
	l := newTestLogging(WithRecorder(rec), WithErrorInjection(inj))

	injectedErr := fmt.Errorf("injected")
	inj.Set("logging", "CreateLogGroup", injectedErr, inject.Always{})

	_, _ = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "fail"})

	calls := rec.CallsFor("logging", "CreateLogGroup")
	assert.Equal(t, 1, len(calls))
	assert.NotNil(t, calls[0].Error)
}

func TestWithMetricsAndRecorderCombined(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	mc := metrics.NewCollector()
	l := newTestLogging(WithRecorder(rec), WithMetrics(mc))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "combo"})
	require.NoError(t, err)

	assert.Equal(t, 1, rec.CallCount())

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.Equal(t, 1, callsCount)
}

func TestAllOperationsRecorded(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	l := newTestLogging(WithRecorder(rec))
	baseTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	_, _ = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
	_, _ = l.GetLogGroup(ctx, "grp")
	_, _ = l.ListLogGroups(ctx)
	_, _ = l.CreateLogStream(ctx, "grp", "s1")
	_, _ = l.ListLogStreams(ctx, "grp")

	_ = l.PutLogEvents(ctx, "grp", "s1", []driver.LogEvent{
		{Timestamp: baseTime, Message: "test"},
	})

	_, _ = l.GetLogEvents(ctx, driver.LogQueryInput{LogGroup: "grp"})
	_ = l.DeleteLogStream(ctx, "grp", "s1")
	_ = l.DeleteLogGroup(ctx, "grp")

	assert.Equal(t, 9, rec.CallCount())

	ops := []string{
		"CreateLogGroup", "GetLogGroup", "ListLogGroups",
		"CreateLogStream", "ListLogStreams",
		"PutLogEvents", "GetLogEvents",
		"DeleteLogStream", "DeleteLogGroup",
	}

	for _, op := range ops {
		count := rec.CallCountFor("logging", op)
		assert.Equal(t, 1, count, "expected 1 call for %s", op)
	}
}

func TestWithRateLimiter(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	limiter := ratelimit.New(1, 1, fc)
	l := newTestLogging(WithRateLimiter(limiter))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp1"})
	require.NoError(t, err)

	_, err = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp2"})
	require.Error(t, err)
}

func TestWithRateLimiterRecorded(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	limiter := ratelimit.New(1, 1, fc)
	rec := recorder.New()
	l := newTestLogging(WithRateLimiter(limiter), WithRecorder(rec))

	_, _ = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp1"})
	_, _ = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp2"})

	calls := rec.CallsFor("logging", "CreateLogGroup")
	assert.Equal(t, 2, len(calls))
	assert.NotNil(t, calls[1].Error)
}

func TestWithLatency(t *testing.T) {
	l := newTestLogging(WithLatency(time.Millisecond))
	ctx := context.Background()

	start := time.Now()

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "lat-grp"})
	require.NoError(t, err)

	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, time.Millisecond)
}

func TestListLogStreamsError(t *testing.T) {
	ctx := context.Background()
	l := newTestLogging()

	_, err := l.ListLogStreams(ctx, "nonexistent")
	require.Error(t, err)
}

func TestAllOperationsMetrics(t *testing.T) {
	ctx := context.Background()
	mc := metrics.NewCollector()
	l := newTestLogging(WithMetrics(mc))
	baseTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	_, _ = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
	_, _ = l.GetLogGroup(ctx, "grp")
	_, _ = l.ListLogGroups(ctx)
	_, _ = l.CreateLogStream(ctx, "grp", "s1")
	_, _ = l.ListLogStreams(ctx, "grp")

	_ = l.PutLogEvents(ctx, "grp", "s1", []driver.LogEvent{
		{Timestamp: baseTime, Message: "test"},
	})

	_, _ = l.GetLogEvents(ctx, driver.LogQueryInput{LogGroup: "grp"})
	_ = l.DeleteLogStream(ctx, "grp", "s1")
	_ = l.DeleteLogGroup(ctx, "grp")

	q := metrics.NewQuery(mc)

	totalCalls := q.ByName("calls_total").ByLabel("service", "logging").Count()
	assert.Equal(t, 9, totalCalls)

	totalDur := q.ByName("call_duration").ByLabel("service", "logging").Count()
	assert.Equal(t, 9, totalDur)
}
