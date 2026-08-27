package sns

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRestoreRoundTrip proves the SNS mock serializes its entire state
// and restores it into a fresh mock identity-preservingly: the topic, its
// subscriptions, and its published-message log come back under their original
// identities.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	_, err := src.CreateTopic(ctx, driver.TopicConfig{Name: "alerts"})
	require.NoError(t, err)

	_, err = src.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: "alerts", Protocol: "sqs", Endpoint: "arn:aws:sqs:us-east-1:123456789012:q",
	})
	require.NoError(t, err)

	_, err = src.Publish(ctx, driver.PublishInput{TopicID: "alerts", Message: "hello"})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)

	if !bytes.Equal(data, data2) {
		t.Fatalf("snapshot not stable across restore:\n first=%s\nsecond=%s", data, data2)
	}

	topic, err := dst.GetTopic(ctx, "alerts")
	require.NoError(t, err)
	require.NotNil(t, topic)

	subs, err := dst.ListSubscriptions(ctx, "alerts")
	require.NoError(t, err)
	assert.Len(t, subs, 1)
}

// TestSnapshotEmpty confirms a fresh mock snapshots and restores without error.
func TestSnapshotEmpty(t *testing.T) {
	ctx := context.Background()

	src := newTestMock()

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	data2, err := dst.Snapshot(ctx, false)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(data, data2))
}
