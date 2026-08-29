package sfn_test

import (
	"context"
	stderrors "errors"
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

// exceptionOf returns the SFN exception name tagged on err, or "" if err is not
// a driver.APIError.
func exceptionOf(err error) string {
	var apiErr *driver.APIError
	if stderrors.As(err, &apiErr) {
		return apiErr.Exception
	}

	return ""
}

func TestCreateRequiresRoleArn(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	// Missing roleArn is InvalidArn (empty is not a valid IAM role ARN).
	_, _, _, err := m.CreateStateMachine(ctx, driver.CreateStateMachineInput{
		Name: "no-role", Definition: definition,
	})
	if ex := exceptionOf(err); ex != driver.ExInvalidArn {
		t.Fatalf("missing roleArn: want InvalidArn, got %q (err=%v)", ex, err)
	}

	// A malformed roleArn is also InvalidArn.
	_, _, _, err = m.CreateStateMachine(ctx, driver.CreateStateMachineInput{
		Name: "bad-role", Definition: definition, RoleArn: "arn:aws:states:::not-a-role",
	})
	if ex := exceptionOf(err); ex != driver.ExInvalidArn {
		t.Fatalf("malformed roleArn: want InvalidArn, got %q (err=%v)", ex, err)
	}

	// A valid IAM role ARN creates the machine.
	if _, _, _, err := m.CreateStateMachine(ctx, driver.CreateStateMachineInput{
		Name: "ok-role", Definition: definition, RoleArn: "arn:aws:iam::000000000000:role/svc",
	}); err != nil {
		t.Fatalf("valid roleArn should create, got %v", err)
	}
}

func TestUpdateRequiresUpdatableField(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "upd")

	before, err := m.DescribeStateMachine(ctx, arn)
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}
	rev0 := before.RevisionID

	// An update supplying none of the updatable fields is MissingRequiredParameter.
	_, err = m.UpdateStateMachine(ctx, driver.UpdateStateMachineInput{ARN: arn})
	if ex := exceptionOf(err); ex != driver.ExMissingRequiredParameter {
		t.Fatalf("empty update: want MissingRequiredParameter, got %q (err=%v)", ex, err)
	}

	// The rejected update must not bump the revision.
	after, err := m.DescribeStateMachine(ctx, arn)
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}
	if after.RevisionID != rev0 {
		t.Fatalf("rejected update bumped revision: %q -> %q", rev0, after.RevisionID)
	}

	// A valid update (new definition) succeeds and changes the revision.
	res, err := m.UpdateStateMachine(ctx, driver.UpdateStateMachineInput{
		ARN: arn, Definition: `{"StartAt":"Done","States":{"Done":{"Type":"Succeed"}}}`,
	})
	if err != nil {
		t.Fatalf("valid update should succeed, got %v", err)
	}
	if res.RevisionID == "" || res.RevisionID == rev0 {
		t.Fatalf("valid update should change revision, got %q (was %q)", res.RevisionID, rev0)
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
	arn := createSM(t, m, "dup")

	// A differing definition on the same name is a genuine collision.
	_, _, _, err := m.CreateStateMachine(context.Background(), driver.CreateStateMachineInput{
		Name: "dup", Definition: `{"StartAt":"Done","States":{"Done":{"Type":"Succeed"}}}`,
		RoleArn: "arn:aws:iam::000000000000:role/r",
	})
	if !errors.IsAlreadyExists(err) {
		t.Fatalf("duplicate name should be AlreadyExists, got %v", err)
	}

	// CreateStateMachine is idempotent: same name + same definition with a
	// different roleArn returns the existing machine (roleArn is ignored).
	got, _, _, err := m.CreateStateMachine(context.Background(), driver.CreateStateMachineInput{
		Name: "dup", Definition: definition, RoleArn: "arn:aws:iam::000000000000:role/other",
	})
	if err != nil {
		t.Fatalf("idempotent create should succeed, got %v", err)
	}

	if got != arn {
		t.Fatalf("idempotent create should return existing ARN %q, got %q", arn, got)
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

func TestRedriveExecution(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "redrive")

	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "r1"})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	res, err := m.RedriveExecution(ctx, exec.ARN)
	if err != nil || res == nil {
		t.Fatalf("RedriveExecution: %v %+v", err, res)
	}

	if _, err := m.RedriveExecution(ctx,
		"arn:aws:states:us-east-1:000000000000:execution:sm:missing"); !errors.IsNotFound(err) {
		t.Fatalf("redrive missing execution should be NotFound, got %v", err)
	}
}

func TestMapRuns(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	arn := createSM(t, m, "maprun")

	exec, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "mr1"})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	mapRunArn, err := m.SeedMapRun(exec.ARN, driver.MapRun{
		MaxConcurrency:  5,
		ExecutionCounts: driver.MapRunCounts{Total: 3, Succeeded: 3},
		ItemCounts:      driver.MapRunCounts{Total: 3, Succeeded: 3},
	})
	if err != nil {
		t.Fatalf("SeedMapRun: %v", err)
	}

	run, err := m.DescribeMapRun(ctx, mapRunArn)
	if err != nil || run.MaxConcurrency != 5 || run.ExecutionCounts.Total != 3 {
		t.Fatalf("DescribeMapRun: %v %+v", err, run)
	}

	runs, err := m.ListMapRuns(ctx, exec.ARN)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListMapRuns: %v len=%d", err, len(runs))
	}

	newMax := int32(10)
	if err := m.UpdateMapRun(ctx, driver.UpdateMapRunInput{MapRunArn: mapRunArn, MaxConcurrency: &newMax}); err != nil {
		t.Fatalf("UpdateMapRun: %v", err)
	}

	run, _ = m.DescribeMapRun(ctx, mapRunArn)
	if run.MaxConcurrency != 10 {
		t.Fatalf("UpdateMapRun did not apply: %+v", run)
	}

	if _, err := m.DescribeMapRun(ctx,
		"arn:aws:states:us-east-1:000000000000:mapRun:sm/e:missing"); !errors.IsNotFound(err) {
		t.Fatalf("describe missing map run should be NotFound, got %v", err)
	}
}

func TestTestState(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	// TestState evaluates a single state definition (not a whole machine).
	res, err := m.TestState(ctx, driver.TestStateInput{
		Definition: `{"Type":"Pass","End":true}`, Input: `{"a":1}`,
	})
	if err != nil {
		t.Fatalf("TestState: %v", err)
	}

	if res.Status != driver.TestStatusSucceeded || res.Output != `{"a":1}` {
		t.Fatalf("unexpected TestState result: %+v", res)
	}

	if _, err := m.TestState(ctx, driver.TestStateInput{}); !errors.IsInvalidArgument(err) {
		t.Fatalf("empty definition should be InvalidArgument, got %v", err)
	}
}

func TestValidateStateMachineDefinition(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	ok, err := m.ValidateStateMachineDefinition(ctx, definition, driver.TypeStandard)
	if err != nil || ok.Result != driver.ValidationResultOK || len(ok.Diagnostics) != 0 {
		t.Fatalf("valid definition: %v %+v", err, ok)
	}

	bad, err := m.ValidateStateMachineDefinition(ctx, "{not json", driver.TypeStandard)
	if err != nil || bad.Result != driver.ValidationResultFail || len(bad.Diagnostics) == 0 {
		t.Fatalf("invalid JSON: %v %+v", err, bad)
	}

	empty, _ := m.ValidateStateMachineDefinition(ctx, "", driver.TypeStandard)
	if empty.Result != driver.ValidationResultFail {
		t.Fatalf("empty definition should FAIL: %+v", empty)
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

// TestAsyncSettleExecution pins that under AsyncSettle an execution reports
// RUNNING (no stop date, no output) until the settle window elapses, then
// SUCCEEDED; and that StopExecution during the RUNNING window aborts it.
func TestAsyncSettleExecution(t *testing.T) {
	fc := config.NewFakeClock(time.Unix(0, 0))
	m := sfn.New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"), config.WithAsyncSettle()))
	ctx := context.Background()
	arn := createSM(t, m, "sm")

	start, err := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "e1", Input: "{}"})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	if start.Status != driver.ExecStatusRunning {
		t.Fatalf("start status = %q, want RUNNING", start.Status)
	}

	got, _ := m.DescribeExecution(ctx, start.ARN)
	if got.Status != driver.ExecStatusRunning || !got.StopDate.IsZero() || got.Output != "" {
		t.Fatalf("running describe = %+v, want RUNNING/no-stop/no-output", got)
	}

	fc.Advance(2 * time.Second) // past DefaultExecutionSettle (1s)
	got, _ = m.DescribeExecution(ctx, start.ARN)
	if got.Status != driver.ExecStatusSucceeded || got.StopDate.IsZero() {
		t.Fatalf("settled describe = %+v, want SUCCEEDED with stop date", got)
	}

	// Stop during the RUNNING window aborts.
	start2, _ := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "e2", Input: "{}"})
	if _, err := m.StopExecution(ctx, start2.ARN, "", ""); err != nil {
		t.Fatalf("StopExecution: %v", err)
	}
	got2, _ := m.DescribeExecution(ctx, start2.ARN)
	if got2.Status != driver.ExecStatusAborted {
		t.Fatalf("stopped status = %q, want ABORTED", got2.Status)
	}
}

// TestAsyncSettleSyncExecutionAndHistory pins that StartSyncExecution bypasses
// the settle overlay (returns the terminal SUCCEEDED result immediately), and
// that GetExecutionHistory omits the terminal event while an async execution is
// still observably RUNNING.
func TestAsyncSettleSyncExecutionAndHistory(t *testing.T) {
	fc := config.NewFakeClock(time.Unix(0, 0))
	m := sfn.New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"), config.WithAsyncSettle()))
	ctx := context.Background()
	arn := createSM(t, m, "sm")

	// Synchronous execution returns terminal SUCCEEDED with output, not RUNNING.
	sync, err := m.StartSyncExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "sync1", Input: `{"k":1}`})
	if err != nil {
		t.Fatalf("StartSyncExecution: %v", err)
	}
	if sync.Status != driver.ExecStatusSucceeded {
		t.Fatalf("sync status = %q, want SUCCEEDED", sync.Status)
	}
	if sync.Output == "" || sync.StopDate.IsZero() {
		t.Fatalf("sync execution missing output/stopDate: %+v", sync)
	}

	// Async execution: history has only ExecutionStarted while RUNNING.
	start, _ := m.StartExecution(ctx, driver.StartExecutionInput{StateMachineArn: arn, Name: "a1", Input: "{}"})
	hist, err := m.GetExecutionHistory(ctx, start.ARN, false)
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}
	if len(hist) != 1 || hist[0].Type != "ExecutionStarted" {
		t.Fatalf("running history = %+v, want only ExecutionStarted", hist)
	}

	fc.Advance(2 * time.Second)
	hist, _ = m.GetExecutionHistory(ctx, start.ARN, false)
	if len(hist) != 4 || hist[len(hist)-1].Type != "ExecutionSucceeded" {
		t.Fatalf("settled history len = %d, want 4 ending ExecutionSucceeded", len(hist))
	}
}
