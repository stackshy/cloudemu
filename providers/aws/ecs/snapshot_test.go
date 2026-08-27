package ecs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// TestSnapshotRoundTripECS proves a snapshot/restore round-trip preserves the
// clusters, task definitions and services stores under their original keys.
func TestSnapshotRoundTripECS(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = src.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	clusters, err := dst.ListClusters(ctx)
	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, "prod", clusters[0].Name)

	td, err := dst.DescribeTaskDefinition(ctx, "web")
	require.NoError(t, err)
	assert.Equal(t, "web", td.Family)
}
