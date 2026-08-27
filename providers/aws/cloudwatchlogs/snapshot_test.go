package cloudwatchlogs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// TestSnapshotRoundTripCloudWatchLogs proves a snapshot/restore round-trip
// preserves a log group, its stream, and the stream's events.
func TestSnapshotRoundTripCloudWatchLogs(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	if _, err := src.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "/svc/app"}); err != nil {
		t.Fatalf("create log group: %v", err)
	}

	if _, err := src.CreateLogStream(ctx, "/svc/app", "s1"); err != nil {
		t.Fatalf("create log stream: %v", err)
	}

	if err := src.PutLogEvents(ctx, "/svc/app", "s1", []driver.LogEvent{
		{Timestamp: time.Unix(1, 0), Message: "hello"},
	}); err != nil {
		t.Fatalf("put log events: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	events, err := dst.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "/svc/app", LogStream: "s1"})
	if err != nil {
		t.Fatalf("get restored events: %v", err)
	}

	if len(events) != 1 || events[0].Message != "hello" {
		t.Fatalf("restored events = %+v, want one 'hello'", events)
	}
}
