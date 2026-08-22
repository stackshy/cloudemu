package container_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/container"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dtest"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestContainerECSRunTaskE2E runs the exact flow a real user runs against AWS ECS:
// register a task definition, RunTask it on FARGATE, poll DescribeTasks until the
// task STOPS, and read the container's logs from CloudWatch Logs — all against
// CloudEmu backed by a real Docker container (no cloud account). It proves the
// real container ran to completion (its exit code reaches the wire) and its real
// stdout was captured and surfaced through the awslogs driver.
func TestContainerECSRunTaskE2E(t *testing.T) {
	if !dtest.DockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := container.New()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAWS(config.WithContainerEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ecsClient := ecs.NewFromConfig(cfg, func(o *ecs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	logsClient := cloudwatchlogs.NewFromConfig(cfg, func(o *cloudwatchlogs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	const (
		marker        = "cloudemu-ecs-marker-99"
		containerName = "app"
		logGroup      = "/ecs/cloudemu"
		logPrefix     = "app"
	)

	// The awslogs driver ships to a group that real ECS requires to exist.
	if _, err = logsClient.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(logGroup),
	}); err != nil {
		t.Fatalf("CreateLogGroup: %v", err)
	}

	// 1. Register a Fargate task definition whose one container echoes the marker
	//    and ships its stdout to CloudWatch Logs via the awslogs driver.
	reg, err := ecsClient.RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String("cloudemu-e2e"),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:    aws.String(containerName),
			Image:   aws.String("alpine:3.20"),
			Command: []string{"/bin/sh", "-c", "echo " + marker},
			LogConfiguration: &ecstypes.LogConfiguration{
				LogDriver: ecstypes.LogDriverAwslogs,
				Options: map[string]string{
					"awslogs-group":         logGroup,
					"awslogs-stream-prefix": logPrefix,
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	tdARN := aws.ToString(reg.TaskDefinition.TaskDefinitionArn)

	// 2. RunTask on FARGATE — exactly like `aws ecs run-task`.
	run, err := ecsClient.RunTask(ctx, &ecs.RunTaskInput{
		TaskDefinition: aws.String(tdARN),
		LaunchType:     ecstypes.LaunchTypeFargate,
		Count:          aws.Int32(1),
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets: []string{"subnet-123"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(run.Tasks) != 1 || run.Tasks[0].TaskArn == nil {
		t.Fatalf("no task returned: %+v", run.Failures)
	}

	taskARN := aws.ToString(run.Tasks[0].TaskArn)
	taskID := taskARN[strings.LastIndex(taskARN, "/")+1:]

	// 3. Poll DescribeTasks until the task is STOPPED, then assert the container
	//    exited 0 — proving the real container ran to completion and its real exit
	//    code reached the wire. The ECS wire type serializes exitCode with
	//    `omitempty`, so a real exit code of 0 arrives as a nil ExitCode (ECS omits
	//    only the zero value); a STOPPED container with a nil exitCode and no
	//    failure reason therefore means the container exited 0.
	var stopped ecstypes.Container

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		desc, derr := ecsClient.DescribeTasks(ctx, &ecs.DescribeTasksInput{Tasks: []string{taskARN}})
		if derr != nil {
			t.Fatalf("DescribeTasks: %v", derr)
		}

		if len(desc.Tasks) != 1 {
			t.Fatalf("expected 1 task, got %d", len(desc.Tasks))
		}

		if aws.ToString(desc.Tasks[0].LastStatus) == "STOPPED" {
			if len(desc.Tasks[0].Containers) != 1 {
				t.Fatalf("stopped task missing container: %+v", desc.Tasks[0].Containers)
			}

			stopped = desc.Tasks[0].Containers[0]

			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if aws.ToString(stopped.LastStatus) != "STOPPED" {
		t.Fatalf("task did not reach STOPPED within the deadline")
	}

	if reason := aws.ToString(stopped.Reason); reason != "" {
		t.Fatalf("container reported a failure reason (did not exit cleanly): %q", reason)
	}

	if exit := aws.ToInt32(stopped.ExitCode); exit != 0 {
		t.Fatalf("container did not exit 0 (got %d)", exit)
	}

	// 4. The container's real stdout must have been surfaced to CloudWatch Logs on
	//    the awslogs "<prefix>/<container>/<taskId>" stream.
	stream := logPrefix + "/" + containerName + "/" + taskID

	events, err := logsClient.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(logGroup),
		LogStreamName: aws.String(stream),
	})
	if err != nil {
		t.Fatalf("GetLogEvents(%s): %v", stream, err)
	}

	var found bool

	for _, e := range events.Events {
		if strings.Contains(aws.ToString(e.Message), marker) {
			found = true

			break
		}
	}

	if !found {
		t.Fatalf("marker %q not found in log stream %q: %+v", marker, stream, events.Events)
	}

	// 5. StopTask (cleanup) — the real container is torn down and no leak remains.
	if _, err := ecsClient.StopTask(ctx, &ecs.StopTaskInput{Task: aws.String(taskARN)}); err != nil {
		t.Fatalf("StopTask: %v", err)
	}
}
