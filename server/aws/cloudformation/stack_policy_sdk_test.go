package cloudformation_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func tableTemplate(name string) string {
	return `{"Resources":{"Table":{"Type":"AWS::DynamoDB::Table","Properties":{
  "TableName":"` + name + `","BillingMode":"PAY_PER_REQUEST",
  "AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],
  "KeySchema":[{"AttributeName":"id","KeyType":"HASH"}]}}}}`
}

const denyTableReplace = `{"Statement":[
  {"Effect":"Deny","Action":"Update:Replace","Principal":"*","Resource":"LogicalResourceId/Table"},
  {"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}`

const allowEverything = `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}`

// fixedStart and settleWait drive the async test clock: settleWait is past
// any stack settle window.
var fixedStart = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test clock start

const settleWait = time.Minute

// A stack policy that denies replacing the table fails the update, rolls
// it back and keeps the table and its item. An override for one update
// lets the replacement through without changing the stored policy.
func TestStackPolicyRealSDK(t *testing.T) {
	ctx := context.Background()
	c, cloud := bootWithProvider(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("p"), TemplateBody: aws.String(tableTemplate("t1")),
		StackPolicyBody: aws.String(denyTableReplace), ClientRequestToken: aws.String("create-1"),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	if err := cloud.DynamoDB.PutItem(ctx, "t1", map[string]any{"id": "keep"}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	got, err := c.GetStackPolicy(ctx, &awscfn.GetStackPolicyInput{StackName: aws.String("p")})
	if err != nil || aws.ToString(got.StackPolicyBody) != denyTableReplace {
		t.Fatalf("GetStackPolicy = %v, %v", got, err)
	}

	if _, err = c.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String("p"), TemplateBody: aws.String(tableTemplate("t2")),
	}); err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}

	if st := describeStack(t, c, "p"); st.StackStatus != cfntypes.StackStatusUpdateRollbackComplete {
		t.Fatalf("status = %s", st.StackStatus)
	}

	assertDeniedEvent(ctx, t, c)

	if item, ierr := cloud.DynamoDB.GetItem(ctx, "t1", map[string]any{"id": "keep"}); ierr != nil || item == nil {
		t.Fatalf("the item must survive: %v %v", item, ierr)
	}

	if _, derr := cloud.DynamoDB.DescribeTable(ctx, "t2"); derr == nil {
		t.Fatal("the denied replacement must not create t2")
	}

	if _, err = c.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String("p"), TemplateBody: aws.String(tableTemplate("t2")),
		StackPolicyDuringUpdateBody: aws.String(allowEverything),
	}); err != nil {
		t.Fatalf("override UpdateStack: %v", err)
	}

	if st := describeStack(t, c, "p"); st.StackStatus != cfntypes.StackStatusUpdateComplete {
		t.Fatalf("override status = %s", st.StackStatus)
	}

	got, _ = c.GetStackPolicy(ctx, &awscfn.GetStackPolicyInput{StackName: aws.String("p")})
	if aws.ToString(got.StackPolicyBody) != denyTableReplace {
		t.Fatal("the override must not change the stored policy")
	}
}

func assertDeniedEvent(ctx context.Context, t *testing.T, c *awscfn.Client) {
	t.Helper()

	out, err := c.DescribeStackEvents(ctx, &awscfn.DescribeStackEventsInput{StackName: aws.String("p")})
	if err != nil {
		t.Fatalf("DescribeStackEvents: %v", err)
	}

	want := "Action denied by stack policy: Statement [#1] does not allow [Update:Replace] for resource [LogicalResourceId/Table]"

	for _, e := range out.StackEvents {
		if aws.ToString(e.LogicalResourceId) == "Table" && e.ResourceStatus == cfntypes.ResourceStatusUpdateFailed {
			if aws.ToString(e.ResourceStatusReason) != want {
				t.Fatalf("reason = %q", aws.ToString(e.ResourceStatusReason))
			}

			return
		}
	}

	t.Fatal("no UPDATE_FAILED event for Table")
}

func TestStackPolicyErrorsRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("e"), TemplateBody: aws.String(tableTemplate("e1")), ClientRequestToken: aws.String("tok"),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	_, err := c.SetStackPolicy(ctx, &awscfn.SetStackPolicyInput{StackName: aws.String("e"), StackPolicyBody: aws.String("{")})
	if code, _ := apiErrorCode(t, err); code != "ValidationError" {
		t.Fatalf("bad policy: %s", code)
	}

	_, err = c.CancelUpdateStack(ctx, &awscfn.CancelUpdateStackInput{StackName: aws.String("e")})
	if code, msg := apiErrorCode(t, err); code != "ValidationError" ||
		msg != "CancelUpdateStack cannot be called from current stack status" {
		t.Fatalf("cancel: %s %s", code, msg)
	}

	_, err = c.RollbackStack(ctx, &awscfn.RollbackStackInput{StackName: aws.String("e")})
	if code, _ := apiErrorCode(t, err); code != "ValidationError" {
		t.Fatalf("rollback: %s", code)
	}

	_, err = c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("e"), ClientRequestToken: aws.String("tok")})

	var exists *cfntypes.TokenAlreadyExistsException
	if !errors.As(err, &exists) {
		t.Fatalf("reused token: %v", err)
	}

	events, err := c.DescribeStackEvents(ctx, &awscfn.DescribeStackEventsInput{StackName: aws.String("e")})
	if err != nil || aws.ToString(events.StackEvents[0].ClientRequestToken) != "tok" {
		t.Fatalf("events carry the token: %v", err)
	}
}

// Under AsyncSettle an update stays UPDATE_IN_PROGRESS, so CancelUpdateStack
// catches it and rolls it back.
func TestCancelUpdateStackRealSDK(t *testing.T) {
	ctx := context.Background()
	fc := config.NewFakeClock(fixedStart)
	cloud := cloudemu.NewAWS(config.WithAsyncSettle(), config.WithClock(fc))
	ts := httptest.NewServer(awsserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	c := awscfn.NewFromConfig(cfg, func(o *awscfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	if _, err = c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("a"), TemplateBody: aws.String(tableTemplate("a1")),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	if st := describeStack(t, c, "a"); st.StackStatus != cfntypes.StackStatusCreateInProgress {
		t.Fatalf("create status = %s", st.StackStatus)
	}

	fc.Advance(settleWait)

	if _, err = c.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String("a"), TemplateBody: aws.String(tableTemplate("a2")),
	}); err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}

	if _, err = c.CancelUpdateStack(ctx, &awscfn.CancelUpdateStackInput{StackName: aws.String("a")}); err != nil {
		t.Fatalf("CancelUpdateStack: %v", err)
	}

	st := describeStack(t, c, "a")
	if st.StackStatus != cfntypes.StackStatusUpdateRollbackInProgress ||
		!strings.Contains(aws.ToString(st.StackStatusReason), "cancelled") {
		t.Fatalf("after cancel = %s %q", st.StackStatus, aws.ToString(st.StackStatusReason))
	}

	fc.Advance(settleWait)

	if st = describeStack(t, c, "a"); st.StackStatus != cfntypes.StackStatusUpdateRollbackComplete {
		t.Fatalf("settled = %s", st.StackStatus)
	}

	if _, err = cloud.DynamoDB.DescribeTable(ctx, "a1"); err != nil {
		t.Fatalf("the old table must remain: %v", err)
	}

	if _, err = cloud.DynamoDB.DescribeTable(ctx, "a2"); err == nil {
		t.Fatal("the replacement table must be deleted")
	}
}
