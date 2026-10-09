package ecs_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/features/recorder"
	awsecs "github.com/stackshy/cloudemu/v2/providers/aws/ecs"
	"github.com/stackshy/cloudemu/v2/services/ecs"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func newWrapper(t *testing.T) (*ecs.ECS, *awsecs.Mock, *recorder.Recorder) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	rec := recorder.New()
	mock := awsecs.New(opts)

	return ecs.NewECS(mock, ecs.WithRecorder(rec)), mock, rec
}

func TestWrapperClusterFlow(t *testing.T) {
	e, _, rec := newWrapper(t)
	ctx := context.Background()

	c, err := e.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	assert.Equal(t, "prod", c.Name)

	list, err := e.ListClusters(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// The wrapper records every call it proxies.
	assert.Equal(t, 1, rec.CallCountFor("ecs", "CreateCluster"))
	assert.Equal(t, 1, rec.CallCountFor("ecs", "ListClusters"))
}

func TestWrapperBatchPartialSuccess(t *testing.T) {
	e, _, _ := newWrapper(t)
	ctx := context.Background()

	_, err := e.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	found, failures, err := e.DescribeClusters(ctx, []string{"prod", "ghost"})
	require.NoError(t, err)
	assert.Len(t, found, 1)
	require.Len(t, failures, 1)
	assert.Equal(t, "MISSING", failures[0].Reason)
}

func TestWrapperTaskAndServiceFlow(t *testing.T) {
	e, mock, _ := newWrapper(t)
	ctx := context.Background()

	_, err := e.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = e.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	// Seed EC2 capacity so the default-launch-type task places.
	mock.SeedContainerInstance("prod", "i-wrapper")

	tasks, failures, err := e.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)
	assert.Empty(t, failures)
	require.Len(t, tasks, 1)

	descTasks, taskFailures, err := e.DescribeTasks(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Empty(t, taskFailures)
	assert.Len(t, descTasks, 1)

	svc, err := e.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "svc", Cluster: "prod", TaskDefinition: "web", DesiredCount: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, svc.RunningCount)

	descSvc, svcFailures, err := e.DescribeServices(ctx, "prod", []string{"svc"})
	require.NoError(t, err)
	assert.Empty(t, svcFailures)
	assert.Len(t, descSvc, 1)
}

func TestWrapperWave4bFlow(t *testing.T) {
	e, _, rec := newWrapper(t)
	ctx := context.Background()

	c, err := e.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	// Tagging.
	require.NoError(t, e.TagResource(ctx, c.ARN, []driver.Tag{{Key: "env", Value: "prod"}}))

	tags, err := e.ListTagsForResource(ctx, c.ARN)
	require.NoError(t, err)
	assert.Len(t, tags, 1)
	require.NoError(t, e.UntagResource(ctx, c.ARN, []string{"env"}))

	// Account settings.
	_, err = e.PutAccountSetting(ctx, "containerInsights", "enabled")
	require.NoError(t, err)
	_, err = e.PutAccountSettingDefault(ctx, "serviceLongArnFormat", "enabled")
	require.NoError(t, err)

	settings, err := e.ListAccountSettings(ctx)
	require.NoError(t, err)
	assert.Len(t, settings, 2)
	_, err = e.DeleteAccountSetting(ctx, "containerInsights")
	require.NoError(t, err)

	// Container instances.
	ci, err := e.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{Cluster: "prod"})
	require.NoError(t, err)

	instances, failures, err := e.UpdateContainerInstancesState(ctx, "prod", []string{ci.ARN}, "DRAINING")
	require.NoError(t, err)
	assert.Empty(t, failures)
	require.Len(t, instances, 1)
	assert.Equal(t, "DRAINING", instances[0].Status)

	_, err = e.DeregisterContainerInstance(ctx, "prod", ci.ARN, false)
	require.NoError(t, err)

	// Cluster mutations.
	_, err = e.UpdateClusterSettings(ctx, "prod", []driver.Setting{{Name: "containerInsights", Value: "enabled"}})
	require.NoError(t, err)
	_, err = e.UpdateCluster(ctx, driver.UpdateClusterInput{Cluster: "prod"})
	require.NoError(t, err)
	_, err = e.PutClusterCapacityProviders(ctx, "prod", []string{"FARGATE"}, nil)
	require.NoError(t, err)

	// Attributes.
	_, err = e.PutAttributes(ctx, "prod", []driver.Attribute{
		{Name: "stack", Value: "prod", TargetID: "i-1", TargetType: "container-instance"},
	})
	require.NoError(t, err)

	attrs, err := e.ListAttributes(ctx, "prod", "container-instance", "", "")
	require.NoError(t, err)
	assert.Len(t, attrs, 1)
	_, err = e.DeleteAttributes(ctx, "prod", []driver.Attribute{{Name: "stack", TargetID: "i-1"}})
	require.NoError(t, err)

	// Task-definition families.
	_, err = e.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	fams, err := e.ListTaskDefinitionFamilies(ctx, "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"web"}, fams)

	// ExecuteCommand through the wrapper (requires a placed task).
	e2, mock2, _ := newWrapper(t)
	_, err = e2.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	_, err = e2.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "nginx", Image: "img"}},
	})
	require.NoError(t, err)
	mock2.SeedContainerInstance("prod", "i-exec")

	tasks, _, err := e2.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", EnableExecuteCommand: true})
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	exec, err := e2.ExecuteCommand(ctx, driver.ExecuteCommandInput{
		Cluster: "prod", Task: tasks[0].ARN, Command: "ls", Interactive: true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, exec.Session.SessionID)

	// The wrapper records every proxied call.
	assert.Equal(t, 1, rec.CallCountFor("ecs", "TagResource"))
}

func TestWrapperCapacityProviderFlow(t *testing.T) {
	e, _, rec := newWrapper(t)
	ctx := context.Background()

	cp, err := e.CreateCapacityProvider(ctx, driver.CreateCapacityProviderInput{
		Name:                     "asg",
		AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{AutoScalingGroupARN: "asg-name"},
	})
	require.NoError(t, err)
	assert.Equal(t, "EC2_AUTOSCALING", cp.Type)

	found, failures, err := e.DescribeCapacityProviders(ctx, "", []string{"asg", "ghost"})
	require.NoError(t, err)
	assert.Len(t, found, 1)
	assert.Len(t, failures, 1)

	updated, err := e.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{
		Name:                     "asg",
		AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{ManagedDraining: "ENABLED"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ENABLED", updated.AutoScalingGroupProvider.ManagedDraining)

	deleted, err := e.DeleteCapacityProvider(ctx, "", "asg")
	require.NoError(t, err)
	assert.Equal(t, "INACTIVE", deleted.Status)

	// Errors propagate through every wrapper.
	_, err = e.CreateCapacityProvider(ctx, driver.CreateCapacityProviderInput{Name: "bad"})
	require.Error(t, err)
	_, err = e.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{Name: "ghost"})
	require.Error(t, err)
	_, err = e.DeleteCapacityProvider(ctx, "", "ghost")
	require.Error(t, err)

	for _, op := range []string{"CreateCapacityProvider", "DescribeCapacityProviders", "UpdateCapacityProvider", "DeleteCapacityProvider"} {
		assert.Positive(t, rec.CallCountFor("ecs", op), op)
	}
}

// TestWrapperOptionalCapabilities drives every optional capability through the
// portable wrapper and checks each call is recorded.
func TestWrapperOptionalCapabilities(t *testing.T) {
	e, _, rec := newWrapper(t)
	ctx := context.Background()

	_, err := e.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = e.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img", Essential: true}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             "awsvpc",
		RequiresCompatibilities: []string{"FARGATE"},
	})
	require.NoError(t, err)

	netCfg := &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}}}

	_, err = e.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "ext", Cluster: "prod", DeploymentController: "EXTERNAL", DesiredCount: 2,
	})
	require.NoError(t, err)

	ts, err := e.CreateTaskSet(ctx, driver.CreateTaskSetInput{
		Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: "FARGATE", NetworkConfiguration: netCfg,
	})
	require.NoError(t, err)

	half := driver.Scale{Unit: driver.ScaleUnitPercent, Value: 50}
	_, err = e.UpdateTaskSet(ctx, driver.UpdateTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: ts.ID, Scale: half})
	require.NoError(t, err)

	sets, failures, err := e.DescribeTaskSets(ctx, "prod", "ext", []string{ts.ID})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, sets, 1)

	_, err = e.UpdateServicePrimaryTaskSet(ctx, "prod", "ext", ts.ID)
	require.NoError(t, err)

	_, err = e.DeleteTaskSet(ctx, driver.DeleteTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: ts.ID, Force: true})
	require.NoError(t, err)

	_, err = e.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: "FARGATE", NetworkConfiguration: netCfg, DesiredCount: 1,
		ServiceConnect: &driver.ServiceConnectConfiguration{Namespace: "mesh"},
	})
	require.NoError(t, err)

	tasks, err := e.ListTasks(ctx, "prod", "", "", "web")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	_, _, err = e.UpdateTaskProtection(ctx, driver.UpdateTaskProtectionInput{Cluster: "prod", Tasks: []string{tasks[0].ARN}, ProtectionEnabled: true})
	require.NoError(t, err)

	got, _, err := e.GetTaskProtection(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.True(t, got[0].ProtectionEnabled)

	deps, next, err := e.ListServiceDeployments(ctx, driver.ListServiceDeploymentsInput{Cluster: "prod", Service: "web"})
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Empty(t, next)

	described, _, err := e.DescribeServiceDeployments(ctx, []string{deps[0].ARN})
	require.NoError(t, err)
	assert.Len(t, described, 1)

	revs, _, err := e.DescribeServiceRevisions(ctx, []string{deps[0].TargetServiceRevision.ARN})
	require.NoError(t, err)
	assert.Len(t, revs, 1)

	_, err = e.StopServiceDeployment(ctx, deps[0].ARN, driver.StopTypeRollback)
	require.Error(t, err, "a completed deployment cannot be stopped")

	arns, _, err := e.ListServicesByNamespace(ctx, "mesh", 0, "")
	require.NoError(t, err)
	assert.Len(t, arns, 1)

	for _, op := range []string{
		"CreateTaskSet", "UpdateTaskSet", "DescribeTaskSets", "UpdateServicePrimaryTaskSet", "DeleteTaskSet",
		"UpdateTaskProtection", "GetTaskProtection", "ListServiceDeployments", "DescribeServiceDeployments",
		"DescribeServiceRevisions", "StopServiceDeployment", "ListServicesByNamespace",
	} {
		assert.Equal(t, 1, rec.CallCountFor("ecs", op), op)
	}
}

// bareDriver satisfies driver.ECS without any optional capability.
type bareDriver struct{ driver.ECS }

// TestWrapperCapabilityMissing pins that a driver without a capability gets a
// typed FailedPrecondition, not a panic.
func TestWrapperCapabilityMissing(t *testing.T) {
	e := ecs.NewECS(bareDriver{})
	ctx := context.Background()

	_, _, err := e.GetTaskProtection(ctx, "prod", []string{"t"})
	require.Error(t, err)

	_, err = e.CreateTaskSet(ctx, driver.CreateTaskSetInput{})
	require.Error(t, err)

	_, _, err = e.ListServiceDeployments(ctx, driver.ListServiceDeploymentsInput{})
	require.Error(t, err)

	_, _, err = e.ListServicesByNamespace(ctx, "ns", 0, "")
	require.Error(t, err)
}
