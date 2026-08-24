package ec2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newAutoScalingClient wires a full in-process AWS server and returns a real
// aws-sdk-go-v2 Auto Scaling client pointed at it, exercising the actual
// autoscaling Query wire protocol a real user hits.
func newAutoScalingClient(t *testing.T) *autoscaling.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return autoscaling.NewFromConfig(cfg)
}

func createASG(t *testing.T, c *autoscaling.Client, name string, minSize, maxSize, desired int32) {
	t.Helper()

	_, err := c.CreateAutoScalingGroup(context.Background(), &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName:    aws.String(name),
		MinSize:                 aws.Int32(minSize),
		MaxSize:                 aws.Int32(maxSize),
		DesiredCapacity:         aws.Int32(desired),
		LaunchConfigurationName: aws.String(name + "-lc"),
		AvailabilityZones:       []string{"us-east-1a"},
	})
	if err != nil {
		t.Fatalf("CreateAutoScalingGroup: %v", err)
	}
}

func describeASG(t *testing.T, c *autoscaling.Client, name string) (minSize, maxSize, desired int32) {
	t.Helper()

	out, err := c.DescribeAutoScalingGroups(context.Background(), &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{name},
	})
	if err != nil {
		t.Fatalf("DescribeAutoScalingGroups: %v", err)
	}

	if len(out.AutoScalingGroups) != 1 {
		t.Fatalf("want 1 group, got %d", len(out.AutoScalingGroups))
	}

	g := out.AutoScalingGroups[0]

	return aws.ToInt32(g.MinSize), aws.ToInt32(g.MaxSize), aws.ToInt32(g.DesiredCapacity)
}

// TestUpdateASGRaisesDesiredToNewMinSize pins the AWS rule: a new MinSize larger
// than the current size, supplied without a DesiredCapacity, raises the group's
// DesiredCapacity to the new MinSize.
// (docs.aws.amazon.com/autoscaling/ec2/APIReference/API_UpdateAutoScalingGroup.html)
func TestUpdateASGRaisesDesiredToNewMinSize(t *testing.T) {
	t.Parallel()

	c := newAutoScalingClient(t)
	createASG(t, c, "asg-min", 1, 5, 2)

	_, err := c.UpdateAutoScalingGroup(context.Background(), &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("asg-min"),
		MinSize:              aws.Int32(4),
	})
	if err != nil {
		t.Fatalf("UpdateAutoScalingGroup: %v", err)
	}

	minSize, maxSize, desired := describeASG(t, c, "asg-min")
	if minSize != 4 || maxSize != 5 || desired != 4 {
		t.Fatalf("after raising MinSize to 4: min=%d max=%d desired=%d, want 4/5/4", minSize, maxSize, desired)
	}
}

// TestUpdateASGLowersDesiredToNewMaxSize pins the AWS rule: a new MaxSize smaller
// than the current size, supplied without a DesiredCapacity, lowers the group's
// DesiredCapacity to the new MaxSize.
func TestUpdateASGLowersDesiredToNewMaxSize(t *testing.T) {
	t.Parallel()

	c := newAutoScalingClient(t)
	createASG(t, c, "asg-max", 1, 5, 4)

	_, err := c.UpdateAutoScalingGroup(context.Background(), &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("asg-max"),
		MaxSize:              aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("UpdateAutoScalingGroup: %v", err)
	}

	minSize, maxSize, desired := describeASG(t, c, "asg-max")
	if minSize != 1 || maxSize != 2 || desired != 2 {
		t.Fatalf("after lowering MaxSize to 2: min=%d max=%d desired=%d, want 1/2/2", minSize, maxSize, desired)
	}
}

// TestUpdateASGKeepsDesiredWithinNewBounds pins that a new MinSize that is not
// larger than the current size leaves DesiredCapacity unchanged: the group is
// not forced up to the new minimum when it already satisfies it.
func TestUpdateASGKeepsDesiredWithinNewBounds(t *testing.T) {
	t.Parallel()

	c := newAutoScalingClient(t)
	createASG(t, c, "asg-keep", 1, 5, 3)

	_, err := c.UpdateAutoScalingGroup(context.Background(), &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String("asg-keep"),
		MinSize:              aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("UpdateAutoScalingGroup: %v", err)
	}

	minSize, maxSize, desired := describeASG(t, c, "asg-keep")
	if minSize != 2 || maxSize != 5 || desired != 3 {
		t.Fatalf("after raising MinSize to 2: min=%d max=%d desired=%d, want 2/5/3", minSize, maxSize, desired)
	}
}
