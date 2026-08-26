package gcs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteBucketRefusesNoncurrentVersions checks that a bucket with no live
// objects but retained noncurrent versions still refuses deletion.
func TestDeleteBucketRefusesNoncurrentVersions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateBucket(ctx, "b1"))
	require.NoError(t, m.SetBucketVersioning(ctx, "b1", true))

	require.NoError(t, m.PutObject(ctx, "b1", "k1", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, "b1", "k1", []byte("v2"), "text/plain", nil))

	// Deleting the live object archives the current generation as noncurrent, so
	// no live object remains but a noncurrent version does.
	require.NoError(t, m.DeleteObject(ctx, "b1", "k1"))

	err := m.DeleteBucket(ctx, "b1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not empty")
}

// TestUpdateObjectBumpsUpdatedNotCreated checks that a metadata-only patch
// advances the object's updated time while leaving timeCreated fixed.
func TestUpdateObjectBumpsUpdatedNotCreated(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	clk := m.opts.Clock.(*config.FakeClock)

	require.NoError(t, m.CreateBucket(ctx, "b1"))

	created, err := m.PutObjectGCS(ctx, "b1", "k1", []byte("data"), "text/plain", nil, nil, driver.GCSPrecondition{})
	require.NoError(t, err)
	require.Equal(t, created.Created, created.LastModified, "a fresh write has equal created and updated")

	clk.Advance(time.Hour)

	val := "bar"
	patched, err := m.UpdateObjectGCS(ctx, "b1", "k1", driver.GCSObjectUpdate{
		Metadata: map[string]*string{"foo": &val},
	}, driver.GCSPrecondition{})
	require.NoError(t, err)

	assert.Equal(t, created.Created, patched.Created, "timeCreated must stay fixed on a metadata patch")
	assert.Greater(t, patched.LastModified, created.LastModified, "updated must advance")
	assert.NotEqual(t, patched.Created, patched.LastModified, "updated must differ from created after patch")
}
