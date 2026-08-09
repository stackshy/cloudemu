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
	createSM(t, c, "dup")

	_, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("dup"), Definition: aws.String(definition),
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
