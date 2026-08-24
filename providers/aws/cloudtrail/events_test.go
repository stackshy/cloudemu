package cloudtrail

import (
	"context"
	"strings"
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

	for i := 0; i < maxRecordedEvents+50; i++ {
		m.RecordEvent(&driver.Event{EventName: "Op" + strings.Repeat("x", 0), EventSource: "ec2.amazonaws.com"})
	}

	m.eventsMu.RLock()
	n := len(m.events)
	m.eventsMu.RUnlock()
	assert.Equal(t, maxRecordedEvents, n, "event log must stay bounded")

	page, _, err := m.LookupEvents(ctx, driver.LookupInput{MaxResults: maxLookupResults})
	require.NoError(t, err)
	assert.Len(t, page, maxLookupResults)
}

// EventRecorder is satisfied by the Mock.
var _ driver.EventRecorder = (*Mock)(nil)
