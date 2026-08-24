package elasticache

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSnapshotFromCluster(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{
		Name: "cluster-1", Engine: "redis", NodeType: "cache.t3.small",
	})
	require.NoError(t, err)

	snap, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{
		SnapshotName: "Snap-1", CacheClusterID: "cluster-1",
	})
	require.NoError(t, err)

	assert.Equal(t, "snap-1", snap.Name, "name should be lowercased")
	assert.Equal(t, "cluster-1", snap.CacheClusterID)
	assert.Equal(t, statusAvailable, snap.Status)
	assert.Equal(t, "manual", snap.Source)
	assert.Equal(t, "redis", snap.Engine)
	assert.Equal(t, "cache.t3.small", snap.NodeType)
	assert.Equal(t, 1, snap.NumCacheNodes)
	assert.Equal(t, defaultRedisPort, snap.Port)
	assert.NotEmpty(t, snap.ARN)
	assert.False(t, snap.CreatedAt.IsZero())
}

func TestCreateSnapshotRequiresName(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.CreateSnapshot(context.Background(), driver.SnapshotConfig{CacheClusterID: "c"})
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestCreateSnapshotDuplicateFails(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1"})
	require.NoError(t, err)

	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "dup", CacheClusterID: "c1"})
	require.NoError(t, err)

	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "dup", CacheClusterID: "c1"})
	require.Error(t, err)
	assert.True(t, cerrors.IsAlreadyExists(err))
}

func TestCreateSnapshotUnknownClusterFails(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.CreateSnapshot(context.Background(), driver.SnapshotConfig{
		SnapshotName: "s", CacheClusterID: "missing",
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestCreateSnapshotUnknownReplicationGroupFails(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.CreateSnapshot(context.Background(), driver.SnapshotConfig{
		SnapshotName: "s", ReplicationGroupID: "missing",
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestCreateSnapshotFromReplicationGroup(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{
		ID: "rg-1", Engine: "redis", NodeType: "cache.r6g.large",
	})
	require.NoError(t, err)

	snap, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{
		SnapshotName: "rg-snap", ReplicationGroupID: "rg-1",
	})
	require.NoError(t, err)

	assert.Equal(t, "rg-1", snap.ReplicationGroupID)
	assert.Empty(t, snap.CacheClusterID)
	assert.Equal(t, "cache.r6g.large", snap.NodeType)
	assert.Equal(t, defaultRedisPort, snap.Port)
}

func TestDescribeSnapshotsFilters(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	for _, id := range []string{"ca", "cb"} {
		_, err := m.CreateCache(ctx, driver.CacheConfig{Name: id})
		require.NoError(t, err)
	}

	_, err := m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "sa", CacheClusterID: "ca"})
	require.NoError(t, err)
	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "sb", CacheClusterID: "cb"})
	require.NoError(t, err)

	all, err := m.DescribeSnapshots(ctx, driver.SnapshotFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	byName, err := m.DescribeSnapshots(ctx, driver.SnapshotFilter{SnapshotName: "sa"})
	require.NoError(t, err)
	require.Len(t, byName, 1)
	assert.Equal(t, "sa", byName[0].Name)

	byCluster, err := m.DescribeSnapshots(ctx, driver.SnapshotFilter{CacheClusterID: "cb"})
	require.NoError(t, err)
	require.Len(t, byCluster, 1)
	assert.Equal(t, "cb", byCluster[0].CacheClusterID)
}

func TestDescribeSnapshotsUnknownNameFails(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.DescribeSnapshots(context.Background(), driver.SnapshotFilter{SnapshotName: "ghost"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}
