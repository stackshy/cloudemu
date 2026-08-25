package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"

	provecs "github.com/stackshy/cloudemu/v2/providers/aws/ecs"
)

// TestSDKServiceTaskDefinitionNormalizedToARN drives the real Terraform-style flow:
// register a task definition (capturing the full ARN AWS returns), then create a
// service referencing it by the bare family. Real ECS normalizes the service's
// taskDefinition (and each deployment's taskDefinition) to the full ARN regardless
// of how the caller referenced it, which is why aws_ecs_service must diff-suppress
// family:revision against the ARN. CreateService, DescribeServices, and
// UpdateService must all echo that ARN.
func TestSDKServiceTaskDefinitionNormalizedToARN(t *testing.T) {
	client, cloud := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	cloud.ECS.SeedContainerInstance("prod", "i-svc", provecs.WithCapacity(8192, 16384))

	td := registerNginx(t, client, ctx)
	wantARN := aws.ToString(td.TaskDefinitionArn)

	// Create the service by bare family — AWS still echoes the full ARN.
	created, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster:        aws.String("prod"),
		ServiceName:    aws.String("web-svc"),
		TaskDefinition: aws.String("web"),
		DesiredCount:   aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if got := aws.ToString(created.Service.TaskDefinition); got != wantARN {
		t.Fatalf("CreateService taskDefinition = %q, want full ARN %q", got, wantARN)
	}

	if len(created.Service.Deployments) != 1 {
		t.Fatalf("CreateService deployments = %d, want 1", len(created.Service.Deployments))
	}

	if got := aws.ToString(created.Service.Deployments[0].TaskDefinition); got != wantARN {
		t.Fatalf("CreateService deployment taskDefinition = %q, want full ARN %q", got, wantARN)
	}

	// DescribeServices echoes the same ARN.
	desc, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster: aws.String("prod"), Services: []string{"web-svc"},
	})
	if err != nil {
		t.Fatalf("DescribeServices: %v", err)
	}

	if len(desc.Services) != 1 {
		t.Fatalf("DescribeServices returned %d services, want 1", len(desc.Services))
	}

	if got := aws.ToString(desc.Services[0].TaskDefinition); got != wantARN {
		t.Fatalf("DescribeServices taskDefinition = %q, want full ARN %q", got, wantARN)
	}

	// Register a second revision and update the service by family:revision short
	// form; the returned service (and its new deployment) must carry the full ARN.
	reg2, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family:               td.Family,
		ContainerDefinitions: td.ContainerDefinitions,
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition rev2: %v", err)
	}

	want2 := aws.ToString(reg2.TaskDefinition.TaskDefinitionArn)

	updated, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster:        aws.String("prod"),
		Service:        aws.String("web-svc"),
		TaskDefinition: aws.String("web:2"),
	})
	if err != nil {
		t.Fatalf("UpdateService: %v", err)
	}

	if got := aws.ToString(updated.Service.TaskDefinition); got != want2 {
		t.Fatalf("UpdateService taskDefinition = %q, want full ARN %q", got, want2)
	}

	if len(updated.Service.Deployments) != 1 ||
		aws.ToString(updated.Service.Deployments[0].TaskDefinition) != want2 {
		t.Fatalf("UpdateService deployment taskDefinition = %+v, want full ARN %q",
			updated.Service.Deployments, want2)
	}
}
