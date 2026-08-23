package ecs_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// TestSDKECSListClustersPagination guards maxResults/nextToken on ListClusters.
func TestSDKECSListClustersPagination(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	for _, n := range []string{"c1", "c2", "c3"} {
		if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{
			ClusterName: aws.String(n),
		}); err != nil {
			t.Fatalf("CreateCluster(%s): %v", n, err)
		}
	}

	first, err := client.ListClusters(ctx, &awsecs.ListClustersInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListClusters(page1): %v", err)
	}

	if len(first.ClusterArns) != 2 || aws.ToString(first.NextToken) == "" {
		t.Fatalf("page1: got %d arns, nextToken=%q", len(first.ClusterArns), aws.ToString(first.NextToken))
	}

	second, err := client.ListClusters(ctx, &awsecs.ListClustersInput{
		MaxResults: aws.Int32(2),
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListClusters(page2): %v", err)
	}

	if len(second.ClusterArns) != 1 || aws.ToString(second.NextToken) != "" {
		t.Fatalf("page2: got %d arns, nextToken=%q", len(second.ClusterArns), aws.ToString(second.NextToken))
	}
}

// TestSDKECSListTaskDefinitionsPagination guards maxResults/nextToken on
// ListTaskDefinitions.
func TestSDKECSListTaskDefinitionsPagination(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	for _, fam := range []string{"a", "b", "c"} {
		if _, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
			Family: aws.String(fam),
			ContainerDefinitions: []ecstypes.ContainerDefinition{{
				Name: aws.String("app"), Image: aws.String("busybox"), Essential: aws.Bool(true),
			}},
		}); err != nil {
			t.Fatalf("RegisterTaskDefinition(%s): %v", fam, err)
		}
	}

	first, err := client.ListTaskDefinitions(ctx, &awsecs.ListTaskDefinitionsInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListTaskDefinitions(page1): %v", err)
	}

	if len(first.TaskDefinitionArns) != 2 || aws.ToString(first.NextToken) == "" {
		t.Fatalf("page1: got %d arns, nextToken=%q", len(first.TaskDefinitionArns), aws.ToString(first.NextToken))
	}

	second, err := client.ListTaskDefinitions(ctx, &awsecs.ListTaskDefinitionsInput{
		MaxResults: aws.Int32(2),
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListTaskDefinitions(page2): %v", err)
	}

	if len(second.TaskDefinitionArns) != 1 {
		t.Fatalf("page2: got %d arns", len(second.TaskDefinitionArns))
	}
}

// TestSDKECSListServicesFilters guards launchType/schedulingStrategy filtering.
func TestSDKECSListServicesFilters(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("cl")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("web"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("app"), Image: aws.String("busybox"), Essential: aws.Bool(true),
		}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	if _, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family:                  aws.String("web-fargate"),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("app"), Image: aws.String("busybox"), Essential: aws.Bool(true),
		}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition(fargate): %v", err)
	}

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("cl"), ServiceName: aws.String("ec2-svc"),
		TaskDefinition: aws.String("web"), DesiredCount: aws.Int32(1),
		LaunchType: ecstypes.LaunchTypeEc2,
	}); err != nil {
		t.Fatalf("CreateService(ec2): %v", err)
	}

	if _, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster: aws.String("cl"), ServiceName: aws.String("fargate-svc"),
		TaskDefinition: aws.String("web-fargate"), DesiredCount: aws.Int32(1),
		LaunchType: ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
		},
	}); err != nil {
		t.Fatalf("CreateService(fargate): %v", err)
	}

	out, err := client.ListServices(ctx, &awsecs.ListServicesInput{
		Cluster:    aws.String("cl"),
		LaunchType: ecstypes.LaunchTypeFargate,
	})
	if err != nil {
		t.Fatalf("ListServices(filter): %v", err)
	}

	if len(out.ServiceArns) != 1 {
		t.Fatalf("launchType filter returned %d services, want 1: %+v", len(out.ServiceArns), out.ServiceArns)
	}
}

// TestSDKECSDescribeTasksMissingCluster guards that DescribeTasks against a
// nonexistent cluster returns ClusterNotFoundException.
func TestSDKECSDescribeTasksMissingCluster(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	_, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("ghost"),
		Tasks:   []string{"00000000000000000000000000000000"},
	})

	var notFound *ecstypes.ClusterNotFoundException
	if !errorsAs(err, &notFound) {
		t.Fatalf("DescribeTasks(ghost): want ClusterNotFoundException, got %v", err)
	}
}
