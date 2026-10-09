package ecs

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// publishedEvent is one event captured by recordingPublisher.
type publishedEvent struct {
	DetailType string
	Detail     any
	Resources  []string
}

// recordingPublisher is a thread-safe awsevents.Publisher that records every
// published event in order.
type recordingPublisher struct {
	mu     sync.Mutex
	events []publishedEvent
	onEmit func()
}

func (r *recordingPublisher) PublishServiceEvent(_ context.Context, _, detailType string, detail any, resources []string) {
	r.mu.Lock()
	r.events = append(r.events, publishedEvent{DetailType: detailType, Detail: detail, Resources: resources})
	hook := r.onEmit
	r.mu.Unlock()

	if hook != nil {
		hook()
	}
}

func (r *recordingPublisher) snapshot() []publishedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]publishedEvent(nil), r.events...)
}

// fargateFixture creates cluster "prod" and the Fargate task definition "fg"
// and returns the awsvpc network configuration RunTask/CreateService need.
func fargateFixture(t *testing.T, m *Mock) *driver.NetworkConfiguration {
	t.Helper()

	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:                  "fg",
		ContainerDefinitions:    []driver.ContainerDefinition{{Name: "c", Image: "img", Essential: true}},
		CPU:                     "256",
		Memory:                  "512",
		NetworkMode:             networkModeAwsvpc,
		RequiresCompatibilities: []string{launchFargate},
	})
	require.NoError(t, err)

	return &driver.NetworkConfiguration{
		AwsVpcConfiguration: &driver.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
	}
}

// instanceRemaining returns the remaining CPU and memory of the single
// container instance registered in cluster.
func (m *Mock) instanceRemaining(t *testing.T, cluster string) [2]int {
	t.Helper()

	for _, ci := range m.instances.All() {
		if instanceClusterName(ci.ARN) == cluster {
			return [2]int{ci.RemainingCPU, ci.RemainingMemory}
		}
	}

	t.Fatalf("no container instance in cluster %q", cluster)

	return [2]int{}
}
