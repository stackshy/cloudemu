package cloudwatchlogs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutRetentionPolicyValidation(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)

	err := m.PutRetentionPolicy(ctx, "g", 2)
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	err = m.PutRetentionPolicy(ctx, "missing", 7)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	require.NoError(t, m.PutRetentionPolicy(ctx, "g", 7))

	g, err := m.GetLogGroup(ctx, "g")
	require.NoError(t, err)
	assert.Equal(t, 7, g.RetentionDays)
}

func TestDeleteRetentionPolicy(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 7)

	require.NoError(t, m.DeleteRetentionPolicy(ctx, "g"))

	g, err := m.GetLogGroup(ctx, "g")
	require.NoError(t, err)
	assert.Equal(t, 0, g.RetentionDays)

	// Never-expire again: a year later the event is still there.
	_, err = m.PutLogEventsWithResult(ctx, "g", "s", []driver.LogEvent{{Timestamp: testClockStart(), Message: "keep"}})
	require.NoError(t, err)
	advanceClock(t, m, 365*24*time.Hour)
	assert.Equal(t, []string{"keep"}, storedMessages(t, m))

	err = m.DeleteRetentionPolicy(ctx, "missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestRetentionPurgeIsConsistent is the regression for GetLogEvents and
// FilterLogEvents disagreeing about expired events: both now read the same
// purged data, and storedBytes drops with the purge.
func TestRetentionPurgeIsConsistent(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 1)
	now := testClockStart()

	_, err := m.PutLogEventsWithResult(ctx, "g", "s", []driver.LogEvent{
		{Timestamp: now.Add(-time.Hour), Message: "older"},
		{Timestamp: now, Message: "newer"},
	})
	require.NoError(t, err)

	before, err := m.ListLogStreams(ctx, "g")
	require.NoError(t, err)

	// 23h later only "older" (now 24h old) has passed the 1-day retention.
	advanceClock(t, m, 23*time.Hour+time.Minute)

	assert.Equal(t, []string{"newer"}, storedMessages(t, m))

	filtered, err := m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{LogGroup: "g"})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "newer", filtered[0].Message)

	g, err := m.GetLogGroup(ctx, "g")
	require.NoError(t, err)
	assert.Equal(t, int64(len("newer")), g.StoredBytes)

	// AWS keeps reporting the stream's first event time after expiry.
	after, err := m.ListLogStreams(ctx, "g")
	require.NoError(t, err)
	assert.Equal(t, before[0].FirstEvent, after[0].FirstEvent)
}

// TestPutRetentionPolicyPurgesImmediately covers shortening retention on a
// group that already holds older events.
func TestPutRetentionPolicyPurgesImmediately(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)
	now := testClockStart()

	// Two uploads: one batch may not span more than 24 hours.
	_, err := m.PutLogEventsWithResult(ctx, "g", "s", []driver.LogEvent{{Timestamp: now.Add(-3 * 24 * time.Hour), Message: "old"}})
	require.NoError(t, err)
	_, err = m.PutLogEventsWithResult(ctx, "g", "s", []driver.LogEvent{{Timestamp: now, Message: "new"}})
	require.NoError(t, err)

	require.NoError(t, m.PutRetentionPolicy(ctx, "g", 1))

	g, err := m.GetLogGroup(ctx, "g")
	require.NoError(t, err)
	assert.Equal(t, int64(len("new")), g.StoredBytes)

	filtered, err := m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{LogGroup: "g"})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "new", filtered[0].Message)
}
