package ecs_test

import (
	"context"
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// liveTagsCase creates one ECS resource with create-time tags and returns its
// ARN, plus a describe func that reads its tags back with include=[TAGS].
type liveTagsCase struct {
	name     string
	create   func(t *testing.T, client *awsecs.Client, ctx context.Context, tags []ecstypes.Tag) string
	describe func(t *testing.T, client *awsecs.Client, ctx context.Context, arn string) []ecstypes.Tag
}

// TestSDKDescribeIncludeTagsReflectsTagWrites guards that every Describe* call
// with include=[TAGS] returns the live tag set: create-time tags, plus what
// TagResource added, minus what UntagResource removed. The describe paths used
// to serialise the entity's create-time Tags field while TagResource and
// UntagResource wrote only the separate ARN-keyed tag store, so describe and
// ListTagsForResource disagreed after the first tag write.
func TestSDKDescribeIncludeTagsReflectsTagWrites(t *testing.T) {
	for _, tc := range liveTagsCases() {
		t.Run(tc.name, func(t *testing.T) {
			client, cloud := newECSServer(t)
			ctx := context.Background()

			if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
				t.Fatalf("CreateCluster: %v", err)
			}

			registerNginx(t, client, ctx)
			cloud.ECS.SeedContainerInstance("prod", "i-0livetags")

			arn := tc.create(t, client, ctx, []ecstypes.Tag{
				{Key: aws.String("env"), Value: aws.String("prod")},
				{Key: aws.String("owner"), Value: aws.String("alice")},
			})

			if _, err := client.TagResource(ctx, &awsecs.TagResourceInput{
				ResourceArn: aws.String(arn),
				Tags:        []ecstypes.Tag{{Key: aws.String("team"), Value: aws.String("platform")}},
			}); err != nil {
				t.Fatalf("TagResource: %v", err)
			}

			if _, err := client.UntagResource(ctx, &awsecs.UntagResourceInput{
				ResourceArn: aws.String(arn),
				TagKeys:     []string{"owner"},
			}); err != nil {
				t.Fatalf("UntagResource: %v", err)
			}

			want := map[string]string{"env": "prod", "team": "platform"}

			if got := tagMap(tc.describe(t, client, ctx, arn)); !maps.Equal(got, want) {
				t.Fatalf("describe include=TAGS = %v, want %v", got, want)
			}

			list, err := client.ListTagsForResource(ctx, &awsecs.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
			if err != nil {
				t.Fatalf("ListTagsForResource: %v", err)
			}

			if got := tagMap(list.Tags); !maps.Equal(got, want) {
				t.Fatalf("ListTagsForResource = %v, want %v", got, want)
			}
		})
	}
}

func liveTagsCases() []liveTagsCase {
	return []liveTagsCase{
		{name: "service", create: createTaggedService, describe: describeServiceTags},
		{name: "cluster", create: createTaggedCluster, describe: describeClusterTags},
		{name: "task-definition", create: createTaggedTaskDef, describe: describeTaskDefTags},
		{name: "task", create: createTaggedTask, describe: describeTaskTags},
		{name: "container-instance", create: createTaggedInstance, describe: describeInstanceTags},
		{name: "capacity-provider", create: createTaggedCapacityProvider, describe: describeCapacityProviderTags},
	}
}

func createTaggedService(t *testing.T, client *awsecs.Client, ctx context.Context, tags []ecstypes.Tag) string {
	t.Helper()

	out, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster:        aws.String("prod"),
		ServiceName:    aws.String("web-svc"),
		TaskDefinition: aws.String("web"),
		DesiredCount:   aws.Int32(1),
		Tags:           tags,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	return aws.ToString(out.Service.ServiceArn)
}

func describeServiceTags(t *testing.T, client *awsecs.Client, ctx context.Context, _ string) []ecstypes.Tag {
	t.Helper()

	out, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String("prod"),
		Services: []string{"web-svc"},
		Include:  []ecstypes.ServiceField{ecstypes.ServiceFieldTags},
	})
	if err != nil {
		t.Fatalf("DescribeServices: %v", err)
	}

	if len(out.Services) != 1 {
		t.Fatalf("DescribeServices = %d services, want 1", len(out.Services))
	}

	return out.Services[0].Tags
}

func createTaggedCluster(t *testing.T, client *awsecs.Client, ctx context.Context, tags []ecstypes.Tag) string {
	t.Helper()

	out, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("tagged"), Tags: tags})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	return aws.ToString(out.Cluster.ClusterArn)
}

func describeClusterTags(t *testing.T, client *awsecs.Client, ctx context.Context, arn string) []ecstypes.Tag {
	t.Helper()

	out, err := client.DescribeClusters(ctx, &awsecs.DescribeClustersInput{
		Clusters: []string{arn},
		Include:  []ecstypes.ClusterField{ecstypes.ClusterFieldTags},
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(out.Clusters) != 1 {
		t.Fatalf("DescribeClusters = %d clusters, want 1", len(out.Clusters))
	}

	return out.Clusters[0].Tags
}

func createTaggedTaskDef(t *testing.T, client *awsecs.Client, ctx context.Context, tags []ecstypes.Tag) string {
	t.Helper()

	out, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("tagged"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("app"), Image: aws.String("nginx:latest"), Memory: aws.Int32(512),
		}},
		Tags: tags,
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	return aws.ToString(out.TaskDefinition.TaskDefinitionArn)
}

func describeTaskDefTags(t *testing.T, client *awsecs.Client, ctx context.Context, arn string) []ecstypes.Tag {
	t.Helper()

	out, err := client.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(arn),
		Include:        []ecstypes.TaskDefinitionField{ecstypes.TaskDefinitionFieldTags},
	})
	if err != nil {
		t.Fatalf("DescribeTaskDefinition: %v", err)
	}

	return out.Tags
}

func createTaggedTask(t *testing.T, client *awsecs.Client, ctx context.Context, tags []ecstypes.Tag) string {
	t.Helper()

	out, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster:        aws.String("prod"),
		TaskDefinition: aws.String("web"),
		Tags:           tags,
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(out.Tasks) != 1 {
		t.Fatalf("RunTask = %d tasks, want 1", len(out.Tasks))
	}

	return aws.ToString(out.Tasks[0].TaskArn)
}

func describeTaskTags(t *testing.T, client *awsecs.Client, ctx context.Context, arn string) []ecstypes.Tag {
	t.Helper()

	out, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"),
		Tasks:   []string{arn},
		Include: []ecstypes.TaskField{ecstypes.TaskFieldTags},
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if len(out.Tasks) != 1 {
		t.Fatalf("DescribeTasks = %d tasks, want 1", len(out.Tasks))
	}

	return out.Tasks[0].Tags
}

func tagMap(tags []ecstypes.Tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, tg := range tags {
		out[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	return out
}

func createTaggedInstance(t *testing.T, client *awsecs.Client, ctx context.Context, tags []ecstypes.Tag) string {
	t.Helper()

	out, err := client.RegisterContainerInstance(ctx, &awsecs.RegisterContainerInstanceInput{
		Cluster: aws.String("prod"),
		Tags:    tags,
	})
	if err != nil {
		t.Fatalf("RegisterContainerInstance: %v", err)
	}

	return aws.ToString(out.ContainerInstance.ContainerInstanceArn)
}

func describeInstanceTags(t *testing.T, client *awsecs.Client, ctx context.Context, arn string) []ecstypes.Tag {
	t.Helper()

	out, err := client.DescribeContainerInstances(ctx, &awsecs.DescribeContainerInstancesInput{
		Cluster:            aws.String("prod"),
		ContainerInstances: []string{arn},
		Include:            []ecstypes.ContainerInstanceField{ecstypes.ContainerInstanceFieldTags},
	})
	if err != nil {
		t.Fatalf("DescribeContainerInstances: %v", err)
	}

	if len(out.ContainerInstances) != 1 {
		t.Fatalf("DescribeContainerInstances = %d instances, want 1", len(out.ContainerInstances))
	}

	return out.ContainerInstances[0].Tags
}

func createTaggedCapacityProvider(t *testing.T, client *awsecs.Client, ctx context.Context, tags []ecstypes.Tag) string {
	t.Helper()

	out, err := client.CreateCapacityProvider(ctx, &awsecs.CreateCapacityProviderInput{
		Name: aws.String("asg-cp"),
		AutoScalingGroupProvider: &ecstypes.AutoScalingGroupProvider{
			AutoScalingGroupArn: aws.String("arn:aws:autoscaling:us-east-1:000000000000:autoScalingGroup:x:autoScalingGroupName/asg"),
		},
		Tags: tags,
	})
	if err != nil {
		t.Fatalf("CreateCapacityProvider: %v", err)
	}

	return aws.ToString(out.CapacityProvider.CapacityProviderArn)
}

func describeCapacityProviderTags(t *testing.T, client *awsecs.Client, ctx context.Context, arn string) []ecstypes.Tag {
	t.Helper()

	out, err := client.DescribeCapacityProviders(ctx, &awsecs.DescribeCapacityProvidersInput{
		CapacityProviders: []string{arn},
		Include:           []ecstypes.CapacityProviderField{ecstypes.CapacityProviderFieldTags},
	})
	if err != nil {
		t.Fatalf("DescribeCapacityProviders: %v", err)
	}

	if len(out.CapacityProviders) != 1 {
		t.Fatalf("DescribeCapacityProviders = %d providers, want 1", len(out.CapacityProviders))
	}

	return out.CapacityProviders[0].Tags
}
