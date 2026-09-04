package elasticache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAutoMinorVersionUpgradeRoundTrip guards the perpetual-diff bug: real
// ElastiCache defaults AutoMinorVersionUpgrade to true and terraform-provider-aws
// shares that schema default, so an absent-then-unset read leaves terraform
// forever wanting to change it. The flag must default to true, round-trip an
// explicit false, and be updatable via ModifyCache.
func TestAutoMinorVersionUpgradeRoundTrip(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	// Omitted → default true.
	info, err := m.CreateCache(ctx, driver.CacheConfig{Name: "def", Engine: "redis"})
	require.NoError(t, err)
	assert.True(t, info.AutoMinorVersionUpgrade)

	got, err := m.GetCache(ctx, "def")
	require.NoError(t, err)
	assert.True(t, got.AutoMinorVersionUpgrade)

	// Explicit false round-trips.
	off := false
	info, err = m.CreateCache(ctx, driver.CacheConfig{
		Name: "off", Engine: "redis", AutoMinorVersionUpgrade: &off,
	})
	require.NoError(t, err)
	assert.False(t, info.AutoMinorVersionUpgrade)

	got, err = m.GetCache(ctx, "off")
	require.NoError(t, err)
	assert.False(t, got.AutoMinorVersionUpgrade)

	// Modify flips it back on.
	on := true
	mod, err := m.ModifyCache(ctx, driver.ModifyCacheConfig{Name: "off", AutoMinorVersionUpgrade: &on})
	require.NoError(t, err)
	assert.True(t, mod.AutoMinorVersionUpgrade)

	// Modify that omits the flag leaves the stored value unchanged.
	mod, err = m.ModifyCache(ctx, driver.ModifyCacheConfig{Name: "off", NodeType: "cache.m5.large"})
	require.NoError(t, err)
	assert.True(t, mod.AutoMinorVersionUpgrade)
}
