package elasticache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReplicationGroupMembersDescribable guards that each member node of a
// replication group is describable as a single-node cache cluster (real
// ElastiCache exposes them through DescribeCacheClusters). Terraform reads every
// member back by id after creating a replication group, so a member that 404s
// aborts `terraform apply`.
func TestReplicationGroupMembersDescribable(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{
		ID: "rg", Engine: "redis", NodeType: "cache.t3.micro", NumCacheNodes: 2,
	})
	require.NoError(t, err)

	for _, id := range []string{"rg-001", "rg-002"} {
		info, err := m.GetCache(ctx, id)
		require.NoError(t, err, "member %s should be describable", id)
		assert.Equal(t, id, info.Name)
		assert.Equal(t, "rg", info.ReplicationGroupID)
		assert.Equal(t, "redis", info.Engine)
		assert.Equal(t, 1, info.NumCacheNodes)
		assert.True(t, info.AutoMinorVersionUpgrade)
		assert.NotEmpty(t, info.Endpoint)
		assert.NotEmpty(t, info.ARN)
	}

	// Members appear in the list alongside standalone clusters, sorted by id.
	createTestCache(t, m, "aaa-standalone")

	list, err := m.ListCaches(ctx, scope.Scope{})
	require.NoError(t, err)

	names := make([]string, 0, len(list))
	for i := range list {
		names = append(names, list[i].Name)
	}

	assert.Equal(t, []string{"aaa-standalone", "rg-001", "rg-002"}, names)

	// A genuinely unknown id still reports not-found.
	_, err = m.GetCache(ctx, "no-such-cluster")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	// Deleting the group removes the synthesized members (no leak).
	require.NoError(t, m.DeleteReplicationGroup(ctx, "rg", driver.DeleteReplicationGroupOptions{}))

	_, err = m.GetCache(ctx, "rg-001")
	require.Error(t, err)

	list, err = m.ListCaches(ctx, scope.Scope{})
	require.NoError(t, err)
	names = names[:0]
	for i := range list {
		names = append(names, list[i].Name)
	}
	assert.Equal(t, []string{"aaa-standalone"}, names)
}

// TestCreateCacheRejectsMemberIDCollision guards that a create whose id collides
// with an existing replication-group member node ("<groupId>-001", …) is rejected
// with AlreadyExists — real ElastiCache returns CacheClusterAlreadyExists. Without
// the guard the standalone cluster and the synthesized member share an id and both
// surface through DescribeCacheClusters (a duplicate).
func TestCreateCacheRejectsMemberIDCollision(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{
		ID: "rg-test", Engine: "redis", NodeType: "cache.t3.micro", NumCacheNodes: 3,
	})
	require.NoError(t, err)

	_, err = m.CreateCache(ctx, driver.CacheConfig{Name: "rg-test-001"})
	require.Error(t, err, "create colliding with a member id must be rejected")
	assert.True(t, errors.IsAlreadyExists(err))

	// The member is still described exactly once (no duplicate leaked in).
	list, err := m.ListCaches(ctx, scope.Scope{})
	require.NoError(t, err)
	count := 0
	for i := range list {
		if list[i].Name == "rg-test-001" {
			count++
		}
	}
	assert.Equal(t, 1, count, "rg-test-001 should appear exactly once")

	// A genuinely new, non-colliding id still creates successfully.
	_, err = m.CreateCache(ctx, driver.CacheConfig{Name: "totally-new"})
	require.NoError(t, err)
}

// TestReplicationGroupMemberCountFollowsModify checks that rescaling a group
// changes which member clusters are describable.
func TestReplicationGroupMemberCountFollowsModify(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{
		ID: "rg", Engine: "redis", NumCacheNodes: 1,
	})
	require.NoError(t, err)

	_, err = m.GetCache(ctx, "rg-002")
	require.Error(t, err, "rg-002 should not exist before scale-up")

	_, err = m.ModifyReplicationGroup(ctx, "rg", 3)
	require.NoError(t, err)

	for _, id := range []string{"rg-001", "rg-002", "rg-003"} {
		_, err := m.GetCache(ctx, id)
		require.NoError(t, err, "member %s should exist after scale-up", id)
	}
}
