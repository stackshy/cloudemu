package bedrockagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func mustFlow(t *testing.T, m *Mock) *driver.Flow {
	t.Helper()

	f, err := m.CreateFlow(context.Background(), driver.FlowConfig{Name: "f", ExecutionRoleArn: "arn:aws:iam::123456789012:role/f"})
	require.NoError(t, err)

	return f
}

func TestFlowAliasValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	f := mustFlow(t, m)
	route := []driver.FlowAliasRoute{{FlowVersion: driver.DraftVersion}}
	maxOver := int32(101)

	cases := map[string]driver.FlowAliasConfig{
		"no routing":        {FlowID: f.ID, Name: "a"},
		"two routes":        {FlowID: f.ID, Name: "a", RoutingConfiguration: append(route, route...)},
		"bad version":       {FlowID: f.ID, Name: "a", RoutingConfiguration: []driver.FlowAliasRoute{{FlowVersion: "v1"}}},
		"manual no max":     {FlowID: f.ID, Name: "a", RoutingConfiguration: route, Concurrency: &driver.FlowAliasConcurrency{Type: "Manual"}},
		"max out of range":  {FlowID: f.ID, Name: "a", RoutingConfiguration: route, Concurrency: &driver.FlowAliasConcurrency{Type: "Manual", MaxConcurrency: &maxOver}},
		"bad concurrency":   {FlowID: f.ID, Name: "a", RoutingConfiguration: route, Concurrency: &driver.FlowAliasConcurrency{Type: "Fixed"}},
		"bad name":          {FlowID: f.ID, Name: "a b", RoutingConfiguration: route},
		"description > 200": {FlowID: f.ID, Name: "a", RoutingConfiguration: route, Description: string(make([]byte, 201))},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateFlowAlias(ctx, cfg)
			assert.True(t, cerrors.IsInvalidArgument(err), "%v", err)
		})
	}
}

func TestFlowVersionErrors(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	f := mustFlow(t, m)

	_, err := m.CreateFlowVersion(ctx, "MISSING000", "")
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.GetFlowVersion(ctx, f.ID, driver.DraftVersion)
	assert.True(t, cerrors.IsInvalidArgument(err))

	err = m.DeleteFlowVersion(ctx, f.ID, "1", false)
	assert.True(t, cerrors.IsNotFound(err))

	_, err = m.GetFlowAlias(ctx, f.ID, "MISSING000")
	assert.True(t, cerrors.IsNotFound(err))

	// An alias of another flow is not found under this one.
	other := mustFlow(t, m)
	alias, err := m.CreateFlowAlias(ctx, driver.FlowAliasConfig{
		FlowID: other.ID, Name: "a", RoutingConfiguration: []driver.FlowAliasRoute{{FlowVersion: driver.DraftVersion}},
	})
	require.NoError(t, err)

	_, err = m.DeleteFlowAlias(ctx, f.ID, alias.ID)
	assert.True(t, cerrors.IsNotFound(err))
}

func TestFlowVersionNumbersNeverReused(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	f := mustFlow(t, m)

	for range 2 {
		_, err := m.CreateFlowVersion(ctx, f.ID, "")
		require.NoError(t, err)
	}

	require.NoError(t, m.DeleteFlowVersion(ctx, f.ID, "2", false))

	v, err := m.CreateFlowVersion(ctx, f.ARN, "")
	require.NoError(t, err)
	assert.Equal(t, "3", v.Version)
}
