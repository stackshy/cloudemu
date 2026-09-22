package aws_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	ecsdriver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	gluedriver "github.com/stackshy/cloudemu/v2/services/glue/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	psdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
	sfndriver "github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// testAccountID is the default account of a provider built with no options.
const testAccountID = "123456789012"

// lifecycleEvent is the EventBridge envelope a rule target receives.
type lifecycleEvent struct {
	Version    string          `json:"version"`
	ID         string          `json:"id"`
	DetailType string          `json:"detail-type"`
	Source     string          `json:"source"`
	Account    string          `json:"account"`
	Region     string          `json:"region"`
	Resources  []string        `json:"resources"`
	Detail     json.RawMessage `json:"detail"`
}

// captureEvents creates an SQS queue and a default-bus rule matching the given
// event pattern that targets it, returning a func draining the delivered
// events. This is exactly how a real user reacts to native AWS events.
func captureEvents(t *testing.T, p *aws.Provider, pattern string) func() []lifecycleEvent {
	t.Helper()

	ctx := context.Background()

	q, err := p.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "lifecycle-events"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := p.EventBridge.PutRule(ctx, &ebdriver.RuleConfig{Name: "native", EventPattern: pattern}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := p.EventBridge.PutTargets(ctx, "", "native", []ebdriver.Target{{ID: "q", ARN: q.ARN}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	return func() []lifecycleEvent {
		t.Helper()

		var out []lifecycleEvent

		for {
			msgs, err := p.SQS.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
			if err != nil {
				t.Fatalf("ReceiveMessages: %v", err)
			}

			if len(msgs) == 0 {
				return out
			}

			for _, msg := range msgs {
				var ev lifecycleEvent
				if err := json.Unmarshal([]byte(msg.Body), &ev); err != nil {
					t.Fatalf("event body is not JSON: %v: %s", err, msg.Body)
				}

				out = append(out, ev)

				if err := p.SQS.DeleteMessage(ctx, q.URL, msg.ReceiptHandle); err != nil {
					t.Fatalf("DeleteMessage: %v", err)
				}
			}
		}
	}
}

// detailOf decodes an event's detail into a generic map.
func detailOf(t *testing.T, ev *lifecycleEvent) map[string]any {
	t.Helper()

	var d map[string]any
	if err := json.Unmarshal(ev.Detail, &d); err != nil {
		t.Fatalf("detail is not a JSON object: %v", err)
	}

	return d
}

// requireEnvelope asserts the event carries the real envelope identity.
func requireEnvelope(t *testing.T, ev *lifecycleEvent, source, detailType string) {
	t.Helper()

	if ev.Source != source || ev.DetailType != detailType {
		t.Fatalf("event = %s / %s, want %s / %s", ev.Source, ev.DetailType, source, detailType)
	}

	if ev.Version != "0" || ev.ID == "" || ev.Account != testAccountID || ev.Region != config.DefaultRegion {
		t.Fatalf("bad envelope: %+v", ev)
	}
}

func TestEC2InstanceStateChangeEvents(t *testing.T) {
	p := aws.New()
	ctx := context.Background()
	drain := captureEvents(t, p,
		`{"source":["aws.ec2"],"detail-type":["EC2 Instance State-change Notification"]}`)

	insts, err := p.EC2.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-12345678", InstanceType: "t3.micro"}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := insts[0].ID

	if err := p.EC2.StopInstances(ctx, []string{id}); err != nil {
		t.Fatalf("StopInstances: %v", err)
	}

	if err := p.EC2.StartInstances(ctx, []string{id}); err != nil {
		t.Fatalf("StartInstances: %v", err)
	}

	// Reboot keeps the instance running: real EC2 emits no state change for it.
	if err := p.EC2.RebootInstances(ctx, []string{id}); err != nil {
		t.Fatalf("RebootInstances: %v", err)
	}

	if err := p.EC2.TerminateInstances(ctx, []string{id}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}

	events := drain()
	want := []string{"pending", "running", "stopping", "stopped", "pending", "running", "shutting-down", "terminated"}

	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d (%v)", len(events), len(want), want)
	}

	arn := "arn:aws:ec2:" + config.DefaultRegion + ":" + testAccountID + ":instance/" + id

	for i := range events {
		requireEnvelope(t, &events[i], "aws.ec2", "EC2 Instance State-change Notification")

		d := detailOf(t, &events[i])
		if d["instance-id"] != id || d["state"] != want[i] {
			t.Fatalf("event %d detail = %v, want instance-id=%s state=%s", i, d, id, want[i])
		}

		if len(events[i].Resources) != 1 || events[i].Resources[0] != arn {
			t.Fatalf("event %d resources = %v, want [%s]", i, events[i].Resources, arn)
		}
	}
}

func TestECSTaskAndServiceEvents(t *testing.T) {
	p := aws.New()
	ctx := context.Background()
	drain := captureEvents(t, p, `{"source":["aws.ecs"]}`)

	if _, err := p.ECS.CreateCluster(ctx, ecsdriver.CreateClusterInput{Name: "prod"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := p.ECS.RegisterTaskDefinition(ctx, ecsdriver.RegisterTaskDefinitionInput{
		Family: "web", ContainerDefinitions: []ecsdriver.ContainerDefinition{{Name: "c", Image: "nginx"}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	tasks, _, err := p.ECS.RunTask(ctx, ecsdriver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", LaunchType: "EXTERNAL"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("RunTask: %v (%d tasks)", err, len(tasks))
	}

	if _, err := p.ECS.StopTask(ctx, "prod", tasks[0].ARN, "done"); err != nil {
		t.Fatalf("StopTask: %v", err)
	}

	svc, err := p.ECS.CreateService(ctx, ecsdriver.CreateServiceInput{
		ServiceName: "api", Cluster: "prod", TaskDefinition: "web", DesiredCount: 0,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	events := drain()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (task RUNNING, task STOPPED, SERVICE_STEADY_STATE)", len(events))
	}

	for i, want := range []string{"RUNNING", "STOPPED"} {
		requireEnvelope(t, &events[i], "aws.ecs", "ECS Task State Change")

		d := detailOf(t, &events[i])
		if d["taskArn"] != tasks[0].ARN || d["lastStatus"] != want || d["clusterArn"] != tasks[0].ClusterARN {
			t.Fatalf("task event %d detail = %v, want lastStatus %s", i, d, want)
		}

		if events[i].Resources[0] != tasks[0].ARN {
			t.Fatalf("task event resources = %v", events[i].Resources)
		}
	}

	if d := detailOf(t, &events[1]); d["stoppedReason"] != "done" || d["desiredStatus"] != "STOPPED" {
		t.Fatalf("STOPPED detail = %v", d)
	}

	requireTaskTimeline(t, detailOf(t, &events[0]), detailOf(t, &events[1]))

	requireEnvelope(t, &events[2], "aws.ecs", "ECS Service Action")

	if d := detailOf(t, &events[2]); d["eventName"] != "SERVICE_STEADY_STATE" || d["eventType"] != "INFO" {
		t.Fatalf("service action detail = %v", d)
	}

	if events[2].Resources[0] != svc.ARN {
		t.Fatalf("service action resources = %v, want [%s]", events[2].Resources, svc.ARN)
	}
}

// requireTaskTimeline asserts the computed task fields of the launch and stop
// events: startedAt/connectivity/availabilityZone on RUNNING, stoppingAt and
// stoppedAt on STOPPED, and the container identity plus a STOPPED-only
// exitCode on containers[].
func requireTaskTimeline(t *testing.T, running, stopped map[string]any) {
	t.Helper()

	if running["startedAt"] == nil || running["connectivity"] != "CONNECTED" || running["availabilityZone"] != "us-east-1a" ||
		running["stoppedAt"] != nil {
		t.Fatalf("RUNNING timeline = %v", running)
	}

	if stopped["stoppingAt"] == nil || stopped["stoppedAt"] == nil || stopped["startedAt"] != running["startedAt"] {
		t.Fatalf("STOPPED timeline = %v", stopped)
	}

	rc := running["containers"].([]any)[0].(map[string]any)
	sc := stopped["containers"].([]any)[0].(map[string]any)

	arn, _ := rc["containerArn"].(string)
	if arn == "" || sc["containerArn"] != arn || rc["name"] != "c" || rc["lastStatus"] != "RUNNING" {
		t.Fatalf("RUNNING container = %v", rc)
	}

	if _, has := rc["exitCode"]; has {
		t.Fatalf("RUNNING container reports exitCode: %v", rc)
	}

	if sc["lastStatus"] != "STOPPED" || sc["exitCode"] == nil {
		t.Fatalf("STOPPED container = %v", sc)
	}
}

// TestECSDeregisterInstanceStopsTasksWithEvents pins that force-deregistering a
// container instance publishes a STOPPED event for each task it stops.
func TestECSDeregisterInstanceStopsTasksWithEvents(t *testing.T) {
	p := aws.New()
	ctx := context.Background()

	if _, err := p.ECS.CreateCluster(ctx, ecsdriver.CreateClusterInput{Name: "prod"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := p.ECS.RegisterTaskDefinition(ctx, ecsdriver.RegisterTaskDefinitionInput{
		Family: "web", ContainerDefinitions: []ecsdriver.ContainerDefinition{{Name: "c", Image: "nginx"}},
	}); err != nil {
		t.Fatalf("RegisterTaskDefinition: %v", err)
	}

	ci := p.ECS.SeedContainerInstance("prod", "i-deregister")

	tasks, _, err := p.ECS.RunTask(ctx, ecsdriver.RunTaskInput{Cluster: "prod", TaskDefinition: "web", Count: 2})
	if err != nil || len(tasks) != 2 {
		t.Fatalf("RunTask: %v (%d tasks)", err, len(tasks))
	}

	drain := captureEvents(t, p, `{"source":["aws.ecs"],"detail":{"lastStatus":["STOPPED"]}}`)

	if _, err := p.ECS.DeregisterContainerInstance(ctx, "prod", ci.ARN, true); err != nil {
		t.Fatalf("DeregisterContainerInstance: %v", err)
	}

	events := drain()
	if len(events) != 2 {
		t.Fatalf("got %d STOPPED events, want 2", len(events))
	}

	for i := range events {
		d := detailOf(t, &events[i])
		if d["stopCode"] != "TerminationNotice" || d["containerInstanceArn"] != ci.ARN || d["stoppedAt"] == nil {
			t.Fatalf("deregister STOPPED detail = %v", d)
		}
	}
}

func TestStepFunctionsExecutionStatusChangeEvents(t *testing.T) {
	p := aws.New()
	ctx := context.Background()
	drain := captureEvents(t, p,
		`{"source":["aws.states"],"detail-type":["Step Functions Execution Status Change"]}`)

	smArn, _, _, err := p.SFN.CreateStateMachine(ctx, sfndriver.CreateStateMachineInput{
		Name:       "flow",
		Definition: `{"StartAt":"Done","States":{"Done":{"Type":"Pass","Result":{"ok":true},"End":true}}}`,
		RoleArn:    "arn:aws:iam::123456789012:role/sfn",
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	exec, err := p.SFN.StartExecution(ctx, sfndriver.StartExecutionInput{StateMachineArn: smArn, Name: "run-1", Input: `{"a":1}`})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	events := drain()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (RUNNING, SUCCEEDED)", len(events))
	}

	for i, want := range []string{"RUNNING", "SUCCEEDED"} {
		requireEnvelope(t, &events[i], "aws.states", "Step Functions Execution Status Change")

		d := detailOf(t, &events[i])
		if d["executionArn"] != exec.ARN || d["stateMachineArn"] != smArn || d["status"] != want || d["name"] != "run-1" {
			t.Fatalf("event %d detail = %v, want status %s", i, d, want)
		}

		if events[i].Resources[0] != exec.ARN {
			t.Fatalf("resources = %v", events[i].Resources)
		}
	}

	running, done := detailOf(t, &events[0]), detailOf(t, &events[1])
	if running["stopDate"] != nil || running["output"] != nil || done["output"] != `{"ok":true}` || done["stopDate"] == nil {
		t.Fatalf("RUNNING=%v SUCCEEDED=%v", running, done)
	}

	requireRedrive(t, running, 0, "NOT_REDRIVABLE", "Execution is RUNNING and cannot be redriven")
	requireRedrive(t, done, 0, "NOT_REDRIVABLE", "Execution is SUCCEEDED and cannot be redriven")

	// A redrive publishes the redriven run's status changes, counting it.
	if _, err := p.SFN.RedriveExecution(ctx, exec.ARN); err != nil {
		t.Fatalf("RedriveExecution: %v", err)
	}

	redriven := drain()
	if len(redriven) != 2 {
		t.Fatalf("got %d events after redrive, want 2", len(redriven))
	}

	for i := range redriven {
		d := detailOf(t, &redriven[i])
		if d["redriveCount"] != float64(1) || d["redriveDate"] == nil {
			t.Fatalf("redriven event %d = %v, want redriveCount 1 and a redriveDate", i, d)
		}
	}
}

// requireRedrive asserts an execution event's redrive fields.
func requireRedrive(t *testing.T, d map[string]any, count float64, status string, reason any) {
	t.Helper()

	if d["redriveCount"] != count || d["redriveStatus"] != status || d["redriveStatusReason"] != reason {
		t.Fatalf("redrive fields = count %v status %v reason %v, want %v %v %v",
			d["redriveCount"], d["redriveStatus"], d["redriveStatusReason"], count, status, reason)
	}

	if _, present := d["redriveDate"]; !present || (count == 0 && d["redriveDate"] != nil) {
		t.Fatalf("redriveDate = %v (present %v)", d["redriveDate"], present)
	}
}

func TestECRImageActionEvents(t *testing.T) {
	p := aws.New()
	ctx := context.Background()
	drain := captureEvents(t, p, `{"source":["aws.ecr"],"detail-type":["ECR Image Action"]}`)

	if _, err := p.ECR.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "app"}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	img, err := p.ECR.PutImage(ctx, &crdriver.ImageManifest{Repository: "app", Tag: "v1", Manifest: `{"schemaVersion":2}`})
	if err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	if err := p.ECR.DeleteImage(ctx, "app", "v1"); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}

	events := drain()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (PUSH, DELETE)", len(events))
	}

	for i, action := range []string{"PUSH", "DELETE"} {
		requireEnvelope(t, &events[i], "aws.ecr", "ECR Image Action")

		d := detailOf(t, &events[i])
		if d["action-type"] != action || d["result"] != "SUCCESS" || d["repository-name"] != "app" ||
			d["image-digest"] != img.Digest || d["image-tag"] != "v1" {
			t.Fatalf("event %d detail = %v, want action %s", i, d, action)
		}
	}
}

func TestSSMParameterStoreChangeEvents(t *testing.T) {
	p := aws.New()
	ctx := context.Background()
	drain := captureEvents(t, p, `{"source":["aws.ssm"],"detail-type":["Parameter Store Change"]}`)

	put := psdriver.PutConfig{Name: "/app/db", Value: "v1", Type: "String", Description: "db host"}
	if _, _, err := p.SSM.PutParameter(ctx, put); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	put.Value, put.Overwrite = "v2", true
	if _, _, err := p.SSM.PutParameter(ctx, put); err != nil {
		t.Fatalf("PutParameter overwrite: %v", err)
	}

	if _, _, err := p.SSM.LabelParameterVersion(ctx, "/app/db", 2, []string{"prod"}); err != nil {
		t.Fatalf("LabelParameterVersion: %v", err)
	}

	if err := p.SSM.DeleteParameter(ctx, "/app/db"); err != nil {
		t.Fatalf("DeleteParameter: %v", err)
	}

	events := drain()
	want := []string{"Create", "Update", "LabelParameterVersion", "Delete"}

	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}

	arn := "arn:aws:ssm:" + config.DefaultRegion + ":" + testAccountID + ":parameter/app/db"

	for i := range events {
		requireEnvelope(t, &events[i], "aws.ssm", "Parameter Store Change")

		d := detailOf(t, &events[i])
		if d["operation"] != want[i] || d["name"] != "/app/db" || d["type"] != "String" || d["description"] != "db host" {
			t.Fatalf("event %d detail = %v, want operation %s", i, d, want[i])
		}

		if len(events[i].Resources) != 1 || events[i].Resources[0] != arn {
			t.Fatalf("resources = %v, want [%s]", events[i].Resources, arn)
		}
	}
}

func TestGlueJobAndCrawlerStateChangeEvents(t *testing.T) {
	p := aws.New()
	ctx := context.Background()
	drain := captureEvents(t, p, `{"source":["aws.glue"]}`)

	role := "arn:aws:iam::123456789012:role/glue"

	if _, err := p.Glue.CreateJob(ctx, gluedriver.Job{
		Name: "etl", Role: role, Command: map[string]any{"Name": "glueetl", "ScriptLocation": "s3://b/s.py"},
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	runID, err := p.Glue.StartJobRun(ctx, "etl", nil)
	if err != nil {
		t.Fatalf("StartJobRun: %v", err)
	}

	if err := p.Glue.CreateCrawler(ctx, gluedriver.Crawler{
		Name: "crawl", Role: role, DatabaseName: "db",
		Targets: map[string]any{"S3Targets": []any{map[string]any{"Path": "s3://b/data"}}},
	}); err != nil {
		t.Fatalf("CreateCrawler: %v", err)
	}

	if err := p.Glue.StartCrawler(ctx, "crawl"); err != nil {
		t.Fatalf("StartCrawler: %v", err)
	}

	events := drain()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (job SUCCEEDED, crawler Started, crawler Succeeded)", len(events))
	}

	requireEnvelope(t, &events[0], "aws.glue", "Glue Job State Change")

	if d := detailOf(t, &events[0]); d["jobName"] != "etl" || d["jobRunId"] != runID ||
		d["state"] != "SUCCEEDED" || d["severity"] != "INFO" {
		t.Fatalf("job detail = %v", d)
	}

	for i, state := range []string{"Started", "Succeeded"} {
		ev := &events[i+1]
		requireEnvelope(t, ev, "aws.glue", "Glue Crawler State Change")

		if d := detailOf(t, ev); d["crawlerName"] != "crawl" || d["state"] != state {
			t.Fatalf("crawler event detail = %v, want state %s", d, state)
		}
	}
}

// TestLifecycleEventsPatternMismatchNotDelivered pins that native events route
// through ordinary rule matching: a rule for a different source receives none.
func TestLifecycleEventsPatternMismatchNotDelivered(t *testing.T) {
	p := aws.New()
	ctx := context.Background()
	drain := captureEvents(t, p, `{"source":["aws.ecr"]}`)

	if _, _, err := p.SSM.PutParameter(ctx, psdriver.PutConfig{Name: "p", Value: "v", Type: "String"}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if events := drain(); len(events) != 0 {
		t.Fatalf("got %d events for a non-matching rule, want 0", len(events))
	}
}

// passMachine is a one-state STANDARD workflow that succeeds immediately.
const passMachine = `{"StartAt":"Done","States":{"Done":{"Type":"Pass","End":true}}}`

// TestStepFunctionsExpressAndAbortEvents pins the two non-default paths: an
// EXPRESS workflow publishes no status-change events (real Step Functions
// emits them for STANDARD only), and aborting a still-running STANDARD
// execution publishes ABORTED.
func TestStepFunctionsExpressAndAbortEvents(t *testing.T) {
	p := aws.New(config.WithAsyncSettle())
	ctx := context.Background()
	drain := captureEvents(t, p, `{"source":["aws.states"]}`)

	expressArn, _, _, err := p.SFN.CreateStateMachine(ctx, sfndriver.CreateStateMachineInput{
		Name: "fast", Definition: passMachine, RoleArn: "arn:aws:iam::123456789012:role/sfn", Type: "EXPRESS",
	})
	if err != nil {
		t.Fatalf("CreateStateMachine EXPRESS: %v", err)
	}

	if _, err := p.SFN.StartExecution(ctx, sfndriver.StartExecutionInput{StateMachineArn: expressArn}); err != nil {
		t.Fatalf("StartExecution EXPRESS: %v", err)
	}

	if events := drain(); len(events) != 0 {
		t.Fatalf("EXPRESS execution published %d events, want 0", len(events))
	}

	stdArn, _, _, err := p.SFN.CreateStateMachine(ctx, sfndriver.CreateStateMachineInput{
		Name: "slow", Definition: passMachine, RoleArn: "arn:aws:iam::123456789012:role/sfn",
	})
	if err != nil {
		t.Fatalf("CreateStateMachine STANDARD: %v", err)
	}

	exec, err := p.SFN.StartExecution(ctx, sfndriver.StartExecutionInput{StateMachineArn: stdArn})
	if err != nil {
		t.Fatalf("StartExecution STANDARD: %v", err)
	}

	if _, err := p.SFN.StopExecution(ctx, exec.ARN, "Cancelled", "user"); err != nil {
		t.Fatalf("StopExecution: %v", err)
	}

	events := drain()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (RUNNING, ABORTED)", len(events))
	}

	aborted := detailOf(t, &events[1])
	if detailOf(t, &events[0])["status"] != "RUNNING" || aborted["status"] != "ABORTED" ||
		aborted["error"] != "Cancelled" || aborted["cause"] != "user" {
		t.Fatalf("events = %v / %v", detailOf(t, &events[0]), aborted)
	}

	// An aborted STANDARD execution is redrivable, so no reason is given.
	requireRedrive(t, aborted, 0, "REDRIVABLE", nil)
}

// TestLifecycleEventLoopIsBounded pins the recursion guard on service events:
// a rule whose target re-triggers the event's producer (every RUNNING
// execution starts another) is synchronous in-process, so without the guard it
// would recurse until the goroutine stack overflows.
func TestLifecycleEventLoopIsBounded(t *testing.T) {
	p := aws.New()
	ctx := context.Background()

	smArn, _, _, err := p.SFN.CreateStateMachine(ctx, sfndriver.CreateStateMachineInput{
		Name: "loop", Definition: passMachine, RoleArn: "arn:aws:iam::123456789012:role/sfn",
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	if _, err := p.EventBridge.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "restart", EventPattern: `{"source":["aws.states"],"detail":{"status":["RUNNING"]}}`,
	}); err != nil {
		t.Fatalf("PutRule: %v", err)
	}

	if err := p.EventBridge.PutTargets(ctx, "", "restart", []ebdriver.Target{{ID: "sm", ARN: smArn}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}

	if _, err := p.SFN.StartExecution(ctx, sfndriver.StartExecutionInput{StateMachineArn: smArn}); err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	execs, err := p.SFN.ListExecutions(ctx, smArn, "")
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}

	// The caller's execution plus one per permitted publish hop.
	if want := recursionguard.MaxDepth + 1; len(execs) != want {
		t.Fatalf("got %d executions, want %d", len(execs), want)
	}
}

// TestServicesWithoutEventsWiredStillWork pins the nil-safe seam: a service
// mock constructed standalone (no EventBridge wired) runs its lifecycle
// exactly as before.
func TestServicesWithoutEventsWiredStillWork(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()

	params := ssm.New(opts)
	if _, _, err := params.PutParameter(ctx, psdriver.PutConfig{Name: "p", Value: "v", Type: "String"}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if err := params.DeleteParameter(ctx, "p"); err != nil {
		t.Fatalf("DeleteParameter: %v", err)
	}

	compute := ec2.New(opts)

	insts, err := compute.RunInstances(ctx, computedriver.InstanceConfig{ImageID: "ami-12345678", InstanceType: "t3.micro"}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	if err := compute.TerminateInstances(ctx, []string{insts[0].ID}); err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}
}
