package ecs_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	provecs "github.com/stackshy/cloudemu/v2/providers/aws/ecs"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// errorsAs is a thin wrapper around errors.As to keep the typed-exception
// assertions readable.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}

// newECSServer builds an httptest-backed ECS client and returns it alongside the
// cloud provider so tests can seed EC2 container-instance capacity (there is no
// RegisterContainerInstance SDK op yet).
func newECSServer(t *testing.T) (*awsecs.Client, *awsprovider.Provider) {
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

	client := awsecs.NewFromConfig(cfg, func(o *awsecs.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, cloud
}

func newECSClient(t *testing.T) *awsecs.Client {
	t.Helper()

	client, _ := newECSServer(t)

	return client
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
	client, cloud := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	registerNginx(t, client, ctx)

	// Seed EC2 capacity so the default-launch-type tasks place onto an instance.
	cloud.ECS.SeedContainerInstance("prod", "i-0task")

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

	if aws.ToString(run.Tasks[0].ContainerInstanceArn) == "" {
		t.Fatalf("RunTask EC2 task has no containerInstanceArn: %+v", run.Tasks[0])
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
	client, cloud := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// Seed EC2 capacity so the (default EC2 launch-type) service converges.
	cloud.ECS.SeedContainerInstance("prod", "i-svc", provecs.WithCapacity(8192, 16384))

	registerNginx(t, client, ctx)

	created, err := client.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster:        aws.String("prod"),
		ServiceName:    aws.String("web-svc"),
		TaskDefinition: aws.String("web"),
		DesiredCount:   aws.Int32(3),
		LoadBalancers: []ecstypes.LoadBalancer{{
			TargetGroupArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg/abc"),
			ContainerName:  aws.String("nginx"), ContainerPort: aws.Int32(80),
		}},
		ServiceRegistries: []ecstypes.ServiceRegistry{{
			RegistryArn: aws.String("arn:aws:servicediscovery:us-east-1:123456789012:service/srv-1"),
		}},
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if created.Service.DesiredCount != 3 || created.Service.RunningCount != 3 {
		t.Fatalf("CreateService counts = desired %d running %d", created.Service.DesiredCount, created.Service.RunningCount)
	}

	// deployments[]/events[]/loadBalancers/serviceRegistries round-trip.
	if len(created.Service.Deployments) != 1 || created.Service.Deployments[0].RolloutState != ecstypes.DeploymentRolloutStateCompleted {
		t.Fatalf("CreateService deployments = %+v", created.Service.Deployments)
	}

	if len(created.Service.Events) == 0 {
		t.Fatal("CreateService returned no events")
	}

	if len(created.Service.LoadBalancers) != 1 || aws.ToString(created.Service.LoadBalancers[0].ContainerName) != "nginx" {
		t.Fatalf("CreateService loadBalancers = %+v", created.Service.LoadBalancers)
	}

	if len(created.Service.ServiceRegistries) != 1 {
		t.Fatalf("CreateService serviceRegistries = %+v", created.Service.ServiceRegistries)
	}

	// ListTasks(serviceName) returns the service's launched tasks.
	svcTasks, err := client.ListTasks(ctx, &awsecs.ListTasksInput{Cluster: aws.String("prod"), ServiceName: aws.String("web-svc")})
	if err != nil {
		t.Fatalf("ListTasks(serviceName): %v", err)
	}

	if len(svcTasks.TaskArns) != 3 {
		t.Fatalf("ListTasks(serviceName) = %d arns, want 3", len(svcTasks.TaskArns))
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

	// Deleting a service with a non-zero desired count requires force.
	if _, err := client.DeleteService(ctx, &awsecs.DeleteServiceInput{
		Cluster: aws.String("prod"),
		Service: aws.String("web-svc"),
	}); err == nil {
		t.Fatal("DeleteService without force = nil error, want InvalidParameterException")
	}

	del, err := client.DeleteService(ctx, &awsecs.DeleteServiceInput{
		Cluster: aws.String("prod"),
		Service: aws.String("web-svc"),
		Force:   aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DeleteService force: %v", err)
	}

	if aws.ToString(del.Service.Status) != "INACTIVE" {
		t.Fatalf("DeleteService status = %q, want INACTIVE", aws.ToString(del.Service.Status))
	}
}

func TestSDKTypedExceptions(t *testing.T) {
	client, cloud := newECSServer(t)
	ctx := context.Background()

	// A missing cluster surfaces ClusterNotFoundException.
	_, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster:        aws.String("ghost"),
		TaskDefinition: aws.String("web"),
	})

	var cnf *ecstypes.ClusterNotFoundException
	if !errorsAs(err, &cnf) {
		t.Fatalf("RunTask missing cluster err = %v, want ClusterNotFoundException", err)
	}

	// A missing service surfaces ServiceNotFoundException.
	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	_, err = client.DeleteService(ctx, &awsecs.DeleteServiceInput{
		Cluster: aws.String("prod"),
		Service: aws.String("ghost-svc"),
	})

	var snf *ecstypes.ServiceNotFoundException
	if !errorsAs(err, &snf) {
		t.Fatalf("DeleteService missing service err = %v, want ServiceNotFoundException", err)
	}

	// A non-empty cluster refuses deletion with ClusterContainsTasksException.
	// The running-task guard is checked before the container-instance guard, so
	// seeding capacity to place the task still surfaces ClusterContainsTasks.
	registerNginx(t, client, ctx)
	cloud.ECS.SeedContainerInstance("prod", "i-0typed")

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("web"),
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(run.Tasks) != 1 {
		t.Fatalf("RunTask = %d tasks, want 1 (failures %+v)", len(run.Tasks), run.Failures)
	}

	_, err = client.DeleteCluster(ctx, &awsecs.DeleteClusterInput{Cluster: aws.String("prod")})

	var cct *ecstypes.ClusterContainsTasksException
	if !errorsAs(err, &cct) {
		t.Fatalf("DeleteCluster non-empty err = %v, want ClusterContainsTasksException", err)
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

func TestSDKEC2Placement(t *testing.T) {
	client, cloud := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	registerNginx(t, client, ctx) // container-level cpu 256 / memory 512

	cloud.ECS.SeedContainerInstance("prod", "i-place", provecs.WithCapacity(1024, 2048))

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster:        aws.String("prod"),
		TaskDefinition: aws.String("web"),
		LaunchType:     ecstypes.LaunchTypeEc2,
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(run.Tasks) != 1 || aws.ToString(run.Tasks[0].ContainerInstanceArn) == "" {
		t.Fatalf("RunTask EC2 = %+v, want 1 task with containerInstanceArn", run.Tasks)
	}

	desc, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"), Tasks: []string{aws.ToString(run.Tasks[0].TaskArn)},
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	if len(desc.Tasks) != 1 || aws.ToString(desc.Tasks[0].ContainerInstanceArn) == "" {
		t.Fatalf("DescribeTasks EC2 = %+v, want containerInstanceArn set", desc.Tasks)
	}

	// The container instance reports decremented remaining resources on the wire.
	ci, err := client.DescribeContainerInstances(ctx, &awsecs.DescribeContainerInstancesInput{
		Cluster:            aws.String("prod"),
		ContainerInstances: []string{aws.ToString(run.Tasks[0].ContainerInstanceArn)},
	})
	if err != nil {
		t.Fatalf("DescribeContainerInstances: %v", err)
	}

	if len(ci.ContainerInstances) != 1 {
		t.Fatalf("DescribeContainerInstances = %d, want 1", len(ci.ContainerInstances))
	}

	if got := remainingResource(ci.ContainerInstances[0].RemainingResources, "MEMORY"); got != 2048-512 {
		t.Fatalf("remaining MEMORY = %d, want %d", got, 2048-512)
	}

	if got := remainingResource(ci.ContainerInstances[0].RemainingResources, "CPU"); got != 1024-256 {
		t.Fatalf("remaining CPU = %d, want %d", got, 1024-256)
	}
}

// remainingResource returns the integerValue of the named INTEGER resource.
func remainingResource(resources []ecstypes.Resource, name string) int32 {
	for i := range resources {
		if aws.ToString(resources[i].Name) == name {
			return resources[i].IntegerValue
		}
	}

	return -1
}

func TestSDKFargatePlacement(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("fg"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name: aws.String("app"), Image: aws.String("app:latest"),
		}},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster:        aws.String("prod"),
		TaskDefinition: aws.String("fg"),
		LaunchType:     ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{Subnets: []string{"subnet-abc"}},
		},
	})
	if err != nil {
		t.Fatalf("RunTask Fargate: %v", err)
	}

	if len(run.Tasks) != 1 {
		t.Fatalf("RunTask Fargate = %d tasks, want 1 (failures %+v)", len(run.Tasks), run.Failures)
	}

	desc, err := client.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"), Tasks: []string{aws.ToString(run.Tasks[0].TaskArn)},
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	task := desc.Tasks[0]
	if aws.ToString(task.PlatformVersion) != "1.4.0" {
		t.Fatalf("Fargate platformVersion = %q, want 1.4.0", aws.ToString(task.PlatformVersion))
	}

	if len(task.Attachments) != 1 || aws.ToString(task.Attachments[0].Type) != "ElasticNetworkInterface" {
		t.Fatalf("Fargate attachments = %+v, want one ElasticNetworkInterface", task.Attachments)
	}

	// A Fargate launch without networkConfiguration is a synchronous error.
	_, err = client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("fg"), LaunchType: ecstypes.LaunchTypeFargate,
	})

	var ipe *ecstypes.InvalidParameterException
	if !errorsAs(err, &ipe) {
		t.Fatalf("RunTask Fargate without network config err = %v, want InvalidParameterException", err)
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

// TestSDKTaskDefinitionRuntimeFields registers a task definition carrying the
// Wave 4a container/task runtime fields over the real ECS client and asserts
// DescribeTaskDefinition echoes them back through the SDK types.
func TestSDKTaskDefinitionRuntimeFields(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	_, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("web"),
		Cpu:    aws.String("256"),
		Memory: aws.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:  aws.String("app"),
			Image: aws.String("nginx:latest"),
			// Essential intentionally left unset to exercise the AWS default.
			PortMappings: []ecstypes.PortMapping{{
				ContainerPort: aws.Int32(8080),
				HostPort:      aws.Int32(8080),
				Protocol:      ecstypes.TransportProtocolTcp,
				Name:          aws.String("http"),
				AppProtocol:   ecstypes.ApplicationProtocolHttp,
			}},
			Environment: []ecstypes.KeyValuePair{{Name: aws.String("ENV"), Value: aws.String("prod")}},
			HealthCheck: &ecstypes.HealthCheck{
				Command:  []string{"CMD-SHELL", "curl -f http://localhost/ || exit 1"},
				Interval: aws.Int32(30), Timeout: aws.Int32(5), Retries: aws.Int32(3),
			},
			LogConfiguration: &ecstypes.LogConfiguration{
				LogDriver: ecstypes.LogDriverAwslogs,
				Options:   map[string]string{"awslogs-group": "/ecs/app"},
			},
			MountPoints: []ecstypes.MountPoint{{
				SourceVolume: aws.String("data"), ContainerPath: aws.String("/data"), ReadOnly: aws.Bool(true),
			}},
			Ulimits: []ecstypes.Ulimit{{Name: ecstypes.UlimitNameNofile, SoftLimit: 1024, HardLimit: 2048}},
			ResourceRequirements: []ecstypes.ResourceRequirement{{
				Value: aws.String("1"), Type: ecstypes.ResourceTypeGpu,
			}},
		}},
		Volumes: []ecstypes.Volume{{
			Name: aws.String("data"),
			Host: &ecstypes.HostVolumeProperties{SourcePath: aws.String("/mnt/data")},
		}},
		EphemeralStorage: &ecstypes.EphemeralStorage{SizeInGiB: 40},
		RuntimePlatform: &ecstypes.RuntimePlatform{
			CpuArchitecture:       ecstypes.CPUArchitectureArm64,
			OperatingSystemFamily: ecstypes.OSFamilyLinux,
		},
		ProxyConfiguration: &ecstypes.ProxyConfiguration{
			Type:          ecstypes.ProxyConfigurationTypeAppmesh,
			ContainerName: aws.String("envoy"),
			Properties:    []ecstypes.KeyValuePair{{Name: aws.String("IgnoredUID"), Value: aws.String("1337")}},
		},
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	desc, err := client.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String("web"),
	})
	if err != nil {
		t.Fatalf("DescribeTaskDefinition: %v", err)
	}

	td := desc.TaskDefinition
	if len(td.ContainerDefinitions) != 1 {
		t.Fatalf("containers = %d, want 1", len(td.ContainerDefinitions))
	}

	c := td.ContainerDefinitions[0]

	// Essential defaults to true when unset.
	if !aws.ToBool(c.Essential) {
		t.Fatalf("Essential = %v, want defaulted true", aws.ToBool(c.Essential))
	}

	if len(c.PortMappings) != 1 || aws.ToString(c.PortMappings[0].Name) != "http" ||
		c.PortMappings[0].AppProtocol != ecstypes.ApplicationProtocolHttp {
		t.Fatalf("PortMappings = %+v", c.PortMappings)
	}

	if len(c.Environment) != 1 || aws.ToString(c.Environment[0].Value) != "prod" {
		t.Fatalf("Environment = %+v", c.Environment)
	}

	if c.HealthCheck == nil || aws.ToInt32(c.HealthCheck.Retries) != 3 {
		t.Fatalf("HealthCheck = %+v", c.HealthCheck)
	}

	if c.LogConfiguration == nil || c.LogConfiguration.LogDriver != ecstypes.LogDriverAwslogs ||
		c.LogConfiguration.Options["awslogs-group"] != "/ecs/app" {
		t.Fatalf("LogConfiguration = %+v", c.LogConfiguration)
	}

	if len(c.MountPoints) != 1 || aws.ToString(c.MountPoints[0].ContainerPath) != "/data" {
		t.Fatalf("MountPoints = %+v", c.MountPoints)
	}

	if len(c.Ulimits) != 1 || c.Ulimits[0].Name != ecstypes.UlimitNameNofile {
		t.Fatalf("Ulimits = %+v", c.Ulimits)
	}

	if len(c.ResourceRequirements) != 1 || c.ResourceRequirements[0].Type != ecstypes.ResourceTypeGpu {
		t.Fatalf("ResourceRequirements = %+v", c.ResourceRequirements)
	}

	if len(td.Volumes) != 1 || td.Volumes[0].Host == nil ||
		aws.ToString(td.Volumes[0].Host.SourcePath) != "/mnt/data" {
		t.Fatalf("Volumes = %+v", td.Volumes)
	}

	if td.EphemeralStorage == nil || td.EphemeralStorage.SizeInGiB != 40 {
		t.Fatalf("EphemeralStorage = %+v", td.EphemeralStorage)
	}

	if td.RuntimePlatform == nil || td.RuntimePlatform.CpuArchitecture != ecstypes.CPUArchitectureArm64 ||
		td.RuntimePlatform.OperatingSystemFamily != ecstypes.OSFamilyLinux {
		t.Fatalf("RuntimePlatform = %+v", td.RuntimePlatform)
	}

	if td.ProxyConfiguration == nil || aws.ToString(td.ProxyConfiguration.ContainerName) != "envoy" ||
		len(td.ProxyConfiguration.Properties) != 1 {
		t.Fatalf("ProxyConfiguration = %+v", td.ProxyConfiguration)
	}
}

// TestSDKContainerEssentialDefault confirms essential defaults to true when
// omitted, and an explicit false is preserved.
func TestSDKContainerEssentialDefault(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	_, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("mix"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{Name: aws.String("primary"), Image: aws.String("img")},
			{Name: aws.String("sidecar"), Image: aws.String("img"), Essential: aws.Bool(false)},
		},
	})
	if err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	desc, err := client.DescribeTaskDefinition(ctx, &awsecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String("mix"),
	})
	if err != nil {
		t.Fatalf("DescribeTaskDefinition: %v", err)
	}

	defs := desc.TaskDefinition.ContainerDefinitions
	if len(defs) != 2 {
		t.Fatalf("containers = %d, want 2", len(defs))
	}

	if !aws.ToBool(defs[0].Essential) {
		t.Fatalf("primary Essential = %v, want defaulted true", aws.ToBool(defs[0].Essential))
	}

	if aws.ToBool(defs[1].Essential) {
		t.Fatalf("sidecar Essential = %v, want explicit false", aws.ToBool(defs[1].Essential))
	}
}

func TestSDKTagRoundtrip(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	created, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{
		ClusterName: aws.String("prod"),
		Tags:        []ecstypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(created.Cluster.ClusterArn)

	if _, err = client.TagResource(ctx, &awsecs.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []ecstypes.Tag{{Key: aws.String("team"), Value: aws.String("platform")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	list, err := client.ListTagsForResource(ctx, &awsecs.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(list.Tags) != 2 {
		t.Fatalf("ListTagsForResource = %d tags, want 2", len(list.Tags))
	}

	if _, err = client.UntagResource(ctx, &awsecs.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	list, err = client.ListTagsForResource(ctx, &awsecs.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource after untag: %v", err)
	}

	if len(list.Tags) != 1 || aws.ToString(list.Tags[0].Key) != "team" {
		t.Fatalf("ListTagsForResource after untag = %+v", list.Tags)
	}
}

func TestSDKAccountSettingsRoundtrip(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	put, err := client.PutAccountSetting(ctx, &awsecs.PutAccountSettingInput{
		Name:  ecstypes.SettingNameContainerInsights,
		Value: aws.String("enabled"),
	})
	if err != nil {
		t.Fatalf("PutAccountSetting: %v", err)
	}

	if aws.ToString(put.Setting.Value) != "enabled" {
		t.Fatalf("PutAccountSetting value = %q, want enabled", aws.ToString(put.Setting.Value))
	}

	list, err := client.ListAccountSettings(ctx, &awsecs.ListAccountSettingsInput{})
	if err != nil {
		t.Fatalf("ListAccountSettings: %v", err)
	}

	if len(list.Settings) != 1 || string(list.Settings[0].Name) != "containerInsights" {
		t.Fatalf("ListAccountSettings = %+v", list.Settings)
	}

	del, err := client.DeleteAccountSetting(ctx, &awsecs.DeleteAccountSettingInput{
		Name: ecstypes.SettingNameContainerInsights,
	})
	if err != nil {
		t.Fatalf("DeleteAccountSetting: %v", err)
	}

	if string(del.Setting.Name) != "containerInsights" {
		t.Fatalf("DeleteAccountSetting name = %q", string(del.Setting.Name))
	}

	list, err = client.ListAccountSettings(ctx, &awsecs.ListAccountSettingsInput{})
	if err != nil {
		t.Fatalf("ListAccountSettings after delete: %v", err)
	}

	if len(list.Settings) != 0 {
		t.Fatalf("ListAccountSettings after delete = %+v, want empty", list.Settings)
	}
}

func TestSDKContainerInstanceRoundtrip(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	reg, err := client.RegisterContainerInstance(ctx, &awsecs.RegisterContainerInstanceInput{
		Cluster:                  aws.String("prod"),
		InstanceIdentityDocument: aws.String(`{"instanceId":"i-0deadbeef"}`),
		TotalResources: []ecstypes.Resource{
			{Name: aws.String("CPU"), Type: aws.String("INTEGER"), IntegerValue: 4096},
			{Name: aws.String("MEMORY"), Type: aws.String("INTEGER"), IntegerValue: 8192},
		},
	})
	if err != nil {
		t.Fatalf("RegisterContainerInstance: %v", err)
	}

	if aws.ToString(reg.ContainerInstance.Ec2InstanceId) != "i-0deadbeef" {
		t.Fatalf("Ec2InstanceId = %q, want i-0deadbeef", aws.ToString(reg.ContainerInstance.Ec2InstanceId))
	}

	ciARN := aws.ToString(reg.ContainerInstance.ContainerInstanceArn)

	listCI, err := client.ListContainerInstances(ctx, &awsecs.ListContainerInstancesInput{Cluster: aws.String("prod")})
	if err != nil {
		t.Fatalf("ListContainerInstances: %v", err)
	}

	if len(listCI.ContainerInstanceArns) != 1 {
		t.Fatalf("ListContainerInstances = %v, want 1", listCI.ContainerInstanceArns)
	}

	descCI, err := client.DescribeContainerInstances(ctx, &awsecs.DescribeContainerInstancesInput{
		Cluster:            aws.String("prod"),
		ContainerInstances: []string{ciARN},
	})
	if err != nil {
		t.Fatalf("DescribeContainerInstances: %v", err)
	}

	if len(descCI.ContainerInstances) != 1 {
		t.Fatalf("DescribeContainerInstances = %d, want 1", len(descCI.ContainerInstances))
	}

	upd, err := client.UpdateContainerInstancesState(ctx, &awsecs.UpdateContainerInstancesStateInput{
		Cluster:            aws.String("prod"),
		ContainerInstances: []string{ciARN, "i-missing"},
		Status:             ecstypes.ContainerInstanceStatusDraining,
	})
	if err != nil {
		t.Fatalf("UpdateContainerInstancesState: %v", err)
	}

	if len(upd.ContainerInstances) != 1 || aws.ToString(upd.ContainerInstances[0].Status) != "DRAINING" {
		t.Fatalf("UpdateContainerInstancesState instances = %+v", upd.ContainerInstances)
	}

	if len(upd.Failures) != 1 {
		t.Fatalf("UpdateContainerInstancesState failures = %+v, want 1", upd.Failures)
	}

	dereg, err := client.DeregisterContainerInstance(ctx, &awsecs.DeregisterContainerInstanceInput{
		Cluster:           aws.String("prod"),
		ContainerInstance: aws.String(ciARN),
	})
	if err != nil {
		t.Fatalf("DeregisterContainerInstance: %v", err)
	}

	if aws.ToString(dereg.ContainerInstance.Status) != "INACTIVE" {
		t.Fatalf("Deregister status = %q, want INACTIVE", aws.ToString(dereg.ContainerInstance.Status))
	}

	listCI, err = client.ListContainerInstances(ctx, &awsecs.ListContainerInstancesInput{Cluster: aws.String("prod")})
	if err != nil {
		t.Fatalf("ListContainerInstances after deregister: %v", err)
	}

	if len(listCI.ContainerInstanceArns) != 0 {
		t.Fatalf("ListContainerInstances after deregister = %v, want empty", listCI.ContainerInstanceArns)
	}
}

func TestSDKUpdateClusterSettingsAndCapacityProviders(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	ucs, err := client.UpdateClusterSettings(ctx, &awsecs.UpdateClusterSettingsInput{
		Cluster:  aws.String("prod"),
		Settings: []ecstypes.ClusterSetting{{Name: ecstypes.ClusterSettingNameContainerInsights, Value: aws.String("enabled")}},
	})
	if err != nil {
		t.Fatalf("UpdateClusterSettings: %v", err)
	}

	if len(ucs.Cluster.Settings) != 1 || aws.ToString(ucs.Cluster.Settings[0].Value) != "enabled" {
		t.Fatalf("UpdateClusterSettings settings = %+v", ucs.Cluster.Settings)
	}

	pccp, err := client.PutClusterCapacityProviders(ctx, &awsecs.PutClusterCapacityProvidersInput{
		Cluster:           aws.String("prod"),
		CapacityProviders: []string{"FARGATE", "FARGATE_SPOT"},
		DefaultCapacityProviderStrategy: []ecstypes.CapacityProviderStrategyItem{
			{CapacityProvider: aws.String("FARGATE"), Base: 1, Weight: 1},
		},
	})
	if err != nil {
		t.Fatalf("PutClusterCapacityProviders: %v", err)
	}

	if len(pccp.Cluster.CapacityProviders) != 2 {
		t.Fatalf("CapacityProviders = %v, want 2", pccp.Cluster.CapacityProviders)
	}

	if len(pccp.Cluster.DefaultCapacityProviderStrategy) != 1 ||
		aws.ToString(pccp.Cluster.DefaultCapacityProviderStrategy[0].CapacityProvider) != "FARGATE" {
		t.Fatalf("DefaultCapacityProviderStrategy = %+v", pccp.Cluster.DefaultCapacityProviderStrategy)
	}
}

func TestSDKAttributesRoundtrip(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.PutAttributes(ctx, &awsecs.PutAttributesInput{
		Cluster: aws.String("prod"),
		Attributes: []ecstypes.Attribute{
			{Name: aws.String("stack"), Value: aws.String("prod"), TargetId: aws.String("i-1"), TargetType: ecstypes.TargetTypeContainerInstance},
			{Name: aws.String("stack"), Value: aws.String("dev"), TargetId: aws.String("i-2"), TargetType: ecstypes.TargetTypeContainerInstance},
		},
	}); err != nil {
		t.Fatalf("PutAttributes: %v", err)
	}

	list, err := client.ListAttributes(ctx, &awsecs.ListAttributesInput{
		Cluster:    aws.String("prod"),
		TargetType: ecstypes.TargetTypeContainerInstance,
	})
	if err != nil {
		t.Fatalf("ListAttributes: %v", err)
	}

	if len(list.Attributes) != 2 {
		t.Fatalf("ListAttributes = %d, want 2", len(list.Attributes))
	}

	if _, err = client.DeleteAttributes(ctx, &awsecs.DeleteAttributesInput{
		Cluster:    aws.String("prod"),
		Attributes: []ecstypes.Attribute{{Name: aws.String("stack"), TargetId: aws.String("i-1")}},
	}); err != nil {
		t.Fatalf("DeleteAttributes: %v", err)
	}

	list, err = client.ListAttributes(ctx, &awsecs.ListAttributesInput{
		Cluster:    aws.String("prod"),
		TargetType: ecstypes.TargetTypeContainerInstance,
	})
	if err != nil {
		t.Fatalf("ListAttributes after delete: %v", err)
	}

	if len(list.Attributes) != 1 || aws.ToString(list.Attributes[0].TargetId) != "i-2" {
		t.Fatalf("ListAttributes after delete = %+v", list.Attributes)
	}
}

func TestSDKListTaskDefinitionFamilies(t *testing.T) {
	client := newECSClient(t)
	ctx := context.Background()

	for _, fam := range []string{"web", "api", "web", "worker"} {
		if _, err := client.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
			Family: aws.String(fam),
			ContainerDefinitions: []ecstypes.ContainerDefinition{{
				Name: aws.String("c"), Image: aws.String("img"),
			}},
		}); err != nil {
			t.Fatalf("RegisterTaskDefinition %s: %v", fam, err)
		}
	}

	out, err := client.ListTaskDefinitionFamilies(ctx, &awsecs.ListTaskDefinitionFamiliesInput{})
	if err != nil {
		t.Fatalf("ListTaskDefinitionFamilies: %v", err)
	}

	want := []string{"api", "web", "worker"}
	if len(out.Families) != len(want) {
		t.Fatalf("Families = %v, want %v", out.Families, want)
	}

	for i := range want {
		if out.Families[i] != want[i] {
			t.Fatalf("Families = %v, want %v", out.Families, want)
		}
	}

	pref, err := client.ListTaskDefinitionFamilies(ctx, &awsecs.ListTaskDefinitionFamiliesInput{
		FamilyPrefix: aws.String("w"),
	})
	if err != nil {
		t.Fatalf("ListTaskDefinitionFamilies prefix: %v", err)
	}

	if len(pref.Families) != 2 {
		t.Fatalf("Families prefix = %v, want [web worker]", pref.Families)
	}
}

func TestSDKExecuteCommand(t *testing.T) {
	client, cloud := newECSServer(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	registerNginx(t, client, ctx)
	cloud.ECS.SeedContainerInstance("prod", "i-exec")

	run, err := client.RunTask(ctx, &awsecs.RunTaskInput{
		Cluster: aws.String("prod"), TaskDefinition: aws.String("web"), Count: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	taskARN := aws.ToString(run.Tasks[0].TaskArn)

	exec, err := client.ExecuteCommand(ctx, &awsecs.ExecuteCommandInput{
		Cluster:     aws.String("prod"),
		Task:        aws.String(taskARN),
		Command:     aws.String("/bin/sh"),
		Interactive: true,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}

	if aws.ToString(exec.TaskArn) != taskARN {
		t.Fatalf("ExecuteCommand TaskArn = %q, want %q", aws.ToString(exec.TaskArn), taskARN)
	}

	if exec.Session == nil || aws.ToString(exec.Session.SessionId) == "" || aws.ToString(exec.Session.StreamUrl) == "" {
		t.Fatalf("ExecuteCommand session = %+v", exec.Session)
	}

	// Unknown task surfaces a typed InvalidParameterException.
	_, err = client.ExecuteCommand(ctx, &awsecs.ExecuteCommandInput{
		Cluster: aws.String("prod"), Task: aws.String("t-missing"), Command: aws.String("ls"),
	})
	if err == nil {
		t.Fatalf("ExecuteCommand missing task: want error")
	}

	var ipe *ecstypes.InvalidParameterException
	if !errorsAs(err, &ipe) {
		t.Fatalf("ExecuteCommand missing task error = %T, want InvalidParameterException", err)
	}
}
