package ecs_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/stackshy/cloudemu/v2"
	cloudconfig "github.com/stackshy/cloudemu/v2/config"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newAsyncECS wires a full in-process AWS server with AsyncSettle enabled and a
// FakeClock the test controls, and returns a real aws-sdk-go-v2 ECS client
// alongside the clock and the provider (for seeding EC2 capacity). This
// exercises the actual wire protocol a real user hits.
func newAsyncECS(t *testing.T) (*awsecs.Client, *awsprovider.Provider, *cloudconfig.FakeClock) {
	t.Helper()

	fc := cloudconfig.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(cloudconfig.WithClock(fc), cloudconfig.WithAsyncSettle())
	srv := awsserver.New(awsserver.Drivers{ECS: cloud.ECS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awsecs.NewFromConfig(cfg), cloud, fc
}

// TestAsyncSettleWireECSTaskLifecycle pins that a real SDK client sees the
// PENDING->RUNNING and STOPPING->STOPPED task-lifecycle transients through the
// wire when AsyncSettle is on, driven by the FakeClock. This is the real-user
// counterpart to the provider-level settle tests.
func TestAsyncSettleWireECSTaskLifecycle(t *testing.T) {
	ctx := context.Background()
	client, cloud, fc := newAsyncECS(t)

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("web"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("nginx"), Image: aws.String("nginx:latest"), Essential: aws.Bool(true),
		}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	// Seed EC2 capacity so the default-launch-type task places onto an instance.
	cloud.ECS.SeedContainerInstance("prod", "i-0task")

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("web"),
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(run.Tasks) != 1 {
		t.Fatalf("RunTask = %d tasks, want 1", len(run.Tasks))
	}

	// The RunTask response reports the EC2 launch transient, as real ECS does:
	// the task has not yet reached RUNNING.
	if got := aws.ToString(run.Tasks[0].LastStatus); got != "PENDING" {
		t.Fatalf("RunTask task status = %q, want PENDING", got)
	}

	taskARN := aws.ToString(run.Tasks[0].TaskArn)

	desc, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{Cluster: aws.String("prod"), Tasks: []string{taskARN}})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if got := aws.ToString(desc.Tasks[0].LastStatus); got != "PENDING" {
		t.Fatalf("describe before settle = %q, want PENDING", got)
	}

	fc.Advance(3 * time.Second) // past DefaultECSTaskStartSettle (2s)

	desc, err = client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{Cluster: aws.String("prod"), Tasks: []string{taskARN}})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if got := aws.ToString(desc.Tasks[0].LastStatus); got != "RUNNING" {
		t.Fatalf("describe after settle = %q, want RUNNING", got)
	}

	stopped, err := client.StopTask(ctx, &awsecs.StopTaskInput{
		Cluster: aws.String("prod"), Task: aws.String(taskARN), Reason: aws.String("done"),
	})
	if err != nil {
		t.Fatalf("StopTask: %v", err)
	}

	// The StopTask response reports the stop transient: desiredStatus flips to
	// STOPPED synchronously but lastStatus lags until the container settles.
	if got := aws.ToString(stopped.Task.LastStatus); got != "STOPPING" {
		t.Fatalf("StopTask task status = %q, want STOPPING", got)
	}

	if got := aws.ToString(stopped.Task.DesiredStatus); got != "STOPPED" {
		t.Fatalf("StopTask desiredStatus = %q, want STOPPED", got)
	}

	fc.Advance(2 * time.Second) // past DefaultECSTaskStopSettle (1s)

	desc, err = client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{Cluster: aws.String("prod"), Tasks: []string{taskARN}})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if got := aws.ToString(desc.Tasks[0].LastStatus); got != "STOPPED" {
		t.Fatalf("describe after stop settle = %q, want STOPPED", got)
	}
}

// TestAsyncSettleWireECSFargateProvisioning pins the Fargate-specific launch
// transient: PROVISIONING (ENI attachment) rather than the EC2 PENDING.
func TestAsyncSettleWireECSFargateProvisioning(t *testing.T) {
	ctx := context.Background()
	client, _, fc := newAsyncECS(t)

	if _, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String("web"),
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("nginx"), Image: aws.String("nginx:latest"), Essential: aws.Bool(true),
		}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		TaskDefinition: aws.String("web"),
		LaunchType:     ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
		},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if got := aws.ToString(run.Tasks[0].LastStatus); got != "PROVISIONING" {
		t.Fatalf("Fargate RunTask task status = %q, want PROVISIONING", got)
	}

	taskARN := aws.ToString(run.Tasks[0].TaskArn)

	fc.Advance(3 * time.Second)

	desc, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{Tasks: []string{taskARN}})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if got := aws.ToString(desc.Tasks[0].LastStatus); got != "RUNNING" {
		t.Fatalf("describe after settle = %q, want RUNNING", got)
	}
}
