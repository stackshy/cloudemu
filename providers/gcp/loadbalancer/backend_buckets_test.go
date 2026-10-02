package loadbalancer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

func TestGCPBackendBucketStore(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	bb := driver.GCPResource{Name: "bb", ID: "1", Body: map[string]any{"bucketName": "assets"}}
	require.NoError(t, m.InsertGCPBackendBucket(ctx, bb))
	assert.True(t, cerrors.IsAlreadyExists(m.InsertGCPBackendBucket(ctx, bb)))

	got, err := m.GetGCPBackendBucket(ctx, "bb")
	require.NoError(t, err)
	assert.Equal(t, driver.GCPBackendBucketCollection, got.Collection)
	assert.Equal(t, "global", got.Scope)

	// A mutate error leaves the stored record unchanged.
	errReject := cerrors.New(cerrors.InvalidArgument, "rejected")
	err = m.UpdateGCPBackendBucket(ctx, "bb", func(res *driver.GCPResource) error {
		res.Body = map[string]any{"bucketName": "other"}
		return errReject
	})
	require.ErrorIs(t, err, errReject)

	got, err = m.GetGCPBackendBucket(ctx, "bb")
	require.NoError(t, err)
	assert.Equal(t, "assets", got.Body["bucketName"])

	require.NoError(t, m.UpdateGCPBackendBucket(ctx, "bb", func(res *driver.GCPResource) error {
		res.Body = map[string]any{"bucketName": "other"}
		return nil
	}))

	list, err := m.ListGCPBackendBuckets(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "other", list[0].Body["bucketName"])

	require.NoError(t, m.DeleteGCPBackendBucket(ctx, "bb"))
	assert.True(t, cerrors.IsNotFound(m.DeleteGCPBackendBucket(ctx, "bb")))

	_, err = m.GetGCPBackendBucket(ctx, "bb")
	assert.True(t, cerrors.IsNotFound(err))
	assert.True(t, cerrors.IsNotFound(m.UpdateGCPBackendBucket(ctx, "bb", func(*driver.GCPResource) error { return nil })))
}
