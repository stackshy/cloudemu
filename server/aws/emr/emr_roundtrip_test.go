package emr_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/emr"
	emrtypes "github.com/aws/aws-sdk-go-v2/service/emr/types"

	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const testAccountID = "123456789012"

// newEMRClient stands up an in-process AWS server with the EMR handler enabled
// and returns a real EMR SDK client pointed at it.
func newEMRClient(t *testing.T) *emr.Client {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC))

	srv := awsserver.New(awsserver.Drivers{
		EMR:       true,
		AccountID: testAccountID,
		Region:    "us-east-1",
		Clock:     clock,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return emr.NewFromConfig(cfg, func(o *emr.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

// runCluster launches a keep-alive cluster with a single step and returns its id.
func runCluster(t *testing.T, c *emr.Client) string {
	t.Helper()

	out, err := c.RunJobFlow(context.Background(), &emr.RunJobFlowInput{
		Name:         aws.String("analytics"),
		ReleaseLabel: aws.String("emr-6.15.0"),
		Instances: &emrtypes.JobFlowInstancesConfig{
			InstanceCount:               aws.Int32(3),
			MasterInstanceType:          aws.String("m5.xlarge"),
			SlaveInstanceType:           aws.String("m5.large"),
			Ec2SubnetId:                 aws.String("subnet-123"),
			KeepJobFlowAliveWhenNoSteps: aws.Bool(true),
		},
		BootstrapActions: []emrtypes.BootstrapActionConfig{{
			Name: aws.String("install-deps"),
			ScriptBootstrapAction: &emrtypes.ScriptBootstrapActionConfig{
				Path: aws.String("s3://bkt/bootstrap.sh"),
				Args: []string{"--verbose"},
			},
		}},
		Applications: []emrtypes.Application{{Name: aws.String("Spark")}},
		Steps: []emrtypes.StepConfig{{
			Name: aws.String("bootstrap-load"),
			HadoopJarStep: &emrtypes.HadoopJarStepConfig{
				Jar:  aws.String("command-runner.jar"),
				Args: []string{"spark-submit", "job.py"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("RunJobFlow: %v", err)
	}

	if aws.ToString(out.JobFlowId) == "" || !strings.HasPrefix(aws.ToString(out.JobFlowId), "j-") {
		t.Fatalf("JobFlowId = %q, want a j- id", aws.ToString(out.JobFlowId))
	}

	if !strings.Contains(aws.ToString(out.ClusterArn), ":cluster/"+aws.ToString(out.JobFlowId)) {
		t.Fatalf("ClusterArn = %q, want cluster/<id>", aws.ToString(out.ClusterArn))
	}

	return aws.ToString(out.JobFlowId)
}

func TestSDKRunJobFlowAndDescribeCluster(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	out, err := c.DescribeCluster(ctx, &emr.DescribeClusterInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	cl := out.Cluster
	if cl.Status.State != emrtypes.ClusterStateWaiting {
		t.Fatalf("state = %s, want WAITING", cl.Status.State)
	}

	if aws.ToString(cl.Name) != "analytics" || aws.ToString(cl.ReleaseLabel) != "emr-6.15.0" {
		t.Fatalf("cluster = %q/%q, want analytics/emr-6.15.0", aws.ToString(cl.Name), aws.ToString(cl.ReleaseLabel))
	}

	if cl.Ec2InstanceAttributes == nil || aws.ToString(cl.Ec2InstanceAttributes.Ec2SubnetId) != "subnet-123" {
		t.Fatalf("Ec2InstanceAttributes = %+v, want subnet-123", cl.Ec2InstanceAttributes)
	}

	if cl.Status.Timeline == nil || cl.Status.Timeline.CreationDateTime == nil {
		t.Fatalf("timeline = %+v, want a creation time", cl.Status.Timeline)
	}
}

func TestSDKListClustersFiltersByState(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	all, err := c.ListClusters(ctx, &emr.ListClustersInput{})
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}

	if !containsCluster(all.Clusters, id) {
		t.Fatalf("ListClusters did not include %s", id)
	}

	waiting, err := c.ListClusters(ctx, &emr.ListClustersInput{
		ClusterStates: []emrtypes.ClusterState{emrtypes.ClusterStateWaiting},
	})
	if err != nil {
		t.Fatalf("ListClusters WAITING: %v", err)
	}

	if !containsCluster(waiting.Clusters, id) {
		t.Fatalf("WAITING filter dropped %s", id)
	}

	terminated, err := c.ListClusters(ctx, &emr.ListClustersInput{
		ClusterStates: []emrtypes.ClusterState{emrtypes.ClusterStateTerminated},
	})
	if err != nil {
		t.Fatalf("ListClusters TERMINATED: %v", err)
	}

	if containsCluster(terminated.Clusters, id) {
		t.Fatalf("TERMINATED filter wrongly included live cluster %s", id)
	}
}

func TestSDKTerminateJobFlows(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	if _, err := c.TerminateJobFlows(ctx, &emr.TerminateJobFlowsInput{JobFlowIds: []string{id}}); err != nil {
		t.Fatalf("TerminateJobFlows: %v", err)
	}

	out, err := c.DescribeCluster(ctx, &emr.DescribeClusterInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeCluster after terminate: %v", err)
	}

	if out.Cluster.Status.State != emrtypes.ClusterStateTerminated {
		t.Fatalf("state = %s, want TERMINATED", out.Cluster.Status.State)
	}

	reason := out.Cluster.Status.StateChangeReason
	if reason == nil || reason.Code != emrtypes.ClusterStateChangeReasonCodeUserRequest {
		t.Fatalf("StateChangeReason = %+v, want USER_REQUEST", reason)
	}
}

func TestSDKStepsLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	added, err := c.AddJobFlowSteps(ctx, &emr.AddJobFlowStepsInput{
		JobFlowId: aws.String(id),
		Steps: []emrtypes.StepConfig{{
			Name:            aws.String("etl"),
			ActionOnFailure: emrtypes.ActionOnFailureContinue,
			HadoopJarStep:   &emrtypes.HadoopJarStepConfig{Jar: aws.String("command-runner.jar")},
		}},
	})
	if err != nil {
		t.Fatalf("AddJobFlowSteps: %v", err)
	}

	if len(added.StepIds) != 1 || !strings.HasPrefix(added.StepIds[0], "s-") {
		t.Fatalf("StepIds = %v, want one s- id", added.StepIds)
	}

	stepID := added.StepIds[0]

	steps, err := c.ListSteps(ctx, &emr.ListStepsInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}

	// The RunJobFlow bootstrap step plus the added ETL step.
	if len(steps.Steps) != 2 {
		t.Fatalf("ListSteps = %d, want 2", len(steps.Steps))
	}

	got, err := c.DescribeStep(ctx, &emr.DescribeStepInput{ClusterId: aws.String(id), StepId: aws.String(stepID)})
	if err != nil {
		t.Fatalf("DescribeStep: %v", err)
	}

	if aws.ToString(got.Step.Name) != "etl" || got.Step.Status.State != emrtypes.StepStateCompleted {
		t.Fatalf("step = %q/%s, want etl/COMPLETED", aws.ToString(got.Step.Name), got.Step.Status.State)
	}
}

func TestSDKDescribeClusterUnknownIsError(t *testing.T) {
	c := newEMRClient(t)

	if _, err := c.DescribeCluster(context.Background(),
		&emr.DescribeClusterInput{ClusterId: aws.String("j-DOESNOTEXIST")}); err == nil {
		t.Fatal("DescribeCluster on unknown id: want error, got nil")
	}
}

func TestSDKInstanceGroupsAndInstances(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	// RunJobFlow synthesized a MASTER (1) + CORE (2) pair from the uniform config.
	groups, err := c.ListInstanceGroups(ctx, &emr.ListInstanceGroupsInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListInstanceGroups: %v", err)
	}

	if len(groups.InstanceGroups) != 2 {
		t.Fatalf("InstanceGroups = %d, want 2 (MASTER+CORE)", len(groups.InstanceGroups))
	}

	var coreID string

	for i := range groups.InstanceGroups {
		g := groups.InstanceGroups[i]
		if g.InstanceGroupType == emrtypes.InstanceGroupTypeCore {
			coreID = aws.ToString(g.Id)

			if aws.ToInt32(g.RunningInstanceCount) != 2 {
				t.Fatalf("CORE running = %d, want 2", aws.ToInt32(g.RunningInstanceCount))
			}
		}
	}

	if coreID == "" {
		t.Fatalf("no CORE group in %+v", groups.InstanceGroups)
	}

	insts, err := c.ListInstances(ctx, &emr.ListInstancesInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	if len(insts.Instances) != 3 {
		t.Fatalf("Instances = %d, want 3 (1 master + 2 core)", len(insts.Instances))
	}

	if insts.Instances[0].Status == nil || insts.Instances[0].Status.State != emrtypes.InstanceStateRunning {
		t.Fatalf("instance state = %+v, want RUNNING", insts.Instances[0].Status)
	}

	// Filter by the CORE group id.
	coreInsts, err := c.ListInstances(ctx,
		&emr.ListInstancesInput{ClusterId: aws.String(id), InstanceGroupId: aws.String(coreID)})
	if err != nil {
		t.Fatalf("ListInstances by group: %v", err)
	}

	if len(coreInsts.Instances) != 2 {
		t.Fatalf("CORE instances = %d, want 2", len(coreInsts.Instances))
	}
}

func TestSDKAddAndModifyInstanceGroups(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	added, err := c.AddInstanceGroups(ctx, &emr.AddInstanceGroupsInput{
		JobFlowId: aws.String(id),
		InstanceGroups: []emrtypes.InstanceGroupConfig{{
			InstanceRole:  emrtypes.InstanceRoleTypeTask,
			InstanceType:  aws.String("m5.large"),
			InstanceCount: aws.Int32(2),
			Name:          aws.String("task-group"),
		}},
	})
	if err != nil {
		t.Fatalf("AddInstanceGroups: %v", err)
	}

	if len(added.InstanceGroupIds) != 1 || aws.ToString(added.JobFlowId) != id {
		t.Fatalf("AddInstanceGroups out = %+v, want one group id for %s", added, id)
	}

	taskID := added.InstanceGroupIds[0]

	if _, err := c.ModifyInstanceGroups(ctx, &emr.ModifyInstanceGroupsInput{
		InstanceGroups: []emrtypes.InstanceGroupModifyConfig{{
			InstanceGroupId: aws.String(taskID),
			InstanceCount:   aws.Int32(5),
		}},
	}); err != nil {
		t.Fatalf("ModifyInstanceGroups: %v", err)
	}

	groups, err := c.ListInstanceGroups(ctx, &emr.ListInstanceGroupsInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListInstanceGroups: %v", err)
	}

	var found bool

	for i := range groups.InstanceGroups {
		g := groups.InstanceGroups[i]
		if aws.ToString(g.Id) == taskID {
			found = true

			if aws.ToInt32(g.RequestedInstanceCount) != 5 || aws.ToInt32(g.RunningInstanceCount) != 5 {
				t.Fatalf("task group counts = %d/%d, want 5/5",
					aws.ToInt32(g.RequestedInstanceCount), aws.ToInt32(g.RunningInstanceCount))
			}
		}
	}

	if !found {
		t.Fatalf("added task group %s missing after modify", taskID)
	}

	// The resize is reflected in the instance list too (3 original + 5 task).
	insts, err := c.ListInstances(ctx, &emr.ListInstancesInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	if len(insts.Instances) != 8 {
		t.Fatalf("Instances = %d, want 8 after resize", len(insts.Instances))
	}
}

func TestSDKListBootstrapActions(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	out, err := c.ListBootstrapActions(ctx, &emr.ListBootstrapActionsInput{ClusterId: aws.String(id)})
	if err != nil {
		t.Fatalf("ListBootstrapActions: %v", err)
	}

	if len(out.BootstrapActions) != 1 {
		t.Fatalf("BootstrapActions = %d, want 1", len(out.BootstrapActions))
	}

	ba := out.BootstrapActions[0]
	if aws.ToString(ba.Name) != "install-deps" || aws.ToString(ba.ScriptPath) != "s3://bkt/bootstrap.sh" {
		t.Fatalf("bootstrap = %q/%q, want install-deps/s3://bkt/bootstrap.sh",
			aws.ToString(ba.Name), aws.ToString(ba.ScriptPath))
	}
}

func TestSDKCancelSteps(t *testing.T) {
	ctx := context.Background()
	c := newEMRClient(t)
	id := runCluster(t, c)

	added, err := c.AddJobFlowSteps(ctx, &emr.AddJobFlowStepsInput{
		JobFlowId: aws.String(id),
		Steps: []emrtypes.StepConfig{{
			Name:          aws.String("etl"),
			HadoopJarStep: &emrtypes.HadoopJarStepConfig{Jar: aws.String("command-runner.jar")},
		}},
	})
	if err != nil {
		t.Fatalf("AddJobFlowSteps: %v", err)
	}

	stepID := added.StepIds[0]

	// Steps execute instantly (COMPLETED), so a cancel request is not honorable:
	// real EMR reports FAILED with a not-cancellable reason.
	out, err := c.CancelSteps(ctx, &emr.CancelStepsInput{
		ClusterId: aws.String(id),
		StepIds:   []string{stepID},
	})
	if err != nil {
		t.Fatalf("CancelSteps: %v", err)
	}

	if len(out.CancelStepsInfoList) != 1 {
		t.Fatalf("CancelStepsInfoList = %d, want 1", len(out.CancelStepsInfoList))
	}

	info := out.CancelStepsInfoList[0]
	if aws.ToString(info.StepId) != stepID || info.Status != emrtypes.CancelStepsRequestStatusFailed {
		t.Fatalf("cancel info = %q/%s, want %s/FAILED", aws.ToString(info.StepId), info.Status, stepID)
	}

	if aws.ToString(info.Reason) == "" {
		t.Fatal("expected a not-cancellable reason for a COMPLETED step")
	}
}

func containsCluster(clusters []emrtypes.ClusterSummary, id string) bool {
	for i := range clusters {
		if aws.ToString(clusters[i].Id) == id {
			return true
		}
	}

	return false
}
