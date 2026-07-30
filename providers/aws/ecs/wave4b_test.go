package ecs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func TestTagLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		Name: "prod",
		Tags: []driver.Tag{{Key: "env", Value: "prod"}},
	})
	require.NoError(t, err)

	// Creation-time tags are visible.
	tags, err := m.ListTagsForResource(ctx, c.ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{{Key: "env", Value: "prod"}}, tags)

	// TagResource upserts: replace env, add team.
	require.NoError(t, m.TagResource(ctx, c.ARN, []driver.Tag{
		{Key: "env", Value: "production"},
		{Key: "team", Value: "platform"},
	}))

	tags, err = m.ListTagsForResource(ctx, c.ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{
		{Key: "env", Value: "production"},
		{Key: "team", Value: "platform"},
	}, tags)

	// UntagResource removes a key.
	require.NoError(t, m.UntagResource(ctx, c.ARN, []string{"env"}))

	tags, err = m.ListTagsForResource(ctx, c.ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{{Key: "team", Value: "platform"}}, tags)

	// Unknown ARN → NotFound.
	_, err = m.ListTagsForResource(ctx, m.arn("cluster/does-not-exist"))
	assert.True(t, errors.IsNotFound(err))
}

func TestAccountSettingsLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	s, err := m.PutAccountSetting(ctx, "containerInsights", "enabled")
	require.NoError(t, err)
	assert.Equal(t, "enabled", s.Value)

	_, err = m.PutAccountSettingDefault(ctx, "serviceLongArnFormat", "enabled")
	require.NoError(t, err)

	list, err := m.ListAccountSettings(ctx)
	require.NoError(t, err)
	// Sorted by name for determinism.
	require.Len(t, list, 2)
	assert.Equal(t, "containerInsights", list[0].Name)
	assert.Equal(t, "serviceLongArnFormat", list[1].Name)

	del, err := m.DeleteAccountSetting(ctx, "containerInsights")
	require.NoError(t, err)
	assert.Equal(t, "enabled", del.Value)

	list, err = m.ListAccountSettings(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestRegisterContainerInstanceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	ci, err := m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{
		Cluster:                  "prod",
		InstanceIdentityDocument: `{"instanceId":"i-abc123","region":"us-east-1"}`,
		TotalResources: []driver.Resource{
			{Name: "CPU", Type: "INTEGER", IntegerValue: 4096},
			{Name: "MEMORY", Type: "INTEGER", IntegerValue: 8192},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "i-abc123", ci.EC2InstanceID)
	assert.Equal(t, 4096, ci.RegisteredCPU)
	assert.Equal(t, 8192, ci.RegisteredMemory)
	assert.Equal(t, statusActive, ci.Status)

	// Default capacity when TotalResources omitted, generated EC2 id.
	ci2, err := m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{Cluster: "prod"})
	require.NoError(t, err)
	assert.Equal(t, defaultInstanceCPU, ci2.RegisteredCPU)
	assert.Equal(t, defaultInstanceMemory, ci2.RegisteredMemory)
	assert.NotEmpty(t, ci2.EC2InstanceID)

	list, err := m.ListContainerInstances(ctx, "prod")
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// DRAINING transition.
	updated, failures, err := m.UpdateContainerInstancesState(ctx, "prod", []string{ci.ARN}, statusDraining)
	require.NoError(t, err)
	assert.Empty(t, failures)
	require.Len(t, updated, 1)
	assert.Equal(t, statusDraining, updated[0].Status)

	// Unknown id → failure, invalid status → error.
	_, failures, err = m.UpdateContainerInstancesState(ctx, "prod", []string{"i-missing"}, statusActive)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	assert.Equal(t, "MISSING", failures[0].Reason)

	_, _, err = m.UpdateContainerInstancesState(ctx, "prod", []string{ci.ARN}, "INACTIVE")
	assert.True(t, errors.IsInvalidArgument(err))

	// Deregister removes it.
	der, err := m.DeregisterContainerInstance(ctx, "prod", ci.ARN, false)
	require.NoError(t, err)
	assert.Equal(t, statusInactive, der.Status)

	list, err = m.ListContainerInstances(ctx, "prod")
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestDeregisterContainerInstanceRunningTasksGuard(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	ci := m.SeedContainerInstance("prod", "i-busy", WithCapacity(2048, 4096))

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img", CPU: 256, Memory: 512}},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 1})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)

	// Without force, a running task blocks deregistration.
	_, err = m.DeregisterContainerInstance(ctx, "prod", ci.ARN, false)
	assert.True(t, errors.IsFailedPrecondition(err))

	// With force, it succeeds.
	_, err = m.DeregisterContainerInstance(ctx, "prod", ci.ARN, true)
	require.NoError(t, err)
}

func TestUpdateClusterSettingsAndCapacityProviders(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	c, err := m.UpdateClusterSettings(ctx, "prod", []driver.Setting{{Name: "containerInsights", Value: "enabled"}})
	require.NoError(t, err)
	assert.Equal(t, []driver.Setting{{Name: "containerInsights", Value: "enabled"}}, c.Settings)

	c, err = m.PutClusterCapacityProviders(ctx, "prod",
		[]string{"FARGATE", "FARGATE_SPOT"},
		[]driver.CapacityProviderStrategyItem{{CapacityProvider: "FARGATE", Base: 1, Weight: 1}})
	require.NoError(t, err)
	assert.Equal(t, []string{"FARGATE", "FARGATE_SPOT"}, c.CapacityProviders)
	require.Len(t, c.DefaultCapacityProviderStrategy, 1)
	assert.Equal(t, "FARGATE", c.DefaultCapacityProviderStrategy[0].CapacityProvider)

	// UpdateCluster echoes settings.
	c, err = m.UpdateCluster(ctx, driver.UpdateClusterInput{
		Cluster:  "prod",
		Settings: []driver.Setting{{Name: "containerInsights", Value: "disabled"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "disabled", c.Settings[0].Value)

	// Unknown cluster → NotFound.
	_, err = m.UpdateClusterSettings(ctx, "ghost", nil)
	assert.True(t, errors.IsNotFound(err))
}

func TestAttributesLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.PutAttributes(ctx, "prod", []driver.Attribute{
		{Name: "stack", Value: "prod", TargetID: "i-2", TargetType: "container-instance"},
		{Name: "stack", Value: "dev", TargetID: "i-1", TargetType: "container-instance"},
		{Name: "zone", Value: "a", TargetID: "i-1", TargetType: "other"},
	})
	require.NoError(t, err)

	// Filter by targetType — sorted by (targetId, name).
	got, err := m.ListAttributes(ctx, "prod", "container-instance", "", "")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "i-1", got[0].TargetID)
	assert.Equal(t, "i-2", got[1].TargetID)

	// Filter by name + value.
	got, err = m.ListAttributes(ctx, "prod", "container-instance", "stack", "prod")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "i-2", got[0].TargetID)

	// Delete one.
	_, err = m.DeleteAttributes(ctx, "prod", []driver.Attribute{{Name: "stack", TargetID: "i-2"}})
	require.NoError(t, err)

	got, err = m.ListAttributes(ctx, "prod", "container-instance", "", "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "i-1", got[0].TargetID)
}

func TestListTaskDefinitionFamilies(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, fam := range []string{"web", "api", "web", "worker"} {
		_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
			Family:               fam,
			ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		})
		require.NoError(t, err)
	}

	// Distinct + sorted.
	fams, err := m.ListTaskDefinitionFamilies(ctx, "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "web", "worker"}, fams)

	// Prefix filter.
	fams, err = m.ListTaskDefinitionFamilies(ctx, "w", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"web", "worker"}, fams)
}

func TestExecuteCommand(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	m.SeedContainerInstance("prod", "i-exec")

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "nginx", Image: "img", CPU: 128, Memory: 256}},
	})
	require.NoError(t, err)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 1})
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	res, err := m.ExecuteCommand(ctx, driver.ExecuteCommandInput{
		Cluster: "prod", Task: tasks[0].ARN, Command: "/bin/sh", Interactive: true,
	})
	require.NoError(t, err)
	assert.Equal(t, tasks[0].ARN, res.TaskARN)
	assert.Equal(t, "nginx", res.ContainerName)
	assert.True(t, res.Interactive)
	assert.NotEmpty(t, res.Session.SessionID)
	assert.NotEmpty(t, res.Session.StreamURL)
	assert.NotEmpty(t, res.Session.TokenValue)

	// Unknown task → error.
	_, err = m.ExecuteCommand(ctx, driver.ExecuteCommandInput{Cluster: "prod", Task: "t-missing", Command: "ls"})
	assert.True(t, errors.IsNotFound(err))
}
