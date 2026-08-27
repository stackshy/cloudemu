package elasticache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotRoundTripElastiCache proves a persist snapshot/restore round-trip
// preserves a cache cluster and its stored key→value entries.
func TestSnapshotRoundTripElastiCache(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	_, err := src.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"})
	require.NoError(t, err)
	require.NoError(t, src.Set(ctx, "c1", "k", []byte("v"), 0))

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	item, err := dst.Get(ctx, "c1", "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), item.Value)
}
