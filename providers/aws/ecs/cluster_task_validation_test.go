package ecs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func TestCreateCluster_ExistingActiveIsIdempotent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	first, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		Name: "prod", Tags: []driver.Tag{{Key: "env", Value: "a"}},
	})
	require.NoError(t, err)

	second, err := m.CreateCluster(ctx, driver.CreateClusterInput{
		Name: "prod", Tags: []driver.Tag{{Key: "env", Value: "b"}},
	})
	require.NoError(t, err)

	assert.Equal(t, first.ARN, second.ARN)
	assert.Equal(t, statusActive, second.Status)
	assert.Equal(t, []driver.Tag{{Key: "env", Value: "a"}}, second.Tags, "existing cluster is returned unchanged")

	list, err := m.ListClusters(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestCreateCluster_ParallelSameName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	const workers = 50

	arns := make([]string, workers)

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			c, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
			if assert.NoError(t, err) {
				arns[i] = c.ARN
			}
		}()
	}

	wg.Wait()

	for _, a := range arns {
		assert.Equal(t, arns[0], a)
	}

	list, err := m.ListClusters(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestCreateCluster_RecreateAfterDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.DeleteCluster(ctx, "prod")
	require.NoError(t, err)

	c, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod", Tags: []driver.Tag{{Key: "k", Value: "v"}}})
	require.NoError(t, err)
	assert.Equal(t, statusActive, c.Status)
	assert.Equal(t, []driver.Tag{{Key: "k", Value: "v"}}, c.Tags)
}

func TestCreateService_AssignPublicIpDefaultsDisabled(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)
	assert.Equal(t, "DISABLED", svc.NetworkConfiguration.AwsVpcConfiguration.AssignPublicIP)
	assert.Empty(t, netCfg.AwsVpcConfiguration.AssignPublicIP, "caller input must not be mutated")

	enabled := &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{
		Subnets: []string{"subnet-1"}, AssignPublicIP: "ENABLED",
	}}

	svc2, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web2", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: enabled,
	})
	require.NoError(t, err)
	assert.Equal(t, "ENABLED", svc2.NetworkConfiguration.AwsVpcConfiguration.AssignPublicIP)

	updated, err := m.UpdateService(ctx, driver.UpdateServiceInput{
		Service: "web", Cluster: "prod",
		NetworkConfiguration: &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{
			Subnets: []string{"subnet-2"},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "DISABLED", updated.NetworkConfiguration.AwsVpcConfiguration.AssignPublicIP)
}

func TestAssignPublicIp_InvalidValue(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	fargateFixture(t, m)

	bad := &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{
		Subnets: []string{"subnet-1"}, AssignPublicIP: "MAYBE",
	}}

	_, _, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: bad,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: bad,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "ok", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{
			Subnets: []string{"subnet-1"},
		}},
	})
	require.NoError(t, err)

	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "ok", Cluster: "prod", NetworkConfiguration: bad})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}

func TestRunTask_InvalidLaunchType(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	// A task definition without requiresCompatibilities, so only the launch-type
	// enum check (not a compatibility mismatch) can reject the request.
	_, err := m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "plain",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	for _, lt := range []string{"BOGUS", "fargate", "Ec2"} {
		tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{
			Cluster: "prod", TaskDefinition: "plain", LaunchType: lt, NetworkConfiguration: netCfg,
		})
		require.Error(t, err, lt)
		assert.Equal(t, excInvalidParameter, ecsException(t, err), lt)
		assert.Empty(t, tasks)
		assert.Empty(t, failures)
	}

	got, err := m.ListTasks(ctx, "prod", "", "", "")
	require.NoError(t, err)
	assert.Empty(t, got, "no task may be stored for a rejected launch type")
}

func TestRunTask_LaunchTypeEnumAccepted(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// EXTERNAL runs unplaced, so it succeeds without capacity.
	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", LaunchType: launchExternal})
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	// Other enum values must pass the enum check; EC2 then fails placement
	// (a failure entry, not an InvalidParameterException).
	_, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", LaunchType: launchEC2})
	require.NoError(t, err)
	assert.NotEmpty(t, failures)

	for _, lt := range []string{launchFargate, "MANAGED_INSTANCES"} {
		_, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", LaunchType: lt})
		if err != nil {
			assert.NotContains(t, err.Error(), "Unsupported launch type", lt)
		}
	}
}

func TestStopTask_AlreadyStoppedKeepsReason(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	first, err := m.StopTask(ctx, "prod", tasks[0].ARN, "first")
	require.NoError(t, err)

	clock, ok := m.opts.Clock.(*config.FakeClock)
	require.True(t, ok)
	clock.Advance(time.Minute)

	second, err := m.StopTask(ctx, "prod", tasks[0].ARN, "second")
	require.NoError(t, err)

	assert.Equal(t, "first", second.StoppedReason)
	assert.Equal(t, first.StoppedAt, second.StoppedAt)
	assert.Equal(t, first.StopCode, second.StopCode)

	got, _, err := m.DescribeTasks(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Equal(t, "first", got[0].StoppedReason)
}

func TestStopTask_AlreadyStoppedNoSideEffects(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 256, 256)
	m.SeedContainerInstance("prod", "i-1", WithCapacity(1024, 1024))

	tasks, failures, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)
	require.Empty(t, failures)

	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "first")
	require.NoError(t, err)

	before := len(rec.snapshot())

	remainingBefore := m.instanceRemaining(t, "prod")

	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "second")
	require.NoError(t, err)

	assert.Equal(t, before, len(rec.snapshot()), "no second STOPPED event")
	assert.Equal(t, remainingBefore, m.instanceRemaining(t, "prod"), "capacity released exactly once")
}

func TestStopTask_ParallelSameTask(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)
	netCfg := fargateFixture(t, m)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	const workers = 50

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.StopTask(ctx, "prod", tasks[0].ARN, "reason-"+string(rune('a'+i%26)))
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	stopped := 0

	for _, e := range rec.snapshot() {
		if d, ok := e.Detail.(taskStateChangeDetail); ok && d.LastStatus == statusStopped {
			stopped++
		}
	}

	assert.Equal(t, 1, stopped, "exactly one STOPPED event")
}

func TestCreateService_DuplicateMessage(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)
	in := driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	}

	_, err := m.CreateService(ctx, in)
	require.NoError(t, err)

	_, err = m.CreateService(ctx, in)
	require.Error(t, err)
	assert.True(t, errors.IsAlreadyExists(err))
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	var ce *errors.Error

	require.ErrorAs(t, err, &ce)
	assert.Equal(t, "Creation of service was not idempotent.", ce.Message)
}

// Regression coverage for #1449 items already fixed on development: tag
// operations on resources of a cluster whose name contains "cluster", and
// tagging an unknown resource.
func TestRegression_TagsOnClusterNamedCluster(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "c-cluster-x"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img"}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	netCfg := &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}}}

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{
		Cluster: "c-cluster-x", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
		Tags: []driver.Tag{{Key: "t", Value: "1"}},
	})
	require.NoError(t, err)

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "svc", Cluster: "c-cluster-x", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, Tags: []driver.Tag{{Key: "s", Value: "1"}},
	})
	require.NoError(t, err)

	got, err := m.ListTagsForResource(ctx, tasks[0].ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{{Key: "t", Value: "1"}}, got, "task ARN of a cluster named *cluster* resolves as a task")

	got, err = m.ListTagsForResource(ctx, svc.ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{{Key: "s", Value: "1"}}, got)

	require.NoError(t, m.TagResource(ctx, svc.ARN, []driver.Tag{{Key: "k", Value: "v"}}))

	err = m.TagResource(ctx, "arn:aws:ecs:us-east-1:000000000000:cluster/does-not-exist", []driver.Tag{{Key: "k", Value: "v"}})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}
