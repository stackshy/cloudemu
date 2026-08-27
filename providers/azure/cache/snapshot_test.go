package cache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshotRoundTripCache(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	_, err := src.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"})
	require.NoError(t, err)
	require.NoError(t, src.Set(ctx, "c1", "k1", []byte("value1"), 0))
	require.NoError(t, src.Set(ctx, "c1", "k2", []byte("value2"), 0))

	data, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	info, err := dst.GetCache(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, "c1", info.Name)

	item, err := dst.Get(ctx, "c1", "k1")
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), item.Value)

	item2, err := dst.Get(ctx, "c1", "k2")
	require.NoError(t, err)
	assert.Equal(t, []byte("value2"), item2.Value)
}
