package databricks

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/databricks/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	azuredbx "github.com/stackshy/cloudemu/providers/azure/databricks"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDatabricks(opts ...Option) *Databricks {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"), config.WithAccountID("sub-1"))

	return NewDatabricks(azuredbx.New(o), opts...)
}

func validConfig() driver.WorkspaceConfig {
	return driver.WorkspaceConfig{
		Name:                   "ws-1",
		ResourceGroup:          "rg-1",
		Location:               "eastus",
		ManagedResourceGroupID: "/subscriptions/sub-1/resourceGroups/managed",
	}
}

func TestLifecycle(t *testing.T) {
	b := newTestDatabricks()
	ctx := context.Background()

	ws, err := b.CreateWorkspace(ctx, validConfig())
	require.NoError(t, err)
	assert.Equal(t, driver.StateSucceeded, ws.ProvisioningState)

	got, err := b.GetWorkspace(ctx, "rg-1", "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", got.Name)

	updated, err := b.UpdateWorkspaceTags(ctx, "rg-1", "ws-1", map[string]string{"env": "prod"})
	require.NoError(t, err)
	assert.Equal(t, "prod", updated.Tags["env"])

	byRG, err := b.ListWorkspacesByResourceGroup(ctx, "rg-1")
	require.NoError(t, err)
	assert.Len(t, byRG, 1)

	all, err := b.ListWorkspaces(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1)

	require.NoError(t, b.DeleteWorkspace(ctx, "rg-1", "ws-1"))

	_, err = b.GetWorkspace(ctx, "rg-1", "ws-1")
	require.Error(t, err)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	b := newTestDatabricks(WithRecorder(rec))

	_, err := b.CreateWorkspace(context.Background(), validConfig())
	require.NoError(t, err)

	calls := rec.Calls()
	require.GreaterOrEqual(t, len(calls), 1)
	assert.Equal(t, "databricks", calls[0].Service)
	assert.Equal(t, "CreateWorkspace", calls[0].Operation)
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	b := newTestDatabricks(WithMetrics(mc))

	_, err := b.ListWorkspaces(context.Background())
	require.NoError(t, err)

	q := metrics.NewQuery(mc)
	assert.GreaterOrEqual(t, q.ByName("calls_total").Count(), 1)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	b := newTestDatabricks(WithErrorInjection(inj))

	inj.Set("databricks", "ListWorkspaces", fmt.Errorf("injected failure"), inject.Always{})

	_, err := b.ListWorkspaces(context.Background())
	require.Error(t, err)
}

func TestWithLatency(t *testing.T) {
	b := newTestDatabricks(WithLatency(time.Millisecond))

	start := time.Now()
	_, err := b.ListWorkspaces(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), time.Millisecond)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))
	lim := ratelimit.New(1, 1, fc)
	b := NewDatabricks(azuredbx.New(o), WithRateLimiter(lim))

	_, err := b.ListWorkspaces(context.Background())
	require.NoError(t, err)

	_, err = b.ListWorkspaces(context.Background())
	require.Error(t, err)
}
