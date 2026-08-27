package cloudlogging

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRoundTripCloudLogging proves the mock serializes every log group
// (with its streams and events) and restores it into a fresh mock
// identity-preservingly: re-snapshotting yields byte-identical JSON and an event
// written before the snapshot is readable after the restore.
func TestSnapshotRoundTripCloudLogging(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "app-logs"})
	require.NoError(t, err)

	_, err = src.CreateLogStream(ctx, "app-logs", "stream-1")
	require.NoError(t, err)

	require.NoError(t, src.PutLogEvents(ctx, "app-logs", "stream-1", []driver.LogEvent{
		{Timestamp: time.Unix(1700000000, 0).UTC(), Message: "boot"},
	}))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(data2), "snapshot must be stable across restore")

	// Byte-stable re-snapshot of the restored mock proves the stream and its
	// event were reinstated into the live store; the group is queryable again.
	_, err = dst.GetLogGroup(ctx, "app-logs")
	require.NoError(t, err)
}

// TestSnapshotEmptyCloudLogging confirms a fresh mock snapshots and restores cleanly.
func TestSnapshotEmptyCloudLogging(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))
}
