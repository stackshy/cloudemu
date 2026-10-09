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

// TestSnapshot_NewStoresRoundTrip proves task sets, service deployments and
// revisions, task protection, exec settings and event/instance versions survive
// a snapshot/restore under their original ARNs.
func TestSnapshot_NewStoresRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	netCfg := fargateFixture(t, src)

	_, err := src.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
		DesiredCount: 1, EnableExecuteCommand: true,
		ServiceConnect: &driver.ServiceConnectConfiguration{Namespace: "mesh", Raw: []byte(`{"enabled":true,"namespace":"mesh"}`)},
	})
	require.NoError(t, err)

	_, err = src.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "ext", Cluster: "prod", DeploymentController: deployControllerExternal, DesiredCount: 2,
	})
	require.NoError(t, err)

	ts, err := src.CreateTaskSet(ctx, driver.CreateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	tasks, err := src.ListTasks(ctx, "prod", "", "RUNNING", "web")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	_, failures := protect(t, src, nil, tasks[0].ARN)
	require.Empty(t, failures)

	deps, _, err := src.ListServiceDeployments(ctx, driver.ListServiceDeploymentsInput{Cluster: "prod", Service: "web"})
	require.NoError(t, err)
	require.Len(t, deps, 1)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	sets, _, err := dst.DescribeTaskSets(ctx, "prod", "ext", []string{ts.ARN})
	require.NoError(t, err)
	require.Len(t, sets, 1)
	assert.Equal(t, ts.ID, sets[0].ID)
	assert.Equal(t, 2, sets[0].RunningCount)

	restored, _, err := dst.ListServiceDeployments(ctx, driver.ListServiceDeploymentsInput{Cluster: "prod", Service: "web"})
	require.NoError(t, err)
	require.Len(t, restored, 1)
	assert.Equal(t, deps[0].ARN, restored[0].ARN)

	revs, _, err := dst.DescribeServiceRevisions(ctx, []string{deps[0].TargetServiceRevision.ARN})
	require.NoError(t, err)
	assert.Len(t, revs, 1)

	got, _, err := dst.GetTaskProtection(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.True(t, got[0].ProtectionEnabled)

	byNS, _, err := dst.ListServicesByNamespace(ctx, "mesh", 0, "")
	require.NoError(t, err)
	assert.Len(t, byNS, 1)

	described, _, err := dst.DescribeTasks(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.True(t, described[0].EnableExecuteCommand)
	assert.Equal(t, tasks[0].EventVersion, described[0].EventVersion)
}
