package cloudwatchlogs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/logging/driver"
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

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func setupGroupAndStream(t *testing.T, m *Mock) {
	t.Helper()

	ctx := context.Background()

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "test-group"})
	requireNoError(t, err)

	_, err = m.CreateLogStream(ctx, "test-group", "test-stream")
	requireNoError(t, err)
}

func TestCreateLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name: "my-group",
		})
		requireNoError(t, err)

		assertEqual(t, "my-group", info.Name)
		assertNotEmpty(t, info.ARN)
		assertNotEmpty(t, info.CreatedAt)
		assertEqual(t, int64(0), info.StoredBytes)
	})

	t.Run("default retention 30 days", func(t *testing.T) {
		m := newTestMock()

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name: "retention-test",
		})
		requireNoError(t, err)

		assertEqual(t, 30, info.RetentionDays)
	})

	t.Run("custom retention", func(t *testing.T) {
		m := newTestMock()

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name:          "custom-ret",
			RetentionDays: 90,
		})
		requireNoError(t, err)

		assertEqual(t, 90, info.RetentionDays)
	})

	t.Run("with tags", func(t *testing.T) {
		m := newTestMock()
		tags := map[string]string{"env": "prod", "team": "backend"}

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name: "tagged-group",
			Tags: tags,
		})
		requireNoError(t, err)

		assertEqual(t, "prod", info.Tags["env"])
		assertEqual(t, "backend", info.Tags["team"])
	})

	t.Run("empty name error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: ""})
		assertError(t, err, true)
	})

	t.Run("duplicate error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "dup"})
		requireNoError(t, err)

		_, err = m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "dup"})
		assertError(t, err, true)
	})
}

func TestDeleteLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "del-me"})
		requireNoError(t, err)

		err = m.DeleteLogGroup(ctx, "del-me")
		requireNoError(t, err)

		_, err = m.GetLogGroup(ctx, "del-me")
		assertError(t, err, true)
	})

	t.Run("not found error", func(t *testing.T) {
		m := newTestMock()

		err := m.DeleteLogGroup(ctx, "nonexistent")
		assertError(t, err, true)
	})
}

func TestGetLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "get-me"})
		requireNoError(t, err)

		info, err := m.GetLogGroup(ctx, "get-me")
		requireNoError(t, err)

		assertEqual(t, "get-me", info.Name)
	})

	t.Run("not found error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.GetLogGroup(ctx, "nonexistent")
		assertError(t, err, true)
	})
}

func TestListLogGroups(t *testing.T) {
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		m := newTestMock()

		groups, err := m.ListLogGroups(ctx)
		requireNoError(t, err)

		assertEqual(t, 0, len(groups))
	})

	t.Run("multiple groups", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g1"})
		requireNoError(t, err)

		_, err = m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g2"})
		requireNoError(t, err)

		groups, err := m.ListLogGroups(ctx)
		requireNoError(t, err)

		assertEqual(t, 2, len(groups))
	})
}

func TestCreateLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		info, err := m.CreateLogStream(ctx, "grp", "stream-1")
		requireNoError(t, err)

		assertEqual(t, "stream-1", info.Name)
		assertNotEmpty(t, info.CreatedAt)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogStream(ctx, "no-group", "s1")
		assertError(t, err, true)
	})

	t.Run("empty stream name error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "")
		assertError(t, err, true)
	})

	t.Run("duplicate stream error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "dup-stream")
		requireNoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "dup-stream")
		assertError(t, err, true)
	})
}

func TestDeleteLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		err := m.DeleteLogStream(ctx, "test-group", "test-stream")
		requireNoError(t, err)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		err := m.DeleteLogStream(ctx, "no-group", "s1")
		assertError(t, err, true)
	})

	t.Run("not found stream error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		err = m.DeleteLogStream(ctx, "grp", "nonexistent")
		assertError(t, err, true)
	})
}

func TestListLogStreams(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "s1")
		requireNoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "s2")
		requireNoError(t, err)

		streams, err := m.ListLogStreams(ctx, "grp")
		requireNoError(t, err)

		assertEqual(t, 2, len(streams))
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.ListLogStreams(ctx, "no-group")
		assertError(t, err, true)
	})
}

func TestPutLogEvents(t *testing.T) {
	ctx := context.Background()
	baseTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		events := []driver.LogEvent{
			{Timestamp: baseTime, Message: "hello"},
			{Timestamp: baseTime.Add(time.Second), Message: "world"},
		}

		err := m.PutLogEvents(ctx, "test-group", "test-stream", events)
		requireNoError(t, err)
	})

	t.Run("updates LastEvent", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		events := []driver.LogEvent{
			{Timestamp: baseTime, Message: "first"},
			{Timestamp: baseTime.Add(5 * time.Second), Message: "last"},
		}

		err := m.PutLogEvents(ctx, "test-group", "test-stream", events)
		requireNoError(t, err)

		streams, err := m.ListLogStreams(ctx, "test-group")
		requireNoError(t, err)

		expected := baseTime.Add(5 * time.Second).UTC().Format(time.RFC3339)
		assertEqual(t, expected, streams[0].LastEvent)
	})

	t.Run("updates StoredBytes", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		events := []driver.LogEvent{
			{Timestamp: baseTime, Message: "12345"},
		}

		err := m.PutLogEvents(ctx, "test-group", "test-stream", events)
		requireNoError(t, err)

		info, err := m.GetLogGroup(ctx, "test-group")
		requireNoError(t, err)

		assertEqual(t, int64(5), info.StoredBytes)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		err := m.PutLogEvents(ctx, "no-group", "s1", nil)
		assertError(t, err, true)
	})

	t.Run("nonexistent stream error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		err = m.PutLogEvents(ctx, "grp", "no-stream", nil)
		assertError(t, err, true)
	})
}

func TestGetLogEvents(t *testing.T) {
	ctx := context.Background()
	baseTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("all events from all streams", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		_, err := m.CreateLogStream(ctx, "test-group", "stream-2")
		requireNoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "msg1"},
		})
		requireNoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "stream-2", []driver.LogEvent{
			{Timestamp: baseTime.Add(time.Second), Message: "msg2"},
		})
		requireNoError(t, err)

		events, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "test-group",
		})
		requireNoError(t, err)

		assertEqual(t, 2, len(events))
	})

	t.Run("specific stream filter", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		_, err := m.CreateLogStream(ctx, "test-group", "other")
		requireNoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "target"},
		})
		requireNoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "other", []driver.LogEvent{
			{Timestamp: baseTime, Message: "ignore"},
		})
		requireNoError(t, err)

		events, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup:  "test-group",
			LogStream: "test-stream",
		})
		requireNoError(t, err)

		assertEqual(t, 1, len(events))
		assertEqual(t, "target", events[0].Message)
	})

	t.Run("pattern filter", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		err := m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "ERROR: something failed"},
			{Timestamp: baseTime.Add(time.Second), Message: "INFO: all good"},
			{Timestamp: baseTime.Add(2 * time.Second), Message: "ERROR: another failure"},
		})
		requireNoError(t, err)

		events, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "test-group",
			Pattern:  "ERROR",
		})
		requireNoError(t, err)

		assertEqual(t, 2, len(events))
	})

	t.Run("time range filter", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		err := m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "early"},
			{Timestamp: baseTime.Add(time.Hour), Message: "middle"},
			{Timestamp: baseTime.Add(2 * time.Hour), Message: "late"},
		})
		requireNoError(t, err)

		events, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup:  "test-group",
			StartTime: baseTime.Add(30 * time.Minute),
			EndTime:   baseTime.Add(90 * time.Minute),
		})
		requireNoError(t, err)

		assertEqual(t, 1, len(events))
		assertEqual(t, "middle", events[0].Message)
	})

	t.Run("with limit", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		events := make([]driver.LogEvent, 10)
		for i := range events {
			events[i] = driver.LogEvent{
				Timestamp: baseTime.Add(time.Duration(i) * time.Second),
				Message:   "msg",
			}
		}

		err := m.PutLogEvents(ctx, "test-group", "test-stream", events)
		requireNoError(t, err)

		result, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "test-group",
			Limit:    3,
		})
		requireNoError(t, err)

		assertEqual(t, 3, len(result))
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "no-group",
		})
		assertError(t, err, true)
	})

	t.Run("nonexistent stream returns empty", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		requireNoError(t, err)

		events, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup:  "grp",
			LogStream: "no-stream",
		})
		requireNoError(t, err)

		assertEqual(t, 0, len(events))
	})

	t.Run("default limit 100", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		events := make([]driver.LogEvent, 150)
		for i := range events {
			events[i] = driver.LogEvent{
				Timestamp: baseTime.Add(time.Duration(i) * time.Second),
				Message:   "msg",
			}
		}

		err := m.PutLogEvents(ctx, "test-group", "test-stream", events)
		requireNoError(t, err)

		result, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "test-group",
		})
		requireNoError(t, err)

		assertEqual(t, 100, len(result))
	})

	t.Run("empty group returns empty slice", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "empty-grp"})
		requireNoError(t, err)

		events, err := m.GetLogEvents(ctx, driver.LogQueryInput{
			LogGroup: "empty-grp",
		})
		requireNoError(t, err)

		assertEqual(t, 0, len(events))
	})
}
