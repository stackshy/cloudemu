package cloudtrail

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

func TestRecordAndLookupEvents(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012")))
	ctx := context.Background()

	m.RecordEvent(&driver.Event{EventName: "RunInstances", EventSource: "ec2.amazonaws.com", ReadOnly: "false", AccessKeyID: "AKIDTEST"})
	fc.Advance(time.Second)
	m.RecordEvent(&driver.Event{EventName: "DescribeInstances", EventSource: "ec2.amazonaws.com", ReadOnly: "true"})
	fc.Advance(time.Second)
	m.RecordEvent(&driver.Event{EventName: "CreateBucket", EventSource: "s3.amazonaws.com", ReadOnly: "false"})

	// Newest first, all fields populated.
	all, next, err := m.LookupEvents(ctx, driver.LookupInput{})
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, all, 3)
	assert.Equal(t, "CreateBucket", all[0].EventName)
	assert.Equal(t, "RunInstances", all[2].EventName)
	assert.False(t, all[0].EventTime.IsZero())
	assert.NotEmpty(t, all[0].EventID)
	assert.Contains(t, all[0].CloudTrailEvent, "CreateBucket")
	assert.Contains(t, all[2].CloudTrailEvent, "AKIDTEST")

	// Filter by EventName.
	byName, _, _ := m.LookupEvents(ctx, driver.LookupInput{
		LookupAttributes: []driver.LookupAttribute{{AttributeKey: "EventName", AttributeValue: "RunInstances"}},
	})
	require.Len(t, byName, 1)
	assert.Equal(t, "RunInstances", byName[0].EventName)

	// Filter by ReadOnly.
	ro, _, _ := m.LookupEvents(ctx, driver.LookupInput{
		LookupAttributes: []driver.LookupAttribute{{AttributeKey: "ReadOnly", AttributeValue: "true"}},
	})
	require.Len(t, ro, 1)
	assert.Equal(t, "DescribeInstances", ro[0].EventName)

	// Filter by EventSource.
	s3ev, _, _ := m.LookupEvents(ctx, driver.LookupInput{
		LookupAttributes: []driver.LookupAttribute{{AttributeKey: "EventSource", AttributeValue: "s3.amazonaws.com"}},
	})
	require.Len(t, s3ev, 1)
	assert.Equal(t, "CreateBucket", s3ev[0].EventName)

	// Time window: only events at/after the second record.
	windowed, _, _ := m.LookupEvents(ctx, driver.LookupInput{StartTime: all[1].EventTime})
	require.Len(t, windowed, 2)

	// Pagination.
	p1, tok, _ := m.LookupEvents(ctx, driver.LookupInput{MaxResults: 1})
	require.Len(t, p1, 1)
	require.NotEmpty(t, tok)
	p2, _, _ := m.LookupEvents(ctx, driver.LookupInput{MaxResults: 1, NextToken: tok})
	require.Len(t, p2, 1)
	assert.NotEqual(t, p1[0].EventID, p2[0].EventID)
}

func TestLookupEventsRingBufferBounded(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// Record well past the high-water mark so the amortized trim fires.
	for i := 0; i < 3*maxRecordedEvents; i++ {
		m.RecordEvent(&driver.Event{EventName: "Op", EventSource: "ec2.amazonaws.com"})
	}

	m.eventsMu.RLock()
	n := len(m.events)
	m.eventsMu.RUnlock()
	// The log is bounded: the amortized trim keeps it at or below the 2x cap.
	assert.LessOrEqual(t, n, 2*maxRecordedEvents, "event log must stay bounded")
	assert.GreaterOrEqual(t, n, maxRecordedEvents, "trim keeps at least cap events")

	page, _, err := m.LookupEvents(ctx, driver.LookupInput{MaxResults: maxLookupResults})
	require.NoError(t, err)
	assert.Len(t, page, maxLookupResults)
}

// EventRecorder is satisfied by the Mock.
var _ driver.EventRecorder = (*Mock)(nil)

// TestLookupEventsPaginationNoDuplicates pins that paginating through all events
// never duplicates or skips, even when new events are recorded between pages
// (the cursor is keyed on the event id, not a positional offset).
func TestLookupEventsPaginationStableAcrossInserts(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc)))
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		fc.Advance(time.Second)
		m.RecordEvent(&driver.Event{EventName: "Op", EventSource: "ec2.amazonaws.com"})
	}

	seen := map[string]int{}
	var token string
	pages := 0

	for {
		fc.Advance(time.Second)
		// A new event is inserted at the front between every page.
		m.RecordEvent(&driver.Event{EventName: "Noise", EventSource: "ec2.amazonaws.com"})

		out, next, err := m.LookupEvents(ctx, driver.LookupInput{
			MaxResults:       2,
			NextToken:        token,
			LookupAttributes: []driver.LookupAttribute{{AttributeKey: "EventName", AttributeValue: "Op"}},
		})
		require.NoError(t, err)

		for i := range out {
			seen[out[i].EventID]++
		}

		pages++
		if next == "" || pages > 10 {
			break
		}
		token = next
	}

	require.Len(t, seen, 5, "should page through exactly the 5 Op events")
	for id, n := range seen {
		assert.Equal(t, 1, n, "event %s returned %d times, want exactly once", id, n)
	}
}
