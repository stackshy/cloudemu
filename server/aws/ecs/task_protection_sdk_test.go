package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestSDK_TaskProtection(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("web"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg, DesiredCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	list, err := client.ListTasks(ctx, &awsecs.ListTasksInput{Cluster: aws.String("prod"), ServiceName: aws.String("web")})
	if err != nil || len(list.TaskArns) != 1 {
		t.Fatalf("ListTasks: %v", err)
	}

	upd, err := client.UpdateTaskProtection(ctx, &awsecs.UpdateTaskProtectionInput{
		Cluster: aws.String("prod"), Tasks: list.TaskArns, ProtectionEnabled: true, ExpiresInMinutes: aws.Int32(30),
	})
	if err != nil || len(upd.ProtectedTasks) != 1 || !upd.ProtectedTasks[0].ProtectionEnabled || upd.ProtectedTasks[0].ExpirationDate == nil {
		t.Fatalf("UpdateTaskProtection: %v %+v", err, upd)
	}

	get, err := client.GetTaskProtection(ctx, &awsecs.GetTaskProtectionInput{Cluster: aws.String("prod"), Tasks: list.TaskArns})
	if err != nil || len(get.ProtectedTasks) != 1 || !get.ProtectedTasks[0].ProtectionEnabled {
		t.Fatalf("GetTaskProtection: %v %+v", err, get)
	}

	if _, err = client.StopTask(ctx, &awsecs.StopTaskInput{Cluster: aws.String("prod"), Task: aws.String(list.TaskArns[0])}); err != nil {
		t.Fatalf("StopTask: %v", err)
	}

	stopped, err := client.UpdateTaskProtection(ctx, &awsecs.UpdateTaskProtectionInput{
		Cluster: aws.String("prod"), Tasks: list.TaskArns, ProtectionEnabled: true,
	})
	if err != nil || len(stopped.ProtectedTasks) != 0 || len(stopped.Failures) != 1 ||
		aws.ToString(stopped.Failures[0].Reason) != "TASK_NOT_VALID" {
		t.Fatalf("protecting a stopped task must be a TASK_NOT_VALID failure: %v %+v", err, stopped)
	}

	missing, err := client.GetTaskProtection(ctx, &awsecs.GetTaskProtectionInput{Cluster: aws.String("prod"), Tasks: []string{"nope"}})
	if err != nil || len(missing.Failures) != 1 || aws.ToString(missing.Failures[0].Reason) != "MISSING" {
		t.Fatalf("missing task must be a MISSING failure: %v %+v", err, missing)
	}

	_, err = client.GetTaskProtection(ctx, &awsecs.GetTaskProtectionInput{Cluster: aws.String("ghost"), Tasks: list.TaskArns})

	var notFound *ecstypes.ClusterNotFoundException
	if !errorsAs(err, &notFound) {
		t.Fatalf("want *ClusterNotFoundException, got %T: %v", err, err)
	}
}
