package ecs

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// eventsOf returns the recorded events of one detail type, in order.
func eventsOf(rec *recordingPublisher, detailType string) []publishedEvent {
	var out []publishedEvent

	for _, e := range rec.snapshot() {
		if e.DetailType == detailType {
			out = append(out, e)
		}
	}

	return out
}

// taskSteps returns the (lastStatus, version) sequence of the task state change
// events published for one task.
func taskSteps(rec *recordingPublisher, taskARN string) (statuses []string, versions []int) {
	for _, e := range eventsOf(rec, eventTaskStateChange) {
		d, ok := e.Detail.(taskStateChangeDetail)
		if ok && d.TaskArn == taskARN {
			statuses = append(statuses, d.LastStatus)
			versions = append(versions, d.Version)
		}
	}

	return statuses, versions
}

func TestTaskStateChange_AllTransitions_Fargate(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)
	netCfg := fargateFixture(t, m)

	task := runFargate(t, m, netCfg, false)

	statuses, versions := taskSteps(rec, task.ARN)
	assert.Equal(t, []string{"PROVISIONING", "PENDING", "ACTIVATING", "RUNNING"}, statuses)
	assert.Equal(t, []int{1, 2, 3, 4}, versions)

	_, err := m.StopTask(context.Background(), "prod", task.ARN, "bye")
	require.NoError(t, err)

	statuses, versions = taskSteps(rec, task.ARN)
	assert.Equal(t, []string{
		"PROVISIONING", "PENDING", "ACTIVATING", "RUNNING", "DEACTIVATING", "STOPPING", "DEPROVISIONING", "STOPPED",
	}, statuses)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8}, versions, "version increases by one per change")

	last, ok := eventsOf(rec, eventTaskStateChange)[7].Detail.(taskStateChangeDetail)
	require.True(t, ok)
	assert.Equal(t, "bye", last.StoppedReason)
	assert.NotEmpty(t, last.StoppedAt)
	assert.Equal(t, statusStopped, last.DesiredStatus)

	// A running-phase event has not started stopping yet.
	running, ok := eventsOf(rec, eventTaskStateChange)[3].Detail.(taskStateChangeDetail)
	require.True(t, ok)
	assert.Empty(t, running.StoppedAt)
	assert.NotEmpty(t, running.StartedAt)

	early, ok := eventsOf(rec, eventTaskStateChange)[0].Detail.(taskStateChangeDetail)
	require.True(t, ok)
	assert.Empty(t, early.StartedAt, "a task that is still provisioning has not started")
}

func TestTaskStateChange_AllTransitions_EC2(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)

	_, err := m.CreateCluster(context.Background(), driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 128, 128)
	m.SeedContainerInstance("prod", "i-1", WithCapacity(1024, 1024))

	tasks, _, err := m.RunTask(context.Background(), driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)

	statuses, versions := taskSteps(rec, tasks[0].ARN)
	assert.Equal(t, []string{"PENDING", "ACTIVATING", "RUNNING"}, statuses)
	assert.Equal(t, []int{1, 2, 3}, versions)

	_, err = m.StopTask(context.Background(), "prod", tasks[0].ARN, "bye")
	require.NoError(t, err)

	statuses, versions = taskSteps(rec, tasks[0].ARN)
	assert.Equal(t, []string{"PENDING", "ACTIVATING", "RUNNING", "DEACTIVATING", "STOPPING", "STOPPED"}, statuses,
		"no DEPROVISIONING without an ENI")
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6}, versions)

	// A second StopTask publishes nothing more.
	_, err = m.StopTask(context.Background(), "prod", tasks[0].ARN, "again")
	require.NoError(t, err)

	again, _ := taskSteps(rec, tasks[0].ARN)
	assert.Len(t, again, 6)
}

func TestDeploymentStateChange(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)
	netCfg := fargateFixture(t, m)
	ctx := context.Background()

	svc, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, DesiredCount: 1,
	})
	require.NoError(t, err)

	deployID := svc.Deployments[0].ID

	var got []deploymentStateChangeDetail

	for _, e := range eventsOf(rec, eventDeploymentStateChange) {
		assert.Equal(t, []string{svc.ARN}, e.Resources)

		d, ok := e.Detail.(deploymentStateChangeDetail)
		require.True(t, ok)

		got = append(got, d)
	}

	require.Len(t, got, 2)
	assert.Equal(t, "SERVICE_DEPLOYMENT_IN_PROGRESS", got[0].EventName)
	assert.Equal(t, "SERVICE_DEPLOYMENT_COMPLETED", got[1].EventName)
	assert.Equal(t, serviceEventTypeInfo, got[0].EventType)
	assert.Equal(t, deployID, got[0].DeploymentID)
	assert.Equal(t, "ECS deployment "+deployID+" in progress.", got[0].Reason)
	assert.Equal(t, "ECS deployment "+deployID+" completed.", got[1].Reason)
	assert.NotEmpty(t, got[0].UpdatedAt)

	// The same deployment also surfaces as service actions.
	var actions []string

	for _, e := range eventsOf(rec, eventServiceAction) {
		actions = append(actions, e.Detail.(serviceActionDetail).EventName)
	}

	assert.Contains(t, actions, "SERVICE_DEPLOYMENT_IN_PROGRESS")
	assert.Contains(t, actions, "SERVICE_DEPLOYMENT_COMPLETED")
	assert.Contains(t, actions, serviceSteadyStateName)

	before := len(eventsOf(rec, eventDeploymentStateChange))

	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", ForceNewDeployment: true})
	require.NoError(t, err)
	assert.Len(t, eventsOf(rec, eventDeploymentStateChange), before+2, "a new deployment emits again")
}

func TestDeploymentStateChange_NoCompletionWhileShort(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)

	_, err := m.CreateCluster(context.Background(), driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 128, 128)

	_, err = m.CreateService(context.Background(), driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "web", DesiredCount: 1,
	})
	require.NoError(t, err)

	evs := eventsOf(rec, eventDeploymentStateChange)
	require.Len(t, evs, 1, "an unplaceable service never completes its deployment")

	d, ok := evs[0].Detail.(deploymentStateChangeDetail)
	require.True(t, ok)
	assert.Equal(t, "SERVICE_DEPLOYMENT_IN_PROGRESS", d.EventName)
}

func TestContainerInstanceStateChange(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)
	registerWeb(t, m, 128, 256)

	ci, err := m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{
		Cluster: "prod", TotalResources: []driver.Resource{
			{Name: "CPU", Type: "INTEGER", IntegerValue: 1024}, {Name: "MEMORY", Type: "INTEGER", IntegerValue: 2048},
		},
	})
	require.NoError(t, err)

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)

	_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "bye")
	require.NoError(t, err)

	_, _, err = m.UpdateContainerInstancesState(ctx, "prod", []string{ci.ARN}, statusDraining)
	require.NoError(t, err)

	_, err = m.DeregisterContainerInstance(ctx, "prod", ci.ARN, false)
	require.NoError(t, err)

	var got []containerInstanceDetail

	for _, e := range eventsOf(rec, eventContainerInstanceStateChange) {
		assert.Equal(t, []string{ci.ARN}, e.Resources)

		d, ok := e.Detail.(containerInstanceDetail)
		require.True(t, ok)

		got = append(got, d)
	}

	require.Len(t, got, 5, "register, task placed, task stopped, draining, deregistered")

	for i, d := range got {
		assert.Equal(t, i+1, d.Version, "version increases by one per change")
		assert.Equal(t, ci.ARN, d.ContainerInstanceArn)
		assert.Equal(t, ci.EC2InstanceID, d.EC2InstanceID)
		assert.Equal(t, tasks[0].ClusterARN, d.ClusterArn)
	}

	assert.Equal(t, statusActive, got[0].Status)
	assert.Equal(t, 1024, ciResourceInt(got[0].RegisteredResources, "CPU"))
	assert.Equal(t, 1024, ciResourceInt(got[0].RemainingResources, "CPU"))
	assert.Equal(t, 896, ciResourceInt(got[1].RemainingResources, "CPU"), "placing the task reserves 128 CPU")
	assert.Equal(t, 1024, ciResourceInt(got[2].RemainingResources, "CPU"), "stopping it gives the CPU back")
	assert.Equal(t, statusDraining, got[3].Status)
	assert.Equal(t, statusInactive, got[4].Status)
}

func TestContainerInstanceStateChange_EmptyAttributesIsNotNull(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{Cluster: "prod"})
	require.NoError(t, err)

	evs := eventsOf(rec, eventContainerInstanceStateChange)
	require.Len(t, evs, 1)

	d, ok := evs[0].Detail.(containerInstanceDetail)
	require.True(t, ok)
	assert.NotNil(t, d.Attributes, "no attributes must marshal as [] not null")
	assert.Empty(t, d.Attributes)

	raw, err := json.Marshal(d)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"attributes":[]`)
}

func ciResourceInt(rs []containerInstanceResource, name string) int {
	for _, r := range rs {
		if r.Name == name {
			return r.IntegerValue
		}
	}

	return -1
}

func TestExternalDesiredCountUpdate_NoDesiredCountEvent(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)
	ctx := context.Background()

	externalFixture(t, m, 1)

	three := 3
	_, err := m.UpdateService(ctx, driver.UpdateServiceInput{Service: "ext", Cluster: "prod", DesiredCount: &three})
	require.NoError(t, err)

	for _, e := range eventsOf(rec, eventServiceAction) {
		assert.NotEqual(t, "SERVICE_DESIRED_COUNT_UPDATED", e.Detail.(serviceActionDetail).EventName)
	}
}

func TestServiceActionEvents(t *testing.T) {
	m := newTestMock()
	rec := &recordingPublisher{}
	m.SetEventPublisher(rec)
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg, DesiredCount: 1,
	})
	require.NoError(t, err)

	three := 3
	_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", DesiredCount: &three})
	require.NoError(t, err)

	var names []string

	for _, e := range eventsOf(rec, eventServiceAction) {
		names = append(names, e.Detail.(serviceActionDetail).EventName)
	}

	assert.NotContains(t, names, "SERVICE_DESIRED_COUNT_UPDATED", "a user-driven count change is not published")

	// EC2 service on a cluster too small for its task: placement failure.
	_, err = m.CreateCluster(ctx, driver.CreateClusterInput{Name: "small"})
	require.NoError(t, err)
	registerWeb(t, m, 512, 512)
	m.SeedContainerInstance("small", "i-small", WithCapacity(256, 4096))

	_, err = m.CreateService(ctx, driver.CreateServiceInput{ServiceName: "cpu", Cluster: "small", TaskDefinition: "web", DesiredCount: 1})
	require.NoError(t, err)

	var failure serviceActionDetail

	for _, e := range eventsOf(rec, eventServiceAction) {
		if d := e.Detail.(serviceActionDetail); d.EventName == "SERVICE_TASK_PLACEMENT_FAILURE" {
			failure = d
		}
	}

	assert.Equal(t, "SERVICE_TASK_PLACEMENT_FAILURE", failure.EventName)
	assert.Equal(t, "ERROR", failure.EventType)
	assert.Equal(t, "RESOURCE:CPU", failure.Reason)
}

// TestEvents_PublishedWithoutLocksHeld re-enters ECS from the event consumer at
// every emit site; an event published while a store, placement, task-set or
// reconcile lock is held would deadlock (the test then times out).
func TestEvents_PublishedWithoutLocksHeld(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	// The consumer stops a service task on its first few events only: stopping on
	// every event would make the service replace tasks forever.
	var stops atomic.Int32

	rec := &recordingPublisher{}
	rec.onEmit = func() {
		// Every call below takes locks the emitting operation must have released.
		_, _ = m.ListTasks(ctx, "prod", "", "", "")
		_, _, _ = m.DescribeServices(ctx, "prod", []string{"web", "ext"})
		_, _, _ = m.DescribeTaskSets(ctx, "prod", "ext", nil)
		_, _, _ = m.ListServiceDeployments(ctx, driver.ListServiceDeploymentsInput{Cluster: "prod", Service: "web"})

		tasks, _ := m.ListTasks(ctx, "prod", "", "RUNNING", "web")
		if len(tasks) > 0 && stops.Add(1) <= 3 {
			_, _ = m.StopTask(ctx, "prod", tasks[0].ARN, "from-consumer")
		}
	}
	m.SetEventPublisher(rec)

	done := make(chan struct{})

	go func() {
		defer close(done)

		_, err := m.CreateService(ctx, driver.CreateServiceInput{
			ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg, DesiredCount: 2,
		})
		assert.NoError(t, err)

		two := 1
		_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Service: "web", Cluster: "prod", DesiredCount: &two, ForceNewDeployment: true})
		assert.NoError(t, err)

		_, err = m.CreateService(ctx, driver.CreateServiceInput{ServiceName: "ext", Cluster: "prod", DeploymentController: deployControllerExternal, DesiredCount: 2})
		assert.NoError(t, err)

		ts, err := m.CreateTaskSet(ctx, driver.CreateTaskSetInput{
			Cluster: "prod", Service: "ext", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg,
		})
		assert.NoError(t, err)

		half := driver.Scale{Unit: driver.ScaleUnitPercent, Value: 50}
		_, err = m.UpdateTaskSet(ctx, driver.UpdateTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: ts.ID, Scale: half})
		assert.NoError(t, err)

		_, err = m.DeleteTaskSet(ctx, driver.DeleteTaskSetInput{Cluster: "prod", Service: "ext", TaskSet: ts.ID, Force: true})
		assert.NoError(t, err)

		tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg})
		assert.NoError(t, err)

		if len(tasks) == 1 {
			_, err = m.StopTask(ctx, "prod", tasks[0].ARN, "x")
			assert.NoError(t, err)
		}

		ci, err := m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{Cluster: "prod"})
		assert.NoError(t, err)

		_, err = m.DeregisterContainerInstance(ctx, "prod", ci.ARN, true)
		assert.NoError(t, err)

		_, err = m.DeleteService(ctx, "prod", "web", true)
		assert.NoError(t, err)

		_, err = m.DeleteService(ctx, "prod", "ext", true)
		assert.NoError(t, err)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("deadlock: an event was published while a lock was held")
	}

	assert.NotEmpty(t, rec.snapshot())
}

func TestEvents_NoPublisherNoPanic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateFixture(t, m)

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "web", Cluster: "prod", TaskDefinition: "fg", LaunchType: launchFargate, NetworkConfiguration: netCfg, DesiredCount: 1,
	})
	require.NoError(t, err)

	_, err = m.RegisterContainerInstance(ctx, driver.RegisterContainerInstanceInput{Cluster: "prod"})
	require.NoError(t, err)

	_, err = m.DeleteService(ctx, "prod", "web", true)
	require.NoError(t, err)
}
