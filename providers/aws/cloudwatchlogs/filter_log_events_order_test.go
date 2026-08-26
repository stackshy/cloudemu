package cloudwatchlogs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilterLogEventsAscendingDeterministic locks the multi-stream ordering fix:
// FilterLogEvents interleaves matches across streams in ascending timestamp
// order, and the result is stable run-to-run (never random map order).
func TestFilterLogEventsAscendingDeterministic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g"})
	require.NoError(t, err)

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	puts := []struct {
		stream string
		ms     int
	}{
		{"s1", 1000}, {"s3", 3000}, {"s2", 2000}, {"s1", 4000},
	}

	for _, p := range puts {
		_, _ = m.CreateLogStream(ctx, "g", p.stream)
		require.NoError(t, m.PutLogEvents(ctx, "g", p.stream, []driver.LogEvent{
			{Timestamp: base.Add(time.Duration(p.ms) * time.Millisecond), Message: p.stream},
		}))
	}

	want := []int64{
		base.Add(1000 * time.Millisecond).UnixMilli(),
		base.Add(2000 * time.Millisecond).UnixMilli(),
		base.Add(3000 * time.Millisecond).UnixMilli(),
		base.Add(4000 * time.Millisecond).UnixMilli(),
	}

	// Repeat to catch map-iteration nondeterminism.
	for range 10 {
		got, err := m.FilterLogEvents(ctx, &driver.FilterLogEventsInput{LogGroup: "g"})
		require.NoError(t, err)
		require.Len(t, got, 4)

		gotMs := make([]int64, len(got))
		for i := range got {
			gotMs[i] = got[i].Timestamp.UnixMilli()
		}
		assert.Equal(t, want, gotMs)
	}
}
