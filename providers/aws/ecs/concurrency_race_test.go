package ecs

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// TestConcurrentTaskAccessRace hammers StopTask (a copy-on-write mutator that
// also rewrites each container's status) concurrently with Get/List reads. Run
// under `go test -race` it guards the aliasing + data-race class fixed by the
// copy-on-write mutators and the clone-on-read helpers.
func TestConcurrentTaskAccessRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family: "web",
		ContainerDefinitions: []driver.ContainerDefinition{
			{Name: "c1", Image: "img1"}, {Name: "c2", Image: "img2"},
		},
	})
	require.NoError(t, err)

	// Seed EC2 capacity so the tasks place; StopTask then also exercises the
	// concurrent capacity-release path under -race.
	m.SeedContainerInstance("prod", "i-race")

	tasks, _, err := m.RunTask(ctx, driver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 5})
	require.NoError(t, err)

	arn := tasks[0].ARN

	const (
		workers = 8
		iters   = 50
	)

	var wg sync.WaitGroup

	for w := range workers {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			for range iters {
				switch id % 3 {
				case 0:
					_, _ = m.StopTask(ctx, "prod", arn, "bye")
				case 1:
					_, _, _ = m.DescribeTasks(ctx, "prod", []string{arn})
				default:
					_, _ = m.ListTasks(ctx, "prod", "", "", "")
				}
			}
		}(w)
	}

	wg.Wait()
}

// TestConcurrentServiceAccessRace hammers the service copy-on-write mutators
// (Update) concurrently with Get/List reads, exercising the copy-on-write and
// clone-on-read paths under `go test -race`.
func TestConcurrentServiceAccessRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img"}},
	})
	require.NoError(t, err)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "svc", Cluster: "prod", TaskDefinition: "web", DesiredCount: 1,
		Tags: []driver.Tag{{Key: "k", Value: "v"}},
	})
	require.NoError(t, err)

	const (
		workers = 8
		iters   = 50
	)

	var wg sync.WaitGroup

	for w := range workers {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			for i := range iters {
				switch id % 4 {
				case 0:
					count := i % 5
					_, _ = m.UpdateService(ctx, driver.UpdateServiceInput{
						Service: "svc", Cluster: "prod", DesiredCount: &count,
					})
				case 1:
					_, _, _ = m.DescribeServices(ctx, "prod", []string{"svc"})
				case 2:
					_, _ = m.ListServices(ctx, "prod")
				default:
					_, _, _ = m.DescribeClusters(ctx, []string{"prod"})
				}
			}
		}(w)
	}

	wg.Wait()
}

// fargateRaceFixture registers a Fargate awsvpc task definition in a fresh
// "prod" cluster and returns its network configuration.
func fargateRaceFixture(t *testing.T, m *Mock) *driver.NetworkConfiguration {
	t.Helper()

	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family: "web", NetworkMode: networkModeAwsvpc, RequiresCompatibilities: []string{launchFargate},
		CPU: "256", Memory: "512",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "c", Image: "img", Essential: true}},
	})
	require.NoError(t, err)

	return &driver.NetworkConfiguration{AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}}}
}

// readTasksUntil starts readers that list and describe every task of "prod"
// until stop closes, and returns a func that waits for them.
func readTasksUntil(m *Mock, stop <-chan struct{}) func() {
	var wg sync.WaitGroup

	for range 4 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				tasks, _ := m.ListTasks(context.Background(), "prod", "", "", "")

				arns := make([]string, 0, len(tasks))
				for i := range tasks {
					arns = append(arns, tasks[i].ARN)
				}

				if len(arns) > 0 && len(arns) <= 100 {
					_, _, _ = m.DescribeTasks(context.Background(), "prod", arns)
				}
			}
		}()
	}

	return wg.Wait
}

// TestConcurrentFargateRunTaskVsReadRace guards launchTask publishing a Fargate
// task to the store before stampLaunch finished writing it: a concurrent
// List/DescribeTasks clone then raced those writes under `go test -race`.
func TestConcurrentFargateRunTaskVsReadRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateRaceFixture(t, m)

	stop := make(chan struct{})
	wait := readTasksUntil(m, stop)

	for range 50 {
		_, _, err := m.RunTask(ctx, driver.RunTaskInput{
			Cluster: "prod", TaskDefinition: "web", LaunchType: launchFargate, NetworkConfiguration: netCfg, Count: 1,
		})
		require.NoError(t, err)
	}

	close(stop)
	wait()
}

// TestConcurrentFargateForceDeployVsReadRace is the same race reached through
// UpdateService ForceNewDeployment launching replacement Fargate tasks.
func TestConcurrentFargateForceDeployVsReadRace(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	netCfg := fargateRaceFixture(t, m)

	_, err := m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "svc", Cluster: "prod", TaskDefinition: "web", LaunchType: launchFargate,
		NetworkConfiguration: netCfg, DesiredCount: 2,
	})
	require.NoError(t, err)

	stop := make(chan struct{})
	wait := readTasksUntil(m, stop)

	for range 50 {
		_, err = m.UpdateService(ctx, driver.UpdateServiceInput{Cluster: "prod", Service: "svc", ForceNewDeployment: true})
		require.NoError(t, err)
	}

	close(stop)
	wait()
}
