package ecs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	c, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	assert.Equal(t, "prod", c.Name)
	assert.Equal(t, statusActive, c.Status)
	assert.Contains(t, c.ARN, "cluster/prod")

	// Duplicate name is rejected.
	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	assert.True(t, errors.IsAlreadyExists(err))

	list, err := m.ListClusters(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	del, err := m.DeleteCluster(ctx, "prod")
	require.NoError(t, err)
	assert.Equal(t, statusInactive, del.Status)

	_, err = m.DeleteCluster(ctx, "missing")
	assert.True(t, errors.IsNotFound(err))
}

func TestCreateClusterDefaultName(t *testing.T) {
	m := newTestMock()

	c, err := m.CreateCluster(context.Background(), driver.CreateClusterInput{})
	require.NoError(t, err)
	assert.Equal(t, defaultCluster, c.Name)
}

func TestDescribeClustersPartialFailure(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	found, failures, err := m.DescribeClusters(ctx, []string{"prod", "ghost"})
	require.NoError(t, err)
	assert.Len(t, found, 1)
	require.Len(t, failures, 1)
	assert.Equal(t, "MISSING", failures[0].Reason)

	// Empty id list returns everything.
	all, _, err := m.DescribeClusters(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestTaskDefinitionRevisions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	in := driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img", Essential: true}},
	}

	td1, err := m.RegisterTaskDefinition(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, 1, td1.Revision)

	td2, err := m.RegisterTaskDefinition(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, 2, td2.Revision)

	// Bare family resolves the latest ACTIVE revision.
	latest, err := m.DescribeTaskDefinition(ctx, "web")
	require.NoError(t, err)
	assert.Equal(t, 2, latest.Revision)

	// family:revision resolves an explicit revision.
	explicit, err := m.DescribeTaskDefinition(ctx, "web:1")
	require.NoError(t, err)
	assert.Equal(t, 1, explicit.Revision)

	// Full ARN resolves too.
	byARN, err := m.DescribeTaskDefinition(ctx, td2.ARN)
	require.NoError(t, err)
	assert.Equal(t, 2, byARN.Revision)

	dereg, err := m.DeregisterTaskDefinition(ctx, "web:2")
	require.NoError(t, err)
	assert.Equal(t, statusInactive, dereg.Status)
	assert.NotEmpty(t, dereg.DeregisteredAt)

	// After deregistering rev 2, the latest ACTIVE is rev 1.
	latest, err = m.DescribeTaskDefinition(ctx, "web")
	require.NoError(t, err)
	assert.Equal(t, 1, latest.Revision)

	_, err = m.DescribeTaskDefinition(ctx, "missing")
	assert.True(t, errors.IsNotFound(err))
}

func TestRegisterTaskDefinitionRequiresFamily(t *testing.T) {
	m := newTestMock()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{})
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestRunAndStopTask(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 3})
	require.NoError(t, err)
	assert.Empty(t, failures)
	assert.Len(t, tasks, 3)
	assert.Equal(t, "RUNNING", tasks[0].LastStatus)

	listed, err := m.ListTasks(ctx, "prod", "web", "RUNNING")
	require.NoError(t, err)
	assert.Len(t, listed, 3)

	// A different cluster yields nothing.
	other, err := m.ListTasks(ctx, "staging", "", "")
	require.NoError(t, err)
	assert.Empty(t, other)

	stopped, err := m.StopTask(ctx, "prod", tasks[0].ARN, "bye")
	require.NoError(t, err)
	assert.Equal(t, "STOPPED", stopped.LastStatus)
	assert.Equal(t, "bye", stopped.StoppedReason)
}

func TestRunTaskUnresolvedDefinition(t *testing.T) {
	m := newTestMock()

	tasks, failures, err := m.RunTask(context.Background(), driver.RunTaskInput{TaskDefinition: "nope"})
	require.NoError(t, err)
	assert.Empty(t, tasks)
	require.Len(t, failures, 1)
	assert.Equal(t, "MISSING", failures[0].Reason)
}

func TestRunTaskCountCappedAtMax(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// Count at the max is allowed.
	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{TaskDefinition: "web", Count: maxRunTaskCount})
	require.NoError(t, err)
	assert.Len(t, tasks, maxRunTaskCount)

	// Beyond the max is rejected (bounds the allocation; matches AWS).
	_, _, err = m.RunTask(ctx, driver.RunTaskInput{TaskDefinition: "web", Count: maxRunTaskCount + 1})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	// An absurd count does not attempt a huge allocation — it errors first.
	_, _, err = m.RunTask(ctx, driver.RunTaskInput{TaskDefinition: "web", Count: 1 << 30})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestServiceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName:    "web-svc",
		Cluster:        "prod",
		TaskDefinition: "web:1",
		DesiredCount:   2,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, svc.RunningCount)
	assert.Equal(t, "REPLICA", svc.SchedulingStrategy)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{ServiceName: "web-svc", Cluster: "prod"})
	assert.True(t, errors.IsAlreadyExists(err))

	count := 5
	upd, err := m.UpdateService(ctx, driver.UpdateServiceInput{
		Service:      "web-svc",
		Cluster:      "prod",
		DesiredCount: &count,
	})
	require.NoError(t, err)
	assert.Equal(t, 5, upd.DesiredCount)
	assert.Equal(t, 5, upd.RunningCount)

	list, err := m.ListServices(ctx, "prod")
	require.NoError(t, err)
	assert.Len(t, list, 1)

	found, failures, err := m.DescribeServices(ctx, "prod", []string{"web-svc", "ghost"})
	require.NoError(t, err)
	assert.Len(t, found, 1)
	assert.Len(t, failures, 1)

	del, err := m.DeleteService(ctx, "prod", "web-svc")
	require.NoError(t, err)
	assert.Equal(t, statusInactive, del.Status)
}

func TestContainerInstances(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	ci := m.SeedContainerInstance("prod", "i-123")
	assert.True(t, ci.AgentConnected)
	assert.Equal(t, statusActive, ci.Status)

	list, err := m.ListContainerInstances(ctx, "prod")
	require.NoError(t, err)
	require.Len(t, list, 1)

	// Instances are scoped to their cluster.
	empty, err := m.ListContainerInstances(ctx, "staging")
	require.NoError(t, err)
	assert.Empty(t, empty)

	found, failures, err := m.DescribeContainerInstances(ctx, "prod", []string{ci.ARN, "ghost"})
	require.NoError(t, err)
	assert.Len(t, found, 1)
	assert.Len(t, failures, 1)
	assert.Equal(t, "i-123", found[0].EC2InstanceID)
}
