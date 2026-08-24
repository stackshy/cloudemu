package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"

	provecs "github.com/stackshy/cloudemu/v2/providers/aws/ecs"
)

// TestSDKServiceRoleAndCreatedBy guards that CreateService echoes the caller's
// role ARN as service.roleArn and records the creating principal as
// service.createdBy, both of which real ECS returns and DescribeServices must
// preserve.
func TestSDKServiceRoleAndCreatedBy(t *testing.T) {
	client, cloud := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	cloud.ECS.SeedContainerInstance("prod", "i-svc", provecs.WithCapacity(8192, 16384))
	registerNginx(t, client, ctx)

	const roleARN = "arn:aws:iam::123456789012:role/ecsServiceRole"

	created, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster:        aws.String("prod"),
		ServiceName:    aws.String("web-svc"),
		TaskDefinition: aws.String("web"),
		DesiredCount:   aws.Int32(1),
		Role:           aws.String(roleARN),
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if aws.ToString(created.Service.RoleArn) != roleARN {
		t.Fatalf("CreateService roleArn = %q, want %q", aws.ToString(created.Service.RoleArn), roleARN)
	}

	if aws.ToString(created.Service.CreatedBy) != "arn:aws:iam::123456789012:root" {
		t.Fatalf("CreateService createdBy = %q, want account-root principal", aws.ToString(created.Service.CreatedBy))
	}

	desc, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster: aws.String("prod"), Services: []string{"web-svc"},
	})
	if err != nil {
		t.Fatalf("DescribeServices: %v", err)
	}

	if len(desc.Services) != 1 {
		t.Fatalf("DescribeServices returned %d services, want 1", len(desc.Services))
	}

	if aws.ToString(desc.Services[0].RoleArn) != roleARN {
		t.Fatalf("DescribeServices roleArn = %q, want %q", aws.ToString(desc.Services[0].RoleArn), roleARN)
	}

	if aws.ToString(desc.Services[0].CreatedBy) != "arn:aws:iam::123456789012:root" {
		t.Fatalf("DescribeServices createdBy = %q, want account-root principal", aws.ToString(desc.Services[0].CreatedBy))
	}
}
