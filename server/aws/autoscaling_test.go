package aws_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	smithy "github.com/aws/smithy-go"
	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newASGTestClient(t *testing.T) *autoscaling.Client {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EC2: provider.EC2, VPC: provider.VPC})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("t", "t", "")))
	require.NoError(t, err)

	return autoscaling.NewFromConfig(cfg, func(o *autoscaling.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// TestASGUpdatePartial verifies that updating only MaxSize leaves MinSize and
// DesiredCapacity (and the running fleet) untouched.
func TestASGUpdatePartial(t *testing.T) {
	asc := newASGTestClient(t)
	ctx := context.Background()

	_, err := asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName:    aws.String("partial-asg"),
		MinSize:                 aws.Int32(2),
		MaxSize:                 aws.Int32(5),
		DesiredCapacity:         aws.Int32(3),
		AvailabilityZones:       []string{"us-east-1a"},
		LaunchConfigurationName: aws.String("lc"),
	})
	require.NoError(t, err)

	_, err = asc.UpdateAutoScalingGroup(ctx, &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("partial-asg"),
		MaxSize:              aws.Int32(10),
	})
	require.NoError(t, err)

	desc, err := asc.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{"partial-asg"},
	})
	require.NoError(t, err)
	require.Len(t, desc.AutoScalingGroups, 1)

	g := desc.AutoScalingGroups[0]
	assert.Equal(t, int32(10), aws.ToInt32(g.MaxSize), "MaxSize should update")
	assert.Equal(t, int32(2), aws.ToInt32(g.MinSize), "MinSize should be unchanged")
	assert.Equal(t, int32(3), aws.ToInt32(g.DesiredCapacity), "DesiredCapacity should be unchanged")
	assert.Len(t, g.Instances, 3, "running fleet should be untouched")
}

// TestASGDescribeInstancesList verifies each running instance is its own
// Instances member (desired=3 -> 3 Instances).
func TestASGDescribeInstancesList(t *testing.T) {
	asc := newASGTestClient(t)
	ctx := context.Background()

	_, err := asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName:    aws.String("fleet-asg"),
		MinSize:                 aws.Int32(1),
		MaxSize:                 aws.Int32(5),
		DesiredCapacity:         aws.Int32(3),
		AvailabilityZones:       []string{"us-east-1a", "us-east-1b"},
		LaunchConfigurationName: aws.String("lc"),
	})
	require.NoError(t, err)

	desc, err := asc.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{"fleet-asg"},
	})
	require.NoError(t, err)
	require.Len(t, desc.AutoScalingGroups, 1)

	insts := desc.AutoScalingGroups[0].Instances
	require.Len(t, insts, 3, "one Instance per running instance")

	for _, inst := range insts {
		assert.NotEmpty(t, aws.ToString(inst.InstanceId))
		assert.Equal(t, astypes.LifecycleStateInService, inst.LifecycleState)
		assert.Equal(t, "Healthy", aws.ToString(inst.HealthStatus))
	}
}

// TestASGLaunchTemplateEcho verifies the group echoes the LaunchTemplate it was
// created with.
func TestASGLaunchTemplateEcho(t *testing.T) {
	asc := newASGTestClient(t)
	ctx := context.Background()

	_, err := asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("lt-asg"),
		MinSize:              aws.Int32(1), MaxSize: aws.Int32(3), DesiredCapacity: aws.Int32(1),
		AvailabilityZones: []string{"us-east-1a"},
		LaunchTemplate: &astypes.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String("web-lt"),
			Version:            aws.String("2"),
		},
	})
	require.NoError(t, err)

	desc, err := asc.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{"lt-asg"},
	})
	require.NoError(t, err)
	require.Len(t, desc.AutoScalingGroups, 1)

	g := desc.AutoScalingGroups[0]
	require.NotNil(t, g.LaunchTemplate)
	assert.Equal(t, "web-lt", aws.ToString(g.LaunchTemplate.LaunchTemplateName))
	assert.Equal(t, "2", aws.ToString(g.LaunchTemplate.Version))
	assert.Empty(t, aws.ToString(g.LaunchConfigurationName))
}

// TestASGLaunchConfigurationEcho verifies the group echoes its
// LaunchConfigurationName.
func TestASGLaunchConfigurationEcho(t *testing.T) {
	asc := newASGTestClient(t)
	ctx := context.Background()

	_, err := asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("lc-asg"),
		MinSize:              aws.Int32(1), MaxSize: aws.Int32(3), DesiredCapacity: aws.Int32(1),
		AvailabilityZones:       []string{"us-east-1a"},
		LaunchConfigurationName: aws.String("my-lc"),
	})
	require.NoError(t, err)

	desc, err := asc.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{"lc-asg"},
	})
	require.NoError(t, err)
	require.Len(t, desc.AutoScalingGroups, 1)

	assert.Equal(t, "my-lc", aws.ToString(desc.AutoScalingGroups[0].LaunchConfigurationName))
	assert.Nil(t, desc.AutoScalingGroups[0].LaunchTemplate)
}

// TestASGDeleteWithInstancesNoForce verifies deleting a group with instances and
// no ForceDelete fails with ResourceInUse.
func TestASGDeleteWithInstancesNoForce(t *testing.T) {
	asc := newASGTestClient(t)
	ctx := context.Background()

	_, err := asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("busy-asg"),
		MinSize:              aws.Int32(1), MaxSize: aws.Int32(3), DesiredCapacity: aws.Int32(2),
		AvailabilityZones:       []string{"us-east-1a"},
		LaunchConfigurationName: aws.String("lc"),
	})
	require.NoError(t, err)

	_, err = asc.DeleteAutoScalingGroup(ctx, &autoscaling.DeleteAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("busy-asg"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "ResourceInUse", apiErr.ErrorCode())
}

// TestASGCreateNoLaunchSource verifies a create with no launch source is
// rejected with a ValidationError.
func TestASGCreateNoLaunchSource(t *testing.T) {
	asc := newASGTestClient(t)
	ctx := context.Background()

	_, err := asc.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("orphan-asg"),
		MinSize:              aws.Int32(1), MaxSize: aws.Int32(3), DesiredCapacity: aws.Int32(1),
		AvailabilityZones: []string{"us-east-1a"},
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "ValidationError", apiErr.ErrorCode())
}
