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

func TestDeleteClusterCascadeGuard(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// A cluster with a running task refuses deletion. EXTERNAL launch runs the
	// task unplaced (no container instance needed), keeping this test focused on
	// the running-task cascade guard rather than capacity.
	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "web", LaunchType: "EXTERNAL",
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)

	_, err = m.DeleteCluster(ctx, "prod")
	require.Error(t, err)
	assert.True(t, errors.IsFailedPrecondition(err))

	// Stopping the task clears the running-task guard.
	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "bye")
	require.NoError(t, err)

	del, err := m.DeleteCluster(ctx, "prod")
	require.NoError(t, err)
	assert.Equal(t, statusInactive, del.Status)
}

func TestDescribeClustersCounts(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// Seed EC2 capacity so both tasks place onto a container instance.
	m.SeedContainerInstance("prod", "i-counts")

	_, runFailures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 2})
	require.NoError(t, err)
	require.Empty(t, runFailures)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "svc", Cluster: "prod", TaskDefinition: "web", DesiredCount: 1,
	})
	require.NoError(t, err)

	found, _, err := m.DescribeClusters(ctx, []string{"prod"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	// 2 standalone tasks + 1 task launched by the desired-count-1 service.
	assert.Equal(t, 3, found[0].RunningTasksCount)
	assert.Equal(t, 1, found[0].ActiveServicesCount)
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

func TestListTaskDefinitionsNumericRevisionOrder(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	in := driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	}

	// Register 12 revisions so lexical ordering (web:10 < web:2) would misorder.
	for range 12 {
		_, err := m.RegisterTaskDefinition(ctx, in)
		require.NoError(t, err)
	}

	asc, err := m.ListTaskDefinitions(ctx, "web", "", "")
	require.NoError(t, err)
	require.Len(t, asc, 12)
	assert.Equal(t, 1, asc[0].Revision)
	assert.Equal(t, 2, asc[1].Revision)
	assert.Equal(t, 10, asc[9].Revision)
	assert.Equal(t, 12, asc[11].Revision)

	desc, err := m.ListTaskDefinitions(ctx, "web", "", "DESC")
	require.NoError(t, err)
	require.Len(t, desc, 12)
	assert.Equal(t, 12, desc[0].Revision)
	assert.Equal(t, 1, desc[11].Revision)
}

func TestRegisterTaskDefinitionRequiresFamily(t *testing.T) {
	m := newTestMock()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{})
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestRunAndStopTask(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	// RunTask/ListTasks validate the target cluster exists.
	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{Name: "staging"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// Seed EC2 capacity so the tasks place onto a container instance.
	m.SeedContainerInstance("prod", "i-runstop")

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 3})
	require.NoError(t, err)
	assert.Empty(t, failures)
	assert.Len(t, tasks, 3)
	assert.Equal(t, "RUNNING", tasks[0].LastStatus)
	assert.NotEmpty(t, tasks[0].ContainerInstanceARN)

	listed, err := m.ListTasks(ctx, "prod", "web", "RUNNING", "")
	require.NoError(t, err)
	assert.Len(t, listed, 3)

	// A different (existing) cluster yields nothing.
	other, err := m.ListTasks(ctx, "staging", "", "", "")
	require.NoError(t, err)
	assert.Empty(t, other)

	// Listing tasks in a non-existent cluster is a typed error.
	_, err = m.ListTasks(ctx, "ghost", "", "", "")
	assert.True(t, errors.IsNotFound(err))

	stopped, err := m.StopTask(ctx, "prod", tasks[0].ARN, "bye")
	require.NoError(t, err)
	assert.Equal(t, "STOPPED", stopped.LastStatus)
	assert.Equal(t, "bye", stopped.StoppedReason)
}

func TestRunTaskUnresolvedDefinition(t *testing.T) {
	m := newTestMock()

	// An unresolved task definition is a synchronous ClientException in real
	// ECS, not a placement failure in failures[].
	tasks, failures, err := m.RunTask(context.Background(), driver.RunTaskInput{TaskDefinition: "nope"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Empty(t, tasks)
	assert.Empty(t, failures)
}

func TestRunTaskCountCappedAtMax(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// Seed capacity in the implicit default cluster so placement succeeds.
	m.SeedContainerInstance("default", "i-cap")

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

	// CreateService validates both the cluster and the task definition exist.
	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// Seed EC2 capacity so the (default EC2 launch-type) service converges.
	m.SeedContainerInstance("prod", "i-svc", WithCapacity(8192, 16384))

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName:    "web-svc",
		Cluster:        "prod",
		TaskDefinition: "web:1",
		DesiredCount:   2,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, svc.RunningCount)
	assert.Equal(t, "REPLICA", svc.SchedulingStrategy)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{ServiceName: "web-svc", Cluster: "prod", TaskDefinition: "web"})
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

	// A service with a non-zero desired count refuses deletion without force.
	_, err = m.DeleteService(ctx, "prod", "web-svc", false)
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	del, err := m.DeleteService(ctx, "prod", "web-svc", true)
	require.NoError(t, err)
	assert.Equal(t, statusInactive, del.Status)

	// The name can be reused after the ACTIVE service is deleted.
	recreated, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web-svc", Cluster: "prod", TaskDefinition: "web", DesiredCount: 0,
	})
	require.NoError(t, err)
	assert.Equal(t, statusActive, recreated.Status)
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

// registerFargate registers a FARGATE task definition with the given task-level
// cpu/memory strings.
func registerFargate(t *testing.T, m *Mock, cpu, memory string) {
	t.Helper()

	_, err := m.RegisterTaskDefinition(context.Background(), driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     cpu,
		Memory:                  memory,
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)
}

func TestRunTaskFargateInvalidCPUMemoryCombo(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	registerFargate(t, m, "512", "512") // 512 vCPU requires >= 1024 MiB

	netCfg := &driver.NetworkConfiguration{
		AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
	}

	_, _, err = m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	// A valid pairing still succeeds.
	registerFargate(t, m, "256", "512")

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
}

// A DAEMON service rejects a caller-supplied desiredCount.
func TestCreateServiceDaemonRejectsDesiredCount(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 128, 256)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		Cluster: "prod", ServiceName: "svc", TaskDefinition: "web",
		SchedulingStrategy: schedDaemon, DesiredCount: 2,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}

// Repeated UpdateService does not grow the deployments list.
func TestUpdateServiceDeploymentsDoNotAccumulate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	m.SeedContainerInstance("prod", "i-1")
	registerWeb(t, m, 128, 256)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		Cluster: "prod", ServiceName: "svc", TaskDefinition: "web", DesiredCount: 1,
	})
	require.NoError(t, err)

	for range 3 {
		_, err = m.UpdateService(ctx, driver.UpdateServiceInput{
			Cluster: "prod", Service: "svc", ForceNewDeployment: true,
		})
		require.NoError(t, err)
	}

	svcs, _, err := m.DescribeServices(ctx, "prod", []string{"svc"})
	require.NoError(t, err)
	require.Len(t, svcs, 1)
	assert.Len(t, svcs[0].Deployments, 1, "deployments must not accumulate across updates")
	assert.Equal(t, deploymentPrimary, svcs[0].Deployments[0].Status)
}

// Force-deregistering an instance stops the tasks placed on it.
func TestForceDeregisterStopsTasks(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	ci := m.SeedContainerInstance("prod", "i-1")
	registerWeb(t, m, 128, 256)

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, tasks, 1)
	require.Equal(t, statusRunning, tasks[0].LastStatus)

	_, err = m.DeregisterContainerInstance(ctx, "prod", ci.ARN, true)
	require.NoError(t, err)

	got, _, err := m.DescribeTasks(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, statusStopped, got[0].LastStatus, "task on a force-deregistered instance must be stopped")
}

// RunTask with count <= 0 defaults to a single task.
func TestRunTaskDefaultsCountToOne(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	m.SeedContainerInstance("prod", "i-1")
	registerWeb(t, m, 128, 256)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 0})
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
}
