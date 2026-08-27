package sqs

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// TestSnapshotRestoreRoundTrip proves the SQS mock serializes its entire state
// and restores it into a fresh mock identity-preservingly: re-snapshotting the
// restored mock yields byte-identical JSON, and the seeded queue plus its
// enqueued messages come back under their original URL.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	q := createStdQueue(src, "orders")
	url := q.URL

	for _, body := range []string{"m1", "m2", "m3"} {
		_, err := src.SendMessage(ctx, driver.SendMessageInput{QueueURL: url, Body: body})
		requireNoError(t, err)
	}

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst, _ := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	info, err := dst.GetQueueInfo(ctx, url)
	requireNoError(t, err)
	if info == nil || info.URL != url {
		t.Fatalf("restored queue info = %v, want URL %q", info, url)
	}

	msgs, err := dst.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: url, MaxMessages: 10})
	requireNoError(t, err)
	if len(msgs) != 3 {
		t.Fatalf("restored queue returned %d messages, want 3", len(msgs))
	}
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst, _ := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	requireNoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("empty snapshot not stable: %s vs %s", data, data2)
	}
}
