package loganalytics

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshotRoundTripLogAnalytics(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g1"})
	require.NoError(t, err)
	_, err = src.CreateLogStream(ctx, "g1", "s1")
	require.NoError(t, err)

	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, src.PutLogEvents(ctx, "g1", "s1", []driver.LogEvent{
		{Timestamp: ts, Message: "hello"},
		{Timestamp: ts.Add(time.Second), Message: "world"},
	}))

	require.NoError(t, src.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "mf1", LogGroup: "g1", FilterPattern: "ERROR", MetricName: "Errors", MetricNamespace: "App",
	}))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	streams, err := dst.ListLogStreams(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, streams, 1)
	assert.Equal(t, "s1", streams[0].Name)

	events, err := dst.GetLogEvents(ctx, &driver.LogQueryInput{LogGroup: "g1", LogStream: "s1"})
	require.NoError(t, err)
	assert.Len(t, events, 2)

	filters, err := dst.DescribeMetricFilters(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, filters, 1)
	assert.Equal(t, "mf1", filters[0].Name)
}
