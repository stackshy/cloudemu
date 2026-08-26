package cloudwatchlogs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetLogEventsTimestampOrder locks the ordering fix: GetLogEvents returns
// events in timestamp order regardless of the order they were ingested. A
// "late" batch is put before an "early" one, yet the query returns [early, late].
func TestGetLogEventsTimestampOrder(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g"})
	require.NoError(t, err)
	_, err = m.CreateLogStream(ctx, "g", "s")
	require.NoError(t, err)

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, m.PutLogEvents(ctx, "g", "s", []driver.LogEvent{
		{Timestamp: base.Add(5000 * time.Millisecond), Message: "late"},
	}))
	require.NoError(t, m.PutLogEvents(ctx, "g", "s", []driver.LogEvent{
		{Timestamp: base.Add(1000 * time.Millisecond), Message: "early"},
	}))

	got, err := m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "g", LogStream: "s"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "early", got[0].Message)
	assert.Equal(t, "late", got[1].Message)
}

// TestRetentionUnsetNeverDrops locks the retention fix: a group with no retention
// policy never expires, so events far older than 30 days are still retained and
// returned by GetLogEvents (no phantom 30-day cutoff).
func TestRetentionUnsetNeverDrops(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g"})
	require.NoError(t, err)
	assert.Equal(t, 0, info.RetentionDays)

	_, err = m.CreateLogStream(ctx, "g", "s")
	require.NoError(t, err)

	// Clock is 2025-01-01; this event is ~1 year old.
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, m.PutLogEvents(ctx, "g", "s", []driver.LogEvent{
		{Timestamp: old, Message: "ancient"},
	}))

	got, err := m.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "g", LogStream: "s"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "ancient", got[0].Message)
}
