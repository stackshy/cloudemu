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
