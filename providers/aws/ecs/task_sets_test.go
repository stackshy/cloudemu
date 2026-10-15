package ecs

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// externalFixture creates an EXTERNAL-controller service named "ext" with the
// given desired count (no task definition, as real ECS allows) and returns the
// network configuration task sets need.
func externalFixture(t *testing.T, m *Mock, desired int) *driver.NetworkConfiguration {
	t.Helper()

	netCfg := fargateFixture(t, m)

	_, err := m.CreateService(context.Background(), driver.CreateServiceInput{
		ServiceName: "ext", Cluster: "prod", DeploymentController: deployControllerExternal, DesiredCount: desired,
	})
	require.NoError(t, err)

	return netCfg
}

func newTaskSet(t *testing.T, m *Mock, netCfg *driver.NetworkConfiguration, scale *driver.Scale) *driver.TaskSet {
	t.Helper()

	ts, err := m.CreateTaskSet(context.Background(), driver.CreateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, Scale: scale,
	})
	require.NoError(t, err)

	return ts
}

func TestCreateTaskSet_RequiresExternalController(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	ctx := context.Background()

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "rolling", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	_, err = m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
		Cluster: "prod", Service: "rolling", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))
}

func TestTaskSet_NotFoundErrors(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 1)
	ctx := context.Background()

	_, err := m.CreateTaskSet(ctx, driver.CreateTaskSetInput{Cluster: "ghost", Service: "ext", TaskDefinition: "fg"})
	assert.Equal(t, excClusterNotFound, ecsException(t, err))

	_, err = m.CreateTaskSet(ctx, driver.CreateTaskSetInput{Cluster: "prod", Service: "ghost", TaskDefinition: "fg"})
	assert.Equal(t, excServiceNotFound, ecsException(t, err))

	ts := newTaskSet(t, m, netCfg, nil)

	_, err = m.UpdateTaskSet(ctx, driver.UpdateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskSet: "ecs-svc/0", Scale: driver.Scale{Unit: driver.ScaleUnitPercent, Value: 50},
	})
	assert.Equal(t, excTaskSetNotFound, ecsException(t, err))

	_, err = m.DeleteTaskSet(ctx, driver.DeleteTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: "ecs-svc/0"})
	assert.Equal(t, excTaskSetNotFound, ecsException(t, err))

	_, err = m.UpdateServicePrimaryTaskSet(ctx, "prod", "ext", "ecs-svc/0")
	assert.Equal(t, excTaskSetNotFound, ecsException(t, err))

	_, err = m.DeleteService(ctx, "prod", "ext", true)
	require.NoError(t, err)

	_, err = m.CreateTaskSet(ctx, driver.CreateTaskSetInput{Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg})
	assert.Equal(t, excServiceNotActive, ecsException(t, err))

	_, err = m.UpdateTaskSet(ctx, driver.UpdateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskSet: ts.ID, Scale: driver.Scale{Unit: driver.ScaleUnitPercent, Value: 10},
	})
	assert.Contains(t, []string{excServiceNotActive, excTaskSetNotFound}, ecsException(t, err))
}

func TestCreateTaskSet_ScaleAndComputedDesiredCount(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 5)

	half := newTaskSet(t, m, netCfg, &driver.Scale{Unit: driver.ScaleUnitPercent, Value: 50})
	assert.Equal(t, 3, half.ComputedDesiredCount, "5 x 50%% rounds up to 3")
	assert.Equal(t, 3, half.RunningCount)
	assert.Equal(t, "ACTIVE", half.Status)
	assert.Contains(t, half.ARN, "task-set/prod/ext/"+half.ID)

	full := newTaskSet(t, m, netCfg, nil)
	assert.Equal(t, driver.Scale{Unit: driver.ScaleUnitPercent, Value: 100}, full.Scale, "default scale is 100 percent")
	assert.Equal(t, 5, full.ComputedDesiredCount)

	tasks, err := m.ListTasks(context.Background(), "prod", "", "RUNNING", "ext")
	require.NoError(t, err)
	assert.Len(t, tasks, 8, "task sets launch their own tasks, the service itself runs none")

	svc, _, err := m.DescribeServices(context.Background(), "prod", []string{"ext"})
	require.NoError(t, err)
	assert.Empty(t, svc[0].Deployments, "an EXTERNAL service has no deployments")
	assert.Len(t, svc[0].TaskSets, 2)
}

func TestTaskSet_ScaleBounds(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 4)
	ctx := context.Background()

	for _, v := range []float64{0, 100} {
		_, err := m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
			Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
			Scale: &driver.Scale{Unit: driver.ScaleUnitPercent, Value: v},
		})
		require.NoError(t, err, v)
	}

	for _, bad := range []driver.Scale{
		{Unit: driver.ScaleUnitPercent, Value: -1}, {Unit: driver.ScaleUnitPercent, Value: 100.5}, {Unit: "COUNT", Value: 1},
	} {
		_, err := m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
			Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg, Scale: &bad,
		})
		require.Error(t, err, bad)
		assert.Equal(t, excInvalidParameter, ecsException(t, err), bad)
	}
}

func TestTaskSet_UpdateScaleAndStability(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 4)
	ctx := context.Background()

	ts := newTaskSet(t, m, netCfg, &driver.Scale{Unit: driver.ScaleUnitPercent, Value: 50})
	assert.Equal(t, "STEADY_STATE", ts.StabilityStatus)
	assert.NotEmpty(t, ts.StabilityStatusAt)

	up, err := m.UpdateTaskSet(ctx, driver.UpdateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskSet: ts.ID, Scale: driver.Scale{Unit: driver.ScaleUnitPercent, Value: 100},
	})
	require.NoError(t, err)
	assert.Equal(t, 4, up.ComputedDesiredCount)
	assert.Equal(t, 4, up.RunningCount)

	down, err := m.UpdateTaskSet(ctx, driver.UpdateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskSet: ts.ARN, Scale: driver.Scale{Unit: driver.ScaleUnitPercent, Value: 25},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, down.ComputedDesiredCount)
	assert.Equal(t, 1, down.RunningCount)

	running, err := m.ListTasks(ctx, "prod", "", "RUNNING", "ext")
	require.NoError(t, err)
	assert.Len(t, running, 1)

	// A service desired-count change rescales every task set.
	eight := 8
	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "ext", Cluster: "prod", DesiredCount: &eight})
	require.NoError(t, err)

	got, _, err := m.DescribeTaskSets(ctx, "prod", "ext", []string{ts.ID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].ComputedDesiredCount)
	assert.Equal(t, 2, got[0].RunningCount)
}

func TestExternalService_CountsFollowTaskSets(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 2)
	ctx := context.Background()

	describe := func() driver.Service {
		t.Helper()

		got, _, err := m.DescribeServices(ctx, "prod", []string{"ext"})
		require.NoError(t, err)
		require.Len(t, got, 1)

		return got[0]
	}

	newTaskSet(t, m, netCfg, nil)

	svc := describe()
	assert.Equal(t, 2, svc.RunningCount)
	assert.Equal(t, 0, svc.PendingCount)

	newTaskSet(t, m, netCfg, &driver.Scale{Unit: driver.ScaleUnitPercent, Value: 50})
	assert.Equal(t, 3, describe().RunningCount, "the service reports the sum over its task sets")

	tasks, err := m.ListTasks(ctx, "prod", "", "RUNNING", "ext")
	require.NoError(t, err)
	require.Len(t, tasks, 3)

	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "test")
	require.NoError(t, err)
	assert.Equal(t, 3, describe().RunningCount, "a stopped task is relaunched and the count recovers")

	four := 4
	up, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: "ext", Cluster: "prod", DesiredCount: &four})
	require.NoError(t, err)

	after := describe()
	assert.Equal(t, 6, after.RunningCount)
	assert.Equal(t, after.RunningCount, up.RunningCount, "UpdateService agrees with DescribeServices")
	assert.Equal(t, after.PendingCount, up.PendingCount)
}

func TestTaskSet_StabilizingWhenTasksStopped(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 2)
	ctx := context.Background()
	ts := newTaskSet(t, m, netCfg, nil)

	tasks, err := m.ListTasks(ctx, "prod", "", "RUNNING", "ext")
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	// The service scheduler relaunches a stopped task-set task, restoring steady state.
	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "test")
	require.NoError(t, err)

	got, _, err := m.DescribeTaskSets(ctx, "prod", "ext", []string{ts.ID})
	require.NoError(t, err)
	assert.Equal(t, 2, got[0].RunningCount)
	assert.Equal(t, "STEADY_STATE", got[0].StabilityStatus)
}

func TestUpdateServicePrimaryTaskSet(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 1)
	ctx := context.Background()

	a := newTaskSet(t, m, netCfg, nil)
	b := newTaskSet(t, m, netCfg, nil)

	got, err := m.UpdateServicePrimaryTaskSet(ctx, "prod", "ext", a.ID)
	require.NoError(t, err)
	assert.Equal(t, "PRIMARY", got.Status)

	_, err = m.UpdateServicePrimaryTaskSet(ctx, "prod", "ext", b.ARN)
	require.NoError(t, err)

	sets, _, err := m.DescribeTaskSets(ctx, "prod", "ext", nil)
	require.NoError(t, err)
	require.Len(t, sets, 2)

	primaries := 0

	for _, s := range sets {
		switch s.ID {
		case b.ID:
			assert.Equal(t, "PRIMARY", s.Status)

			primaries++
		case a.ID:
			assert.Equal(t, "ACTIVE", s.Status)
		}
	}

	assert.Equal(t, 1, primaries, "exactly one PRIMARY")

	svc, _, err := m.DescribeServices(ctx, "prod", []string{"ext"})
	require.NoError(t, err)
	require.Len(t, svc[0].TaskSets, 2)
}

func TestDeleteTaskSet_DrainsWithOrWithoutForce(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 2)
	ctx := context.Background()

	ts := newTaskSet(t, m, netCfg, nil)

	// A scaled set deletes without force (Terraform sends Force=false).
	deleted, err := m.DeleteTaskSet(ctx, driver.DeleteTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: ts.ID})
	require.NoError(t, err)
	assert.Equal(t, "DRAINING", deleted.Status)
	assert.Zero(t, deleted.RunningCount)
	assert.Zero(t, deleted.PendingCount)
	assert.Zero(t, deleted.ComputedDesiredCount)

	running, err := m.ListTasks(ctx, "prod", "", "RUNNING", "ext")
	require.NoError(t, err)
	assert.Empty(t, running)

	_, err = m.UpdateServicePrimaryTaskSet(ctx, "prod", "ext", ts.ID)
	assert.Equal(t, excTaskSetNotFound, ecsException(t, err))

	// A forced delete behaves the same.
	forced := newTaskSet(t, m, netCfg, nil)
	deleted, err = m.DeleteTaskSet(ctx, driver.DeleteTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: forced.ID, Force: true})
	require.NoError(t, err)
	assert.Equal(t, "DRAINING", deleted.Status)

	// A set scaled to zero deletes too.
	zero := newTaskSet(t, m, netCfg, &driver.Scale{Unit: driver.ScaleUnitPercent, Value: 0})
	_, err = m.DeleteTaskSet(ctx, driver.DeleteTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: zero.ID})
	require.NoError(t, err)

	// Deleting the PRIMARY leaves no primary.
	p := newTaskSet(t, m, netCfg, nil)
	_, err = m.UpdateServicePrimaryTaskSet(ctx, "prod", "ext", p.ID)
	require.NoError(t, err)
	_, err = m.DeleteTaskSet(ctx, driver.DeleteTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: p.ID, Force: true})
	require.NoError(t, err)

	left, _, err := m.DescribeTaskSets(ctx, "prod", "ext", nil)
	require.NoError(t, err)
	assert.Empty(t, left)
}

func TestDescribeTaskSets_MissingAndDeepCopy(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 1)
	ctx := context.Background()

	ts, err := m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
		Tags: []driver.Tag{{Key: "k", Value: "v"}},
	})
	require.NoError(t, err)

	got, failures, err := m.DescribeTaskSets(ctx, "prod", "ext", []string{ts.ID, "ecs-svc/missing"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, failures, 1)
	assert.Equal(t, "MISSING", failures[0].Reason)
	assert.Equal(t, "ecs-svc/missing", failures[0].ARN)

	got[0].Tags[0].Value = "mutated"
	got[0].NetworkConfiguration.AwsVpcConfiguration.Subnets[0] = "mutated"

	again, _, err := m.DescribeTaskSets(ctx, "prod", "ext", []string{ts.ARN})
	require.NoError(t, err)
	assert.Equal(t, "v", again[0].Tags[0].Value)
	assert.Equal(t, "subnet-1", again[0].NetworkConfiguration.AwsVpcConfiguration.Subnets[0])
}

func TestCreateTaskSet_ClientTokenIdempotent(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 1)
	ctx := context.Background()

	in := driver.CreateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, ClientToken: "token-1",
	}

	a, err := m.CreateTaskSet(ctx, in)
	require.NoError(t, err)

	b, err := m.CreateTaskSet(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, a.ID, b.ID)

	sets, _, err := m.DescribeTaskSets(ctx, "prod", "ext", nil)
	require.NoError(t, err)
	assert.Len(t, sets, 1)

	long := in
	long.ClientToken = "0123456789012345678901234567890123456"
	_, err = m.CreateTaskSet(ctx, long)
	assert.Equal(t, excInvalidParameter, ecsException(t, err), "clientToken is at most 36 characters")
}

func TestCreateTaskSet_ParallelSameToken(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 1)
	ctx := context.Background()

	ids := make([]string, 50)

	var wg sync.WaitGroup

	for i := range ids {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ts, err := m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
				Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate,
				NetworkConfiguration: netCfg, ClientToken: "same",
			})
			if assert.NoError(t, err) {
				ids[i] = ts.ID
			}
		}()
	}

	wg.Wait()

	for _, id := range ids {
		assert.Equal(t, ids[0], id)
	}

	sets, _, err := m.DescribeTaskSets(ctx, "prod", "ext", nil)
	require.NoError(t, err)
	assert.Len(t, sets, 1)
}

func TestDeleteService_CascadesTaskSets(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 2)
	ctx := context.Background()

	ts, err := m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
		Tags: []driver.Tag{{Key: "k", Value: "v"}},
	})
	require.NoError(t, err)

	_, err = m.DeleteService(ctx, "prod", "ext", true)
	require.NoError(t, err)

	assert.Equal(t, 0, m.taskSets.Len(), "no orphan task sets")

	running, err := m.ListTasks(ctx, "prod", "", "RUNNING", "ext")
	require.NoError(t, err)
	assert.Empty(t, running)

	_, err = m.ListTagsForResource(ctx, ts.ARN)
	require.Error(t, err, "the task set's tag entry is gone with it")
}

func TestDeleteService_ParallelWithCreateTaskSetLeavesNoOrphans(t *testing.T) {
	for range 20 {
		m := newTestMock()
		netCfg := externalFixture(t, m, 1)
		ctx := context.Background()

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
				Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
			})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.DeleteService(ctx, "prod", "ext", true)
		}()

		wg.Wait()

		svc, _, err := m.DescribeServices(ctx, "prod", []string{"ext"})
		require.NoError(t, err)
		require.Len(t, svc, 1)

		if svc[0].Status == statusInactive {
			assert.Equal(t, 0, m.taskSets.Len(), "an inactive service must own no task sets")

			running, err := m.ListTasks(ctx, "prod", "", "RUNNING", "ext")
			require.NoError(t, err)
			assert.Empty(t, running)
		}
	}
}

func TestTaskSet_TagsAndTagResource(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 1)
	ctx := context.Background()
	ts := newTaskSet(t, m, netCfg, nil)

	require.NoError(t, m.TagResource(ctx, ts.ARN, []driver.Tag{{Key: "a", Value: "b"}}))

	tags, err := m.ListTagsForResource(ctx, ts.ARN)
	require.NoError(t, err)
	assert.Equal(t, []driver.Tag{{Key: "a", Value: "b"}}, tags)

	require.NoError(t, m.UntagResource(ctx, ts.ARN, []string{"a"}))
}

func TestMalformedAndForeignIdentifiers_TaskSets(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 1)
	ctx := context.Background()
	ts := newTaskSet(t, m, netCfg, nil)

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "other"})
	require.NoError(t, err)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "ext", Cluster: "other", DeploymentController: deployControllerExternal,
	})
	require.NoError(t, err)

	got, failures, err := m.DescribeTaskSets(ctx, "other", "ext", []string{ts.ID, ts.ARN})
	require.NoError(t, err)
	assert.Empty(t, got, "another cluster's task set is invisible")
	assert.Len(t, failures, 2)

	_, err = m.UpdateTaskSet(ctx, driver.UpdateTaskSetInput{
		Cluster: "other", Service: "ext", TaskSet: ts.ARN, Scale: driver.Scale{Unit: driver.ScaleUnitPercent, Value: 1},
	})
	assert.Equal(t, excTaskSetNotFound, ecsException(t, err))
}

// externalUpdateFixture creates a second EXTERNAL service "ext2" carrying a task
// definition, platform version, network configuration, load balancers and
// service registries, so identical echoes can be compared.
func externalUpdateFixture(t *testing.T, m *Mock, netCfg *driver.NetworkConfiguration) driver.CreateServiceInput {
	t.Helper()

	in := driver.CreateServiceInput{
		ServiceName: "ext2", Cluster: "prod", DeploymentController: deployControllerExternal, DesiredCount: 2,
		TaskDefinition: "fg", LaunchType: launchFargate, PlatformVersion: "1.4.0", NetworkConfiguration: netCfg,
		LoadBalancers:     []driver.LoadBalancer{{TargetGroupARN: "tg-1", ContainerName: "c", ContainerPort: 80}},
		ServiceRegistries: []driver.ServiceRegistry{{RegistryARN: "reg-1"}},
	}
	_, err := m.CreateService(context.Background(), in)
	require.NoError(t, err)

	return in
}

func TestUpdateService_ExternalRejectsTaskDefinitionChange(t *testing.T) {
	m := newTestMock()
	externalFixture(t, m, 2)
	ctx := context.Background()

	_, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: "ext", Cluster: "prod", TaskDefinition: "fg"})
	require.Error(t, err)
	assert.Equal(t, excInvalidParameter, ecsException(t, err))

	got, _, err := m.DescribeServices(ctx, "prod", []string{"ext"})
	require.NoError(t, err)
	assert.Empty(t, got[0].TaskDefinition, "the stored service keeps its (empty) task definition")

	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "ext", Cluster: "prod", TaskDefinition: "missing"})
	require.Error(t, err)
	assert.NotEqual(t, excInvalidParameter, ecsException(t, err), "an unresolvable reference is the not-found error")
}

func TestUpdateService_ExternalRejectsTaskSetFields(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 2)
	ctx := context.Background()
	base := externalUpdateFixture(t, m, netCfg)

	newNet := &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-2"}}}
	otherLB := []driver.LoadBalancer{{TargetGroupARN: "tg-2", ContainerName: "c", ContainerPort: 80}}
	seven := 7

	tests := []struct {
		name string
		in   driver.UpdateServiceInput
	}{
		{"platformVersion", driver.UpdateServiceInput{PlatformVersion: "1.3.0"}},
		{"networkConfiguration", driver.UpdateServiceInput{NetworkConfiguration: newNet}},
		{"loadBalancers", driver.UpdateServiceInput{LoadBalancers: otherLB}},
		{"emptyLoadBalancers", driver.UpdateServiceInput{LoadBalancers: []driver.LoadBalancer{}}},
		{"capacityProviderStrategy", driver.UpdateServiceInput{
			CapacityProviderStrategy: []driver.CapacityProviderStrategyItem{{CapacityProvider: "FARGATE", Weight: 1}}}},
		{"serviceRegistries", driver.UpdateServiceInput{ServiceRegistries: []driver.ServiceRegistry{{RegistryARN: "reg-2"}}}},
		{"fieldWithDesiredCount", driver.UpdateServiceInput{PlatformVersion: "1.3.0", DesiredCount: &seven}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before, _, err := m.DescribeServices(ctx, "prod", []string{base.ServiceName})
			require.NoError(t, err)

			tc.in.Service, tc.in.Cluster = base.ServiceName, "prod"

			_, err = m.UpdateService(ctx, tc.in)
			require.Error(t, err)
			assert.Equal(t, excInvalidParameter, ecsException(t, err))

			after, _, err := m.DescribeServices(ctx, "prod", []string{base.ServiceName})
			require.NoError(t, err)
			assert.Equal(t, before, after, "a rejected update changes nothing, desiredCount included")
		})
	}
}

func TestUpdateService_ExternalAcceptsIdenticalEchoes(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 2)
	ctx := context.Background()
	base := externalUpdateFixture(t, m, netCfg)

	before, _, err := m.DescribeServices(ctx, "prod", []string{"ext2"})
	require.NoError(t, err)

	echo := &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{
		Subnets: []string{"subnet-1"}, AssignPublicIP: assignPublicIPDisabled}}
	td := before[0].TaskDefinition

	for _, ref := range []string{td, "fg", "fg:1"} {
		_, err = m.UpdateService(ctx, driver.UpdateServiceInput{
			Service: "ext2", Cluster: "prod", TaskDefinition: ref, PlatformVersion: base.PlatformVersion,
			NetworkConfiguration: echo, LoadBalancers: base.LoadBalancers, ServiceRegistries: base.ServiceRegistries,
		})
		require.NoError(t, err, ref)
	}

	after, _, err := m.DescribeServices(ctx, "prod", []string{"ext2"})
	require.NoError(t, err)
	assert.Equal(t, before[0].TaskDefinition, after[0].TaskDefinition)
	assert.Equal(t, before[0].NetworkConfiguration, after[0].NetworkConfiguration)
	assert.Equal(t, before[0].LoadBalancers, after[0].LoadBalancers)
}

func TestUpdateService_ExternalAllowsScalarUpdates(t *testing.T) {
	m := newTestMock()
	netCfg := externalFixture(t, m, 2)
	ctx := context.Background()
	newTaskSet(t, m, netCfg, nil)

	four, grace := 4, 30

	_, err := m.UpdateService(ctx, driver.UpdateServiceInput{
		Service: "ext", Cluster: "prod", DesiredCount: &four, HealthCheckGracePeriodSeconds: &grace, PropagateTags: propagateService,
	})
	require.NoError(t, err)

	got, _, err := m.DescribeServices(ctx, "prod", []string{"ext"})
	require.NoError(t, err)
	assert.Equal(t, 4, got[0].DesiredCount)
	assert.Equal(t, 4, got[0].RunningCount, "the task set rescaled")
	assert.Equal(t, propagateService, got[0].PropagateTags)
	require.NotNil(t, got[0].HealthCheckGracePeriodSeconds)
	assert.Equal(t, grace, *got[0].HealthCheckGracePeriodSeconds)
}

func TestUpdateService_ECSControllerStillAcceptsTaskSetFields(t *testing.T) {
	m := newTestMock()
	netCfg := fargateFixture(t, m)
	ctx := context.Background()

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "rolling", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	newNet := &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-2"}}}
	lbs := []driver.LoadBalancer{{TargetGroupARN: "tg-2", ContainerName: "c", ContainerPort: 80}}

	got, err := m.UpdateService(ctx, driver.UpdateServiceInput{
		Service: "rolling", Cluster: "prod", TaskDefinition: "fg", PlatformVersion: "1.3.0", NetworkConfiguration: newNet,
		LoadBalancers: lbs, ServiceRegistries: []driver.ServiceRegistry{{RegistryARN: "reg-2"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "1.3.0", got.PlatformVersion)
	assert.Equal(t, lbs, got.LoadBalancers)
	assert.Equal(t, []string{"subnet-2"}, got.NetworkConfiguration.AwsVpcConfiguration.Subnets)
}
