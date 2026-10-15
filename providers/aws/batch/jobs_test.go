package batch_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/batch"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

var jobEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// recordingPublisher captures published EventBridge events.
type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	source, detailType string
	detail             map[string]any
	resources          []string
}

func (p *recordingPublisher) PublishServiceEvent(_ context.Context, source, detailType string, detail any, resources []string) {
	b, _ := json.Marshal(detail)

	var d map[string]any
	_ = json.Unmarshal(b, &d)

	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = append(p.events, recordedEvent{source: source, detailType: detailType, detail: d, resources: resources})
}

func (p *recordingPublisher) statuses() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]string, 0, len(p.events))
	for _, e := range p.events {
		out = append(out, e.detail["status"].(string))
	}

	return out
}

// newAsyncMock returns a mock whose jobs progress on a fake clock.
func newAsyncMock(t *testing.T) (*batch.Mock, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(jobEpoch)

	return batch.New(config.NewOptions(
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
		config.WithClock(fc),
		config.WithAsyncSettle(),
	)), fc
}

// setupQueue creates a compute environment, a job queue "q" and a container
// job definition "jd".
func setupQueue(t *testing.T, m *batch.Mock) {
	t.Helper()

	ctx := context.Background()
	mustCreateCE(t, m, "ce")

	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name:                    "q",
		Priority:                1,
		ComputeEnvironmentOrder: []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "ce"}},
	}); err != nil {
		t.Fatalf("create queue: %v", err)
	}

	if _, err := m.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
		Name: "jd", Type: driver.JDTypeContainer,
		ContainerProperties: json.RawMessage(`{"image":"busybox","command":["echo","hi"]}`),
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func submit(t *testing.T, m *batch.Mock, in driver.SubmitJobInput) *driver.Job {
	t.Helper()

	if in.JobName == "" {
		in.JobName = "job"
	}

	if in.JobQueue == "" {
		in.JobQueue = "q"
	}

	if in.JobDefinition == "" {
		in.JobDefinition = "jd"
	}

	j, err := m.SubmitJob(context.Background(), in)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	return j
}

func describe(t *testing.T, m *batch.Mock, id string) driver.Job {
	t.Helper()

	jobs, err := m.DescribeJobs(context.Background(), []string{id})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("DescribeJobs(%s) = %v, %v", id, jobs, err)
	}

	return jobs[0]
}

func TestSubmitJobSyncSucceedsImmediately(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)

	j := submit(t, m, driver.SubmitJobInput{
		ContainerOverrides: json.RawMessage(`{"command":["echo","bye"]}`),
		Parameters:         map[string]string{"p": "v"},
	})
	if j.JobID == "" || j.JobARN != "arn:aws:batch:us-east-1:000000000000:job/"+j.JobID {
		t.Fatalf("bad identity: %+v", j)
	}

	got := describe(t, m, j.JobID)
	if got.Status != driver.JobStatusSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", got.Status)
	}

	if got.CreatedAt == 0 || got.StartedAt == 0 || got.StoppedAt == 0 {
		t.Fatalf("timestamps not set: %+v", got)
	}

	if got.JobQueue != "arn:aws:batch:us-east-1:000000000000:job-queue/q" ||
		got.JobDefinition != "arn:aws:batch:us-east-1:000000000000:job-definition/jd:1" {
		t.Fatalf("queue/definition not resolved to ARNs: %s %s", got.JobQueue, got.JobDefinition)
	}

	var c map[string]any
	if err := json.Unmarshal(got.Container, &c); err != nil {
		t.Fatalf("container: %v", err)
	}

	if c["image"] != "busybox" || c["command"].([]any)[1] != "bye" || c["exitCode"] != float64(0) {
		t.Fatalf("container overrides/exitCode not applied: %v", c)
	}
}

func TestJobLifecycleAdvancesWithClock(t *testing.T) {
	m, fc := newAsyncMock(t)
	setupQueue(t, m)

	j := submit(t, m, driver.SubmitJobInput{})

	want := []string{
		driver.JobStatusSubmitted, driver.JobStatusRunnable, driver.JobStatusStarting,
		driver.JobStatusRunning, driver.JobStatusSucceeded,
	}

	for i, w := range want {
		got := describe(t, m, j.JobID)
		if got.Status != w {
			t.Fatalf("step %d: status = %s, want %s", i, got.Status, w)
		}

		if w == driver.JobStatusRunning && got.StartedAt == 0 {
			t.Fatal("RUNNING job has no startedAt")
		}

		if w == driver.JobStatusSucceeded && (got.StoppedAt <= got.StartedAt || got.StatusReason == "") {
			t.Fatalf("SUCCEEDED job times/reason wrong: %+v", got)
		}

		fc.Advance(time.Second)
	}
}

func TestSubmitJobValidation(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	one, tooMany := int32(1), int32(10001)

	cases := map[string]driver.SubmitJobInput{
		"missing name":       {JobQueue: "q", JobDefinition: "jd"},
		"bad name":           {JobName: "-bad", JobQueue: "q", JobDefinition: "jd"},
		"missing queue":      {JobName: "j", JobQueue: "nope", JobDefinition: "jd"},
		"missing definition": {JobName: "j", JobQueue: "q", JobDefinition: "nope"},
		"missing revision":   {JobName: "j", JobQueue: "q", JobDefinition: "jd:9"},
		"array size 1":       {JobName: "j", JobQueue: "q", JobDefinition: "jd", ArraySize: &one},
		"array size 10001":   {JobName: "j", JobQueue: "q", JobDefinition: "jd", ArraySize: &tooMany},
		"unknown dependency": {JobName: "j", JobQueue: "q", JobDefinition: "jd",
			DependsOn: []driver.JobDependency{{JobID: "missing"}}},
		"bad dependency type": {JobName: "j", JobQueue: "q", JobDefinition: "jd",
			DependsOn: []driver.JobDependency{{JobID: "x", Type: "BOGUS"}}},
		"N_TO_N on non-array": {JobName: "j", JobQueue: "q", JobDefinition: "jd",
			DependsOn: []driver.JobDependency{{JobID: "x", Type: driver.DependencyNToN}}},
		"share id on FIFO queue": {JobName: "j", JobQueue: "q", JobDefinition: "jd", ShareIdentifier: "A"},
		"short timeout": {JobName: "j", JobQueue: "q", JobDefinition: "jd",
			Timeout: json.RawMessage(`{"attemptDurationSeconds":30}`)},
	}

	for name, in := range cases {
		if _, err := m.SubmitJob(ctx, in); !errors.IsInvalidArgument(err) {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}

	// A DISABLED queue accepts no new jobs.
	disabled := driver.StateDisabled
	if _, err := m.UpdateJobQueue(ctx, driver.UpdateJobQueueInput{Name: "q", State: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if _, err := m.SubmitJob(ctx, driver.SubmitJobInput{JobName: "j", JobQueue: "q", JobDefinition: "jd"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("submit to DISABLED queue: want InvalidArgument, got %v", err)
	}
}

func TestSubmitJobUsesLatestActiveRevision(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	if _, err := m.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
		Name: "jd", Type: driver.JDTypeContainer, ContainerProperties: json.RawMessage(`{"image":"v2"}`),
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.DeregisterJobDefinition(ctx, "jd:2"); err != nil {
		t.Fatal(err)
	}

	j := describe(t, m, submit(t, m, driver.SubmitJobInput{}).JobID)
	if j.JobDefinition != "arn:aws:batch:us-east-1:000000000000:job-definition/jd:1" {
		t.Fatalf("want latest ACTIVE revision 1, got %s", j.JobDefinition)
	}
}

func TestDependentJobWaitsAndFailsWithDependency(t *testing.T) {
	m, fc := newAsyncMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	parent := submit(t, m, driver.SubmitJobInput{JobName: "parent"})
	child := submit(t, m, driver.SubmitJobInput{
		JobName: "child", DependsOn: []driver.JobDependency{{JobID: parent.JobID}},
	})

	fc.Advance(time.Second)

	if got := describe(t, m, child.JobID).Status; got != driver.JobStatusPending {
		t.Fatalf("child before parent finishes = %s, want PENDING", got)
	}

	// Parent finishes at +4s; child then runs RUNNABLE/STARTING/RUNNING and
	// succeeds at +7s.
	fc.Advance(3 * time.Second)

	if got := describe(t, m, child.JobID).Status; got != driver.JobStatusRunnable {
		t.Fatalf("child after parent succeeded = %s, want RUNNABLE", got)
	}

	fc.Advance(3 * time.Second)

	if got := describe(t, m, child.JobID).Status; got != driver.JobStatusSucceeded {
		t.Fatalf("child = %s, want SUCCEEDED", got)
	}

	// A dependency that fails fails its dependents.
	p2 := submit(t, m, driver.SubmitJobInput{JobName: "p2"})
	c2 := submit(t, m, driver.SubmitJobInput{JobName: "c2", DependsOn: []driver.JobDependency{{JobID: p2.JobID}}})

	if err := m.CancelJob(ctx, p2.JobID, "stop it"); err != nil {
		t.Fatal(err)
	}

	fc.Advance(time.Second)

	got := describe(t, m, c2.JobID)
	if got.Status != driver.JobStatusFailed || got.StatusReason != "Dependent Job failed" {
		t.Fatalf("dependent of cancelled job = %s (%q), want FAILED (Dependent Job failed)", got.Status, got.StatusReason)
	}
}

func TestCancelVersusTerminate(t *testing.T) {
	m, fc := newAsyncMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	// Cancel affects a job that hasn't reached STARTING.
	runnable := submit(t, m, driver.SubmitJobInput{JobName: "runnable"})
	fc.Advance(time.Second)

	if err := m.CancelJob(ctx, runnable.JobID, "user cancel"); err != nil {
		t.Fatal(err)
	}

	got := describe(t, m, runnable.JobID)
	if got.Status != driver.JobStatusFailed || !got.IsCancelled || got.IsTerminated || got.StatusReason != "user cancel" {
		t.Fatalf("cancelled RUNNABLE job = %+v", got)
	}

	// Cancel leaves a RUNNING job alone (and still succeeds).
	running := submit(t, m, driver.SubmitJobInput{JobName: "running"})
	fc.Advance(3 * time.Second)

	if err := m.CancelJob(ctx, running.JobID, "too late"); err != nil {
		t.Fatalf("cancel of RUNNING job should succeed, got %v", err)
	}

	if got := describe(t, m, running.JobID); got.Status != driver.JobStatusRunning || got.IsCancelled {
		t.Fatalf("cancel must not affect a RUNNING job: %+v", got)
	}

	// Terminate stops a RUNNING job, keeping its startedAt.
	if err := m.TerminateJob(ctx, running.JobID, "kill"); err != nil {
		t.Fatal(err)
	}

	got = describe(t, m, running.JobID)
	if got.Status != driver.JobStatusFailed || !got.IsTerminated || got.StartedAt == 0 || got.StatusReason != "kill" {
		t.Fatalf("terminated RUNNING job = %+v", got)
	}

	// Terminate on a SUBMITTED job cancels it.
	fresh := submit(t, m, driver.SubmitJobInput{JobName: "fresh"})
	if err := m.TerminateJob(ctx, fresh.JobID, "early"); err != nil {
		t.Fatal(err)
	}

	if got := describe(t, m, fresh.JobID); got.Status != driver.JobStatusFailed || !got.IsCancelled || got.IsTerminated {
		t.Fatalf("terminate before STARTING should cancel: %+v", got)
	}

	// Finished jobs are untouched; unknown jobs and missing reasons are errors.
	fc.Advance(10 * time.Second)

	if err := m.TerminateJob(ctx, running.JobID, "again"); err != nil {
		t.Fatal(err)
	}

	if got := describe(t, m, running.JobID); got.StatusReason != "kill" {
		t.Fatalf("terminating a finished job changed it: %+v", got)
	}

	if err := m.CancelJob(ctx, "missing", "r"); !errors.IsNotFound(err) {
		t.Fatalf("unknown job: want NotFound, got %v", err)
	}

	if err := m.CancelJob(ctx, fresh.JobID, ""); !errors.IsInvalidArgument(err) {
		t.Fatalf("missing reason: want InvalidArgument, got %v", err)
	}
}

func TestArrayJobChildren(t *testing.T) {
	m, fc := newAsyncMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	size := int32(3)
	parent := submit(t, m, driver.SubmitJobInput{
		ArraySize: &size, DependsOn: []driver.JobDependency{{Type: driver.DependencySequential}},
	})

	fc.Advance(2 * time.Second)

	p := describe(t, m, parent.JobID)
	if p.Status != driver.JobStatusPending || p.ArrayProperties == nil || p.ArrayProperties.Size != 3 {
		t.Fatalf("array parent while children run = %+v", p)
	}

	if p.ArrayProperties.StatusSummary[driver.JobStatusStarting] != 1 ||
		p.ArrayProperties.StatusSummary[driver.JobStatusPending] != 2 {
		t.Fatalf("SEQUENTIAL children should run one at a time: %v", p.ArrayProperties.StatusSummary)
	}

	child := describe(t, m, parent.JobID+":0")
	if child.ArrayProperties == nil || child.ArrayProperties.Index == nil || *child.ArrayProperties.Index != 0 {
		t.Fatalf("child index missing: %+v", child)
	}

	// An index past int32 must not wrap around to a real child.
	for _, bad := range []string{":4294967296", ":2147483648", ":-1"} {
		if jobs, err := m.DescribeJobs(ctx, []string{parent.JobID + bad}); err != nil || len(jobs) != 0 {
			t.Fatalf("DescribeJobs(%s) = %+v, %v; want no job", bad, jobs, err)
		}
	}

	all := driver.JobStatusPending
	kids, err := listJobs(m, driver.ListJobsInput{ArrayJobID: parent.JobID, JobStatus: all})
	if err != nil || len(kids) != 2 {
		t.Fatalf("ListJobs(arrayJobId, PENDING) = %d, %v; want 2", len(kids), err)
	}

	fc.Advance(20 * time.Second)

	if got := describe(t, m, parent.JobID); got.Status != driver.JobStatusSucceeded ||
		got.ArrayProperties.StatusSummary[driver.JobStatusSucceeded] != 3 {
		t.Fatalf("array parent after children finish = %+v", got)
	}

	// Terminating an array parent fails every unfinished child.
	p2 := submit(t, m, driver.SubmitJobInput{JobName: "arr2", ArraySize: &size})
	if err := m.TerminateJob(ctx, p2.JobID, "abort"); err != nil {
		t.Fatal(err)
	}

	got := describe(t, m, p2.JobID)
	if got.Status != driver.JobStatusFailed || got.ArrayProperties.StatusSummary[driver.JobStatusFailed] != 3 {
		t.Fatalf("terminated array = %+v", got)
	}
}

func TestListJobs(t *testing.T) {
	m, fc := newAsyncMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	a := submit(t, m, driver.SubmitJobInput{JobName: "alpha"})
	fc.Advance(time.Second)
	b := submit(t, m, driver.SubmitJobInput{JobName: "beta"})
	fc.Advance(2 * time.Second) // alpha RUNNING, beta STARTING

	running, err := listJobs(m, driver.ListJobsInput{JobQueue: "q"})
	if err != nil || len(running) != 1 || running[0].JobID != a.JobID {
		t.Fatalf("default ListJobs (RUNNING) = %+v, %v", running, err)
	}

	byName, err := listJobs(m, driver.ListJobsInput{
		JobQueue: "arn:aws:batch:us-east-1:000000000000:job-queue/q",
		Filters:  []driver.JobFilter{{Name: driver.FilterJobName, Values: []string{"BET*"}}},
	})
	if err != nil || len(byName) != 1 || byName[0].JobID != b.JobID {
		t.Fatalf("JOB_NAME filter = %+v, %v", byName, err)
	}

	all, err := listJobs(m, driver.ListJobsInput{
		JobQueue: "q", Filters: []driver.JobFilter{{Name: driver.FilterJobDefinition, Values: []string{"jd"}}},
	})
	if err != nil || len(all) != 2 || all[0].JobID != b.JobID {
		t.Fatalf("JOB_DEFINITION filter should list both, newest first: %+v, %v", all, err)
	}

	bad := []driver.ListJobsInput{
		{},
		{JobQueue: "q", ArrayJobID: "x"},
		{JobQueue: "q", JobStatus: "DONE"},
		{JobQueue: "q", Filters: []driver.JobFilter{{Name: "NOPE"}}},
		{JobQueue: "q", Filters: []driver.JobFilter{{Name: driver.FilterJobName}, {Name: driver.FilterJobDefinition}}},
	}

	for i, in := range bad {
		if _, err := m.ListJobs(ctx, in); !errors.IsInvalidArgument(err) {
			t.Errorf("case %d: want InvalidArgument, got %v", i, err)
		}
	}
}

func TestJobStateChangeEvents(t *testing.T) {
	m := newMock(t)
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)
	setupQueue(t, m)

	j := submit(t, m, driver.SubmitJobInput{})

	got := pub.statuses()
	want := []string{"RUNNABLE", "STARTING", "RUNNING", "SUCCEEDED"}

	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v (no event for the initial submission)", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}

	e := pub.events[3]
	if e.source != "aws.batch" || e.detailType != "Batch Job State Change" ||
		len(e.resources) != 1 || e.resources[0] != j.JobARN || e.detail["jobId"] != j.JobID {
		t.Fatalf("bad event envelope: %+v", e)
	}

	// Describing again publishes nothing new.
	describe(t, m, j.JobID)

	if n := len(pub.statuses()); n != 4 {
		t.Fatalf("re-observing a finished job published more events: %d", n)
	}
}

func TestJobEventsFollowAsyncLifecycle(t *testing.T) {
	m, fc := newAsyncMock(t)
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)
	setupQueue(t, m)
	ctx := context.Background()

	j := submit(t, m, driver.SubmitJobInput{})
	if n := len(pub.statuses()); n != 0 {
		t.Fatalf("submission published %d events, want 0", n)
	}

	fc.Advance(time.Second)
	describe(t, m, j.JobID)

	if got := pub.statuses(); len(got) != 1 || got[0] != "RUNNABLE" {
		t.Fatalf("events after 1s = %v, want [RUNNABLE]", got)
	}

	if err := m.CancelJob(ctx, j.JobID, "stop"); err != nil {
		t.Fatal(err)
	}

	if got := pub.statuses(); len(got) != 2 || got[1] != "FAILED" {
		t.Fatalf("events after cancel = %v, want [RUNNABLE FAILED]", got)
	}
}

func TestSchedulingPolicyCRUDAndQueueBinding(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreateCE(t, m, "ce")

	p, err := m.CreateSchedulingPolicy(ctx, driver.CreateSchedulingPolicyInput{
		Name:            "fair",
		FairsharePolicy: json.RawMessage(`{"shareDecaySeconds":3600,"computeReservation":1,"shareDistribution":[{"shareIdentifier":"A*","weightFactor":0.5}]}`),
		Tags:            map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if p.ARN != "arn:aws:batch:us-east-1:000000000000:scheduling-policy/fair" {
		t.Fatalf("arn = %s", p.ARN)
	}

	invalid := []driver.CreateSchedulingPolicyInput{
		{Name: "fair"},
		{Name: "bad name"},
		{Name: "x", FairsharePolicy: json.RawMessage(`{"computeReservation":100}`)},
		{Name: "x", FairsharePolicy: json.RawMessage(`{"shareDecaySeconds":604801}`)},
		{Name: "x", FairsharePolicy: json.RawMessage(`{"shareDistribution":[{"shareIdentifier":"A","weightFactor":1000}]}`)},
		{Name: "x", FairsharePolicy: json.RawMessage(`{}`), QuotaSharePolicy: json.RawMessage(`{}`)},
	}

	for i, in := range invalid {
		if _, err := m.CreateSchedulingPolicy(ctx, in); err == nil {
			t.Errorf("case %d: want error", i)
		}
	}

	if err := m.UpdateSchedulingPolicy(ctx, driver.UpdateSchedulingPolicyInput{
		ARN: p.ARN, FairsharePolicy: json.RawMessage(`{"shareDecaySeconds":60}`),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := m.DescribeSchedulingPolicies(ctx, []string{p.ARN, "arn:aws:batch:us-east-1:000000000000:scheduling-policy/none"})
	if err != nil || len(got) != 1 || string(got[0].FairsharePolicy) != `{"shareDecaySeconds":60}` || got[0].Tags["k"] != "v" {
		t.Fatalf("describe = %+v, %v", got, err)
	}

	if _, err := m.DescribeSchedulingPolicies(ctx, nil); !errors.IsInvalidArgument(err) {
		t.Fatalf("describe without arns: want InvalidArgument, got %v", err)
	}

	// A queue must reference an existing policy.
	order := []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "ce"}}
	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name: "bad", Priority: 1, ComputeEnvironmentOrder: order,
		SchedulingPolicyARN: "arn:aws:batch:us-east-1:000000000000:scheduling-policy/none",
	}); !errors.IsInvalidArgument(err) {
		t.Fatalf("queue with unknown policy: want InvalidArgument, got %v", err)
	}

	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name: "fq", Priority: 1, ComputeEnvironmentOrder: order, SchedulingPolicyARN: p.ARN,
	}); err != nil {
		t.Fatalf("fair-share queue: %v", err)
	}

	// The policy can't be removed from the queue, and a FIFO queue can't gain one.
	empty := ""
	if _, err := m.UpdateJobQueue(ctx, driver.UpdateJobQueueInput{Name: "fq", SchedulingPolicyARN: &empty}); !errors.IsInvalidArgument(err) {
		t.Fatalf("removing policy: want InvalidArgument, got %v", err)
	}

	if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{Name: "fifo", Priority: 1, ComputeEnvironmentOrder: order}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.UpdateJobQueue(ctx, driver.UpdateJobQueueInput{Name: "fifo", SchedulingPolicyARN: &p.ARN}); !errors.IsInvalidArgument(err) {
		t.Fatalf("adding policy to FIFO queue: want InvalidArgument, got %v", err)
	}

	// Jobs on a fair-share queue need a share identifier.
	if _, err := m.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
		Name: "jd", Type: driver.JDTypeContainer, ContainerProperties: json.RawMessage(`{"image":"x"}`),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.SubmitJob(ctx, driver.SubmitJobInput{JobName: "j", JobQueue: "fq", JobDefinition: "jd"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("fair-share submit without shareIdentifier: want InvalidArgument, got %v", err)
	}

	if _, err := m.SubmitJob(ctx, driver.SubmitJobInput{JobName: "j", JobQueue: "fq", JobDefinition: "jd", ShareIdentifier: "A1"}); err != nil {
		t.Fatalf("fair-share submit: %v", err)
	}

	// An in-use policy can't be deleted.
	if err := m.DeleteSchedulingPolicy(ctx, p.ARN); !errors.IsFailedPrecondition(err) {
		t.Fatalf("delete in-use policy: want FailedPrecondition, got %v", err)
	}

	disabled := driver.StateDisabled
	if _, err := m.UpdateJobQueue(ctx, driver.UpdateJobQueueInput{Name: "fq", State: &disabled}); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteJobQueue(ctx, "fq"); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteSchedulingPolicy(ctx, p.ARN); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if list, _ := m.ListSchedulingPolicies(ctx); len(list) != 0 {
		t.Fatalf("policy not deleted: %+v", list)
	}

	if err := m.DeleteSchedulingPolicy(ctx, p.ARN); !errors.IsNotFound(err) {
		t.Fatalf("delete missing policy: want NotFound, got %v", err)
	}
}

func TestRegisterJobDefinitionTypeValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cases := map[string]driver.RegisterJobDefinitionInput{
		"unknown type":               {Name: "a", Type: "batch", ContainerProperties: json.RawMessage(`{"image":"x"}`)},
		"wrong case":                 {Name: "a", Type: "CONTAINER", ContainerProperties: json.RawMessage(`{"image":"x"}`)},
		"container without props":    {Name: "a", Type: driver.JDTypeContainer},
		"multinode without nodeprop": {Name: "a", Type: driver.JDTypeMultiNode, ContainerProperties: json.RawMessage(`{"image":"x"}`)},
	}

	for name, in := range cases {
		if _, err := m.RegisterJobDefinition(ctx, in); !errors.IsInvalidArgument(err) {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}

	if _, err := m.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
		Name: "mn", Type: driver.JDTypeMultiNode, NodeProperties: json.RawMessage(`{"numNodes":2,"mainNode":0}`),
	}); err != nil {
		t.Fatalf("valid multinode: %v", err)
	}

	if _, err := m.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
		Name: "eks", Type: driver.JDTypeContainer, EksProperties: json.RawMessage(`{"podProperties":{}}`),
	}); err != nil {
		t.Fatalf("valid eks container: %v", err)
	}
}

func TestJobsAndPoliciesSurviveSnapshot(t *testing.T) {
	src, fc := newAsyncMock(t)
	setupQueue(t, src)
	ctx := context.Background()

	if _, err := src.CreateSchedulingPolicy(ctx, driver.CreateSchedulingPolicyInput{Name: "sp"}); err != nil {
		t.Fatal(err)
	}

	j := submit(t, src, driver.SubmitJobInput{Tags: map[string]string{"t": "1"}})
	fc.Advance(time.Second)

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

	if got := describe(t, dst, j.JobID); got.Status != driver.JobStatusRunnable || got.Tags["t"] != "1" {
		t.Fatalf("restored job = %+v", got)
	}

	fc.Advance(5 * time.Second)

	if got := describe(t, dst, j.JobID); got.Status != driver.JobStatusSucceeded {
		t.Fatalf("restored job did not resume its lifecycle: %s", got.Status)
	}

	if list, _ := dst.ListSchedulingPolicies(ctx); len(list) != 1 {
		t.Fatalf("scheduling policy not restored: %+v", list)
	}

	// Sequence numbering continues after restore.
	k := submit(t, dst, driver.SubmitJobInput{JobName: "after"})

	all, _ := listJobs(dst, driver.ListJobsInput{JobQueue: "q", Filters: []driver.JobFilter{{Name: driver.FilterJobDefinition, Values: []string{"jd"}}}})
	if len(all) != 2 || all[0].JobID != k.JobID {
		t.Fatalf("post-restore ordering wrong: %+v", all)
	}
}
