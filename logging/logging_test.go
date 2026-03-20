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
)

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

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func newTestLogging(opts ...Option) *Logging {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewLogging(cloudwatchlogs.New(o), opts...)
}

func setupTestGroupAndStream(t *testing.T, l *Logging) {
	t.Helper()

	ctx := context.Background()

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "test-group"})
	requireNoError(t, err)

	_, err = l.CreateLogStream(ctx, "test-group", "test-stream")
	requireNoError(t, err)
}

func TestCreateLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		info, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		assertEqual(t, "grp", info.Name)
		assertNotEmpty(t, info.ARN)
	})

	t.Run("empty name error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: ""})
		assertError(t, err, true)
	})
}

func TestDeleteLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "del"})
		requireNoError(t, err)

		err = l.DeleteLogGroup(ctx, "del")
		requireNoError(t, err)
	})

	t.Run("not found error", func(t *testing.T) {
		l := newTestLogging()

		err := l.DeleteLogGroup(ctx, "nope")
		assertError(t, err, true)
	})
}

func TestGetLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g1"})
		requireNoError(t, err)

		info, err := l.GetLogGroup(ctx, "g1")
		requireNoError(t, err)

		assertEqual(t, "g1", info.Name)
	})

	t.Run("not found error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.GetLogGroup(ctx, "missing")
		assertError(t, err, true)
	})
}

func TestListLogGroups(t *testing.T) {
	ctx := context.Background()
	l := newTestLogging()

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "a"})
	requireNoError(t, err)

	_, err = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "b"})
	requireNoError(t, err)

	groups, err := l.ListLogGroups(ctx)
	requireNoError(t, err)

	assertEqual(t, 2, len(groups))
}

func TestCreateLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		info, err := l.CreateLogStream(ctx, "grp", "s1")
		requireNoError(t, err)

		assertEqual(t, "s1", info.Name)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.CreateLogStream(ctx, "nope", "s1")
		assertError(t, err, true)
	})
}

func TestDeleteLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		l := newTestLogging()
		setupTestGroupAndStream(t, l)

		err := l.DeleteLogStream(ctx, "test-group", "test-stream")
		requireNoError(t, err)
	})

	t.Run("not found error", func(t *testing.T) {
		l := newTestLogging()

		err := l.DeleteLogStream(ctx, "nope", "nope")
		assertError(t, err, true)
	})
}

func TestListLogStreams(t *testing.T) {
	ctx := context.Background()
	l := newTestLogging()
	setupTestGroupAndStream(t, l)

	streams, err := l.ListLogStreams(ctx, "test-group")
	requireNoError(t, err)

	assertEqual(t, 1, len(streams))
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
		requireNoError(t, err)
	})

	t.Run("nonexistent stream error", func(t *testing.T) {
		l := newTestLogging()

		err := l.PutLogEvents(ctx, "nope", "nope", nil)
		assertError(t, err, true)
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
		requireNoError(t, err)

		events, err := l.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "test-group",
		})
		requireNoError(t, err)

		assertEqual(t, 2, len(events))
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		l := newTestLogging()

		_, err := l.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "nope",
		})
		assertError(t, err, true)
	})
}

func TestWithRecorder(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	l := newTestLogging(WithRecorder(rec))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "rec-grp"})
	requireNoError(t, err)

	assertEqual(t, 1, rec.CallCount())

	calls := rec.CallsFor("logging", "CreateLogGroup")
	assertEqual(t, 1, len(calls))

	assertEqual(t, "logging", calls[0].Service)
	assertEqual(t, "CreateLogGroup", calls[0].Operation)

	if calls[0].Error != nil {
		t.Errorf("expected nil error in recorded call, got %v", calls[0].Error)
	}
}

func TestWithRecorderRecordsErrors(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	l := newTestLogging(WithRecorder(rec))

	_, err := l.GetLogGroup(ctx, "nonexistent")
	assertError(t, err, true)

	calls := rec.CallsFor("logging", "GetLogGroup")
	assertEqual(t, 1, len(calls))

	if calls[0].Error == nil {
		t.Error("expected error in recorded call, got nil")
	}
}

func TestWithRecorderMultipleOps(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	l := newTestLogging(WithRecorder(rec))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
	requireNoError(t, err)

	_, err = l.CreateLogStream(ctx, "grp", "s1")
	requireNoError(t, err)

	_ = l.DeleteLogGroup(ctx, "grp")

	assertEqual(t, 3, rec.CallCount())
	assertEqual(t, 1, rec.CallCountFor("logging", "CreateLogGroup"))
	assertEqual(t, 1, rec.CallCountFor("logging", "CreateLogStream"))
	assertEqual(t, 1, rec.CallCountFor("logging", "DeleteLogGroup"))
}

func TestWithMetrics(t *testing.T) {
	ctx := context.Background()
	mc := metrics.NewCollector()
	l := newTestLogging(WithMetrics(mc))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "m-grp"})
	requireNoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").
		ByLabel("service", "logging").
		ByLabel("operation", "CreateLogGroup").
		Count()
	assertEqual(t, 1, callsCount)

	durCount := q.ByName("call_duration").
		ByLabel("service", "logging").
		ByLabel("operation", "CreateLogGroup").
		Count()
	assertEqual(t, 1, durCount)
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
	assertEqual(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	ctx := context.Background()
	inj := inject.NewInjector()
	l := newTestLogging(WithErrorInjection(inj))

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("logging", "CreateLogGroup", injectedErr, inject.Always{})

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "fail"})
	assertError(t, err, true)

	assertEqual(t, injectedErr.Error(), err.Error())
}

func TestWithErrorInjectionSelectiveOp(t *testing.T) {
	ctx := context.Background()
	inj := inject.NewInjector()
	l := newTestLogging(WithErrorInjection(inj))

	injectedErr := fmt.Errorf("injected")
	inj.Set("logging", "DeleteLogGroup", injectedErr, inject.Always{})

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "ok-grp"})
	requireNoError(t, err)

	err = l.DeleteLogGroup(ctx, "ok-grp")
	assertError(t, err, true)
}

func TestWithErrorInjectionCountdown(t *testing.T) {
	ctx := context.Background()
	inj := inject.NewInjector()
	l := newTestLogging(WithErrorInjection(inj))

	injectedErr := fmt.Errorf("countdown error")
	inj.Set("logging", "ListLogGroups", injectedErr, inject.NewCountdown(1))

	_, err := l.ListLogGroups(ctx)
	assertError(t, err, true)

	groups, err := l.ListLogGroups(ctx)
	requireNoError(t, err)

	assertEqual(t, 0, len(groups))
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
	assertEqual(t, 1, len(calls))

	if calls[0].Error == nil {
		t.Error("expected error in recorded call, got nil")
	}
}

func TestWithMetricsAndRecorderCombined(t *testing.T) {
	ctx := context.Background()
	rec := recorder.New()
	mc := metrics.NewCollector()
	l := newTestLogging(WithRecorder(rec), WithMetrics(mc))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "combo"})
	requireNoError(t, err)

	assertEqual(t, 1, rec.CallCount())

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assertEqual(t, 1, callsCount)
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

	assertEqual(t, 9, rec.CallCount())

	ops := []string{
		"CreateLogGroup", "GetLogGroup", "ListLogGroups",
		"CreateLogStream", "ListLogStreams",
		"PutLogEvents", "GetLogEvents",
		"DeleteLogStream", "DeleteLogGroup",
	}

	for _, op := range ops {
		count := rec.CallCountFor("logging", op)
		if count != 1 {
			t.Errorf("expected 1 call for %s, got %d", op, count)
		}
	}
}

func TestWithRateLimiter(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	limiter := ratelimit.New(1, 1, fc)
	l := newTestLogging(WithRateLimiter(limiter))

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp1"})
	requireNoError(t, err)

	_, err = l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp2"})
	assertError(t, err, true)
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
	assertEqual(t, 2, len(calls))

	if calls[1].Error == nil {
		t.Error("expected rate limit error in second call")
	}
}

func TestWithLatency(t *testing.T) {
	l := newTestLogging(WithLatency(time.Millisecond))
	ctx := context.Background()

	start := time.Now()

	_, err := l.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "lat-grp"})
	requireNoError(t, err)

	elapsed := time.Since(start)

	if elapsed < time.Millisecond {
		t.Errorf("expected at least 1ms latency, got %v", elapsed)
	}
}

func TestListLogStreamsError(t *testing.T) {
	ctx := context.Background()
	l := newTestLogging()

	_, err := l.ListLogStreams(ctx, "nonexistent")
	assertError(t, err, true)
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
	assertEqual(t, 9, totalCalls)

	totalDur := q.ByName("call_duration").ByLabel("service", "logging").Count()
	assertEqual(t, 9, totalDur)
}
