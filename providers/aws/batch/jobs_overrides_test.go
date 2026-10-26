package batch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/batch"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func registerJD(t *testing.T, m *batch.Mock, in driver.RegisterJobDefinitionInput) {
	t.Helper()

	if _, err := m.RegisterJobDefinition(context.Background(), in); err != nil {
		t.Fatalf("register %s: %v", in.Name, err)
	}
}

func decodeMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}

	return out
}

func TestSubmitJobAppliesEcsPropertiesOverride(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "ecs", Type: driver.JDTypeContainer,
		EcsProperties: json.RawMessage(`{"taskProperties":[{"executionRoleArn":"role","containers":[
			{"name":"app","image":"app:1","command":["run"],"environment":[{"name":"A","value":"1"},{"name":"B","value":"2"}]},
			{"name":"side","image":"side:1","command":["tail"]}]}]}`),
	})

	j := submit(t, m, driver.SubmitJobInput{
		JobDefinition: "ecs",
		EcsPropertiesOverride: json.RawMessage(`{"taskProperties":[{"containers":[
			{"name":"app","command":["run","--fast"],"environment":[{"name":"B","value":"20"},{"name":"C","value":"3"}]}]}]}`),
	})

	got := decodeMap(t, describe(t, m, j.JobID).EcsProperties)
	task := got["taskProperties"].([]any)[0].(map[string]any)

	if task["executionRoleArn"] != "role" {
		t.Fatalf("task-level properties lost: %v", task)
	}

	containers := task["containers"].([]any)
	app := containers[0].(map[string]any)
	side := containers[1].(map[string]any)

	if fmt.Sprint(app["command"]) != "[run --fast]" || app["image"] != "app:1" {
		t.Fatalf("app command not overridden or image lost: %v", app)
	}

	if fmt.Sprint(app["environment"]) != "[map[name:A value:1] map[name:B value:20] map[name:C value:3]]" {
		t.Fatalf("app environment not merged by name: %v", app["environment"])
	}

	if fmt.Sprint(side["command"]) != "[tail]" {
		t.Fatalf("container that wasn't overridden changed: %v", side)
	}

	// The job definition itself is untouched.
	j2 := submit(t, m, driver.SubmitJobInput{JobDefinition: "ecs"})
	if c := decodeMap(t, describe(t, m, j2.JobID).EcsProperties)["taskProperties"].([]any)[0].(map[string]any)["containers"].([]any)[0].(map[string]any); fmt.Sprint(c["command"]) != "[run]" {
		t.Fatalf("override leaked into the job definition: %v", c)
	}
}

func TestSubmitJobAppliesEksPropertiesOverride(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "eks", Type: driver.JDTypeContainer,
		EksProperties: json.RawMessage(`{"podProperties":{"hostNetwork":true,
			"containers":[{"name":"main","image":"img:1","command":["sleep"],"env":[{"name":"X","value":"1"}]}],
			"metadata":{"labels":{"team":"a","env":"dev"}}}}`),
	})

	j := submit(t, m, driver.SubmitJobInput{
		JobDefinition: "eks",
		EksPropertiesOverride: json.RawMessage(`{"podProperties":{
			"containers":[{"image":"img:2","args":["60"],"env":[{"name":"Y","value":"2"}]}],
			"metadata":{"labels":{"env":"prod"}}}}`),
	})

	pod := decodeMap(t, describe(t, m, j.JobID).EksProperties)["podProperties"].(map[string]any)
	c := pod["containers"].([]any)[0].(map[string]any)

	if c["image"] != "img:2" || fmt.Sprint(c["args"]) != "[60]" || fmt.Sprint(c["command"]) != "[sleep]" {
		t.Fatalf("unnamed override on the only container not applied: %v", c)
	}

	if fmt.Sprint(c["env"]) != "[map[name:X value:1] map[name:Y value:2]]" {
		t.Fatalf("env not merged: %v", c["env"])
	}

	labels := pod["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["team"] != "a" || labels["env"] != "prod" || pod["hostNetwork"] != true {
		t.Fatalf("metadata labels not merged or pod settings lost: %v", pod)
	}
}

func TestSubmitJobAppliesNodeOverrides(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)
	setupEC2Queue(t, m)

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "mnp", Type: driver.JDTypeMultiNode,
		NodeProperties: json.RawMessage(`{"numNodes":2,"mainNode":0,"nodeRangeProperties":[
			{"targetNodes":"0","container":{"image":"main","command":["lead"]}},
			{"targetNodes":"1:","container":{"image":"worker","command":["work"]}}]}`),
	})

	j := submit(t, m, driver.SubmitJobInput{
		JobDefinition: "mnp", JobQueue: ec2Queue,
		NodeOverrides: json.RawMessage(`{"numNodes":4,"nodePropertyOverrides":[
			{"targetNodes":"1:","containerOverrides":{"command":["work","--hard"]},"instanceTypes":["c5.large"]}]}`),
	})

	np := decodeMap(t, describe(t, m, j.JobID).NodeProperties)
	if np["numNodes"] != float64(4) {
		t.Fatalf("numNodes not overridden: %v", np)
	}

	ranges := np["nodeRangeProperties"].([]any)
	lead := ranges[0].(map[string]any)["container"].(map[string]any)
	worker := ranges[1].(map[string]any)

	if fmt.Sprint(lead["command"]) != "[lead]" {
		t.Fatalf("range outside targetNodes changed: %v", lead)
	}

	if wc := worker["container"].(map[string]any); fmt.Sprint(wc["command"]) != "[work --hard]" || wc["image"] != "worker" {
		t.Fatalf("worker container override not merged: %v", wc)
	}

	if fmt.Sprint(worker["instanceTypes"]) != "[c5.large]" {
		t.Fatalf("instanceTypes not overridden: %v", worker)
	}
}

func TestSubmitJobRejectsUnusableOverrides(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)
	setupEC2Queue(t, m)
	ctx := context.Background()

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "ecs2", Type: driver.JDTypeContainer,
		EcsProperties: json.RawMessage(`{"taskProperties":[{"containers":[{"name":"a","image":"a"},{"name":"b","image":"b"}]}]}`),
	})
	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "mnp", Type: driver.JDTypeMultiNode,
		NodeProperties: json.RawMessage(`{"numNodes":2,"mainNode":1,"nodeRangeProperties":[{"targetNodes":"0:1","container":{"image":"x"}}]}`),
	})

	cases := map[string]driver.SubmitJobInput{
		"ecs override on a containerProperties definition": {JobDefinition: "jd",
			EcsPropertiesOverride: json.RawMessage(`{"taskProperties":[{"containers":[{"command":["x"]}]}]}`)},
		"eks override on an ECS definition": {JobDefinition: "ecs2",
			EksPropertiesOverride: json.RawMessage(`{"podProperties":{"containers":[{"image":"x"}]}}`)},
		"node override on a container definition": {JobDefinition: "jd",
			NodeOverrides: json.RawMessage(`{"numNodes":3}`)},
		"unknown container name": {JobDefinition: "ecs2",
			EcsPropertiesOverride: json.RawMessage(`{"taskProperties":[{"containers":[{"name":"zzz","command":["x"]}]}]}`)},
		"unnamed override with two containers": {JobDefinition: "ecs2",
			EcsPropertiesOverride: json.RawMessage(`{"taskProperties":[{"containers":[{"command":["x"]}]}]}`)},
		"numNodes without an open-ended range": {JobDefinition: "mnp",
			NodeOverrides: json.RawMessage(`{"numNodes":3}`)},
		"targetNodes covering no range": {JobDefinition: "mnp",
			NodeOverrides: json.RawMessage(`{"nodePropertyOverrides":[{"targetNodes":"5:6","containerOverrides":{"command":["x"]}}]}`)},
	}

	for name, in := range cases {
		// An EC2 queue, so the override rules (not the Fargate platform
		// rule) are what rejects each case.
		in.JobName, in.JobQueue = "j", ec2Queue
		if _, err := m.SubmitJob(ctx, in); !errors.IsInvalidArgument(err) {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}
}

func TestSubmitJobRejectsInactiveRevision(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)
	ctx := context.Background()

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "jd", Type: driver.JDTypeContainer, ContainerProperties: json.RawMessage(`{"image":"v2"}`),
	})

	if err := m.DeregisterJobDefinition(ctx, "jd:1"); err != nil {
		t.Fatal(err)
	}

	for _, ref := range []string{"jd:1", "arn:aws:batch:us-east-1:000000000000:job-definition/jd:1"} {
		_, err := m.SubmitJob(ctx, driver.SubmitJobInput{JobName: "j", JobQueue: "q", JobDefinition: ref})
		if !errors.IsInvalidArgument(err) {
			t.Fatalf("submit with INACTIVE %s: want InvalidArgument, got %v", ref, err)
		}
	}

	// The ACTIVE revision still works by explicit revision.
	submit(t, m, driver.SubmitJobInput{JobDefinition: "jd:2"})
}

func TestReplayedEventsCarryNoExitCode(t *testing.T) {
	m := newMock(t)
	pub := &recordingPublisher{}
	m.SetEventPublisher(pub)
	setupQueue(t, m)

	submit(t, m, driver.SubmitJobInput{})

	pub.mu.Lock()
	defer pub.mu.Unlock()

	if len(pub.events) != 4 {
		t.Fatalf("want 4 events, got %d", len(pub.events))
	}

	for _, e := range pub.events {
		c, _ := e.detail["container"].(map[string]any)
		if c == nil || c["image"] != "busybox" {
			t.Fatalf("%s event lost the container: %v", e.detail["status"], e.detail)
		}

		_, hasExit := c["exitCode"]
		if want := e.detail["status"] == driver.JobStatusSucceeded; hasExit != want {
			t.Fatalf("%s event exitCode present = %v, want %v", e.detail["status"], hasExit, want)
		}
	}
}

func TestListArrayChildrenAppliesFilters(t *testing.T) {
	m := newMock(t)
	setupQueue(t, m)

	size := int32(3)
	parent := submit(t, m, driver.SubmitJobInput{JobName: "arr", ArraySize: &size})

	nomatch, err := listJobs(m, driver.ListJobsInput{
		ArrayJobID: parent.JobID,
		Filters:    []driver.JobFilter{{Name: driver.FilterJobName, Values: []string{"nomatch"}}},
	})
	if err != nil || len(nomatch) != 0 {
		t.Fatalf("non-matching JOB_NAME filter listed children: %v %v", nomatch, err)
	}

	match, err := listJobs(m, driver.ListJobsInput{
		ArrayJobID: parent.JobID,
		Filters:    []driver.JobFilter{{Name: driver.FilterJobName, Values: []string{"ARR*"}}},
	})
	if err != nil || len(match) != 3 {
		t.Fatalf("matching JOB_NAME filter: got %d children, %v", len(match), err)
	}
}

// TestSchedulingPolicyBindingIsAtomic races queue creation/update against
// policy deletion: no queue may end up bound to a deleted policy.
func TestSchedulingPolicyBindingIsAtomic(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	mustCreateCE(t, m, "ce")

	order := []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "ce"}}

	for i := 0; i < 50; i++ {
		pol := fmt.Sprintf("p%d", i)
		spare := fmt.Sprintf("s%d", i)

		for _, n := range []string{pol, spare} {
			if _, err := m.CreateSchedulingPolicy(ctx, driver.CreateSchedulingPolicyInput{Name: n}); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
			Name: "u" + pol, Priority: 1, ComputeEnvironmentOrder: order, SchedulingPolicyARN: spare,
		}); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup

		wg.Add(3)

		go func() {
			defer wg.Done()

			_, _ = m.CreateJobQueue(ctx, driver.CreateJobQueueInput{
				Name: "c" + pol, Priority: 1, ComputeEnvironmentOrder: order, SchedulingPolicyARN: pol,
			})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.UpdateJobQueue(ctx, driver.UpdateJobQueueInput{Name: "u" + pol, SchedulingPolicyARN: &pol})
		}()

		go func() {
			defer wg.Done()

			_ = m.DeleteSchedulingPolicy(ctx, pol)
		}()

		wg.Wait()
	}

	queues, _ := m.DescribeJobQueues(ctx, nil)
	for i := range queues {
		q := &queues[i]
		if q.SchedulingPolicyARN == "" {
			continue
		}

		if got, _ := m.DescribeSchedulingPolicies(ctx, []string{q.SchedulingPolicyARN}); len(got) != 1 {
			t.Fatalf("queue %s is bound to deleted policy %s", q.Name, q.SchedulingPolicyARN)
		}
	}
}

func TestMultinodeJobDefinitionEchoesNoDefaultPlatform(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "mnp", Type: driver.JDTypeMultiNode,
		NodeProperties: json.RawMessage(`{"numNodes":2,"mainNode":0,"nodeRangeProperties":[{"targetNodes":"0:","container":{"image":"x"}}]}`),
	})
	registerJD(t, m, driver.RegisterJobDefinitionInput{
		Name: "fg", Type: driver.JDTypeContainer, PlatformCapabilities: []string{"FARGATE"},
		ContainerProperties: json.RawMessage(`{"image":"x"}`),
	})

	defs, err := m.DescribeJobDefinitions(ctx, driver.DescribeJobDefinitionsInput{})
	if err != nil || len(defs) != 2 {
		t.Fatalf("describe: %v %v", defs, err)
	}

	for i := range defs {
		d := &defs[i]

		switch d.Name {
		case "mnp":
			if len(d.PlatformCapabilities) != 0 {
				t.Fatalf("multinode definition echoes a default platform: %v", d.PlatformCapabilities)
			}
		case "fg":
			if fmt.Sprint(d.PlatformCapabilities) != "[FARGATE]" {
				t.Fatalf("explicit platform lost: %v", d.PlatformCapabilities)
			}
		}
	}
}
