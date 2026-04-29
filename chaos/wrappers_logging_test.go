package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	logdriver "github.com/stackshy/cloudemu/logging/driver"
)

func newChaosLogging(t *testing.T) (logdriver.Logging, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapLogging(cloudemu.NewAWS().CloudWatchLogs, e), e
}

func TestWrapLoggingCreateLogGroupChaos(t *testing.T) {
	l, e := newChaosLogging(t)
	ctx := context.Background()

	if _, err := l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "ok"}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("logging", time.Hour))

	if _, err := l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "fail"}); err == nil {
		t.Error("expected chaos error on CreateLogGroup")
	}
}

func TestWrapLoggingDeleteLogGroupChaos(t *testing.T) {
	l, e := newChaosLogging(t)
	ctx := context.Background()
	_, _ = l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "del"})

	e.Apply(chaos.ServiceOutage("logging", time.Hour))

	if err := l.DeleteLogGroup(ctx, "del"); err == nil {
		t.Error("expected chaos error on DeleteLogGroup")
	}
}

func TestWrapLoggingGetLogGroupChaos(t *testing.T) {
	l, e := newChaosLogging(t)
	ctx := context.Background()
	_, _ = l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "g"})

	e.Apply(chaos.ServiceOutage("logging", time.Hour))

	if _, err := l.GetLogGroup(ctx, "g"); err == nil {
		t.Error("expected chaos error on GetLogGroup")
	}
}

func TestWrapLoggingListLogGroupsChaos(t *testing.T) {
	l, e := newChaosLogging(t)
	ctx := context.Background()

	e.Apply(chaos.ServiceOutage("logging", time.Hour))

	if _, err := l.ListLogGroups(ctx); err == nil {
		t.Error("expected chaos error on ListLogGroups")
	}
}

func TestWrapLoggingPutLogEventsChaos(t *testing.T) {
	l, e := newChaosLogging(t)
	ctx := context.Background()
	_, _ = l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "p"})
	_, _ = l.CreateLogStream(ctx, "p", "s")

	e.Apply(chaos.ServiceOutage("logging", time.Hour))

	events := []logdriver.LogEvent{{Timestamp: time.Now(), Message: "x"}}
	if err := l.PutLogEvents(ctx, "p", "s", events); err == nil {
		t.Error("expected chaos error on PutLogEvents")
	}
}

func TestWrapLoggingGetLogEventsChaos(t *testing.T) {
	l, e := newChaosLogging(t)
	ctx := context.Background()
	_, _ = l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "ge"})

	e.Apply(chaos.ServiceOutage("logging", time.Hour))

	in := &logdriver.LogQueryInput{LogGroup: "ge", StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), Limit: 10}
	if _, err := l.GetLogEvents(ctx, in); err == nil {
		t.Error("expected chaos error on GetLogEvents")
	}
}

func TestWrapLoggingFilterLogEventsChaos(t *testing.T) {
	l, e := newChaosLogging(t)
	ctx := context.Background()
	_, _ = l.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: "fe"})

	e.Apply(chaos.ServiceOutage("logging", time.Hour))

	in := &logdriver.FilterLogEventsInput{LogGroup: "fe", StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), Limit: 10}
	if _, err := l.FilterLogEvents(ctx, in); err == nil {
		t.Error("expected chaos error on FilterLogEvents")
	}
}
