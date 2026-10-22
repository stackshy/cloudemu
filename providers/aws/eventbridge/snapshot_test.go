package eventbridge

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the EventBridge mock serializes its entire
// state (buses, rules, targets, and tags) and restores it into a fresh mock
// identity-preservingly.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	_, err := src.CreateEventBus(ctx, driver.EventBusConfig{Name: "app-bus", Tags: map[string]string{"env": "prod"}})
	require.NoError(t, err)

	_, err = src.PutRule(ctx, &driver.RuleConfig{
		Name: "orders", EventBus: "app-bus", EventPattern: `{"source":["app.orders"]}`, State: "ENABLED",
	})
	require.NoError(t, err)

	require.NoError(t, src.PutTargets(ctx, "app-bus", "orders", []driver.Target{
		{ID: "t1", ARN: "arn:aws:sqs:us-east-1:123456789012:q"},
	}))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	rules, err := dst.ListRules(ctx, "app-bus")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "orders", rules[0].Name)
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
// A fresh mock already holds the default event bus, so this also exercises
// overwriting an existing bus on restore.
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

// TestSnapshotConnectionsAndAPIDestinations proves connections (with their
// credentials and secret ARN) and API destinations survive a snapshot and
// restore.
func TestSnapshotConnectionsAndAPIDestinations(t *testing.T) {
	ctx := context.Background()

	src, _ := newConnectionMock(t)

	c, err := src.CreateConnection(ctx, basicConnection("c1"))
	require.NoError(t, err)

	_, err = src.CreateAPIDestination(ctx, destinationConfig("d1", c.ARN))
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(data2))

	got, err := dst.DescribeConnection(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, c.SecretARN, got.SecretARN)

	stored, ok := dst.connections.Get("c1")
	require.True(t, ok)
	assert.Equal(t, "pass", stored.Info.AuthParameters.Basic.Password, "credentials survive a restore")

	d, err := dst.DescribeAPIDestination(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", d.State)
}

// TestSnapshotArchivesAndReplays proves archives (with their archived events)
// and replays survive a snapshot and restore.
func TestSnapshotArchivesAndReplays(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()
	a := createTestArchive(t, src, "arch", "default", `{"source":["s"]}`)
	putTestEvent(t, src, "", "s")

	_, err := src.StartReplay(ctx, &driver.ReplayConfig{
		Name: "rp", EventSourceARN: a.ARN,
		EventStartTime: a.CreatedAt.Add(-time.Hour), EventEndTime: a.CreatedAt.Add(time.Hour),
		Destination: driver.ReplayDestination{ARN: busARN("default")},
	})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(data2))

	arch, err := dst.DescribeArchive(ctx, "arch")
	require.NoError(t, err)
	assert.Equal(t, int64(1), arch.EventCount)

	// The restored archive keeps capturing from its bus.
	putTestEvent(t, dst, "", "s")

	arch, err = dst.DescribeArchive(ctx, "arch")
	require.NoError(t, err)
	assert.Equal(t, int64(2), arch.EventCount)

	rp, err := dst.DescribeReplay(ctx, "rp")
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", rp.State)
}
