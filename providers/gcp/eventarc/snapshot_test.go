package eventarc

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the Eventarc mock serializes its entire
// state — channels, triggers (rules), and targets — and restores it into a fresh
// mock identity-preservingly.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src, _ := newTestMock()

	_, err := src.CreateEventBus(ctx, driver.EventBusConfig{Name: "app-channel"})
	require.NoError(t, err)

	_, err = src.PutRule(ctx, &driver.RuleConfig{
		Name: "trigger1", EventBus: "app-channel", EventPattern: `{"source":["app"]}`, State: "ENABLED",
	})
	require.NoError(t, err)

	require.NoError(t, src.PutTargets(ctx, "app-channel", "trigger1", []driver.Target{
		{ID: "t1", ARN: "projects/test-project/locations/us-central1/services/run"},
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

	rules, err := dst.ListRules(ctx, "app-channel")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "trigger1", rules[0].Name)
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
