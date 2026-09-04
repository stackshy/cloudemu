package elasticache

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAsyncMock builds an ElastiCache mock with AsyncSettle enabled and a
// FakeClock so create/modify report their real transient states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

// TestAsyncSettleCacheClusterCreateModify pins the AsyncSettle transitions for a
// cache cluster: creating then available on create, modifying then available on
// modify, observed through both Get and List.
func TestAsyncSettleCacheClusterCreateModify(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1"})
	require.NoError(t, err)
	assert.Equal(t, statusCreating, created.Status)

	got, err := m.GetCache(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, statusCreating, got.Status)

	list, err := m.ListCaches(ctx, scope.Scope{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, statusCreating, list[0].Status)

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultCacheSettle - time.Millisecond)
	got, _ = m.GetCache(ctx, "c1")
	assert.Equal(t, statusCreating, got.Status)

	// Past the window: terminal available.
	fc.Advance(time.Millisecond)
	got, _ = m.GetCache(ctx, "c1")
	assert.Equal(t, statusAvailable, got.Status)

	// Modify → modifying → available.
	modified, err := m.ModifyCache(ctx, driver.ModifyCacheConfig{Name: "c1", NodeType: "cache.m5.large"})
	require.NoError(t, err)
	assert.Equal(t, statusModifying, modified.Status)

	got, _ = m.GetCache(ctx, "c1")
	assert.Equal(t, statusModifying, got.Status)

	fc.Advance(settle.DefaultCacheModifySettle)
	got, _ = m.GetCache(ctx, "c1")
	assert.Equal(t, statusAvailable, got.Status)
}

// TestAsyncSettleReplicationGroupCreateModify pins the transitions for a
// replication group: creating then available on create, modifying then available
// on modify, observed through DescribeReplicationGroups.
func TestAsyncSettleReplicationGroupCreateModify(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg1"})
	require.NoError(t, err)
	assert.Equal(t, statusCreating, created.Status)

	groups, err := m.DescribeReplicationGroups(ctx, []string{"rg1"})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, statusCreating, groups[0].Status)

	fc.Advance(settle.DefaultCacheSettle)
	groups, _ = m.DescribeReplicationGroups(ctx, nil)
	require.Len(t, groups, 1)
	assert.Equal(t, statusAvailable, groups[0].Status)

	// Modify → modifying → available.
	modified, err := m.ModifyReplicationGroup(ctx, "rg1", 2)
	require.NoError(t, err)
	assert.Equal(t, statusModifying, modified.Status)

	fc.Advance(settle.DefaultCacheModifySettle)
	groups, _ = m.DescribeReplicationGroups(ctx, []string{"rg1"})
	require.Len(t, groups, 1)
	assert.Equal(t, statusAvailable, groups[0].Status)
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and modify are observed as available
// immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m, _ := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	created, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1"})
	require.NoError(t, err)
	assert.Equal(t, statusAvailable, created.Status)

	got, err := m.GetCache(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, statusAvailable, got.Status)

	modified, err := m.ModifyCache(ctx, driver.ModifyCacheConfig{Name: "c1", NodeType: "cache.m5.large"})
	require.NoError(t, err)
	assert.Equal(t, statusAvailable, modified.Status)

	rg, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg1"})
	require.NoError(t, err)
	assert.Equal(t, statusAvailable, rg.Status)
}
