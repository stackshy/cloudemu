package cloudtrail

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// TestSnapshotRoundTripCloudTrail proves a snapshot/restore round-trip preserves
// trails (with their ARN index), event data stores, tags, and the recorded
// management-event log under their original identities.
func TestSnapshotRoundTripCloudTrail(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	tr, err := src.CreateTrail(ctx, driver.CreateTrailInput{Name: "my-trail", S3BucketName: "bkt"})
	require.NoError(t, err)

	eds, err := src.CreateEventDataStore(ctx, driver.CreateEventDataStoreInput{Name: "eds1"})
	require.NoError(t, err)

	require.NoError(t, src.AddTags(ctx, tr.TrailARN, map[string]string{"env": "prod"}))

	src.RecordEvent(&driver.Event{
		EventID: "e1", EventName: "RunInstances", EventSource: "ec2.amazonaws.com",
		EventTime: time.Unix(100, 0).UTC(), Username: "alice",
	})

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newMock()
	require.NoError(t, dst.Restore(ctx, raw))

	// Trail resolves by both name and its ARN (the ARN index survived).
	got, err := dst.GetTrail(ctx, "my-trail")
	require.NoError(t, err)
	assert.Equal(t, "my-trail", got.Name)

	byARN, err := dst.GetTrail(ctx, tr.TrailARN)
	require.NoError(t, err)
	assert.Equal(t, "my-trail", byARN.Name)

	gotEDS, err := dst.GetEventDataStore(ctx, eds.ARN)
	require.NoError(t, err)
	assert.Equal(t, "eds1", gotEDS.Name)

	tags, err := dst.ListTags(ctx, []string{tr.TrailARN})
	require.NoError(t, err)
	assert.Equal(t, "prod", tags[tr.TrailARN]["env"])

	events, _, err := dst.LookupEvents(ctx, driver.LookupInput{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "RunInstances", events[0].EventName)
}
