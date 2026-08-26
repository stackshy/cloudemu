package elasticache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestParamGroup(t *testing.T, m *Mock, name, family string) {
	t.Helper()

	_, err := m.CreateCacheParameterGroup(context.Background(), name, family, "desc")
	require.NoError(t, err)
}

func findParameter(params []Parameter, name string) (Parameter, bool) {
	for _, p := range params {
		if p.Name == name {
			return p, true
		}
	}

	return Parameter{}, false
}

func TestDescribeCacheParametersDefaults(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "pg", "redis7")

	params, err := m.DescribeCacheParameters(context.Background(), "pg", "")
	require.NoError(t, err)
	require.NotEmpty(t, params)

	mp, ok := findParameter(params, "maxmemory-policy")
	require.True(t, ok, "maxmemory-policy should be present")
	assert.Equal(t, "volatile-lru", mp.Value)
	assert.Equal(t, "system", mp.Source)
}

func TestDescribeCacheParametersMemcachedFamily(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "mc", "memcached1.6")

	params, err := m.DescribeCacheParameters(context.Background(), "mc", "")
	require.NoError(t, err)
	require.NotEmpty(t, params)

	_, ok := findParameter(params, "max_item_size")
	assert.True(t, ok, "memcached family should expose max_item_size")

	_, redisLeak := findParameter(params, "maxmemory-policy")
	assert.False(t, redisLeak, "memcached family must not expose redis parameters")
}

func TestModifyCacheParametersApplied(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "pg", "redis7")

	err := m.ModifyCacheParameterGroup(context.Background(), "pg",
		[]ParameterUpdate{{Name: "maxmemory-policy", Value: "allkeys-lru"}})
	require.NoError(t, err)

	params, err := m.DescribeCacheParameters(context.Background(), "pg", "")
	require.NoError(t, err)

	mp, ok := findParameter(params, "maxmemory-policy")
	require.True(t, ok)
	assert.Equal(t, "allkeys-lru", mp.Value)
	assert.Equal(t, "user", mp.Source)
}

func TestModifyCacheParametersUnknownRejected(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "pg", "redis7")

	err := m.ModifyCacheParameterGroup(context.Background(), "pg",
		[]ParameterUpdate{{Name: "bogus", Value: "x"}})
	require.Error(t, err)
}

func TestModifyCacheParametersNotModifiableRejected(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "pg", "redis7")

	err := m.ModifyCacheParameterGroup(context.Background(), "pg",
		[]ParameterUpdate{{Name: "databases", Value: "32"}})
	require.Error(t, err)
}

func TestModifyCacheParametersMissingGroup(t *testing.T) {
	m, _ := newTestMock()

	err := m.ModifyCacheParameterGroup(context.Background(), "nope",
		[]ParameterUpdate{{Name: "maxmemory-policy", Value: "allkeys-lru"}})
	require.Error(t, err)
}

func TestDescribeCacheParametersSourceFilter(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "pg", "redis7")

	require.NoError(t, m.ModifyCacheParameterGroup(context.Background(), "pg",
		[]ParameterUpdate{{Name: "maxmemory-policy", Value: "allkeys-lru"}}))

	user, err := m.DescribeCacheParameters(context.Background(), "pg", "user")
	require.NoError(t, err)
	require.Len(t, user, 1)
	assert.Equal(t, "maxmemory-policy", user[0].Name)

	system, err := m.DescribeCacheParameters(context.Background(), "pg", "system")
	require.NoError(t, err)

	_, leaked := findParameter(system, "maxmemory-policy")
	assert.False(t, leaked, "system filter must exclude user-modified parameters")
}

func TestResetCacheParameterGroupAll(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "pg", "redis7")

	require.NoError(t, m.ModifyCacheParameterGroup(context.Background(), "pg",
		[]ParameterUpdate{{Name: "maxmemory-policy", Value: "allkeys-lru"}}))

	require.NoError(t, m.ResetCacheParameterGroup(context.Background(), "pg", true, nil))

	params, err := m.DescribeCacheParameters(context.Background(), "pg", "")
	require.NoError(t, err)

	mp, ok := findParameter(params, "maxmemory-policy")
	require.True(t, ok)
	assert.Equal(t, "volatile-lru", mp.Value)
	assert.Equal(t, "system", mp.Source)
}

func TestResetCacheParameterGroupNamed(t *testing.T) {
	m, _ := newTestMock()
	createTestParamGroup(t, m, "pg", "redis7")

	require.NoError(t, m.ModifyCacheParameterGroup(context.Background(), "pg", []ParameterUpdate{
		{Name: "maxmemory-policy", Value: "allkeys-lru"},
		{Name: "timeout", Value: "60"},
	}))

	require.NoError(t, m.ResetCacheParameterGroup(context.Background(), "pg", false, []string{"timeout"}))

	params, err := m.DescribeCacheParameters(context.Background(), "pg", "")
	require.NoError(t, err)

	tmo, ok := findParameter(params, "timeout")
	require.True(t, ok)
	assert.Equal(t, "system", tmo.Source, "reset parameter returns to system source")

	mp, ok := findParameter(params, "maxmemory-policy")
	require.True(t, ok)
	assert.Equal(t, "user", mp.Source, "un-reset parameter keeps its user override")
}
