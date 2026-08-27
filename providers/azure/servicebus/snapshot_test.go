package servicebus

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the Service Bus mock serializes its entire
// state and restores it into a fresh mock identity-preservingly.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	url := createStdQueue(t, src)

	for _, body := range []string{"m1", "m2", "m3"} {
		_, err := src.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: body})
		require.NoError(t, err)
	}

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	info, err := dst.GetQueueInfo(ctx, url)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, url, info.URL)

	msgs, err := dst.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 10})
	require.NoError(t, err)
	assert.Len(t, msgs, 3)
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(data, data2))
}
