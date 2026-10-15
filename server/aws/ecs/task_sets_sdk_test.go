package ecs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestSDK_TaskSets_FullLifecycle(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("ext"), DesiredCount: aws.Int32(4),
		DeploymentController: &ecstypes.DeploymentController{Type: ecstypes.DeploymentControllerTypeExternal},
	}); err != nil {
		t.Fatalf("CreateService EXTERNAL: %v", err)
	}

	created, err := client.CreateTaskSet(ctx, &awsecs.CreateTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
		Scale: &ecstypes.Scale{Unit: ecstypes.ScaleUnitPercent, Value: 50},
		Tags:  []ecstypes.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}

	set := created.TaskSet
	if set.ComputedDesiredCount != 2 || set.RunningCount != 2 || aws.ToString(set.Status) != "ACTIVE" ||
		set.StabilityStatus != ecstypes.StabilityStatusSteadyState || aws.ToString(set.Id) == "" {
		t.Fatalf("unexpected task set: %+v", set)
	}

	svcs, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{Cluster: aws.String("prod"), Services: []string{"ext"}})
	if err != nil || len(svcs.Services) != 1 || svcs.Services[0].RunningCount != 2 || svcs.Services[0].PendingCount != 0 {
		t.Fatalf("EXTERNAL service counts must follow its task sets: %v %+v", err, svcs)
	}

	plain, err := client.DescribeTaskSets(ctx, &awsecs.DescribeTaskSetsInput{Cluster: aws.String("prod"), Service: aws.String("ext")})
	if err != nil || len(plain.TaskSets) != 1 || len(plain.TaskSets[0].Tags) != 0 {
		t.Fatalf("DescribeTaskSets must omit tags without include=TAGS: %v %+v", err, plain)
	}

	withTags, err := client.DescribeTaskSets(ctx, &awsecs.DescribeTaskSetsInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskSets: []string{aws.ToString(set.Id), "ecs-svc/1"},
		Include: []ecstypes.TaskSetField{ecstypes.TaskSetFieldTags},
	})
	if err != nil || len(withTags.TaskSets) != 1 || len(withTags.TaskSets[0].Tags) != 1 || len(withTags.Failures) != 1 ||
		aws.ToString(withTags.Failures[0].Reason) != "MISSING" {
		t.Fatalf("include=TAGS/failures: %v %+v", err, withTags)
	}

	if _, err := client.UpdateTaskSet(ctx, &awsecs.UpdateTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskSet: set.TaskSetArn,
		Scale: &ecstypes.Scale{Unit: ecstypes.ScaleUnitPercent, Value: 100},
	}); err != nil {
		t.Fatalf("UpdateTaskSet: %v", err)
	}

	prim, err := client.UpdateServicePrimaryTaskSet(ctx, &awsecs.UpdateServicePrimaryTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), PrimaryTaskSet: set.Id,
	})
	if err != nil || aws.ToString(prim.TaskSet.Status) != "PRIMARY" {
		t.Fatalf("UpdateServicePrimaryTaskSet: %v %+v", err, prim)
	}

	svc, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{Cluster: aws.String("prod"), Services: []string{"ext"}})
	if err != nil || len(svc.Services[0].TaskSets) != 1 || aws.ToString(svc.Services[0].TaskSets[0].Status) != "PRIMARY" {
		t.Fatalf("DescribeServices taskSets: %v", err)
	}

	del, err := client.DeleteTaskSet(ctx, &awsecs.DeleteTaskSetInput{Cluster: aws.String("prod"), Service: aws.String("ext"), TaskSet: set.Id})
	if err != nil || aws.ToString(del.TaskSet.Status) != "DRAINING" {
		t.Fatalf("DeleteTaskSet without force: %v %+v", err, del)
	}

	second, err := client.CreateTaskSet(ctx, &awsecs.CreateTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	})
	if err != nil {
		t.Fatalf("CreateTaskSet second: %v", err)
	}

	forced, err := client.DeleteTaskSet(ctx, &awsecs.DeleteTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskSet: second.TaskSet.Id, Force: aws.Bool(true),
	})
	if err != nil || aws.ToString(forced.TaskSet.Status) != "DRAINING" {
		t.Fatalf("DeleteTaskSet force: %v %+v", err, forced)
	}

	_, err = client.UpdateTaskSet(ctx, &awsecs.UpdateTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskSet: set.Id,
		Scale: &ecstypes.Scale{Unit: ecstypes.ScaleUnitPercent, Value: 1},
	})

	var notFound *ecstypes.TaskSetNotFoundException
	if !errorsAs(err, &notFound) {
		t.Fatalf("want *TaskSetNotFoundException, got %T: %v", err, err)
	}

	if _, err := client.DeleteService(ctx, &awsecs.DeleteServiceInput{Cluster: aws.String("prod"), Service: aws.String("ext"), Force: aws.Bool(true)}); err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateTaskSet(ctx, &awsecs.CreateTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	})

	var inactive *ecstypes.ServiceNotActiveException
	if !errorsAs(err, &inactive) {
		t.Fatalf("want *ServiceNotActiveException, got %T: %v", err, err)
	}
}

func TestSDK_TaskSets_RequireExternalController(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("rolling"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := client.CreateTaskSet(ctx, &awsecs.CreateTaskSetInput{
		Cluster: aws.String("prod"), Service: aws.String("rolling"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	})

	var invalid *ecstypes.InvalidParameterException
	if !errorsAs(err, &invalid) {
		t.Fatalf("want *InvalidParameterException, got %T: %v", err, err)
	}

	_, err = client.DescribeTaskSets(ctx, &awsecs.DescribeTaskSetsInput{Cluster: aws.String("prod"), Service: aws.String("ghost")})

	var noService *ecstypes.ServiceNotFoundException
	if !errorsAs(err, &noService) {
		t.Fatalf("want *ServiceNotFoundException, got %T: %v", err, err)
	}
}

func TestSDK_UpdateService_ExternalRejectsTaskDefinition(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	fargateSetup(t, client)

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("ext"), DesiredCount: aws.Int32(1),
		DeploymentController: &ecstypes.DeploymentController{Type: ecstypes.DeploymentControllerTypeExternal},
	}); err != nil {
		t.Fatalf("CreateService EXTERNAL: %v", err)
	}

	_, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), TaskDefinition: aws.String("fg"), DesiredCount: aws.Int32(3),
	})

	var invalid *ecstypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("UpdateService taskDefinition on EXTERNAL = %v, want InvalidParameterException", err)
	}

	got, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{Cluster: aws.String("prod"), Services: []string{"ext"}})
	if err != nil || got.Services[0].DesiredCount != 1 || aws.ToString(got.Services[0].TaskDefinition) != "" {
		t.Fatalf("a rejected update must change nothing: %v %+v", err, got)
	}

	if _, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster: aws.String("prod"), Service: aws.String("ext"), DesiredCount: aws.Int32(3),
	}); err != nil {
		t.Fatalf("UpdateService desiredCount on EXTERNAL: %v", err)
	}
}
