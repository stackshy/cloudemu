package memorystore

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

// newAsyncMock builds a Memorystore mock with AsyncSettle enabled and a FakeClock
// so create/update report their real transient GCP states deterministically.
func newAsyncMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-central1"),
		config.WithProjectID("test-project"),
		config.WithAsyncSettle(),
	)

	return New(opts), fc
}

// TestAsyncSettleCreateUpdate pins the AsyncSettle transitions: an instance
// reports CREATING then READY on create, and UPDATING then READY on patch, all
// driven by the FakeClock and observed through both Get and List.
func TestAsyncSettleCreateUpdate(t *testing.T) {
	m, fc := newAsyncMock()
	ctx := context.Background()

	created, err := m.CreateCache(ctx, driver.CacheConfig{Name: "cache1"})
	require.NoError(t, err)
	assert.Equal(t, stateCreating, created.Status)

	got, err := m.GetCache(ctx, "cache1")
	require.NoError(t, err)
	assert.Equal(t, stateCreating, got.Status)

	// List observes the transient state too.
	list, err := m.ListCaches(ctx, scope.Scope{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, stateCreating, list[0].Status)

	// Still transient one instant before the window elapses.
	fc.Advance(settle.DefaultCacheSettle - time.Millisecond)
	got, _ = m.GetCache(ctx, "cache1")
	assert.Equal(t, stateCreating, got.Status)

	// Past the window: terminal READY.
	fc.Advance(time.Millisecond)
	got, _ = m.GetCache(ctx, "cache1")
	assert.Equal(t, stateReady, got.Status)

	// Update → UPDATING → READY.
	updated, err := m.UpdateCache(ctx, driver.CacheConfig{Name: "cache1", NodeType: "M3"})
	require.NoError(t, err)
	assert.Equal(t, stateUpdating, updated.Status)

	got, _ = m.GetCache(ctx, "cache1")
	assert.Equal(t, stateUpdating, got.Status)

	fc.Advance(settle.DefaultCacheModifySettle)
	got, _ = m.GetCache(ctx, "cache1")
	assert.Equal(t, stateReady, got.Status)
}

// TestAsyncSettleDefaultOff confirms the default (AsyncSettle unset) path is
// byte-for-byte synchronous: create and update are observed as READY
// immediately, with no transient state.
func TestAsyncSettleDefaultOff(t *testing.T) {
	m, _ := newTestMock() // no WithAsyncSettle
	ctx := context.Background()

	created, err := m.CreateCache(ctx, driver.CacheConfig{Name: "cache1"})
	require.NoError(t, err)
	assert.Equal(t, stateReady, created.Status)

	got, err := m.GetCache(ctx, "cache1")
	require.NoError(t, err)
	assert.Equal(t, stateReady, got.Status)

	updated, err := m.UpdateCache(ctx, driver.CacheConfig{Name: "cache1", NodeType: "M3"})
	require.NoError(t, err)
	assert.Equal(t, stateReady, updated.Status)
}
