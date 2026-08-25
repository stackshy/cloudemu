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
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	provecs "github.com/stackshy/cloudemu/v2/providers/aws/ecs"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newECSAndELBServer wires both the ECS and ELBv2 wire handlers to the same
// cloud provider on one httptest server, so the cross-service target
// registration wired in providers/aws.New (ECS.SetTargetRegistrar(ELB)) is
// exercised end-to-end, exactly as it is in a real `cloudemu serve` process.
func newECSAndELBServer(t *testing.T) (*awsecs.Client, *elb.Client, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{ECS: cloud.ECS, ELB: cloud.ELB})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ecsClient := awsecs.NewFromConfig(cfg, func(o *awsecs.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	elbClient := elb.NewFromConfig(cfg, func(o *elb.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return ecsClient, elbClient, cloud
}

// TestSDKServiceRegistersTargetsWithELBv2 drives a real bridge-mode ECS
// service through the live wire server end-to-end: CreateTargetGroup ->
// CreateService(desiredCount=2, loadBalancers=[...]) must register both
// RUNNING tasks with the target group (DescribeTargetHealth shows 2 targets),
// DescribeTasks must report the dynamically assigned host port as a
// networkBinding, and scaling the service to 0 must deregister the targets.
func TestSDKServiceRegistersTargetsWithELBv2(t *testing.T) {
	ecsClient, elbClient, cloud := newECSAndELBServer(t)
	ctx := context.Background()

	if _, err := ecsClient.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("prod")}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// Bridge mode (the EC2 default) with a dynamic host port (hostPort unset),
	// so registration must resolve the ECS-assigned port, not a caller-supplied one.
	if _, err := ecsClient.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
		Family: aws.String("web"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:      aws.String("nginx"),
			Image:     aws.String("nginx:latest"),
			Cpu:       128,
			Memory:    aws.Int32(256),
			Essential: aws.Bool(true),
			PortMappings: []ecstypes.PortMapping{{
				ContainerPort: aws.Int32(80),
			}},
		}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	// Seed two instances, each with capacity for exactly one task, so the
	// service's first-fit placement spreads the two tasks across both instances
	// (rather than stacking them on one) and registration must be keyed per task.
	cloud.ECS.SeedContainerInstance("prod", "i-web1", provecs.WithCapacity(128, 256))
	cloud.ECS.SeedContainerInstance("prod", "i-web2", provecs.WithCapacity(128, 256))

	tg, err := elbClient.CreateTargetGroup(ctx, &elb.CreateTargetGroupInput{
		Name:       aws.String("web-tg"),
		Protocol:   elbtypes.ProtocolEnumHttp,
		Port:       aws.Int32(80),
		VpcId:      aws.String("vpc-123"),
		TargetType: elbtypes.TargetTypeEnumInstance,
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}

	tgARN := aws.ToString(tg.TargetGroups[0].TargetGroupArn)

	svc, err := ecsClient.CreateService(ctx, &awsecs.CreateServiceInput{
		Cluster:        aws.String("prod"),
		ServiceName:    aws.String("web-svc"),
		TaskDefinition: aws.String("web"),
		DesiredCount:   aws.Int32(2),
		LoadBalancers: []ecstypes.LoadBalancer{{
			TargetGroupArn: aws.String(tgARN),
			ContainerName:  aws.String("nginx"),
			ContainerPort:  aws.Int32(80),
		}},
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	if svc.Service.RunningCount != 2 {
		t.Fatalf("CreateService runningCount = %d, want 2", svc.Service.RunningCount)
	}

	// The HIGH-severity regression: DescribeTargetHealth must show both
	// converged tasks registered, not the empty set the bug reported.
	health, err := elbClient.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{TargetGroupArn: aws.String(tgARN)})
	if err != nil {
		t.Fatalf("DescribeTargetHealth: %v", err)
	}

	if len(health.TargetHealthDescriptions) != 2 {
		t.Fatalf("DescribeTargetHealth registered = %d, want 2: %+v", len(health.TargetHealthDescriptions), health.TargetHealthDescriptions)
	}

	registeredIDs := map[string]bool{}

	for _, th := range health.TargetHealthDescriptions {
		id := aws.ToString(th.Target.Id)
		registeredIDs[id] = true

		if id != "i-web1" && id != "i-web2" {
			t.Fatalf("registered target id = %q, want i-web1 or i-web2", id)
		}

		port := aws.ToInt32(th.Target.Port)
		if port < 32768 || port > 65535 {
			t.Fatalf("registered target port = %d, want a dynamic ephemeral port", port)
		}
	}

	if len(registeredIDs) != 2 {
		t.Fatalf("registered target ids = %v, want both container instances distinctly registered", registeredIDs)
	}

	// The MEDIUM-severity regression: DescribeTasks must report the same
	// dynamically assigned host port as a networkBinding.
	listTasks, err := ecsClient.ListTasks(ctx, &awsecs.ListTasksInput{
		Cluster: aws.String("prod"), ServiceName: aws.String("web-svc"),
	})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	if len(listTasks.TaskArns) != 2 {
		t.Fatalf("ListTasks = %d arns, want 2", len(listTasks.TaskArns))
	}

	descTasks, err := ecsClient.DescribeTasks(ctx, &awsecs.DescribeTasksInput{
		Cluster: aws.String("prod"), Tasks: listTasks.TaskArns,
	})
	if err != nil {
		t.Fatalf("DescribeTasks: %v", err)
	}

	for _, task := range descTasks.Tasks {
		if len(task.Containers) != 1 {
			t.Fatalf("task containers = %d, want 1: %+v", len(task.Containers), task.Containers)
		}

		bindings := task.Containers[0].NetworkBindings
		if len(bindings) != 1 {
			t.Fatalf("task networkBindings = %d, want 1: %+v", len(bindings), bindings)
		}

		nb := bindings[0]
		if aws.ToInt32(nb.ContainerPort) != 80 {
			t.Fatalf("networkBinding containerPort = %d, want 80", aws.ToInt32(nb.ContainerPort))
		}

		if aws.ToInt32(nb.HostPort) < 32768 || aws.ToInt32(nb.HostPort) > 65535 {
			t.Fatalf("networkBinding hostPort = %d, want a dynamic ephemeral port", aws.ToInt32(nb.HostPort))
		}

		if aws.ToString(nb.BindIP) != "0.0.0.0" {
			t.Fatalf("networkBinding bindIP = %q, want 0.0.0.0", aws.ToString(nb.BindIP))
		}

		if nb.Protocol != ecstypes.TransportProtocolTcp {
			t.Fatalf("networkBinding protocol = %q, want tcp", nb.Protocol)
		}
	}

	// Scaling the service to 0 must drain its tasks and deregister them.
	if _, err := ecsClient.UpdateService(ctx, &awsecs.UpdateServiceInput{
		Cluster: aws.String("prod"), Service: aws.String("web-svc"), DesiredCount: aws.Int32(0),
	}); err != nil {
		t.Fatalf("UpdateService scale to 0: %v", err)
	}

	healthAfter, err := elbClient.DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{TargetGroupArn: aws.String(tgARN)})
	if err != nil {
		t.Fatalf("DescribeTargetHealth after scale to 0: %v", err)
	}

	if len(healthAfter.TargetHealthDescriptions) != 0 {
		t.Fatalf("DescribeTargetHealth after scale to 0 = %d, want 0: %+v",
			len(healthAfter.TargetHealthDescriptions), healthAfter.TargetHealthDescriptions)
	}
}
