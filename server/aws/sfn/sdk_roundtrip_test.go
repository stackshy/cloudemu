package sfn_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const definition = `{"StartAt":"Pass","States":{"Pass":{"Type":"Pass","End":true}}}`

func newSFNClient(t *testing.T) *awssfn.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{SFN: cloud.SFN})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awssfn.NewFromConfig(cfg, func(o *awssfn.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createSM(t *testing.T, c *awssfn.Client, name string) string {
	t.Helper()

	out, err := c.CreateStateMachine(context.Background(), &awssfn.CreateStateMachineInput{
		Name:       aws.String(name),
		Definition: aws.String(definition),
		RoleArn:    aws.String("arn:aws:iam::123456789012:role/svc"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	return aws.ToString(out.StateMachineArn)
}

func TestSDKStateMachineLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	arn := createSM(t, c, "hello")
	if !strings.Contains(arn, ":states:") {
		t.Fatalf("unexpected ARN: %s", arn)
	}

	desc, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}

	if aws.ToString(desc.Definition) != definition {
		t.Fatalf("definition not round-tripped: %q", aws.ToString(desc.Definition))
	}

	if desc.Status != sfntypes.StateMachineStatusActive || desc.Type != sfntypes.StateMachineTypeStandard {
		t.Fatalf("unexpected status/type: %s/%s", desc.Status, desc.Type)
	}
}

func TestSDKExecutionLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "run")

	start, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arn), Name: aws.String("e1"), Input: aws.String(`{"a":1}`),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	execArn := aws.ToString(start.ExecutionArn)

	de, err := c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{ExecutionArn: aws.String(execArn)})
	if err != nil {
		t.Fatalf("DescribeExecution: %v", err)
	}

	if de.Status != sfntypes.ExecutionStatusSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", de.Status)
	}

	if aws.ToString(de.Output) != `{"a":1}` {
		t.Fatalf("output = %q, want echoed input", aws.ToString(de.Output))
	}

	hist, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{ExecutionArn: aws.String(execArn)})
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	if len(hist.Events) == 0 || hist.Events[0].Type != sfntypes.HistoryEventTypeExecutionStarted {
		t.Fatalf("unexpected first event: %+v", hist.Events)
	}

	last := hist.Events[len(hist.Events)-1]
	if last.Type != sfntypes.HistoryEventTypeExecutionSucceeded {
		t.Fatalf("last event = %s, want ExecutionSucceeded", last.Type)
	}

	le, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}

	if len(le.Executions) != 1 {
		t.Fatalf("want 1 execution, got %d", len(le.Executions))
	}
}

func TestSDKListAndDelete(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	a := createSM(t, c, "a")
	createSM(t, c, "b")

	list, err := c.ListStateMachines(ctx, &awssfn.ListStateMachinesInput{})
	if err != nil {
		t.Fatalf("ListStateMachines: %v", err)
	}

	if len(list.StateMachines) != 2 {
		t.Fatalf("want 2 machines, got %d", len(list.StateMachines))
	}

	if _, err := c.DeleteStateMachine(ctx, &awssfn.DeleteStateMachineInput{StateMachineArn: aws.String(a)}); err != nil {
		t.Fatalf("DeleteStateMachine: %v", err)
	}

	list, _ = c.ListStateMachines(ctx, &awssfn.ListStateMachinesInput{})
	if len(list.StateMachines) != 1 {
		t.Fatalf("after delete want 1, got %d", len(list.StateMachines))
	}
}

func TestSDKTags(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "tagged")

	if _, err := c.TagResource(ctx, &awssfn.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []sfntypes.Tag{{Key: aws.String("team"), Value: aws.String("platform")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awssfn.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "team" {
		t.Fatalf("unexpected tags: %+v", tags.Tags)
	}

	if _, err := c.UntagResource(ctx, &awssfn.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}
}

func TestSDKVersionsAndAliases(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "vers")

	pub, err := c.PublishStateMachineVersion(ctx, &awssfn.PublishStateMachineVersionInput{
		StateMachineArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("PublishStateMachineVersion: %v", err)
	}

	versionArn := aws.ToString(pub.StateMachineVersionArn)

	alias, err := c.CreateStateMachineAlias(ctx, &awssfn.CreateStateMachineAliasInput{
		Name: aws.String("live"),
		RoutingConfiguration: []sfntypes.RoutingConfigurationListItem{
			{StateMachineVersionArn: aws.String(versionArn), Weight: 100},
		},
	})
	if err != nil {
		t.Fatalf("CreateStateMachineAlias: %v", err)
	}

	da, err := c.DescribeStateMachineAlias(ctx, &awssfn.DescribeStateMachineAliasInput{
		StateMachineAliasArn: alias.StateMachineAliasArn,
	})
	if err != nil {
		t.Fatalf("DescribeStateMachineAlias: %v", err)
	}

	if aws.ToString(da.Name) != "live" {
		t.Fatalf("alias name = %s", aws.ToString(da.Name))
	}
}

func TestSDKActivities(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	act, err := c.CreateActivity(ctx, &awssfn.CreateActivityInput{Name: aws.String("worker")})
	if err != nil {
		t.Fatalf("CreateActivity: %v", err)
	}

	da, err := c.DescribeActivity(ctx, &awssfn.DescribeActivityInput{ActivityArn: act.ActivityArn})
	if err != nil {
		t.Fatalf("DescribeActivity: %v", err)
	}

	if aws.ToString(da.Name) != "worker" {
		t.Fatalf("activity name = %s", aws.ToString(da.Name))
	}
}

func TestSDKRedriveExecution(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "redrive")

	start, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arn), Name: aws.String("r1"),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	out, err := c.RedriveExecution(ctx, &awssfn.RedriveExecutionInput{ExecutionArn: start.ExecutionArn})
	if err != nil {
		t.Fatalf("RedriveExecution: %v", err)
	}

	if out.RedriveDate == nil {
		t.Fatal("RedriveExecution: expected a redriveDate")
	}
}

func TestSDKListMapRunsEmpty(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "maprun")

	start, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arn), Name: aws.String("mr1"),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	out, err := c.ListMapRuns(ctx, &awssfn.ListMapRunsInput{ExecutionArn: start.ExecutionArn})
	if err != nil {
		t.Fatalf("ListMapRuns: %v", err)
	}

	if len(out.MapRuns) != 0 {
		t.Fatalf("want 0 map runs for a non-map execution, got %d", len(out.MapRuns))
	}
}

// disableHostPrefix returns an API-option that disables the "sync-" endpoint
// host prefix (used by TestState / StartSyncExecution) so the SDK talks to the
// local httptest server instead of a rewritten, unresolvable host.
func disableHostPrefix(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("disableHostPrefix",
		func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
		) (middleware.InitializeOutput, middleware.Metadata, error) {
			return next.HandleInitialize(smithyhttp.DisableEndpointHostPrefix(ctx, true), in)
		}), middleware.Before)
}

func TestSDKTestState(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	out, err := c.TestState(ctx, &awssfn.TestStateInput{
		Definition: aws.String(`{"Type":"Pass","End":true}`),
		RoleArn:    aws.String("arn:aws:iam::123456789012:role/svc"),
		Input:      aws.String(`{"x":1}`),
	}, func(o *awssfn.Options) {
		o.APIOptions = append(o.APIOptions, disableHostPrefix)
	})
	if err != nil {
		t.Fatalf("TestState: %v", err)
	}

	if out.Status != sfntypes.TestExecutionStatusSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", out.Status)
	}

	if aws.ToString(out.Output) != `{"x":1}` {
		t.Fatalf("output = %q, want echoed input", aws.ToString(out.Output))
	}
}

func TestSDKValidateStateMachineDefinition(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	ok, err := c.ValidateStateMachineDefinition(ctx, &awssfn.ValidateStateMachineDefinitionInput{
		Definition: aws.String(definition),
	})
	if err != nil {
		t.Fatalf("ValidateStateMachineDefinition: %v", err)
	}

	if ok.Result != sfntypes.ValidateStateMachineDefinitionResultCodeOk {
		t.Fatalf("result = %s, want OK", ok.Result)
	}

	bad, err := c.ValidateStateMachineDefinition(ctx, &awssfn.ValidateStateMachineDefinitionInput{
		Definition: aws.String("{not json"),
	})
	if err != nil {
		t.Fatalf("ValidateStateMachineDefinition(bad): %v", err)
	}

	if bad.Result != sfntypes.ValidateStateMachineDefinitionResultCodeFail || len(bad.Diagnostics) == 0 {
		t.Fatalf("bad definition should FAIL with diagnostics: %+v", bad)
	}
}

func TestSDKDescribeMissingMapRun(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	_, err := c.DescribeMapRun(ctx, &awssfn.DescribeMapRunInput{
		MapRunArn: aws.String("arn:aws:states:us-east-1:123456789012:mapRun:sm/e:missing"),
	})
	if err == nil {
		t.Fatal("expected error for missing map run")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want smithy API error, got %v", err)
	}
}

func TestSDKDescribeMissingStateMachine(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	_, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
		StateMachineArn: aws.String("arn:aws:states:us-east-1:123456789012:stateMachine:missing"),
	})
	if err == nil {
		t.Fatal("expected error for missing state machine")
	}

	var dne *sfntypes.StateMachineDoesNotExist
	if !errors.As(err, &dne) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want StateMachineDoesNotExist, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want StateMachineDoesNotExist, got %v", err)
	}
}

func TestSDKDuplicateStateMachine(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "dup")

	// A same-name create whose definition differs collides:
	// StateMachineAlreadyExists.
	const otherDefinition = `{"StartAt":"Done","States":{"Done":{"Type":"Succeed"}}}`

	// A same-name create with conflicting parameters (different role) is a
	// StateMachineAlreadyExists; an identical request would be idempotent.
	_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("dup"), Definition: aws.String(otherDefinition),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/svc"),
	})
	if err == nil {
		t.Fatal("expected error for duplicate state machine")
	}

	var exists *sfntypes.StateMachineAlreadyExists
	if !errors.As(err, &exists) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want StateMachineAlreadyExists, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want StateMachineAlreadyExists, got %v", err)
	}

	// CreateStateMachine is idempotent: a same-name create with the same
	// definition/type returns the existing machine (HTTP 200) even when the
	// roleArn differs — roleArn is ignored for the idempotency check.
	out, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("dup"), Definition: aws.String(definition),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/other"),
	})
	if err != nil {
		t.Fatalf("idempotent CreateStateMachine: %v", err)
	}

	if got := aws.ToString(out.StateMachineArn); got != arn {
		t.Fatalf("want existing ARN %q, got %q", arn, got)
	}
}

func TestSDKDescribeMissingExecution(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	_, err := c.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{
		ExecutionArn: aws.String("arn:aws:states:us-east-1:123456789012:execution:sm:missing"),
	})
	if err == nil {
		t.Fatal("expected error for missing execution")
	}

	var dne *sfntypes.ExecutionDoesNotExist
	if !errors.As(err, &dne) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want ExecutionDoesNotExist, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want ExecutionDoesNotExist, got %v", err)
	}
}

func TestSDKCreateStateMachineInvalidDefinition(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("bad"),
		Definition: aws.String(`{"foo":"bar"}`),
		RoleArn:    aws.String("arn:aws:iam::123456789012:role/svc"),
	})
	if err == nil {
		t.Fatal("CreateStateMachine with a non-ASL definition should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidDefinition" {
		t.Fatalf("want InvalidDefinition, got %T: %v", err, err)
	}
}

func TestSDKCreateStateMachineIdempotent(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	in := &awssfn.CreateStateMachineInput{
		Name:       aws.String("idem"),
		Definition: aws.String(definition),
		RoleArn:    aws.String("arn:aws:iam::123456789012:role/svc"),
	}

	first, err := c.CreateStateMachine(ctx, in)
	if err != nil {
		t.Fatalf("CreateStateMachine #1: %v", err)
	}

	second, err := c.CreateStateMachine(ctx, in)
	if err != nil {
		t.Fatalf("identical CreateStateMachine should be idempotent, got: %v", err)
	}

	if aws.ToString(first.StateMachineArn) != aws.ToString(second.StateMachineArn) {
		t.Fatalf("idempotent create returned a different ARN: %q vs %q",
			aws.ToString(first.StateMachineArn), aws.ToString(second.StateMachineArn))
	}
}

func TestSDKDescribeStateMachineConfigs(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	arn := createSM(t, c, "cfg")

	desc, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
		StateMachineArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}

	if desc.LoggingConfiguration == nil {
		t.Fatal("LoggingConfiguration nil, want default")
	}

	if desc.TracingConfiguration == nil {
		t.Fatal("TracingConfiguration nil, want default")
	}
}

func TestSDKListStateMachinesPaginates(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	for _, n := range []string{"sm-a", "sm-b", "sm-c"} {
		createSM(t, c, n)
	}

	first, err := c.ListStateMachines(ctx, &awssfn.ListStateMachinesInput{MaxResults: 2})
	if err != nil {
		t.Fatalf("ListStateMachines: %v", err)
	}

	if len(first.StateMachines) != 2 {
		t.Fatalf("page 1 = %d, want 2", len(first.StateMachines))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatal("NextToken empty; paginator cannot advance")
	}

	second, err := c.ListStateMachines(ctx, &awssfn.ListStateMachinesInput{NextToken: first.NextToken})
	if err != nil {
		t.Fatalf("ListStateMachines page 2: %v", err)
	}

	if len(first.StateMachines)+len(second.StateMachines) != 3 {
		t.Fatalf("total across pages = %d, want 3", len(first.StateMachines)+len(second.StateMachines))
	}
}

func TestSDKListExecutionsPaginates(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	arn := createSM(t, c, "execs")

	for _, n := range []string{"e1", "e2", "e3"} {
		if _, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
			StateMachineArn: aws.String(arn), Name: aws.String(n), Input: aws.String("{}"),
		}); err != nil {
			t.Fatalf("StartExecution(%s): %v", n, err)
		}
	}

	first, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{
		StateMachineArn: aws.String(arn), MaxResults: 2,
	})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}

	if len(first.Executions) != 2 {
		t.Fatalf("page 1 = %d, want 2", len(first.Executions))
	}

	if aws.ToString(first.NextToken) == "" {
		t.Fatal("NextToken empty; paginator cannot advance")
	}

	second, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{
		StateMachineArn: aws.String(arn), NextToken: first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListExecutions page 2: %v", err)
	}

	if len(first.Executions)+len(second.Executions) != 3 {
		t.Fatalf("total across pages = %d, want 3", len(first.Executions)+len(second.Executions))
	}
}

func TestSDKGetExecutionHistoryStateDetails(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	arn := createSM(t, c, "hist")

	start, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: aws.String(arn), Name: aws.String("run"), Input: aws.String(`{"a":1}`),
	})
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	hist, err := c.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{
		ExecutionArn: start.ExecutionArn,
	})
	if err != nil {
		t.Fatalf("GetExecutionHistory: %v", err)
	}

	var sawEntered, sawExited bool

	for _, e := range hist.Events {
		if e.Type == sfntypes.HistoryEventTypePassStateEntered {
			if e.StateEnteredEventDetails == nil || aws.ToString(e.StateEnteredEventDetails.Name) == "" {
				t.Fatalf("PassStateEntered has nil/empty details: %+v", e)
			}

			sawEntered = true
		}

		if e.Type == sfntypes.HistoryEventTypePassStateExited {
			if e.StateExitedEventDetails == nil || aws.ToString(e.StateExitedEventDetails.Name) == "" {
				t.Fatalf("PassStateExited has nil/empty details: %+v", e)
			}

			sawExited = true
		}
	}

	if !sawEntered || !sawExited {
		t.Fatalf("missing populated StateEntered/StateExited details (entered=%v exited=%v)", sawEntered, sawExited)
	}
}
