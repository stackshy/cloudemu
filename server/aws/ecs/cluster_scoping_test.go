package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// TestSDKStopTaskClusterScoping guards that StopTask honors its cluster
// argument: a task is scoped to the cluster that owns it, so stopping it against
// a nonexistent cluster is ClusterNotFoundException and stopping it against a
// different (existing) cluster is InvalidParameterException — matching the
// StopTask Errors section. The task must survive both rejected calls.
func TestSDKStopTaskClusterScoping(t *testing.T) {
	client, cloud := newECSServer(t)
	ctx := context.Background()

	for _, name := range []string{"prod", "staging"} {
		if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String(name)}); err != nil {
			t.Fatalf("CreateCluster(%s): %v", name, err)
		}
	}

	registerNginx(t, client, ctx)
	cloud.ECS.SeedContainerInstance("prod", "i-scope")

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster:        aws.String("prod"),
		TaskDefinition: aws.String("web"),
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(run.Tasks) != 1 {
		t.Fatalf("RunTask = %d tasks, want 1 (failures %+v)", len(run.Tasks), run.Failures)
	}

	taskARN := aws.ToString(run.Tasks[0].TaskArn)

	// A cluster that was never created is ClusterNotFoundException.
	_, err = client.StopTask(ctx, &awsecs.StopTaskInput{
		Cluster: aws.String("ghost"),
		Task:    aws.String(taskARN),
	})

	var cnf *ecstypes.ClusterNotFoundException
	if !errorsAs(err, &cnf) {
		t.Fatalf("StopTask(ghost cluster) err = %v, want ClusterNotFoundException", err)
	}

	// An existing cluster that does not own the task is InvalidParameterException.
	_, err = client.StopTask(ctx, &awsecs.StopTaskInput{
		Cluster: aws.String("staging"),
		Task:    aws.String(taskARN),
	})

	var ipe *ecstypes.InvalidParameterException
	if !errorsAs(err, &ipe) {
		t.Fatalf("StopTask(wrong cluster) err = %v, want InvalidParameterException", err)
	}

	// The task must be untouched by the two rejected calls.
	desc, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"),
		Tasks:   []string{taskARN},
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if len(desc.Tasks) != 1 || aws.ToString(desc.Tasks[0].LastStatus) != "RUNNING" {
		t.Fatalf("task after rejected StopTasks = %+v, want a single RUNNING task", desc.Tasks)
	}

	// The correct cluster stops it.
	stopped, err := client.StopTask(ctx, &awsecs.StopTaskInput{
		Cluster: aws.String("prod"),
		Task:    aws.String(taskARN),
		Reason:  aws.String("done"),
	})
	if err != nil {
		t.Fatalf("StopTask(prod): %v", err)
	}

	if aws.ToString(stopped.Task.LastStatus) != "STOPPED" {
		t.Fatalf("StopTask(prod) status = %q, want STOPPED", aws.ToString(stopped.Task.LastStatus))
	}
}

// TestSDKListServicesMissingCluster guards that ListServices against a
// nonexistent cluster returns ClusterNotFoundException rather than an empty
// list, as documented in the ListServices Errors section.
func TestSDKListServicesMissingCluster(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	_, err := client.ListServices(ctx, &awsecs.ListServicesInput{Cluster: aws.String("ghost")})

	var cnf *ecstypes.ClusterNotFoundException
	if !errorsAs(err, &cnf) {
		t.Fatalf("ListServices(ghost) err = %v, want ClusterNotFoundException", err)
	}
}

// TestSDKDescribeServicesMissingCluster guards that DescribeServices against a
// nonexistent cluster returns ClusterNotFoundException rather than an empty
// services list with a MISSING failure.
func TestSDKDescribeServicesMissingCluster(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	_, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String("ghost"),
		Services: []string{"svc"},
	})

	var cnf *ecstypes.ClusterNotFoundException
	if !errorsAs(err, &cnf) {
		t.Fatalf("DescribeServices(ghost) err = %v, want ClusterNotFoundException", err)
	}
}

// TestSDKListServicesEmptyClusterExists guards that ListServices against a real
// but empty cluster still succeeds with an empty list (the guard rejects only
// missing clusters, not empty ones).
func TestSDKListServicesEmptyClusterExists(t *testing.T) {
	client, _ := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.ListServices(ctx, &awsecs.ListServicesInput{Cluster: aws.String("prod")})
	if err != nil {
		t.Fatalf("ListServices(prod): %v", err)
	}

	if len(out.ServiceArns) != 0 {
		t.Fatalf("ListServices(empty prod) = %v, want no services", out.ServiceArns)
	}
}
