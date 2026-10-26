package cloudwatchlogs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWindowMock(t *testing.T, retentionDays int) *Mock {
	t.Helper()

	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g"})
	require.NoError(t, err)
	_, err = m.CreateLogStream(ctx, "g", "s")
	require.NoError(t, err)

	if retentionDays > 0 {
		require.NoError(t, m.PutRetentionPolicy(ctx, "g", retentionDays))
	}

	return m
}

func intPtr(v int) *int { return &v }

func storedMessages(t *testing.T, m *Mock) []string {
	t.Helper()

	got, err := m.GetLogEvents(context.Background(), &driver.LogQueryInput{LogGroup: "g", LogStream: "s"})
	require.NoError(t, err)

	out := make([]string, 0, len(got))
	for _, e := range got {
		out = append(out, e.Message)
	}

	return out
}

func TestPutLogEventsIngestWindow(t *testing.T) {
	now := testClockStart()

	tests := []struct {
		name      string
		retention int
		events    []driver.LogEvent
		wantKept  []string
		wantRej   *driver.RejectedLogEventsInfo
	}{
		{
			name:     "all inside the window",
			events:   []driver.LogEvent{{Timestamp: now.Add(-time.Hour), Message: "a"}, {Timestamp: now, Message: "b"}},
			wantKept: []string{"a", "b"},
		},
		{
			name: "too old prefix dropped",
			events: []driver.LogEvent{
				{Timestamp: now.Add(-15 * 24 * time.Hour), Message: "old1"},
				{Timestamp: now.Add(-15*24*time.Hour + time.Minute), Message: "old2"},
				{Timestamp: now, Message: "ok"},
			},
			wantKept: []string{"ok"},
			wantRej:  &driver.RejectedLogEventsInfo{TooOldLogEventEndIndex: intPtr(2)},
		},
		{
			name: "too new suffix dropped",
			events: []driver.LogEvent{
				{Timestamp: now, Message: "ok"},
				{Timestamp: now.Add(2*time.Hour + time.Second), Message: "future"},
			},
			wantKept: []string{"ok"},
			wantRej:  &driver.RejectedLogEventsInfo{TooNewLogEventStartIndex: intPtr(1)},
		},
		{
			name:     "exactly two hours ahead is accepted",
			events:   []driver.LogEvent{{Timestamp: now.Add(2 * time.Hour), Message: "edge"}},
			wantKept: []string{"edge"},
		},
		{
			name:      "older than retention dropped as expired",
			retention: 1,
			events: []driver.LogEvent{
				{Timestamp: now.Add(-2 * 24 * time.Hour), Message: "expired"},
				{Timestamp: now, Message: "ok"},
			},
			wantKept: []string{"ok"},
			wantRej:  &driver.RejectedLogEventsInfo{ExpiredLogEventEndIndex: intPtr(1)},
		},
		{
			name:      "old, expired and future together",
			retention: 3,
			events: []driver.LogEvent{
				{Timestamp: now.Add(-20 * 24 * time.Hour), Message: "old"},
				{Timestamp: now.Add(-5 * 24 * time.Hour), Message: "expired"},
				{Timestamp: now, Message: "ok"},
				{Timestamp: now.Add(3 * time.Hour), Message: "future"},
			},
			wantKept: []string{"ok"},
			wantRej: &driver.RejectedLogEventsInfo{
				TooOldLogEventEndIndex:   intPtr(1),
				ExpiredLogEventEndIndex:  intPtr(2),
				TooNewLogEventStartIndex: intPtr(3),
			},
		},
		{
			name:     "every event rejected still succeeds",
			events:   []driver.LogEvent{{Timestamp: now.Add(5 * time.Hour), Message: "future"}},
			wantKept: []string{},
			wantRej:  &driver.RejectedLogEventsInfo{TooNewLogEventStartIndex: intPtr(0)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newWindowMock(t, tc.retention)

			res, err := m.PutLogEventsWithResult(context.Background(), "g", "s", tc.events)
			require.NoError(t, err)
			assert.Equal(t, tc.wantRej, res.Rejected)
			assert.Equal(t, tc.wantKept, storedMessages(t, m))
		})
	}
}

func TestPutLogEventsSpanAppliesToAcceptedEventsOnly(t *testing.T) {
	now := testClockStart()
	m := newWindowMock(t, 0)

	// 20 days old (dropped) to now would span far more than 24h, but only the
	// accepted event counts, so the batch succeeds.
	res, err := m.PutLogEventsWithResult(context.Background(), "g", "s", []driver.LogEvent{
		{Timestamp: now.Add(-20 * 24 * time.Hour), Message: "old"},
		{Timestamp: now, Message: "ok"},
	})
	require.NoError(t, err)
	assert.Equal(t, intPtr(1), res.Rejected.TooOldLogEventEndIndex)

	// Two accepted events 25h apart still fail the whole batch.
	_, err = m.PutLogEventsWithResult(context.Background(), "g", "s", []driver.LogEvent{
		{Timestamp: now.Add(-25 * time.Hour), Message: "a"},
		{Timestamp: now, Message: "b"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, []string{"ok"}, storedMessages(t, m))
}

func TestPutLogEventsRejectedEventsAreNotCounted(t *testing.T) {
	now := testClockStart()
	m := newWindowMock(t, 0)

	_, err := m.PutLogEventsWithResult(context.Background(), "g", "s", []driver.LogEvent{
		{Timestamp: now, Message: "12345"},
		{Timestamp: now.Add(3 * time.Hour), Message: "future-bytes"},
	})
	require.NoError(t, err)

	g, err := m.GetLogGroup(context.Background(), "g")
	require.NoError(t, err)
	assert.Equal(t, int64(5), g.StoredBytes)
}

func TestPutLogEventsSequenceToken(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)
	ev := []driver.LogEvent{{Timestamp: testClockStart(), Message: "m"}}

	first, err := m.PutLogEventsWithResult(ctx, "g", "s", ev)
	require.NoError(t, err)
	second, err := m.PutLogEventsWithResult(ctx, "g", "s", ev)
	require.NoError(t, err)

	assert.Len(t, first.NextSequenceToken, 56)
	assert.Equal(t, -1, strings.IndexFunc(first.NextSequenceToken, func(r rune) bool { return r < '0' || r > '9' }))
	assert.NotEqual(t, first.NextSequenceToken, second.NextSequenceToken)

	// The upload count survives a snapshot, so tokens keep moving forward.
	raw, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, raw))

	third, err := restored.PutLogEventsWithResult(ctx, "g", "s", ev)
	require.NoError(t, err)
	assert.Greater(t, third.NextSequenceToken, second.NextSequenceToken)
}
