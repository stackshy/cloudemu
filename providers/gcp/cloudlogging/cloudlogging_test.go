package cloudlogging

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func setupGroupAndStream(t *testing.T, m *Mock) {
	t.Helper()

	ctx := context.Background()

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "test-group"})
	require.NoError(t, err)

	_, err = m.CreateLogStream(ctx, "test-group", "test-stream")
	require.NoError(t, err)
}

func TestCreateLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name: "my-bucket",
		})
		require.NoError(t, err)

		assert.Equal(t, "my-bucket", info.Name)
		assert.NotEmpty(t, info.ResourceID)
		assert.NotEmpty(t, info.CreatedAt)
		assert.Equal(t, int64(0), info.StoredBytes)
	})

	t.Run("default retention 30 days", func(t *testing.T) {
		m := newTestMock()

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name: "retention-test",
		})
		require.NoError(t, err)

		assert.Equal(t, 30, info.RetentionDays)
	})

	t.Run("custom retention", func(t *testing.T) {
		m := newTestMock()

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name:          "custom-ret",
			RetentionDays: 90,
		})
		require.NoError(t, err)

		assert.Equal(t, 90, info.RetentionDays)
	})

	t.Run("with tags", func(t *testing.T) {
		m := newTestMock()
		tags := map[string]string{"env": "prod", "team": "backend"}

		info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{
			Name: "tagged-bucket",
			Tags: tags,
		})
		require.NoError(t, err)

		assert.Equal(t, "prod", info.Tags["env"])
		assert.Equal(t, "backend", info.Tags["team"])
	})

	t.Run("empty name error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: ""})
		require.Error(t, err)
	})

	t.Run("duplicate error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "dup"})
		require.NoError(t, err)

		_, err = m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "dup"})
		require.Error(t, err)
	})
}

func TestDeleteLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "del-me"})
		require.NoError(t, err)

		err = m.DeleteLogGroup(ctx, "del-me")
		require.NoError(t, err)

		_, err = m.GetLogGroup(ctx, "del-me")
		require.Error(t, err)
	})

	t.Run("not found error", func(t *testing.T) {
		m := newTestMock()

		err := m.DeleteLogGroup(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestGetLogGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "get-me"})
		require.NoError(t, err)

		info, err := m.GetLogGroup(ctx, "get-me")
		require.NoError(t, err)

		assert.Equal(t, "get-me", info.Name)
	})

	t.Run("not found error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.GetLogGroup(ctx, "nonexistent")
		require.Error(t, err)
	})
}

func TestListLogGroups(t *testing.T) {
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		m := newTestMock()

		groups, err := m.ListLogGroups(ctx)
		require.NoError(t, err)

		assert.Equal(t, 0, len(groups))
	})

	t.Run("multiple groups", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g1"})
		require.NoError(t, err)

		_, err = m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g2"})
		require.NoError(t, err)

		groups, err := m.ListLogGroups(ctx)
		require.NoError(t, err)

		assert.Equal(t, 2, len(groups))
	})
}

func TestCreateLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		info, err := m.CreateLogStream(ctx, "grp", "stream-1")
		require.NoError(t, err)

		assert.Equal(t, "stream-1", info.Name)
		assert.NotEmpty(t, info.CreatedAt)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogStream(ctx, "no-group", "s1")
		require.Error(t, err)
	})

	t.Run("empty stream name error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "")
		require.Error(t, err)
	})

	t.Run("duplicate stream error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "dup-stream")
		require.NoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "dup-stream")
		require.Error(t, err)
	})
}

func TestDeleteLogStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		err := m.DeleteLogStream(ctx, "test-group", "test-stream")
		require.NoError(t, err)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		err := m.DeleteLogStream(ctx, "no-group", "s1")
		require.Error(t, err)
	})

	t.Run("not found stream error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		err = m.DeleteLogStream(ctx, "grp", "nonexistent")
		require.Error(t, err)
	})
}

func TestListLogStreams(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "s1")
		require.NoError(t, err)

		_, err = m.CreateLogStream(ctx, "grp", "s2")
		require.NoError(t, err)

		streams, err := m.ListLogStreams(ctx, "grp")
		require.NoError(t, err)

		assert.Equal(t, 2, len(streams))
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.ListLogStreams(ctx, "no-group")
		require.Error(t, err)
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
		require.NoError(t, err)
	})

	t.Run("updates LastEvent", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		events := []driver.LogEvent{
			{Timestamp: baseTime, Message: "first"},
			{Timestamp: baseTime.Add(5 * time.Second), Message: "last"},
		}

		err := m.PutLogEvents(ctx, "test-group", "test-stream", events)
		require.NoError(t, err)

		streams, err := m.ListLogStreams(ctx, "test-group")
		require.NoError(t, err)

		expected := baseTime.Add(5 * time.Second).UTC().Format(time.RFC3339)
		assert.Equal(t, expected, streams[0].LastEvent)
	})

	t.Run("updates StoredBytes", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		events := []driver.LogEvent{
			{Timestamp: baseTime, Message: "12345"},
		}

		err := m.PutLogEvents(ctx, "test-group", "test-stream", events)
		require.NoError(t, err)

		info, err := m.GetLogGroup(ctx, "test-group")
		require.NoError(t, err)

		assert.Equal(t, int64(5), info.StoredBytes)
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		err := m.PutLogEvents(ctx, "no-group", "s1", nil)
		require.Error(t, err)
	})

	t.Run("nonexistent stream error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		err = m.PutLogEvents(ctx, "grp", "no-stream", nil)
		require.Error(t, err)
	})
}

func TestGetLogEvents(t *testing.T) {
	ctx := context.Background()
	baseTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("all events from all streams", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		_, err := m.CreateLogStream(ctx, "test-group", "stream-2")
		require.NoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "msg1"},
		})
		require.NoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "stream-2", []driver.LogEvent{
			{Timestamp: baseTime.Add(time.Second), Message: "msg2"},
		})
		require.NoError(t, err)

		events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup: "test-group",
		})
		require.NoError(t, err)

		assert.Equal(t, 2, len(events))
	})

	t.Run("specific stream filter", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		_, err := m.CreateLogStream(ctx, "test-group", "other")
		require.NoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "target"},
		})
		require.NoError(t, err)

		err = m.PutLogEvents(ctx, "test-group", "other", []driver.LogEvent{
			{Timestamp: baseTime, Message: "ignore"},
		})
		require.NoError(t, err)

		events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup:  "test-group",
			LogStream: "test-stream",
		})
		require.NoError(t, err)

		assert.Equal(t, 1, len(events))
		assert.Equal(t, "target", events[0].Message)
	})

	t.Run("pattern filter", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		err := m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "ERROR: something failed"},
			{Timestamp: baseTime.Add(time.Second), Message: "INFO: all good"},
			{Timestamp: baseTime.Add(2 * time.Second), Message: "ERROR: another failure"},
		})
		require.NoError(t, err)

		events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup: "test-group",
			Pattern:  "ERROR",
		})
		require.NoError(t, err)

		assert.Equal(t, 2, len(events))
	})

	t.Run("time range filter", func(t *testing.T) {
		m := newTestMock()
		setupGroupAndStream(t, m)

		err := m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
			{Timestamp: baseTime, Message: "early"},
			{Timestamp: baseTime.Add(time.Hour), Message: "middle"},
			{Timestamp: baseTime.Add(2 * time.Hour), Message: "late"},
		})
		require.NoError(t, err)

		events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup:  "test-group",
			StartTime: baseTime.Add(30 * time.Minute),
			EndTime:   baseTime.Add(90 * time.Minute),
		})
		require.NoError(t, err)

		assert.Equal(t, 1, len(events))
		assert.Equal(t, "middle", events[0].Message)
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
		require.NoError(t, err)

		result, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup: "test-group",
			Limit:    3,
		})
		require.NoError(t, err)

		assert.Equal(t, 3, len(result))
	})

	t.Run("nonexistent group error", func(t *testing.T) {
		m := newTestMock()

		_, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup: "no-group",
		})
		require.Error(t, err)
	})

	t.Run("nonexistent stream returns empty", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "grp"})
		require.NoError(t, err)

		events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup:  "grp",
			LogStream: "no-stream",
		})
		require.NoError(t, err)

		assert.Equal(t, 0, len(events))
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
		require.NoError(t, err)

		result, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup: "test-group",
		})
		require.NoError(t, err)

		assert.Equal(t, 100, len(result))
	})

	t.Run("empty group returns empty slice", func(t *testing.T) {
		m := newTestMock()

		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "empty-grp"})
		require.NoError(t, err)

		events, err := m.GetLogEvents(ctx, &driver.LogQueryInput{
			LogGroup: "empty-grp",
		})
		require.NoError(t, err)

		assert.Equal(t, 0, len(events))
	})
}
