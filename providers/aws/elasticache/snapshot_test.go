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

func TestCopySnapshotCreatesIndependentCopy(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{
		Name: "cluster-1", Engine: "redis", NodeType: "cache.t3.small",
	})
	require.NoError(t, err)

	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "src", CacheClusterID: "cluster-1"})
	require.NoError(t, err)

	cp, err := m.CopySnapshot(ctx, driver.CopySnapshotConfig{
		SourceSnapshotName: "SRC", TargetSnapshotName: "Dst",
	})
	require.NoError(t, err)
	assert.Equal(t, "dst", cp.Name, "target name should be lowercased")
	assert.Equal(t, "copied", cp.Source)
	assert.Equal(t, statusAvailable, cp.Status)
	assert.Equal(t, "cache.t3.small", cp.NodeType)
	assert.Contains(t, cp.ARN, ":snapshot:dst")

	// Both source and copy coexist as independent records.
	all, err := m.DescribeSnapshots(ctx, driver.SnapshotFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// TestCopySnapshotDeepCopyIndependence proves the copy shares no mutable state
// with the source: deleting the source leaves the copy intact, and a later
// mutation of the source cluster does not reach either snapshot.
func TestCopySnapshotDeepCopyIndependence(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{
		Name: "c1", Engine: "redis", NodeType: "cache.t3.micro",
	})
	require.NoError(t, err)

	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "s1", CacheClusterID: "c1"})
	require.NoError(t, err)

	_, err = m.CopySnapshot(ctx, driver.CopySnapshotConfig{SourceSnapshotName: "s1", TargetSnapshotName: "s2"})
	require.NoError(t, err)

	// Mutate the source cluster after both snapshots exist.
	_, err = m.ModifyCache(ctx, driver.ModifyCacheConfig{Name: "c1", NodeType: "cache.r6g.large"})
	require.NoError(t, err)

	// Delete the source snapshot; the copy must survive untouched.
	del, err := m.DeleteSnapshot(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "deleting", del.Status)

	_, err = m.DescribeSnapshots(ctx, driver.SnapshotFilter{SnapshotName: "s1"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	survivor, err := m.DescribeSnapshots(ctx, driver.SnapshotFilter{SnapshotName: "s2"})
	require.NoError(t, err)
	require.Len(t, survivor, 1)
	// The copy still reflects the cluster's shape at snapshot time, not the
	// post-snapshot mutation.
	assert.Equal(t, "cache.t3.micro", survivor[0].NodeType)
}

func TestCopySnapshotMissingSourceFails(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.CopySnapshot(context.Background(), driver.CopySnapshotConfig{
		SourceSnapshotName: "ghost", TargetSnapshotName: "dst",
	})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestCopySnapshotDuplicateTargetFails(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1"})
	require.NoError(t, err)
	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "s1", CacheClusterID: "c1"})
	require.NoError(t, err)
	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "s2", CacheClusterID: "c1"})
	require.NoError(t, err)

	_, err = m.CopySnapshot(ctx, driver.CopySnapshotConfig{SourceSnapshotName: "s1", TargetSnapshotName: "s2"})
	require.Error(t, err)
	assert.True(t, cerrors.IsAlreadyExists(err))
}

func TestCopySnapshotRequiresTargetName(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.CopySnapshot(context.Background(), driver.CopySnapshotConfig{SourceSnapshotName: "s1"})
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestDeleteSnapshotUnknownFails(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.DeleteSnapshot(context.Background(), "ghost")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestRestoreCacheClusterFromSnapshot(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{
		Name: "origin", Engine: "redis", NodeType: "cache.r6g.large", EngineVersion: "7.0",
	})
	require.NoError(t, err)

	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "snap", CacheClusterID: "origin"})
	require.NoError(t, err)

	// Restore into a new cluster, providing no engine/node-type: they seed from
	// the snapshot.
	restored, err := m.CreateCache(ctx, driver.CacheConfig{Name: "restored", SnapshotName: "SNAP"})
	require.NoError(t, err)
	assert.Equal(t, "redis", restored.Engine)
	assert.Equal(t, "cache.r6g.large", restored.NodeType)
	assert.Equal(t, "7.0", restored.EngineVersion)
	assert.Equal(t, 1, restored.NumCacheNodes)

	// An explicit request field overrides the snapshot's value.
	override, err := m.CreateCache(ctx, driver.CacheConfig{
		Name: "override", SnapshotName: "snap", NodeType: "cache.t3.micro",
	})
	require.NoError(t, err)
	assert.Equal(t, "cache.t3.micro", override.NodeType)
	assert.Equal(t, "redis", override.Engine)
}

func TestRestoreCacheClusterUnknownSnapshotFails(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: "restored", SnapshotName: "ghost"})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestRestoreReplicationGroupFromSnapshot(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{
		ID: "rg-src", Engine: "redis", NodeType: "cache.r6g.large",
	})
	require.NoError(t, err)

	_, err = m.CreateSnapshot(ctx, driver.SnapshotConfig{SnapshotName: "rgsnap", ReplicationGroupID: "rg-src"})
	require.NoError(t, err)

	restored, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{
		ID: "rg-restored", SnapshotName: "rgsnap",
	})
	require.NoError(t, err)
	assert.Equal(t, "redis", restored.Engine)
	assert.Equal(t, "cache.r6g.large", restored.NodeType)
}
