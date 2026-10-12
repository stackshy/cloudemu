package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// fargateSetup creates cluster "prod" and the Fargate task definition "fg" and
// returns the awsvpc network configuration the launch calls need.
func fargateSetup(t *testing.T, client *awsecs.Client) *ecstypes.NetworkConfiguration {
	t.Helper()

	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	_, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String("fg"),
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("c"), Image: aws.String("img"), Essential: aws.Bool(true),
		}},
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	return &ecstypes.NetworkConfiguration{AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{Subnets: []string{"subnet-1"}}}
}

func TestSDK_AssignPublicIpDefault(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("web"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	got, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster: aws.String("prod"), Services: []string{"web"},
	})
	if err != nil || len(got.Services) != 1 {
		t.Fatalf("DescribeServices: %v %+v", err, got)
	}

	if v := got.Services[0].NetworkConfiguration.AwsvpcConfiguration.AssignPublicIp; v != ecstypes.AssignPublicIpDisabled {
		t.Fatalf("assignPublicIp = %q, want DISABLED", v)
	}
}

func TestSDK_RunTask_InvalidLaunchType(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	_, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchType("BOGUS"), NetworkConfiguration: netCfg,
	})
	if err == nil {
		t.Fatal("RunTask with launchType BOGUS must fail")
	}

	var invalid *ecstypes.InvalidParameterException
	if !errorsAs(err, &invalid) {
		t.Fatalf("want *InvalidParameterException, got %T: %v", err, err)
	}
}

func TestSDK_CreateService_Duplicate(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	in := &awsecs.CreateServiceInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("web"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	}

	if _, err := client.CreateService(ctx, in); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	_, err := client.CreateService(ctx, in)

	var invalid *ecstypes.InvalidParameterException
	if !errorsAs(err, &invalid) {
		t.Fatalf("want *InvalidParameterException, got %T: %v", err, err)
	}

	if got := aws.ToString(invalid.Message); got != "Creation of service was not idempotent." {
		t.Fatalf("message = %q", got)
	}
}

func TestSDK_CreateCluster_Idempotent(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	a, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")})
	if err != nil {
		t.Fatal(err)
	}

	b, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")})
	if err != nil {
		t.Fatalf("second CreateCluster must succeed: %v", err)
	}

	if aws.ToString(a.Cluster.ClusterArn) != aws.ToString(b.Cluster.ClusterArn) {
		t.Fatalf("ARN changed: %s vs %s", aws.ToString(a.Cluster.ClusterArn), aws.ToString(b.Cluster.ClusterArn))
	}
}

func TestSDK_StopTask_TwiceKeepsReason(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("fg"),
		LaunchType: ecstypes.LaunchTypeFargate, NetworkConfiguration: netCfg,
	})
	if err != nil || len(run.Tasks) != 1 {
		t.Fatalf("RunTask: %v", err)
	}

	arn := run.Tasks[0].TaskArn
	if _, err := client.StopTask(ctx, &awsecs.StopTaskInput{Cluster: aws.String("prod"), Task: arn, Reason: aws.String("first")}); err != nil {
		t.Fatal(err)
	}

	second, err := client.StopTask(ctx, &awsecs.StopTaskInput{Cluster: aws.String("prod"), Task: arn, Reason: aws.String("second")})
	if err != nil {
		t.Fatal(err)
	}

	if got := aws.ToString(second.Task.StoppedReason); got != "first" {
		t.Fatalf("stoppedReason = %q, want first", got)
	}
}

// Regression coverage for #1449 items that were already fixed on development.

func TestRegression_CapacityProviderCRUD(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	asg := &ecstypes.AutoScalingGroupProvider{
		AutoScalingGroupArn: aws.String("arn:aws:autoscaling:us-east-1:000000000000:autoScalingGroup:x:autoScalingGroupName/asg"),
	}

	created, err := client.CreateCapacityProvider(ctx, &awsecs.CreateCapacityProviderInput{
		Name: aws.String("cp1"), AutoScalingGroupProvider: asg,
	})
	if err != nil || aws.ToString(created.CapacityProvider.Name) != "cp1" {
		t.Fatalf("CreateCapacityProvider: %v", err)
	}

	desc, err := client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{CapacityProviders: []string{"cp1"}})
	if err != nil || len(desc.CapacityProviders) != 1 {
		t.Fatalf("DescribeCapacityProviders: %v", err)
	}

	if _, err := client.UpdateCapacityProvider(ctx, &awsecs.UpdateCapacityProviderInput{
		Name: aws.String("cp1"), AutoScalingGroupProvider: &ecstypes.AutoScalingGroupProviderUpdate{
			ManagedTerminationProtection: ecstypes.ManagedTerminationProtectionDisabled,
		},
	}); err != nil {
		t.Fatalf("UpdateCapacityProvider: %v", err)
	}

	if _, err := client.DeleteCapacityProvider(ctx, &awsecs.DeleteCapacityProviderInput{CapacityProvider: aws.String("cp1")}); err != nil {
		t.Fatalf("DeleteCapacityProvider: %v", err)
	}
}

func TestRegression_ContainerArnAndIncludeTags(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("fg"), LaunchType: ecstypes.LaunchTypeFargate,
		NetworkConfiguration: netCfg, Tags: []ecstypes.Tag{{Key: aws.String("a"), Value: aws.String("b")}},
	})
	if err != nil || len(run.Tasks) != 1 {
		t.Fatalf("RunTask: %v", err)
	}

	if aws.ToString(run.Tasks[0].Containers[0].ContainerArn) == "" {
		t.Fatal("containerArn missing on RunTask container")
	}

	plain, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{Cluster: aws.String("prod"), Tasks: []string{aws.ToString(run.Tasks[0].TaskArn)}})
	if err != nil || len(plain.Tasks) != 1 || len(plain.Tasks[0].Tags) != 0 {
		t.Fatalf("tags must be omitted without include=TAGS: %v", err)
	}

	withTags, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"), Tasks: []string{aws.ToString(run.Tasks[0].TaskArn)}, Include: []ecstypes.TaskField{ecstypes.TaskFieldTags},
	})
	if err != nil || len(withTags.Tasks[0].Tags) != 1 {
		t.Fatalf("include=TAGS must return tags: %v", err)
	}
}

func TestSDK_ExecuteCommand_Errors(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	off, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("fg"), LaunchType: ecstypes.LaunchTypeFargate,
		NetworkConfiguration: netCfg,
	})
	if err != nil || len(off.Tasks) != 1 {
		t.Fatalf("RunTask: %v", err)
	}

	_, err = client.ExecuteCommand(ctx, &awsecs.ExecuteCommandInput{
		Cluster: aws.String("prod"), Task: off.Tasks[0].TaskArn, Command: aws.String("ls"), Interactive: true,
	})

	var invalid *ecstypes.InvalidParameterException
	if !errorsAs(err, &invalid) {
		t.Fatalf("want *InvalidParameterException for a task without enableExecuteCommand, got %T: %v", err, err)
	}

	on, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("fg"), LaunchType: ecstypes.LaunchTypeFargate,
		NetworkConfiguration: netCfg, EnableExecuteCommand: true,
	})
	if err != nil || len(on.Tasks) != 1 || !on.Tasks[0].EnableExecuteCommand {
		t.Fatalf("RunTask with exec: %v", err)
	}

	if got := on.Tasks[0].Containers[0].ManagedAgents; len(got) != 1 || got[0].Name != ecstypes.ManagedAgentNameExecuteCommandAgent {
		t.Fatalf("managedAgents = %+v", got)
	}

	if _, err := client.ExecuteCommand(ctx, &awsecs.ExecuteCommandInput{
		Cluster: aws.String("prod"), Task: on.Tasks[0].TaskArn, Command: aws.String("ls"), Interactive: true,
	}); err != nil {
		t.Fatalf("ExecuteCommand on enabled running task: %v", err)
	}

	if _, err := client.StopTask(ctx, &awsecs.StopTaskInput{Cluster: aws.String("prod"), Task: on.Tasks[0].TaskArn}); err != nil {
		t.Fatal(err)
	}

	_, err = client.ExecuteCommand(ctx, &awsecs.ExecuteCommandInput{
		Cluster: aws.String("prod"), Task: on.Tasks[0].TaskArn, Command: aws.String("ls"), Interactive: true,
	})
	if !errorsAs(err, &invalid) {
		t.Fatalf("want *InvalidParameterException for a stopped task, got %T: %v", err, err)
	}
}

func TestSDK_DescribeTasks_NetworkInterfaces(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()
	netCfg := fargateSetup(t, client)

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("fg"), LaunchType: ecstypes.LaunchTypeFargate,
		NetworkConfiguration: netCfg,
	})
	if err != nil || len(run.Tasks) != 1 {
		t.Fatalf("RunTask: %v", err)
	}

	desc, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"), Tasks: []string{aws.ToString(run.Tasks[0].TaskArn)},
	})
	if err != nil || len(desc.Tasks) != 1 {
		t.Fatalf("DescribeTasks: %v", err)
	}

	task := desc.Tasks[0]
	if len(task.Attachments) != 1 || aws.ToString(task.Attachments[0].Id) == "" {
		t.Fatalf("attachment id missing: %+v", task.Attachments)
	}

	nis := task.Containers[0].NetworkInterfaces
	if len(nis) != 1 || aws.ToString(nis[0].AttachmentId) != aws.ToString(task.Attachments[0].Id) ||
		aws.ToString(nis[0].PrivateIpv4Address) == "" {
		t.Fatalf("networkInterfaces = %+v", nis)
	}
}
