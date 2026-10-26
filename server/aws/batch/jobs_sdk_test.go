package batch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsbatch "github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
)

// setupSDKQueue creates a Fargate CE, a queue "q" (optionally fair-share) and a
// container job definition "jd".
func setupSDKQueue(t *testing.T, c *awsbatch.Client, policyArn *string) {
	t.Helper()

	ctx := context.Background()
	createFargateCE(t, c, "ce")

	if _, err := c.CreateJobQueue(ctx, &awsbatch.CreateJobQueueInput{
		JobQueueName:        aws.String("q"),
		Priority:            aws.Int32(1),
		SchedulingPolicyArn: policyArn,
		ComputeEnvironmentOrder: []batchtypes.ComputeEnvironmentOrder{
			{Order: aws.Int32(1), ComputeEnvironment: aws.String("ce")},
		},
	}); err != nil {
		t.Fatalf("CreateJobQueue: %v", err)
	}

	if _, err := c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName: aws.String("jd"),
		Type:              batchtypes.JobDefinitionTypeContainer,
		ContainerProperties: &batchtypes.ContainerProperties{
			Image:   aws.String("busybox"),
			Command: []string{"echo", "hi"},
		},
	}); err != nil {
		t.Fatalf("RegisterJobDefinition: %v", err)
	}
}

func requireClientException(t *testing.T, err error, what string) {
	t.Helper()

	var ce *batchtypes.ClientException
	if !errors.As(err, &ce) {
		t.Fatalf("%s: want ClientException, got %v", what, err)
	}
}

func TestSDKJobLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	setupSDKQueue(t, c, nil)

	sub, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName:            aws.String("hello"),
		JobQueue:           aws.String("q"),
		JobDefinition:      aws.String("jd"),
		ContainerOverrides: &batchtypes.ContainerOverrides{Command: []string{"echo", "bye"}},
		Tags:               map[string]string{"team": "a"},
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	if aws.ToString(sub.JobId) == "" || aws.ToString(sub.JobArn) == "" || aws.ToString(sub.JobName) != "hello" {
		t.Fatalf("SubmitJob response: %+v", sub)
	}

	desc, err := c.DescribeJobs(ctx, &awsbatch.DescribeJobsInput{Jobs: []string{aws.ToString(sub.JobId), "unknown"}})
	if err != nil {
		t.Fatalf("DescribeJobs: %v", err)
	}

	if len(desc.Jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(desc.Jobs))
	}

	j := desc.Jobs[0]
	if j.Status != batchtypes.JobStatusSucceeded || j.Container == nil || aws.ToInt32(j.Container.ExitCode) != 0 ||
		j.Container.Command[1] != "bye" || aws.ToInt64(j.CreatedAt) == 0 || aws.ToInt64(j.StoppedAt) == 0 ||
		j.Tags["team"] != "a" {
		t.Fatalf("DescribeJobs detail: %+v", j)
	}

	list, err := c.ListJobs(ctx, &awsbatch.ListJobsInput{JobQueue: aws.String("q"), JobStatus: batchtypes.JobStatusSucceeded})
	if err != nil || len(list.JobSummaryList) != 1 || aws.ToString(list.JobSummaryList[0].JobId) != aws.ToString(sub.JobId) {
		t.Fatalf("ListJobs: %+v, %v", list, err)
	}

	// Default ListJobs lists RUNNING jobs only.
	running, err := c.ListJobs(ctx, &awsbatch.ListJobsInput{JobQueue: aws.String("q")})
	if err != nil || len(running.JobSummaryList) != 0 {
		t.Fatalf("default ListJobs: %+v, %v", running, err)
	}

	// Cancel/Terminate of a finished job succeed with no effect; unknown jobs fail.
	if _, err := c.TerminateJob(ctx, &awsbatch.TerminateJobInput{JobId: sub.JobId, Reason: aws.String("r")}); err != nil {
		t.Fatalf("TerminateJob: %v", err)
	}

	_, err = c.CancelJob(ctx, &awsbatch.CancelJobInput{JobId: aws.String("missing"), Reason: aws.String("r")})
	requireClientException(t, err, "CancelJob unknown job")

	_, err = c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("x"), JobQueue: aws.String("nope"), JobDefinition: aws.String("jd"),
	})
	requireClientException(t, err, "SubmitJob unknown queue")

	_, err = c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("x"), JobQueue: aws.String("q"), JobDefinition: aws.String("jd"),
		ArrayProperties: &batchtypes.ArrayProperties{Size: aws.Int32(1)},
	})
	requireClientException(t, err, "SubmitJob array size 1")
}

func TestSDKArrayJob(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	setupSDKQueue(t, c, nil)

	sub, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("arr"), JobQueue: aws.String("q"), JobDefinition: aws.String("jd"),
		ArrayProperties: &batchtypes.ArrayProperties{Size: aws.Int32(3)},
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	desc, err := c.DescribeJobs(ctx, &awsbatch.DescribeJobsInput{Jobs: []string{aws.ToString(sub.JobId), aws.ToString(sub.JobId) + ":2"}})
	if err != nil || len(desc.Jobs) != 2 {
		t.Fatalf("DescribeJobs: %+v, %v", desc, err)
	}

	parent, child := desc.Jobs[0], desc.Jobs[1]
	if aws.ToInt32(parent.ArrayProperties.Size) != 3 || parent.ArrayProperties.StatusSummary["SUCCEEDED"] != 3 {
		t.Fatalf("parent arrayProperties: %+v", parent.ArrayProperties)
	}

	if aws.ToInt32(child.ArrayProperties.Index) != 2 {
		t.Fatalf("child arrayProperties: %+v", child.ArrayProperties)
	}

	kids, err := c.ListJobs(ctx, &awsbatch.ListJobsInput{ArrayJobId: sub.JobId, JobStatus: batchtypes.JobStatusSucceeded})
	if err != nil || len(kids.JobSummaryList) != 3 {
		t.Fatalf("ListJobs children: %+v, %v", kids, err)
	}
}

func TestSDKSchedulingPolicies(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)

	created, err := c.CreateSchedulingPolicy(ctx, &awsbatch.CreateSchedulingPolicyInput{
		Name: aws.String("fair"),
		FairsharePolicy: &batchtypes.FairsharePolicy{
			ShareDecaySeconds:  aws.Int32(3600),
			ComputeReservation: aws.Int32(1),
			ShareDistribution: []batchtypes.ShareAttributes{
				{ShareIdentifier: aws.String("A*"), WeightFactor: aws.Float32(0.5)},
			},
		},
		Tags: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("CreateSchedulingPolicy: %v", err)
	}

	setupSDKQueue(t, c, created.Arn)

	desc, err := c.DescribeSchedulingPolicies(ctx, &awsbatch.DescribeSchedulingPoliciesInput{Arns: []string{aws.ToString(created.Arn)}})
	if err != nil || len(desc.SchedulingPolicies) != 1 ||
		aws.ToInt32(desc.SchedulingPolicies[0].FairsharePolicy.ShareDecaySeconds) != 3600 ||
		desc.SchedulingPolicies[0].Tags["k"] != "v" {
		t.Fatalf("DescribeSchedulingPolicies: %+v, %v", desc, err)
	}

	if _, err := c.UpdateSchedulingPolicy(ctx, &awsbatch.UpdateSchedulingPolicyInput{
		Arn: created.Arn, FairsharePolicy: &batchtypes.FairsharePolicy{ShareDecaySeconds: aws.Int32(60)},
	}); err != nil {
		t.Fatalf("UpdateSchedulingPolicy: %v", err)
	}

	list, err := c.ListSchedulingPolicies(ctx, &awsbatch.ListSchedulingPoliciesInput{})
	if err != nil || len(list.SchedulingPolicies) != 1 || aws.ToString(list.SchedulingPolicies[0].Arn) != aws.ToString(created.Arn) {
		t.Fatalf("ListSchedulingPolicies: %+v, %v", list, err)
	}

	// Fair-share queue: shareIdentifier is required.
	_, err = c.SubmitJob(ctx, &awsbatch.SubmitJobInput{JobName: aws.String("j"), JobQueue: aws.String("q"), JobDefinition: aws.String("jd")})
	requireClientException(t, err, "SubmitJob without shareIdentifier")

	if _, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("j"), JobQueue: aws.String("q"), JobDefinition: aws.String("jd"), ShareIdentifier: aws.String("A1"),
	}); err != nil {
		t.Fatalf("SubmitJob with shareIdentifier: %v", err)
	}

	// In-use policy can't be deleted.
	_, err = c.DeleteSchedulingPolicy(ctx, &awsbatch.DeleteSchedulingPolicyInput{Arn: created.Arn})
	requireClientException(t, err, "DeleteSchedulingPolicy in use")

	// A queue can't reference a missing policy.
	_, err = c.CreateJobQueue(ctx, &awsbatch.CreateJobQueueInput{
		JobQueueName:        aws.String("q2"),
		Priority:            aws.Int32(1),
		SchedulingPolicyArn: aws.String("arn:aws:batch:us-east-1:000000000000:scheduling-policy/none"),
		ComputeEnvironmentOrder: []batchtypes.ComputeEnvironmentOrder{
			{Order: aws.Int32(1), ComputeEnvironment: aws.String("ce")},
		},
	})
	requireClientException(t, err, "CreateJobQueue unknown policy")

	// Tags on the policy ARN.
	tags, err := c.ListTagsForResource(ctx, &awsbatch.ListTagsForResourceInput{ResourceArn: created.Arn})
	if err != nil || tags.Tags["k"] != "v" {
		t.Fatalf("ListTagsForResource(policy): %+v, %v", tags, err)
	}
}

func TestSDKRegisterJobDefinitionInvalidType(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)

	_, err := c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName:   aws.String("bad"),
		Type:                batchtypes.JobDefinitionType("batch"),
		ContainerProperties: &batchtypes.ContainerProperties{Image: aws.String("busybox")},
	})
	requireClientException(t, err, "RegisterJobDefinition invalid type")

	_, err = c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName: aws.String("mn"),
		Type:              batchtypes.JobDefinitionTypeMultinode,
	})
	requireClientException(t, err, "RegisterJobDefinition multinode without nodeProperties")
}

// TestSDKSubmitJobEcsPropertiesOverride checks an ECS override round-trips
// through the wire layer into DescribeJobs instead of being dropped.
func TestSDKSubmitJobEcsPropertiesOverride(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	setupSDKQueue(t, c, nil)

	if _, err := c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName: aws.String("ecs"),
		Type:              batchtypes.JobDefinitionTypeContainer,
		EcsProperties: &batchtypes.EcsProperties{TaskProperties: []batchtypes.EcsTaskProperties{{
			Containers: []batchtypes.TaskContainerProperties{{Name: aws.String("app"), Image: aws.String("app:1"), Command: []string{"run"}}},
		}}},
	}); err != nil {
		t.Fatalf("RegisterJobDefinition: %v", err)
	}

	sub, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("ecs"), JobQueue: aws.String("q"), JobDefinition: aws.String("ecs"),
		EcsPropertiesOverride: &batchtypes.EcsPropertiesOverride{TaskProperties: []batchtypes.TaskPropertiesOverride{{
			Containers: []batchtypes.TaskContainerOverrides{{Name: aws.String("app"), Command: []string{"run", "--fast"}}},
		}}},
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	desc, err := c.DescribeJobs(ctx, &awsbatch.DescribeJobsInput{Jobs: []string{aws.ToString(sub.JobId)}})
	if err != nil || len(desc.Jobs) != 1 {
		t.Fatalf("DescribeJobs: %v %v", desc, err)
	}

	ep := desc.Jobs[0].EcsProperties
	if ep == nil || len(ep.TaskProperties) != 1 || len(ep.TaskProperties[0].Containers) != 1 {
		t.Fatalf("ecsProperties missing: %+v", ep)
	}

	if got := ep.TaskProperties[0].Containers[0].Command; len(got) != 2 || got[1] != "--fast" {
		t.Fatalf("override not applied: %v", got)
	}

	// An ECS override on a containerProperties definition is a ClientException.
	_, err = c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("bad"), JobQueue: aws.String("q"), JobDefinition: aws.String("jd"),
		EcsPropertiesOverride: &batchtypes.EcsPropertiesOverride{TaskProperties: []batchtypes.TaskPropertiesOverride{{
			Containers: []batchtypes.TaskContainerOverrides{{Command: []string{"x"}}},
		}}},
	})
	requireClientException(t, err, "ecs override on containerProperties definition")
}

// setupSDKMultiNode creates an EC2 queue "q-ec2" and a multinode job definition
// "mnp" (two nodes, main node 0) next to setupSDKQueue's Fargate queue "q".
func setupSDKMultiNode(t *testing.T, c *awsbatch.Client) {
	t.Helper()

	ctx := context.Background()

	if _, err := c.CreateComputeEnvironment(ctx, &awsbatch.CreateComputeEnvironmentInput{
		ComputeEnvironmentName: aws.String("ce-ec2"),
		Type:                   batchtypes.CETypeManaged,
		ComputeResources: &batchtypes.ComputeResource{
			Type: batchtypes.CRTypeEc2, MaxvCpus: aws.Int32(16), InstanceTypes: []string{"optimal"},
			Subnets: []string{"subnet-1111"}, SecurityGroupIds: []string{"sg-abcd"},
		},
	}); err != nil {
		t.Fatalf("CreateComputeEnvironment EC2: %v", err)
	}

	if _, err := c.CreateJobQueue(ctx, &awsbatch.CreateJobQueueInput{
		JobQueueName: aws.String("q-ec2"), Priority: aws.Int32(1),
		ComputeEnvironmentOrder: []batchtypes.ComputeEnvironmentOrder{
			{Order: aws.Int32(1), ComputeEnvironment: aws.String("ce-ec2")},
		},
	}); err != nil {
		t.Fatalf("CreateJobQueue EC2: %v", err)
	}

	if _, err := c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName: aws.String("mnp"),
		Type:              batchtypes.JobDefinitionTypeMultinode,
		NodeProperties: &batchtypes.NodeProperties{
			NumNodes: aws.Int32(2), MainNode: aws.Int32(0),
			NodeRangeProperties: []batchtypes.NodeRangeProperty{
				{TargetNodes: aws.String("0:"), Container: &batchtypes.ContainerProperties{Image: aws.String("mpi")}},
			},
		},
	}); err != nil {
		t.Fatalf("RegisterJobDefinition multinode: %v", err)
	}
}

func TestSDKMultiNodeJobNodes(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	setupSDKQueue(t, c, nil)
	setupSDKMultiNode(t, c)

	sub, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("mpi"), JobQueue: aws.String("q-ec2"), JobDefinition: aws.String("mnp"),
		NodeOverrides: &batchtypes.NodeOverrides{NumNodes: aws.Int32(3)},
	})
	if err != nil {
		t.Fatalf("SubmitJob multinode: %v", err)
	}

	id := aws.ToString(sub.JobId)

	desc, err := c.DescribeJobs(ctx, &awsbatch.DescribeJobsInput{Jobs: []string{id, id + "#0", id + "#2", id + "#3"}})
	if err != nil || len(desc.Jobs) != 3 {
		t.Fatalf("DescribeJobs: %+v, %v", desc, err)
	}

	parent, main, last := desc.Jobs[0], desc.Jobs[1], desc.Jobs[2]
	if parent.NodeDetails != nil || parent.NodeProperties == nil || aws.ToInt32(parent.NodeProperties.NumNodes) != 3 {
		t.Fatalf("parent: nodeDetails %+v nodeProperties %+v", parent.NodeDetails, parent.NodeProperties)
	}

	if main.NodeDetails == nil || !aws.ToBool(main.NodeDetails.IsMainNode) || aws.ToInt32(main.NodeDetails.NodeIndex) != 0 ||
		aws.ToString(main.JobId) != id+"#0" || main.Container == nil || aws.ToString(main.Container.Image) != "mpi" ||
		main.Status != parent.Status {
		t.Fatalf("main node: %+v", main)
	}

	if last.NodeDetails == nil || aws.ToBool(last.NodeDetails.IsMainNode) || aws.ToInt32(last.NodeDetails.NodeIndex) != 2 {
		t.Fatalf("node #2: %+v", last.NodeDetails)
	}

	list, err := c.ListJobs(ctx, &awsbatch.ListJobsInput{MultiNodeJobId: aws.String(id), JobStatus: batchtypes.JobStatusSucceeded})
	if err != nil || len(list.JobSummaryList) != 3 {
		t.Fatalf("ListJobs multiNodeJobId: %+v, %v", list, err)
	}

	for i, s := range list.JobSummaryList {
		np := s.NodeProperties
		if np == nil || aws.ToInt32(np.NodeIndex) != int32(i) || aws.ToInt32(np.NumNodes) != 3 || aws.ToBool(np.IsMainNode) != (i == 0) {
			t.Fatalf("summary %d nodeProperties: %+v", i, np)
		}
	}
}

func TestSDKSubmitJobRejectsNodeOverridesOnFargate(t *testing.T) {
	ctx := context.Background()
	c := newBatchClient(t)
	setupSDKQueue(t, c, nil)
	setupSDKMultiNode(t, c)

	_, err := c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("x"), JobQueue: aws.String("q"), JobDefinition: aws.String("mnp"),
		NodeOverrides: &batchtypes.NodeOverrides{NumNodes: aws.Int32(3)},
	})
	requireClientException(t, err, "SubmitJob nodeOverrides on a Fargate queue")

	_, err = c.SubmitJob(ctx, &awsbatch.SubmitJobInput{
		JobName: aws.String("x"), JobQueue: aws.String("q"), JobDefinition: aws.String("mnp"),
	})
	requireClientException(t, err, "SubmitJob multinode definition on a Fargate queue")

	_, err = c.RegisterJobDefinition(ctx, &awsbatch.RegisterJobDefinitionInput{
		JobDefinitionName: aws.String("mnp-fg"), Type: batchtypes.JobDefinitionTypeMultinode,
		PlatformCapabilities: []batchtypes.PlatformCapability{batchtypes.PlatformCapabilityFargate},
		NodeProperties: &batchtypes.NodeProperties{
			NumNodes: aws.Int32(2), MainNode: aws.Int32(0),
			NodeRangeProperties: []batchtypes.NodeRangeProperty{
				{TargetNodes: aws.String("0:"), Container: &batchtypes.ContainerProperties{Image: aws.String("mpi")}},
			},
		},
	})
	requireClientException(t, err, "RegisterJobDefinition multinode with FARGATE")
}
