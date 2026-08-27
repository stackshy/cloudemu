package vertexai

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// TestSnapshotRoundTripVertexai proves a snapshot/restore cycle reinstates the
// mock's state under the original identities: a model uploaded before the
// snapshot (which also records a long-running operation) is readable from a
// fresh mock after restore, and re-snapshotting yields byte-identical JSON, so
// every store round-trips.
func TestSnapshotRoundTripVertexai(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, model, err := src.UploadModel(ctx, driver.ModelConfig{Location: "us-central1", DisplayName: "m1"})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	got, err := dst.GetModel(ctx, model.Name)
	require.NoError(t, err)
	assert.Equal(t, model.Name, got.Name)
	assert.Equal(t, "m1", got.DisplayName)

	reSnap, err := dst.Snapshot(ctx, true)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(raw, reSnap), "re-snapshot differs; a store did not round-trip")
}
