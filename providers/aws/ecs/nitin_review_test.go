package ecs

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// registerPlain registers a simple ACTIVE task definition for the given family
// and returns its "family:revision" key.
func registerPlain(t *testing.T, m *Mock, family string) string {
	t.Helper()

	td, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family:               family,
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	return td.Family + ":" + strconv.Itoa(td.Revision)
}

// --- INACTIVE task definition guard (RunTask / CreateService / UpdateService) ---

// A deregistered (INACTIVE) task definition, referenced by explicit
// family:revision, can no longer launch new tasks — real ECS rejects it.
func TestRunTaskRejectsInactiveTaskDef(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	ref := registerPlain(t, m, "web")
	m.SeedContainerInstance("default", "i-a")

	// While ACTIVE it runs.
	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{TaskDefinition: ref})
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	// Deregister it, then a run against the same explicit revision is rejected.
	_, err = m.DeregisterTaskDefinition(ctx, ref)
	require.NoError(t, err)

	_, _, err = m.RunTask(ctx, driver.RunTaskInput{TaskDefinition: ref})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "want InvalidArgument for INACTIVE def, got %v", err)
}

func TestCreateServiceRejectsInactiveTaskDef(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	ref := registerPlain(t, m, "web")
	_, err := m.DeregisterTaskDefinition(ctx, ref)
	require.NoError(t, err)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "svc", TaskDefinition: ref, DesiredCount: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "got %v", err)
}

func TestUpdateServiceRejectsInactiveTaskDef(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	active := registerPlain(t, m, "web") // web:1
	stale := registerPlain(t, m, "web")  // web:2
	m.SeedContainerInstance("default", "i-a")

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "svc", TaskDefinition: active, DesiredCount: 1,
	})
	require.NoError(t, err)

	// Deregister web:2 and try to update the service onto it.
	_, err = m.DeregisterTaskDefinition(ctx, stale)
	require.NoError(t, err)

	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{
		Service: "svc", TaskDefinition: stale,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "got %v", err)
}

// --- INACTIVE (deleted) cluster guard + name reuse ---

// A deleted cluster is an INACTIVE tombstone; new work against it is rejected
// with ClusterNotFoundException rather than landing on a dead cluster.
func TestRunTaskRejectsDeletedCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	registerPlain(t, m, "web")

	_, err = m.DeleteCluster(ctx, "prod")
	require.NoError(t, err)

	_, _, err = m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "want NotFound (ClusterNotFound), got %v", err)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		Cluster: "prod", ServiceName: "svc", TaskDefinition: "web", DesiredCount: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// A deleted cluster's name can be recreated (the INACTIVE tombstone is not a
// permanent AlreadyExists), while an ACTIVE cluster of the same name still is.
func TestCreateClusterReusesDeletedName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	// Re-creating an ACTIVE cluster of the same name is a conflict.
	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.Error(t, err)
	assert.True(t, errors.IsAlreadyExists(err), "got %v", err)

	// After delete, the name is free to reuse and the new cluster is ACTIVE.
	_, err = m.DeleteCluster(ctx, "prod")
	require.NoError(t, err)

	recreated, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	assert.Equal(t, statusActive, recreated.Status)
}

// --- ListServices excludes INACTIVE tombstones ---

func TestListServicesExcludesInactive(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	registerPlain(t, m, "web")

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "keep", TaskDefinition: "web", DesiredCount: 0,
	})
	require.NoError(t, err)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "gone", TaskDefinition: "web", DesiredCount: 0,
	})
	require.NoError(t, err)

	_, err = m.DeleteService(ctx, "default", "gone", true)
	require.NoError(t, err)

	list, err := m.ListServices(ctx, "default")
	require.NoError(t, err)

	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Name)
	}

	assert.Contains(t, names, "keep")
	assert.NotContains(t, names, "gone", "deleted (INACTIVE) service must not appear in ListServices")

	// DescribeServices still resolves the tombstone.
	found, _, err := m.DescribeServices(ctx, "default", []string{"gone"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, statusInactive, found[0].Status)
}

// --- UpdateService rejects desiredCount under DAEMON (parity with CreateService) ---

func TestUpdateServiceDaemonRejectsDesiredCount(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	registerPlain(t, m, "web")

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "d", TaskDefinition: "web", SchedulingStrategy: schedDaemon,
	})
	require.NoError(t, err)

	want := 3
	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "d", DesiredCount: &want})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err), "got %v", err)
}

// --- Write-side aliasing: mutating caller input after a store call must not
// corrupt the stored record. ---

func TestRegisterTaskDefinitionDoesNotAliasInput(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	env := []driver.KeyValue{{Name: "K", Value: "V"}}
	ports := []driver.PortMapping{{ContainerPort: 80}}

	td, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family: "web",
		ContainerDefinitions: []driver.ContainerDefinition{{
			Name: "c", Image: "img", Environment: env, PortMappings: ports,
		}},
	})
	require.NoError(t, err)

	// Mutate the caller's slices after registering.
	env[0].Value = "MUTATED"
	ports[0].ContainerPort = 9999

	got, err := m.DescribeTaskDefinition(ctx, td.Family+":"+strconv.Itoa(td.Revision))
	require.NoError(t, err)
	require.Len(t, got.ContainerDefinitions, 1)

	assert.Equal(t, "V", got.ContainerDefinitions[0].Environment[0].Value,
		"stored task def aliased the caller's Environment slice")
	assert.EqualValues(t, 80, got.ContainerDefinitions[0].PortMappings[0].ContainerPort,
		"stored task def aliased the caller's PortMappings slice")
}

func TestUpdateClusterDoesNotAliasConfiguration(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	cfg := json.RawMessage(`{"executeCommandConfiguration":{"logging":"DEFAULT"}}`)
	_, err = m.UpdateCluster(ctx, driver.UpdateClusterInput{Cluster: "prod", Configuration: cfg})
	require.NoError(t, err)

	// Mutate the caller's byte slice after the update.
	cfg[0] = 'X'

	got, _, err := m.DescribeClusters(ctx, []string{"prod"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, byte('{'), got[0].Configuration[0],
		"stored cluster aliased the caller's Configuration byte slice")
}
