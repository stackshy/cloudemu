package batch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/batch"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// ec2Queue is the EC2-backed job queue setupEC2Queue creates.
const ec2Queue = "q-ec2"

// setupEC2Queue creates an EC2 compute environment and the job queue ec2Queue
// on it (multi-node parallel jobs need EC2 resources).
func setupEC2Queue(t *testing.T, m *batch.Mock) {
	t.Helper()

	ctx := context.Background()

	if _, err := m.CreateComputeEnvironment(ctx, driver.CreateComputeEnvironmentInput{
		Name: "ce-ec2", Type: driver.CETypeManaged,
		ComputeResources: json.RawMessage(`{"type":"EC2","minvCpus":0,"maxvCpus":16,` +
			`"instanceTypes":["optimal"],"subnets":["subnet-1"],"securityGroupIds":["sg-1"]}`),
	}); err != nil {
		t.Fatalf("create EC2 compute environment: %v", err)
	}

	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name: ec2Queue, Priority: 1,
		ComputeEnvironmentOrder: []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "ce-ec2"}},
	}); err != nil {
		t.Fatalf("create EC2 queue: %v", err)
	}
}

// registerMNP registers "mnp": three nodes, main node 1, nodes 0-1 on image
// "lead" and node 2 on image "worker".
func registerMNP(t *testing.T, m *batch.Mock) {
	t.Helper()

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "mnp", Type: driver.JDTypeMultiNode,
		NodeProperties: json.RawMessage(`{"numNodes":3,"mainNode":1,"nodeRangeProperties":[
			{"targetNodes":"0:1","container":{"image":"lead"}},
			{"targetNodes":"2:","container":{"image":"worker"}}]}`),
	})
}

func describeAll(t *testing.T, m *batch.Mock, ids ...string) []driver.Job {
	t.Helper()

	jobs, err := m.DescribeJobs(context.Background(), ids)
	if err != nil {
		t.Fatalf("DescribeJobs: %v", err)
	}

	return jobs
}

func containerImage(t *testing.T, j *driver.Job) string {
	t.Helper()

	img, _ := decodeMap(t, j.Container)["image"].(string)

	return img
}

func TestMultiNodeJobDescribesEachNode(t *testing.T) {
	m, fc := newAsyncMock(t)
	setupEC2Queue(t, m)
	registerMNP(t, m)

	j := submit(t, m, driver.SubmitJobInput{JobQueue: ec2Queue, JobDefinition: "mnp"})

	parent := describe(t, m, j.JobID)
	if parent.NodeDetails != nil || !strings.Contains(string(parent.NodeProperties), `"numNodes":3`) {
		t.Fatalf("parent should carry nodeProperties and no nodeDetails: %+v", parent)
	}

	nodes := describeAll(t, m, j.JobID+"#0", j.JobID+"#1", j.JobID+"#2", j.JobID+"#3", j.JobID+"#-1")
	if len(nodes) != 3 {
		t.Fatalf("want nodes #0-#2 (and #3, #-1 omitted), got %d", len(nodes))
	}

	wantImage := []string{"lead", "lead", "worker"}

	for i := range nodes {
		n := &nodes[i]
		if n.NodeDetails == nil || int(n.NodeDetails.NodeIndex) != i || n.NodeDetails.IsMainNode != (i == 1) {
			t.Fatalf("node %d nodeDetails = %+v", i, n.NodeDetails)
		}

		if n.JobARN != j.JobARN+"#"+string(rune('0'+i)) || n.NodeProperties != nil || n.Status != driver.JobStatusSubmitted {
			t.Fatalf("node %d = %+v", i, n)
		}

		if got := containerImage(t, n); got != wantImage[i] {
			t.Fatalf("node %d container image = %q, want %q", i, got, wantImage[i])
		}
	}

	// Nodes follow the job's lifecycle.
	fc.Advance(3 * time.Second)

	if n := describe(t, m, j.JobID+"#2"); n.Status != driver.JobStatusRunning || n.StartedAt == 0 {
		t.Fatalf("node #2 while the job runs = %s (startedAt %d)", n.Status, n.StartedAt)
	}

	fc.Advance(10 * time.Second)

	p := describe(t, m, j.JobID)
	main := describe(t, m, j.JobID+"#1")

	if p.Status != driver.JobStatusSucceeded || main.Status != driver.JobStatusSucceeded ||
		main.StoppedAt != p.StoppedAt || decodeMap(t, main.Container)["exitCode"] != float64(0) {
		t.Fatalf("finished job %s / main node %+v", p.Status, main)
	}
}

func TestMultiNodeNodesFollowCancel(t *testing.T) {
	m, _ := newAsyncMock(t)
	setupEC2Queue(t, m)
	registerMNP(t, m)

	j := submit(t, m, driver.SubmitJobInput{JobQueue: ec2Queue, JobDefinition: "mnp"})

	if err := m.CancelJob(context.Background(), j.JobID, "stop"); err != nil {
		t.Fatal(err)
	}

	n := describe(t, m, j.JobID+"#0")
	if n.Status != driver.JobStatusFailed || !n.IsCancelled || n.StatusReason != "stop" {
		t.Fatalf("node of a canceled job = %+v", n)
	}
}

func TestListJobsByMultiNodeJobID(t *testing.T) {
	m, _ := newAsyncMock(t)
	setupEC2Queue(t, m)
	registerMNP(t, m)
	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "single", Type: driver.JDTypeContainer, ContainerProperties: json.RawMessage(`{"image":"x"}`),
	})

	j := submit(t, m, driver.SubmitJobInput{JobName: "mnpjob", JobQueue: ec2Queue, JobDefinition: "mnp"})
	single := submit(t, m, driver.SubmitJobInput{JobQueue: ec2Queue, JobDefinition: "single"})

	all, err := listJobs(m, driver.ListJobsInput{MultiNodeJobID: j.JobID, JobStatus: driver.JobStatusSubmitted})
	if err != nil || len(all) != 3 {
		t.Fatalf("ListJobs multiNodeJobId = %d jobs, %v", len(all), err)
	}

	for i := range all {
		if all[i].JobID != j.JobID+"#"+string(rune('0'+i)) || all[i].NodeDetails == nil ||
			all[i].NodeDetails.NumNodes != 3 || all[i].NodeDetails.IsMainNode != (i == 1) {
			t.Fatalf("node %d = %+v", i, all[i])
		}
	}

	// Pages of two.
	max2 := int32(2)

	page, err := m.ListJobs(context.Background(), driver.ListJobsInput{
		MultiNodeJobID: j.JobID, JobStatus: driver.JobStatusSubmitted, MaxResults: &max2,
	})
	if err != nil || len(page.Jobs) != 2 || page.NextToken == "" {
		t.Fatalf("first page = %+v, %v", page, err)
	}

	rest, err := m.ListJobs(context.Background(), driver.ListJobsInput{
		MultiNodeJobID: j.JobID, JobStatus: driver.JobStatusSubmitted, MaxResults: &max2, NextToken: page.NextToken,
	})
	if err != nil || len(rest.Jobs) != 1 || rest.Jobs[0].JobID != j.JobID+"#2" || rest.NextToken != "" {
		t.Fatalf("second page = %+v, %v", rest, err)
	}

	empty := map[string]driver.ListJobsInput{
		"default RUNNING status":   {MultiNodeJobID: j.JobID},
		"non-matching job name":    {MultiNodeJobID: j.JobID, Filters: []driver.JobFilter{{Name: driver.FilterJobName, Values: []string{"nomatch"}}}},
		"unknown job":              {MultiNodeJobID: "nope", JobStatus: driver.JobStatusSubmitted},
		"job that isn't multinode": {MultiNodeJobID: single.JobID, JobStatus: driver.JobStatusSubmitted},
	}

	for name, in := range empty {
		got, err := listJobs(m, in)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: got %d jobs, %v", name, len(got), err)
		}
	}

	if got, _ := listJobs(m, driver.ListJobsInput{
		MultiNodeJobID: j.JobID, Filters: []driver.JobFilter{{Name: driver.FilterJobName, Values: []string{"mnp*"}}},
	}); len(got) != 3 {
		t.Fatalf("matching filter returned %d nodes", len(got))
	}
}

func TestMultiNodeNodesSendNoEvents(t *testing.T) {
	m := newMock(t)
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)
	setupEC2Queue(t, m)
	registerMNP(t, m)

	j := submit(t, m, driver.SubmitJobInput{JobQueue: ec2Queue, JobDefinition: "mnp"})
	describeAll(t, m, j.JobID+"#0", j.JobID+"#1")

	pub.mu.Lock()
	defer pub.mu.Unlock()

	if len(pub.events) != 4 {
		t.Fatalf("want the job's 4 events only, got %d", len(pub.events))
	}

	for _, e := range pub.events {
		if len(e.resources) != 1 || e.resources[0] != j.JobARN {
			t.Fatalf("event for something other than the job: %v", e.resources)
		}
	}
}

func TestMultiNodeNodesSurviveSnapshot(t *testing.T) {
	src, fc := newAsyncMock(t)
	setupEC2Queue(t, src)
	registerMNP(t, src)
	ctx := context.Background()

	j := submit(t, src, driver.SubmitJobInput{
		JobQueue: ec2Queue, JobDefinition: "mnp",
		NodeOverrides: json.RawMessage(`{"nodePropertyOverrides":[{"targetNodes":"2:","containerOverrides":{"command":["go"]}}]}`),
	})

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := batch.New(config.NewOptions(
		config.WithRegion("us-east-1"), config.WithAccountID("000000000000"),
		config.WithClock(fc), config.WithAsyncSettle(),
	))
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	n := describe(t, dst, j.JobID+"#2")
	if n.NodeDetails == nil || n.NodeDetails.NodeIndex != 2 || !strings.Contains(string(n.Container), `"command":["go"]`) {
		t.Fatalf("restored node = %+v", n)
	}
}

func TestSubmitJobRejectsMultiNodeOnFargate(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m) // queue "q" is on a FARGATE compute environment
	setupEC2Queue(t, m)
	registerMNP(t, m)
	ctx := context.Background()

	if _, err := m.CreateComputeEnvironment(ctx, driver.CreateComputeEnvironmentInput{
		Name: "ce-spot", Type: driver.CETypeManaged,
		ComputeResources: json.RawMessage(`{"type":"FARGATE_SPOT","maxvCpus":4,"subnets":["s"]}`),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name: "q-spot", Priority: 1,
		ComputeEnvironmentOrder: []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "ce-spot"}},
	}); err != nil {
		t.Fatal(err)
	}

	nodeOverrides := json.RawMessage(`{"nodePropertyOverrides":[{"targetNodes":"0:","containerOverrides":{"command":["x"]}}]}`)

	cases := []struct {
		name, queue, jd, want string
		overrides             json.RawMessage
	}{
		{"nodeOverrides on a Fargate queue", "q", "mnp", "nodeOverrides isn't applicable", nodeOverrides},
		{"nodeOverrides with a container definition on Fargate", "q", "jd", "nodeOverrides isn't applicable", nodeOverrides},
		{"multinode definition on a Fargate queue", "q", "mnp", "can't run on Fargate", nil},
		{"multinode definition on a FARGATE_SPOT queue (by ARN)",
			"arn:aws:batch:us-east-1:000000000000:job-queue/q-spot", "mnp", "can't run on Fargate", nil},
	}

	for _, tc := range cases {
		_, err := m.SubmitJob(ctx, driver.SubmitJobInput{
			JobName: "j", JobQueue: tc.queue, JobDefinition: tc.jd, NodeOverrides: tc.overrides,
		})
		if !errors.IsInvalidArgument(err) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want InvalidArgument %q, got %v", tc.name, tc.want, err)
		}
	}

	// The same submissions are accepted on an EC2 queue.
	submit(t, m, driver.SubmitJobInput{JobQueue: ec2Queue, JobDefinition: "mnp", NodeOverrides: nodeOverrides})

	// A container job on a Fargate queue is unaffected.
	submit(t, m, driver.SubmitJobInput{JobQueue: "q", JobDefinition: "jd"})
}

func TestRegisterMultiNodeRejectsFargatePlatform(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	props := json.RawMessage(`{"numNodes":2,"mainNode":0,"nodeRangeProperties":[{"targetNodes":"0:","container":{"image":"x"}}]}`)

	for _, typ := range []string{driver.JDTypeMultiNode, driver.JDTypeContainer} {
		_, err := m.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
			Name: "fg", Type: typ, NodeProperties: props, PlatformCapabilities: []string{"FARGATE"},
			ContainerProperties: json.RawMessage(`{"image":"x"}`),
		})
		if !errors.IsInvalidArgument(err) {
			t.Errorf("%s nodeProperties with FARGATE: want InvalidArgument, got %v", typ, err)
		}
	}

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "ec2", Type: driver.JDTypeMultiNode, NodeProperties: props, PlatformCapabilities: []string{"EC2"},
	})
}
