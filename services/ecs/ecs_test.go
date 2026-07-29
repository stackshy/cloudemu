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

func newWrapper(t *testing.T) (*ecs.ECS, *recorder.Recorder) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	rec := recorder.New()

	return ecs.NewECS(awsecs.New(opts), ecs.WithRecorder(rec)), rec
}

func TestWrapperClusterFlow(t *testing.T) {
	e, rec := newWrapper(t)
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
	e, _ := newWrapper(t)
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
	e, _ := newWrapper(t)
	ctx := context.Background()

	_, err := e.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	tasks, failures, err := e.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web"})
	require.NoError(t, err)
	assert.Empty(t, failures)
	require.Len(t, tasks, 1)

	descTasks, taskFailures, err := e.DescribeTasks(ctx, "prod", []string{tasks[0].ARN})
	require.NoError(t, err)
	assert.Empty(t, taskFailures)
	assert.Len(t, descTasks, 1)

	svc, err := e.CreateService(ctx, driver.CreateServiceInput{ServiceName: "svc", Cluster: "prod", DesiredCount: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, svc.RunningCount)

	descSvc, svcFailures, err := e.DescribeServices(ctx, "prod", []string{"svc"})
	require.NoError(t, err)
	assert.Empty(t, svcFailures)
	assert.Len(t, descSvc, 1)
}
