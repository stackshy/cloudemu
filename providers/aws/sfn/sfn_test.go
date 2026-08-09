package sfn_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/sfn"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

const definition = `{"StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`

func newMock(t *testing.T) *sfn.Mock {
	t.Helper()

	return sfn.New(config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	))
}

func createSM(t *testing.T, m *sfn.Mock, name string) string {
	t.Helper()

	arn, _, _, err := m.CreateStateMachine(context.Background(), driver.CreateStateMachineInput{
		Name: name, Definition: definition, RoleArn: "arn:aws:iam::000000000000:role/r",
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	return arn
}

func TestCreateRequiresNameAndDefinition(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, _, err := m.CreateStateMachine(ctx, driver.CreateStateMachineInput{Definition: definition}); !errors.IsInvalidArgument(err) {
		t.Fatalf("missing name should be InvalidArgument, got %v", err)
	}

	if _, _, _, err := m.CreateStateMachine(ctx, driver.CreateStateMachineInput{Name: "x"}); !errors.IsInvalidArgument(err) {
		t.Fatalf("missing definition should be InvalidArgument, got %v", err)
	}
}

func TestCreateDescribeStateMachine(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "hello")

	if !strings.Contains(arn, ":states:") || !strings.Contains(arn, "stateMachine:hello") {
		t.Fatalf("unexpected ARN: %s", arn)
	}

	sm, err := m.DescribeStateMachine(ctx, arn)
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}

	if sm.Definition != definition {
		t.Fatalf("definition not stored verbatim: %q", sm.Definition)
	}

	if sm.Status != driver.SMStatusActive || sm.Type != driver.TypeStandard {
		t.Fatalf("unexpected status/type: %s/%s", sm.Status, sm.Type)
	}
}

func TestCreateDuplicateNameFails(t *testing.T) {
	m := newMock(t)
	createSM(t, m, "dup")

	_, _, _, err := m.CreateStateMachine(context.Background(), driver.CreateStateMachineInput{
		Name: "dup", Definition: definition,
	})
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate name should be AlreadyExists, got %v", err)
	}
}

func TestDescribeMissingStateMachine(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeStateMachine(context.Background(),
		"arn:aws:states:us-east-1:000000000000:stateMachine:nope")
	if !errors.IsNotFound(err) {
		t.Fatalf("missing SM should be NotFound, got %v", err)
	}
}

func TestStartExecutionSucceedsAndEchoesInput(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "run")

	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{
		StateMachineArn: arn, Name: "e1", Input: `{"a":1}`,
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	if exec.Status != driver.ExecStatusSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", exec.Status)
	}

	if exec.Output != `{"a":1}` {
		t.Fatalf("output = %q, want echoed input", exec.Output)
	}

	got, err := m.DescribeExecution(ctx, exec.ARN)
	if err != nil {
		t.Fatalf("DescribeExecution: %v", err)
	}

	if got.Status != driver.ExecStatusSucceeded {
		t.Fatalf("described status = %s", got.Status)
	}
}

func TestStartExecutionMissingStateMachine(t *testing.T) {
	m := newMock(t)

	_, err := m.StartExecution(context.Background(), driver.StartExecutionInput{
		StateMachineArn: "arn:aws:states:us-east-1:000000000000:stateMachine:ghost",
	})
	if !errors.IsNotFound(err) {
		t.Fatalf("missing SM should be NotFound, got %v", err)
	}
}

func TestDuplicateExecutionName(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "dupexec")

	if _, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "same"}); err != nil {
		t.Fatalf("first StartExecution: %v", err)
	}

	_, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "same"})
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate execution name should be AlreadyExists, got %v", err)
	}
}

func TestGetExecutionHistory(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "hist")

	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "h1", Input: "{}"})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	events, err := m.GetExecutionHistory(ctx, exec.ARN, false)
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	if len(events) == 0 || events[0].Type != "ExecutionStarted" {
		t.Fatalf("first event = %+v", events)
	}

	if events[len(events)-1].Type != "ExecutionSucceeded" {
		t.Fatalf("last event = %s, want ExecutionSucceeded", events[len(events)-1].Type)
	}

	rev, err := m.GetExecutionHistory(ctx, exec.ARN, true)
	if err != nil {
		t.Fatalf("GetExecutionHistory reverse: %v", err)
	}

	if rev[0].Type != "ExecutionSucceeded" {
		t.Fatalf("reversed first event = %s", rev[0].Type)
	}
}

func TestListAndDeleteStateMachines(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	a := createSM(t, m, "a")
	createSM(t, m, "b")

	list, err := m.ListStateMachines(ctx)
	if err != nil {
		t.Fatalf("ListStateMachines: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("want 2 machines, got %d", len(list))
	}

	if err := m.DeleteStateMachine(ctx, a); err != nil {
		t.Fatalf("DeleteStateMachine: %v", err)
	}

	list, _ = m.ListStateMachines(ctx)
	if len(list) != 1 {
		t.Fatalf("after delete want 1, got %d", len(list))
	}
}

func TestStopExecution(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "stop")

	exec, _ := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "s1"})

	if _, err := m.StopExecution(ctx, exec.ARN, "Halt", "manual"); err != nil {
		t.Fatalf("StopExecution: %v", err)
	}
}

func TestVersionsAndAliases(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "vers")

	versionArn, _, err := m.PublishStateMachineVersion(ctx, arn, "v1")
	if err != nil {
		t.Fatalf("PublishStateMachineVersion: %v", err)
	}

	versions, err := m.ListStateMachineVersions(ctx, arn)
	if err != nil || len(versions) != 1 {
		t.Fatalf("ListStateMachineVersions: %v len=%d", err, len(versions))
	}

	aliasArn, _, err := m.CreateStateMachineAlias(ctx, "live", "prod",
		[]driver.RouteEntry{{StateMachineVersionArn: versionArn, Weight: 100}})
	if err != nil {
		t.Fatalf("CreateStateMachineAlias: %v", err)
	}

	alias, err := m.DescribeStateMachineAlias(ctx, aliasArn)
	if err != nil || alias.Name != "live" {
		t.Fatalf("DescribeStateMachineAlias: %v %+v", err, alias)
	}

	aliases, err := m.ListStateMachineAliases(ctx, arn)
	if err != nil || len(aliases) != 1 {
		t.Fatalf("ListStateMachineAliases: %v len=%d", err, len(aliases))
	}

	if err := m.DeleteStateMachineAlias(ctx, aliasArn); err != nil {
		t.Fatalf("DeleteStateMachineAlias: %v", err)
	}
}

func TestActivities(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	arn, _, err := m.CreateActivity(ctx, "worker", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("CreateActivity: %v", err)
	}

	if _, _, err := m.CreateActivity(ctx, "worker", nil); !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate activity should be AlreadyExists, got %v", err)
	}

	act, err := m.DescribeActivity(ctx, arn)
	if err != nil || act.Name != "worker" {
		t.Fatalf("DescribeActivity: %v %+v", err, act)
	}

	acts, err := m.ListActivities(ctx)
	if err != nil || len(acts) != 1 {
		t.Fatalf("ListActivities: %v len=%d", err, len(acts))
	}

	// No task is ever scheduled: GetActivityTask returns an empty token.
	token, _, err := m.GetActivityTask(ctx, arn, "w")
	if err != nil || token != "" {
		t.Fatalf("GetActivityTask: token=%q err=%v", token, err)
	}

	if err := m.SendTaskHeartbeat(ctx, "bogus"); !errors.IsInvalidArgument(err) {
		t.Fatalf("unknown task token should be InvalidArgument, got %v", err)
	}

	if err := m.DeleteActivity(ctx, arn); err != nil {
		t.Fatalf("DeleteActivity: %v", err)
	}
}

func TestTags(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "tagged")

	if err := m.TagResource(ctx, arn, map[string]string{"team": "platform"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := m.ListTagsForResource(ctx, arn)
	if err != nil || tags["team"] != "platform" {
		t.Fatalf("ListTagsForResource: %v %+v", err, tags)
	}

	if err := m.UntagResource(ctx, arn, []string{"team"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, arn)
	if len(tags) != 0 {
		t.Fatalf("tags not removed: %+v", tags)
	}
}

func TestInvalidArnFormats(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.DescribeStateMachine(ctx, "not-an-arn"); !errors.IsInvalidArgument(err) {
		t.Fatalf("malformed SM ARN should be InvalidArgument, got %v", err)
	}

	if _, err := m.DescribeExecution(ctx, "not-an-arn"); !errors.IsInvalidArgument(err) {
		t.Fatalf("malformed execution ARN should be InvalidArgument, got %v", err)
	}
}
