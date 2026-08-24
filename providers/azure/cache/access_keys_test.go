package cache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateCacheGeneratesAccessKeys(t *testing.T) {
	m, _ := newTestMock()

	info, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: "c1"})
	require.NoError(t, err)

	assert.NotEmpty(t, info.PrimaryKey)
	assert.NotEmpty(t, info.SecondaryKey)
	assert.NotEqual(t, info.PrimaryKey, info.SecondaryKey)
}

func TestCreateCacheStoresLocation(t *testing.T) {
	m, _ := newTestMock()

	info, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: "c1", Location: "westus2"})
	require.NoError(t, err)
	assert.Equal(t, "westus2", info.Location)

	got, err := m.GetCache(context.Background(), "c1")
	require.NoError(t, err)
	assert.Equal(t, "westus2", got.Location)
}

func TestListCacheKeys(t *testing.T) {
	m, _ := newTestMock()
	createTestCache(t, m, "c1")

	primary, secondary, err := m.ListCacheKeys(context.Background(), "c1")
	require.NoError(t, err)
	assert.NotEmpty(t, primary)
	assert.NotEmpty(t, secondary)

	_, _, err = m.ListCacheKeys(context.Background(), "missing")
	assert.Error(t, err)
}

func TestRegenerateCacheKeyPrimary(t *testing.T) {
	m, _ := newTestMock()
	createTestCache(t, m, "c1")

	before, secBefore, err := m.ListCacheKeys(context.Background(), "c1")
	require.NoError(t, err)

	primary, secondary, err := m.RegenerateCacheKey(context.Background(), "c1", "Primary")
	require.NoError(t, err)
	assert.NotEqual(t, before, primary, "primary key must rotate")
	assert.Equal(t, secBefore, secondary, "secondary key must be unchanged")
}

func TestRegenerateCacheKeySecondary(t *testing.T) {
	m, _ := newTestMock()
	createTestCache(t, m, "c1")

	priBefore, secBefore, err := m.ListCacheKeys(context.Background(), "c1")
	require.NoError(t, err)

	primary, secondary, err := m.RegenerateCacheKey(context.Background(), "c1", "Secondary")
	require.NoError(t, err)
	assert.Equal(t, priBefore, primary, "primary key must be unchanged")
	assert.NotEqual(t, secBefore, secondary, "secondary key must rotate")
}

func TestRegenerateCacheKeyInvalidType(t *testing.T) {
	m, _ := newTestMock()
	createTestCache(t, m, "c1")

	_, _, err := m.RegenerateCacheKey(context.Background(), "c1", "Tertiary")
	assert.Error(t, err)
}

func TestRegenerateCacheKeyNotFound(t *testing.T) {
	m, _ := newTestMock()

	_, _, err := m.RegenerateCacheKey(context.Background(), "missing", "Primary")
	assert.Error(t, err)
}
