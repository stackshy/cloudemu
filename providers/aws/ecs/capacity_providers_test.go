package ecs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

func asgInput(name string) driver.CreateCapacityProviderInput {
	return driver.CreateCapacityProviderInput{
		Name:                     name,
		AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{AutoScalingGroupARN: "asg-" + name},
	}
}

func TestCreateCapacityProviderValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	bad := []struct {
		name string
		in   driver.CreateCapacityProviderInput
	}{
		{"empty name", asgInput("")},
		{"bad characters", asgInput("has space")},
		{"aws prefix", asgInput("AWSthing")},
		{"ecs prefix", asgInput("ecs-x")},
		{"no provider block", driver.CreateCapacityProviderInput{Name: "none"}},
		{"both provider blocks", driver.CreateCapacityProviderInput{
			Name:                     "both",
			AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{AutoScalingGroupARN: "a"},
			ManagedInstancesProvider: json.RawMessage(`{}`),
		}},
		{"asg without arn", driver.CreateCapacityProviderInput{
			Name: "noarn", AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{},
		}},
		{"target capacity out of range", driver.CreateCapacityProviderInput{
			Name: "range", AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{
				AutoScalingGroupARN: "a", ManagedScaling: &driver.ManagedScaling{TargetCapacity: ptrInt(101)},
			},
		}},
		{"bad scaling status", driver.CreateCapacityProviderInput{
			Name: "status", AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{
				AutoScalingGroupARN: "a", ManagedScaling: &driver.ManagedScaling{Status: "ON"},
			},
		}},
		{"managed instances without cluster", driver.CreateCapacityProviderInput{
			Name: "mi", ManagedInstancesProvider: json.RawMessage(`{"infrastructureRoleArn":"r"}`),
		}},
		{"reserved tag key", driver.CreateCapacityProviderInput{
			Name:                     "tagged",
			AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{AutoScalingGroupARN: "a"},
			Tags:                     []driver.Tag{{Key: "aws:x", Value: "1"}},
		}},
	}

	for _, tc := range bad {
		_, err := m.CreateCapacityProvider(ctx, tc.in)
		assert.True(t, errors.IsInvalidArgument(err), "%s: err = %v", tc.name, err)
	}

	_, err = m.CreateCapacityProvider(ctx, driver.CreateCapacityProviderInput{
		Name: "mi", Cluster: "ghost", ManagedInstancesProvider: json.RawMessage(`{}`),
	})
	assert.True(t, errors.IsNotFound(err), "unknown cluster: %v", err)

	_, err = m.CreateCapacityProvider(ctx, driver.CreateCapacityProviderInput{
		Name: "asg-ghost", Cluster: "ghost",
		AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{AutoScalingGroupARN: "a"},
	})
	assert.True(t, errors.IsNotFound(err), "unknown cluster (asg): %v", err)
}

func TestManagedInstancesCapacityProvider(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, driver.CreateClusterInput{Name: "prod"})
	require.NoError(t, err)

	cp, err := m.CreateCapacityProvider(ctx, driver.CreateCapacityProviderInput{
		Name: "mi", Cluster: "prod", ManagedInstancesProvider: json.RawMessage(`{"infrastructureRoleArn":"r1"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, cpTypeManagedInstances, cp.Type)
	assert.Equal(t, "prod", cp.Cluster)

	// Cluster scoping: visible through its own cluster without association.
	found, _, err := m.DescribeCapacityProviders(ctx, "prod", nil)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "mi", found[0].Name)

	_, _, err = m.DescribeCapacityProviders(ctx, "ghost", nil)
	assert.True(t, errors.IsNotFound(err))

	// An ASG block does not apply to a Managed Instances provider.
	_, err = m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{
		Name: "mi", AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{ManagedDraining: "ENABLED"},
	})
	assert.True(t, errors.IsInvalidArgument(err))

	updated, err := m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{
		Name: "mi", ManagedInstancesProvider: json.RawMessage(`{"infrastructureRoleArn":"r2"}`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"infrastructureRoleArn":"r2"}`, string(updated.ManagedInstancesProvider))
	assert.Equal(t, cpUpdateComplete, updated.UpdateStatus)
}

func TestUpdateCapacityProviderErrors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCapacityProvider(ctx, asgInput("asg"))
	require.NoError(t, err)

	_, err = m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{Name: "FARGATE"})
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{Name: "ghost"})
	assert.True(t, errors.IsNotFound(err))

	_, err = m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{
		Name: "asg", ManagedInstancesProvider: json.RawMessage(`{}`),
	})
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{
		Name: "asg", AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{
			ManagedScaling: &driver.ManagedScaling{InstanceWarmupPeriod: ptrInt(-1)},
		},
	})
	assert.True(t, errors.IsInvalidArgument(err))

	updated, err := m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{
		Name: "asg", AutoScalingGroupProvider: &driver.AutoScalingGroupProvider{ManagedTerminationProtection: "ENABLED"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ENABLED", updated.AutoScalingGroupProvider.ManagedTerminationProtection)

	// A deleted provider cannot be updated, and its name can be reused.
	_, err = m.DeleteCapacityProvider(ctx, "", "asg")
	require.NoError(t, err)

	_, err = m.UpdateCapacityProvider(ctx, driver.UpdateCapacityProviderInput{Name: "asg"})
	assert.True(t, errors.IsNotFound(err))

	_, err = m.DeleteCapacityProvider(ctx, "", "asg")
	assert.True(t, errors.IsNotFound(err))

	again, err := m.CreateCapacityProvider(ctx, asgInput("asg"))
	require.NoError(t, err)
	assert.Equal(t, statusActive, again.Status)
}

func TestDeleteCapacityProviderInUseByService(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCapacityProvider(ctx, asgInput("asg"))
	require.NoError(t, err)

	_, err = m.RegisterTaskDefinition(ctx, driver.RegisterTaskDefinitionInput{
		Family:               "web",
		ContainerDefinitions: []driver.ContainerDefinition{{Name: "app", Image: "nginx", Memory: 128}},
	})
	require.NoError(t, err)

	_, err = m.CreateService(ctx, driver.CreateServiceInput{
		ServiceName: "s", TaskDefinition: "web",
		CapacityProviderStrategy: []driver.CapacityProviderStrategyItem{{CapacityProvider: "asg", Weight: 1}},
	})
	require.NoError(t, err)

	_, err = m.DeleteCapacityProvider(ctx, "", "asg")
	assert.True(t, errors.IsFailedPrecondition(err), "err = %v", err)
}

func TestCapacityProviderTagsAndSnapshot(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	in := asgInput("asg")
	in.Tags = []driver.Tag{{Key: "team", Value: "a"}}

	cp, err := m.CreateCapacityProvider(ctx, in)
	require.NoError(t, err)

	require.NoError(t, m.TagResource(ctx, cp.ARN, []driver.Tag{{Key: "env", Value: "dev"}}))

	// The predefined providers are readable but not taggable.
	fargate := m.arn("capacity-provider/FARGATE")
	tags, err := m.ListTagsForResource(ctx, fargate)
	require.NoError(t, err)
	assert.Empty(t, tags)
	assert.True(t, errors.IsInvalidArgument(m.TagResource(ctx, fargate, []driver.Tag{{Key: "a", Value: "b"}})))

	snap, err := m.Snapshot(ctx, false)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, snap))

	found, failures, err := restored.DescribeCapacityProviders(ctx, "", []string{cp.ARN})
	require.NoError(t, err)
	assert.Empty(t, failures)
	require.Len(t, found, 1)
	assert.Equal(t, []driver.Tag{{Key: "team", Value: "a"}, {Key: "env", Value: "dev"}}, found[0].Tags)

	// Deleting a provider deletes its tags.
	_, err = restored.DeleteCapacityProvider(ctx, "", "asg")
	require.NoError(t, err)

	_, err = restored.ListTagsForResource(ctx, cp.ARN)
	assert.True(t, errors.IsNotFound(err))
}
