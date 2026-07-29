package ecs_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newECSClient(t *testing.T) *awsecs.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{ECS: cloud.ECS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsecs.NewFromConfig(cfg, func(o *awsecs.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func registerNginx(t *testing.T, client *awsecs.Client, ctx context.Context) *ecstypes.TaskDefinition {
	t.Helper()

	reg, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("web"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:      aws.String("nginx"),
			Image:     aws.String("nginx:latest"),
			Cpu:       256,
			Memory:    aws.Int32(512),
			Essential: aws.Bool(true),
		}},
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	return reg.TaskDefinition
}

func TestSDKClusterLifecycle(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	created, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{
		ClusterName: aws.String("prod"),
		Tags:        []ecstypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if aws.ToString(created.Cluster.ClusterName) != "prod" || aws.ToString(created.Cluster.Status) != "ACTIVE" {
		t.Fatalf("CreateCluster = %+v", created.Cluster)
	}

	list, err := client.ListClusters(ctx, &awsecs.ListClustersInput{})
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}

	if len(list.ClusterArns) != 1 {
		t.Fatalf("ListClusters = %v, want 1 arn", list.ClusterArns)
	}

	desc, err := client.DescribeClusters(ctx, &awsecs.DescribeClustersInput{Clusters: []string{"prod"}})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(desc.Clusters) != 1 || aws.ToString(desc.Clusters[0].ClusterName) != "prod" {
		t.Fatalf("DescribeClusters = %+v", desc.Clusters)
	}
}

func TestSDKDefaultClusterName(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	created, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if aws.ToString(created.Cluster.ClusterName) != "default" {
		t.Fatalf("CreateCluster empty name = %q, want default", aws.ToString(created.Cluster.ClusterName))
	}
}

func TestSDKTaskDefinitionLifecycle(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	td := registerNginx(t, client, ctx)
	if td.Revision != 1 || string(td.Status) != "ACTIVE" {
		t.Fatalf("RegisterTaskDefinition = revision %d status %q", td.Revision, string(td.Status))
	}

	// A second registration for the same family bumps the revision.
	td2 := registerNginx(t, client, ctx)
	if td2.Revision != 2 {
		t.Fatalf("second RegisterTaskDefinition revision = %d, want 2", td2.Revision)
	}

	// Describe by bare family resolves the latest ACTIVE revision.
	desc, err := client.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String("web"),
	})
	if err != nil {
		t.Fatalf("DescribeTaskDefinition: %v", err)
	}

	if desc.TaskDefinition.Revision != 2 {
		t.Fatalf("DescribeTaskDefinition(web) revision = %d, want 2", desc.TaskDefinition.Revision)
	}

	if len(desc.TaskDefinition.ContainerDefinitions) != 1 ||
		aws.ToString(desc.TaskDefinition.ContainerDefinitions[0].Image) != "nginx:latest" {
		t.Fatalf("DescribeTaskDefinition containers = %+v", desc.TaskDefinition.ContainerDefinitions)
	}

	list, err := client.ListTaskDefinitions(ctx, &awsecs.ListTaskDefinitionsInput{FamilyPrefix: aws.String("web")})
	if err != nil {
		t.Fatalf("ListTaskDefinitions: %v", err)
	}

	if len(list.TaskDefinitionArns) != 2 {
		t.Fatalf("ListTaskDefinitions = %v, want 2 arns", list.TaskDefinitionArns)
	}

	dereg, err := client.DeregisterTaskDefinition(ctx, &awsecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String("web:1"),
	})
	if err != nil {
		t.Fatalf("DeregisterTaskDefinition: %v", err)
	}

	if string(dereg.TaskDefinition.Status) != "INACTIVE" {
		t.Fatalf("DeregisterTaskDefinition status = %q, want INACTIVE", string(dereg.TaskDefinition.Status))
	}
}

func TestSDKTaskLifecycle(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	registerNginx(t, client, ctx)

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster:        aws.String("prod"),
		TaskDefinition: aws.String("web"),
		Count:          aws.Int32(2),
		StartedBy:      aws.String("tester"),
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(run.Tasks) != 2 {
		t.Fatalf("RunTask = %d tasks, want 2", len(run.Tasks))
	}

	if aws.ToString(run.Tasks[0].LastStatus) != "RUNNING" {
		t.Fatalf("RunTask task status = %q, want RUNNING", aws.ToString(run.Tasks[0].LastStatus))
	}

	listTasks, err := client.ListTasks(ctx, &awsecs.ListTasksInput{Cluster: aws.String("prod")})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	if len(listTasks.TaskArns) != 2 {
		t.Fatalf("ListTasks = %v, want 2 arns", listTasks.TaskArns)
	}

	taskARN := aws.ToString(run.Tasks[0].TaskArn)

	descTasks, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"),
		Tasks:   []string{taskARN},
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if len(descTasks.Tasks) != 1 || aws.ToString(descTasks.Tasks[0].TaskArn) != taskARN {
		t.Fatalf("DescribeTasks = %+v", descTasks.Tasks)
	}

	stopped, err := client.StopTask(ctx, &awsecs.StopTaskInput{
		Cluster: aws.String("prod"),
		Task:    aws.String(taskARN),
		Reason:  aws.String("done"),
	})
	if err != nil {
		t.Fatalf("StopTask: %v", err)
	}

	if aws.ToString(stopped.Task.LastStatus) != "STOPPED" || aws.ToString(stopped.Task.StoppedReason) != "done" {
		t.Fatalf("StopTask = %+v", stopped.Task)
	}
}

func TestSDKServiceLifecycle(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	registerNginx(t, client, ctx)

	created, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster:        aws.String("prod"),
		ServiceName:    aws.String("web-svc"),
		TaskDefinition: aws.String("web"),
		DesiredCount:   aws.Int32(3),
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if created.Service.DesiredCount != 3 || created.Service.RunningCount != 3 {
		t.Fatalf("CreateService counts = desired %d running %d", created.Service.DesiredCount, created.Service.RunningCount)
	}

	desc, err := client.DescribeServices(ctx, &awsecs.DescribeServicesInput{
		Cluster:  aws.String("prod"),
		Services: []string{"web-svc"},
	})
	if err != nil {
		t.Fatalf("DescribeServices: %v", err)
	}

	if len(desc.Services) != 1 || aws.ToString(desc.Services[0].ServiceName) != "web-svc" {
		t.Fatalf("DescribeServices = %+v", desc.Services)
	}

	upd, err := client.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster:      aws.String("prod"),
		Service:      aws.String("web-svc"),
		DesiredCount: aws.Int32(5),
	})
	if err != nil {
		t.Fatalf("UpdateService: %v", err)
	}

	if upd.Service.DesiredCount != 5 || upd.Service.RunningCount != 5 {
		t.Fatalf("UpdateService counts = desired %d running %d", upd.Service.DesiredCount, upd.Service.RunningCount)
	}

	list, err := client.ListServices(ctx, &awsecs.ListServicesInput{Cluster: aws.String("prod")})
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	if len(list.ServiceArns) != 1 {
		t.Fatalf("ListServices = %v, want 1 arn", list.ServiceArns)
	}

	del, err := client.DeleteService(ctx, &awsecs.DeleteServiceInput{
		Cluster: aws.String("prod"),
		Service: aws.String("web-svc"),
	})
	if err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if aws.ToString(del.Service.Status) != "INACTIVE" {
		t.Fatalf("DeleteService status = %q, want INACTIVE", aws.ToString(del.Service.Status))
	}
}

func TestSDKContainerInstances(t *testing.T) {
	cloud := cloudemu.NewAWS()
	cloud.ECS.SeedContainerInstance("prod", "i-0abc123")

	srv := awsserver.New(awsserver.Drivers{ECS: cloud.ECS})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awsecs.NewFromConfig(cfg, func(o *awsecs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	list, err := client.ListContainerInstances(ctx, &awsecs.ListContainerInstancesInput{Cluster: aws.String("prod")})
	if err != nil {
		t.Fatalf("ListContainerInstances: %v", err)
	}

	if len(list.ContainerInstanceArns) != 1 {
		t.Fatalf("ListContainerInstances = %v, want 1", list.ContainerInstanceArns)
	}

	desc, err := client.DescribeContainerInstances(ctx, &awsecs.DescribeContainerInstancesInput{
		Cluster:            aws.String("prod"),
		ContainerInstances: list.ContainerInstanceArns,
	})
	if err != nil {
		t.Fatalf("DescribeContainerInstances: %v", err)
	}

	if len(desc.ContainerInstances) != 1 || aws.ToString(desc.ContainerInstances[0].Ec2InstanceId) != "i-0abc123" {
		t.Fatalf("DescribeContainerInstances = %+v", desc.ContainerInstances)
	}

	if !desc.ContainerInstances[0].AgentConnected {
		t.Fatal("DescribeContainerInstances agentConnected = false, want true")
	}
}

func TestSDKPartialFailures(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// A bogus cluster id lands in failures, not an error.
	desc, err := client.DescribeClusters(ctx, &awsecs.DescribeClustersInput{
		Clusters: []string{"prod", "does-not-exist"},
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(desc.Clusters) != 1 {
		t.Fatalf("DescribeClusters resolved = %d, want 1", len(desc.Clusters))
	}

	if len(desc.Failures) != 1 || aws.ToString(desc.Failures[0].Reason) != "MISSING" {
		t.Fatalf("DescribeClusters failures = %+v, want one MISSING", desc.Failures)
	}

	// A bogus task id lands in failures too.
	descTasks, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"),
		Tasks:   []string{"bogus-task-id"},
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if len(descTasks.Tasks) != 0 || len(descTasks.Failures) != 1 {
		t.Fatalf("DescribeTasks = %d tasks %d failures, want 0/1", len(descTasks.Tasks), len(descTasks.Failures))
	}
}
