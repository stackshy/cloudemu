package batch_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsbatch "github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newBatchClient(t *testing.T) *awsbatch.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Batch: cloud.Batch})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsbatch.NewFromConfig(cfg, func(o *awsbatch.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createFargateCE(t *testing.T, c *awsbatch.Client, name string) batchtypes.ComputeResource {
	t.Helper()

	cr := batchtypes.ComputeResource{
		Type:             batchtypes.CRTypeFargate,
		MaxvCpus:         aws.Int32(16),
		Subnets:          []string{"subnet-1111", "subnet-2222"},
		SecurityGroupIds: []string{"sg-abcd"},
	}

	out, err := c.CreateComputeEnvironment(context.Background(), &awsbatch.CreateComputeEnvironmentInput{
		ComputeEnvironmentName: aws.String(name),
		Type:                   batchtypes.CETypeManaged,
		ComputeResources:       &cr,
		ServiceRole:            aws.String("arn:aws:iam::000000000000:role/AWSBatchServiceRole"),
	})
	if err != nil {
		t.Fatalf("CreateComputeEnvironment: %v", err)
	}

	if aws.ToString(out.ComputeEnvironmentName) != name || aws.ToString(out.ComputeEnvironmentArn) == "" {
		t.Fatalf("unexpected create response: %+v", out)
	}

	return cr
}

func TestSDKComputeEnvironmentLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	createFargateCE(t, c, "ce-app")

	// Describe must round-trip the compute resources, report VALID immediately,
	// and populate ecsClusterArn.
	desc, err := c.DescribeComputeEnvironments(ctx, &awsbatch.DescribeComputeEnvironmentsInput{
		ComputeEnvironments: []string{"ce-app"},
	})
	if err != nil {
		t.Fatalf("DescribeComputeEnvironments: %v", err)
	}

	if len(desc.ComputeEnvironments) != 1 {
		t.Fatalf("want 1 CE, got %d", len(desc.ComputeEnvironments))
	}

	ce := desc.ComputeEnvironments[0]
	if ce.Status != batchtypes.CEStatusValid {
		t.Fatalf("want status VALID immediately, got %q", ce.Status)
	}

	if aws.ToString(ce.EcsClusterArn) == "" {
		t.Fatalf("ecsClusterArn not populated: %+v", ce)
	}

	if ce.ComputeResources == nil || ce.ComputeResources.Type != batchtypes.CRTypeFargate ||
		aws.ToInt32(ce.ComputeResources.MaxvCpus) != 16 {
		t.Fatalf("computeResources did not round-trip: %+v", ce.ComputeResources)
	}

	if len(ce.ComputeResources.Subnets) != 2 || len(ce.ComputeResources.SecurityGroupIds) != 1 {
		t.Fatalf("nested slices did not round-trip: %+v", ce.ComputeResources)
	}

	// FARGATE must not gain injected EC2-only defaults (no drift).
	if len(ce.ComputeResources.InstanceTypes) != 0 || ce.ComputeResources.MinvCpus != nil {
		t.Fatalf("injected defaults on FARGATE compute resources: %+v", ce.ComputeResources)
	}

	// Update max_vcpus in place; the merge must preserve subnets/security groups.
	if _, err = c.UpdateComputeEnvironment(ctx, &awsbatch.UpdateComputeEnvironmentInput{
		ComputeEnvironment: aws.String("ce-app"),
		ComputeResources:   &batchtypes.ComputeResourceUpdate{MaxvCpus: aws.Int32(32)},
	}); err != nil {
		t.Fatalf("UpdateComputeEnvironment: %v", err)
	}

	desc, _ = c.DescribeComputeEnvironments(ctx, &awsbatch.DescribeComputeEnvironmentsInput{
		ComputeEnvironments: []string{"ce-app"},
	})
	ce = desc.ComputeEnvironments[0]

	if aws.ToInt32(ce.ComputeResources.MaxvCpus) != 32 {
		t.Fatalf("update did not apply maxvCpus: %+v", ce.ComputeResources)
	}

	if len(ce.ComputeResources.Subnets) != 2 {
		t.Fatalf("update dropped unchanged subnets: %+v", ce.ComputeResources)
	}
}

func TestSDKComputeEnvironmentDeleteRequiresDisabled(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	createFargateCE(t, c, "ce-del")

	// Deleting an ENABLED environment is rejected (matches real Batch).
	if _, err := c.DeleteComputeEnvironment(ctx, &awsbatch.DeleteComputeEnvironmentInput{
		ComputeEnvironment: aws.String("ce-del"),
	}); err == nil {
		t.Fatalf("expected delete of ENABLED CE to fail")
	}

	if _, err := c.UpdateComputeEnvironment(ctx, &awsbatch.UpdateComputeEnvironmentInput{
		ComputeEnvironment: aws.String("ce-del"),
		State:              batchtypes.CEStateDisabled,
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if _, err := c.DeleteComputeEnvironment(ctx, &awsbatch.DeleteComputeEnvironmentInput{
		ComputeEnvironment: aws.String("ce-del"),
	}); err != nil {
		t.Fatalf("delete after disable: %v", err)
	}
}

func TestSDKJobQueueLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	createFargateCE(t, c, "ce-q")

	created, err := c.CreateJobQueue(ctx, &awsbatch.CreateJobQueueInput{
		JobQueueName: aws.String("jq-app"),
		Priority:     aws.Int32(1),
		State:        batchtypes.JQStateEnabled,
		ComputeEnvironmentOrder: []batchtypes.ComputeEnvironmentOrder{
			{Order: aws.Int32(1), ComputeEnvironment: aws.String("ce-q")},
		},
	})
	if err != nil {
		t.Fatalf("CreateJobQueue: %v", err)
	}

	if aws.ToString(created.JobQueueArn) == "" {
		t.Fatalf("empty job queue arn")
	}

	desc, err := c.DescribeJobQueues(ctx, &awsbatch.DescribeJobQueuesInput{JobQueues: []string{"jq-app"}})
	if err != nil {
		t.Fatalf("DescribeJobQueues: %v", err)
	}

	q := desc.JobQueues[0]
	if q.Status != batchtypes.JQStatusValid {
		t.Fatalf("want status VALID immediately, got %q", q.Status)
	}

	if aws.ToInt32(q.Priority) != 1 || len(q.ComputeEnvironmentOrder) != 1 ||
		aws.ToInt32(q.ComputeEnvironmentOrder[0].Order) != 1 {
		t.Fatalf("job queue did not round-trip: %+v", q)
	}

	// Update priority in place.
	if _, err = c.UpdateJobQueue(ctx, &awsbatch.UpdateJobQueueInput{
		JobQueue: aws.String("jq-app"),
		Priority: aws.Int32(5),
	}); err != nil {
		t.Fatalf("UpdateJobQueue: %v", err)
	}

	desc, _ = c.DescribeJobQueues(ctx, &awsbatch.DescribeJobQueuesInput{JobQueues: []string{"jq-app"}})
	if aws.ToInt32(desc.JobQueues[0].Priority) != 5 {
		t.Fatalf("priority update not applied: %+v", desc.JobQueues[0])
	}

	// Deleting an ENABLED queue is rejected.
	if _, err = c.DeleteJobQueue(ctx, &awsbatch.DeleteJobQueueInput{JobQueue: aws.String("jq-app")}); err == nil {
		t.Fatalf("expected delete of ENABLED queue to fail")
	}

	if _, err = c.UpdateJobQueue(ctx, &awsbatch.UpdateJobQueueInput{
		JobQueue: aws.String("jq-app"),
		State:    batchtypes.JQStateDisabled,
	}); err != nil {
		t.Fatalf("disable queue: %v", err)
	}

	if _, err = c.DeleteJobQueue(ctx, &awsbatch.DeleteJobQueueInput{JobQueue: aws.String("jq-app")}); err != nil {
		t.Fatalf("delete after disable: %v", err)
	}
}

func TestSDKJobDefinitionRevisionIncrements(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)

	container := &batchtypes.ContainerProperties{
		Image:   aws.String("public.ecr.aws/amazonlinux/amazonlinux:latest"),
		Command: []string{"echo", "hello"},
		ResourceRequirements: []batchtypes.ResourceRequirement{
			{Type: batchtypes.ResourceTypeVcpu, Value: aws.String("0.25")},
			{Type: batchtypes.ResourceTypeMemory, Value: aws.String("512")},
		},
	}

	reg1, err := c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName:    aws.String("jd-app"),
		Type:                 batchtypes.JobDefinitionTypeContainer,
		ContainerProperties:  container,
		PlatformCapabilities: []batchtypes.PlatformCapability{batchtypes.PlatformCapabilityFargate},
	})
	if err != nil {
		t.Fatalf("RegisterJobDefinition #1: %v", err)
	}

	if aws.ToInt32(reg1.Revision) != 1 {
		t.Fatalf("first revision must be 1, got %d", aws.ToInt32(reg1.Revision))
	}

	reg2, err := c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName:    aws.String("jd-app"),
		Type:                 batchtypes.JobDefinitionTypeContainer,
		ContainerProperties:  container,
		PlatformCapabilities: []batchtypes.PlatformCapability{batchtypes.PlatformCapabilityFargate},
	})
	if err != nil {
		t.Fatalf("RegisterJobDefinition #2: %v", err)
	}

	if aws.ToInt32(reg2.Revision) != 2 {
		t.Fatalf("re-register must bump to revision 2, got %d", aws.ToInt32(reg2.Revision))
	}

	if aws.ToString(reg2.JobDefinitionArn) == aws.ToString(reg1.JobDefinitionArn) {
		t.Fatalf("revision 2 must get a new ARN")
	}

	// Describe by the exact revision ARN round-trips container properties, status
	// ACTIVE, and the FARGATE platform capability.
	desc, err := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{
		JobDefinitions: []string{aws.ToString(reg2.JobDefinitionArn)},
	})
	if err != nil {
		t.Fatalf("DescribeJobDefinitions: %v", err)
	}

	if len(desc.JobDefinitions) != 1 {
		t.Fatalf("want 1 revision, got %d", len(desc.JobDefinitions))
	}

	jd := desc.JobDefinitions[0]
	if aws.ToString(jd.Status) != "ACTIVE" || aws.ToInt32(jd.Revision) != 2 {
		t.Fatalf("job definition status/revision wrong: %+v", jd)
	}

	if jd.ContainerProperties == nil || aws.ToString(jd.ContainerProperties.Image) == "" {
		t.Fatalf("containerProperties did not round-trip: %+v", jd.ContainerProperties)
	}

	if len(jd.PlatformCapabilities) != 1 || jd.PlatformCapabilities[0] != batchtypes.PlatformCapabilityFargate {
		t.Fatalf("platformCapabilities did not round-trip: %+v", jd.PlatformCapabilities)
	}

	// Prior revision stays ACTIVE and describable by name (both revisions).
	all, _ := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{
		JobDefinitionName: aws.String("jd-app"),
	})
	if len(all.JobDefinitions) != 2 {
		t.Fatalf("want 2 ACTIVE revisions by name, got %d", len(all.JobDefinitions))
	}

	// Deregister revision 1 -> INACTIVE, so only revision 2 remains ACTIVE.
	if _, err = c.DeregisterJobDefinition(ctx, &awsbatch.DeregisterJobDefinitionInput{
		JobDefinition: aws.String(aws.ToString(reg1.JobDefinitionArn)),
	}); err != nil {
		t.Fatalf("DeregisterJobDefinition: %v", err)
	}

	active, _ := c.DescribeJobDefinitions(ctx, &awsbatch.DescribeJobDefinitionsInput{
		JobDefinitionName: aws.String("jd-app"),
	})
	if len(active.JobDefinitions) != 1 || aws.ToInt32(active.JobDefinitions[0].Revision) != 2 {
		t.Fatalf("after deregister want only revision 2 ACTIVE, got %+v", active.JobDefinitions)
	}
}
