package cloudformation_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

const queuesV1 = `Resources:
  Tuned:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: cs-tuned
      VisibilityTimeout: 30
  Renamed:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: cs-old
`

// queuesV2 changes Tuned in place and renames Renamed, which replaces it.
const queuesV2 = `Resources:
  Tuned:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: cs-tuned
      VisibilityTimeout: 60
  Renamed:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: cs-new
`

func waitChangeSet(t *testing.T, c *awscfn.Client, id string) *awscfn.DescribeChangeSetOutput {
	t.Helper()

	w := awscfn.NewChangeSetCreateCompleteWaiter(c, func(o *awscfn.ChangeSetCreateCompleteWaiterOptions) {
		o.MinDelay, o.MaxDelay = time.Millisecond, time.Millisecond
	})

	out, err := w.WaitForOutput(context.Background(), &awscfn.DescribeChangeSetInput{ChangeSetName: aws.String(id)}, time.Second)
	if err != nil {
		t.Fatalf("change set waiter: %v", err)
	}

	return out
}

func TestChangeSetCreateThenUpdateRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	created, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("cs-stack"), ChangeSetName: aws.String("create"),
		ChangeSetType: cfntypes.ChangeSetTypeCreate, TemplateBody: aws.String(queuesV1),
	})
	if err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}

	if st := describeStack(t, c, "cs-stack"); st.StackStatus != cfntypes.StackStatusReviewInProgress {
		t.Fatalf("stack status %s", st.StackStatus)
	}

	out := waitChangeSet(t, c, *created.Id)
	if out.ExecutionStatus != cfntypes.ExecutionStatusAvailable || len(out.Changes) != 2 {
		t.Fatalf("create change set: %s, %d changes", out.ExecutionStatus, len(out.Changes))
	}

	add := out.Changes[0]
	if add.Type != cfntypes.ChangeTypeResource || add.ResourceChange.Action != cfntypes.ChangeActionAdd ||
		aws.ToString(add.ResourceChange.ResourceType) != "AWS::SQS::Queue" {
		t.Fatalf("add change: %+v", add.ResourceChange)
	}

	if _, err = c.ExecuteChangeSet(ctx, &awscfn.ExecuteChangeSetInput{ChangeSetName: created.Id}); err != nil {
		t.Fatalf("ExecuteChangeSet: %v", err)
	}

	st := describeStack(t, c, "cs-stack")
	if st.StackStatus != cfntypes.StackStatusCreateComplete || aws.ToString(st.ChangeSetId) != *created.Id {
		t.Fatalf("after create: %s, change set %q", st.StackStatus, aws.ToString(st.ChangeSetId))
	}

	upd, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("cs-stack"), ChangeSetName: aws.String("update"), TemplateBody: aws.String(queuesV2),
	})
	if err != nil {
		t.Fatalf("CreateChangeSet UPDATE: %v", err)
	}

	desc := waitChangeSet(t, c, *upd.Id)
	checkUpdateChanges(t, desc.Changes)

	if _, err = c.ExecuteChangeSet(ctx, &awscfn.ExecuteChangeSetInput{
		StackName: aws.String("cs-stack"), ChangeSetName: aws.String("update"),
	}); err != nil {
		t.Fatalf("ExecuteChangeSet UPDATE: %v", err)
	}

	if st = describeStack(t, c, "cs-stack"); st.StackStatus != cfntypes.StackStatusUpdateComplete {
		t.Fatalf("after update: %s", st.StackStatus)
	}

	res, err := c.DescribeStackResources(ctx, &awscfn.DescribeStackResourcesInput{StackName: aws.String("cs-stack")})
	if err != nil {
		t.Fatalf("DescribeStackResources: %v", err)
	}

	for _, r := range res.StackResources {
		if aws.ToString(r.LogicalResourceId) == "Renamed" && !strings.HasSuffix(aws.ToString(r.PhysicalResourceId), "/cs-new") {
			t.Fatalf("Renamed not replaced: %s", aws.ToString(r.PhysicalResourceId))
		}
	}
}

func checkUpdateChanges(t *testing.T, changes []cfntypes.Change) {
	t.Helper()

	if len(changes) != 2 {
		t.Fatalf("want 2 changes, got %d", len(changes))
	}

	renamed, tuned := changes[0].ResourceChange, changes[1].ResourceChange
	if aws.ToString(renamed.LogicalResourceId) != "Renamed" || renamed.Action != cfntypes.ChangeActionModify ||
		renamed.Replacement != cfntypes.ReplacementTrue || renamed.PolicyAction != cfntypes.PolicyActionReplaceAndDelete {
		t.Fatalf("Renamed change: %+v", renamed)
	}

	d := renamed.Details[0]
	if d.Target.Attribute != cfntypes.ResourceAttributeProperties || aws.ToString(d.Target.Name) != "QueueName" ||
		d.Target.RequiresRecreation != cfntypes.RequiresRecreationAlways ||
		d.Evaluation != cfntypes.EvaluationTypeStatic || d.ChangeSource != cfntypes.ChangeSourceDirectModification {
		t.Fatalf("Renamed detail: %+v / %+v", d, d.Target)
	}

	if tuned.Replacement != cfntypes.ReplacementFalse || len(tuned.Scope) != 1 || tuned.Scope[0] != cfntypes.ResourceAttributeProperties ||
		!strings.HasSuffix(aws.ToString(tuned.PhysicalResourceId), "/cs-tuned") {
		t.Fatalf("Tuned change: %+v", tuned)
	}

	if tuned.Details[0].Target.RequiresRecreation != cfntypes.RequiresRecreationNever {
		t.Fatalf("Tuned recreation: %s", tuned.Details[0].Target.RequiresRecreation)
	}
}

func TestChangeSetErrorsRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("s"), TemplateBody: aws.String(queuesV1),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	same, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("s"), ChangeSetName: aws.String("same"), TemplateBody: aws.String(queuesV1),
	})
	if err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}

	desc, err := c.DescribeChangeSet(ctx, &awscfn.DescribeChangeSetInput{ChangeSetName: same.Id})
	if err != nil {
		t.Fatalf("DescribeChangeSet: %v", err)
	}

	if desc.Status != cfntypes.ChangeSetStatusFailed || desc.ExecutionStatus != cfntypes.ExecutionStatusUnavailable ||
		!strings.HasPrefix(aws.ToString(desc.StatusReason), "The submitted information didn't contain changes.") {
		t.Fatalf("no-change change set: %s %s %q", desc.Status, desc.ExecutionStatus, aws.ToString(desc.StatusReason))
	}

	_, err = c.ExecuteChangeSet(ctx, &awscfn.ExecuteChangeSetInput{ChangeSetName: same.Id})

	var badStatus *cfntypes.InvalidChangeSetStatusException
	if !errors.As(err, &badStatus) {
		t.Fatalf("want InvalidChangeSetStatusException, got %v", err)
	}

	_, err = c.DescribeChangeSet(ctx, &awscfn.DescribeChangeSetInput{StackName: aws.String("s"), ChangeSetName: aws.String("nope")})

	var notFound *cfntypes.ChangeSetNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("want ChangeSetNotFoundException, got %v", err)
	}

	_, err = c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("s"), ChangeSetName: aws.String("same"), TemplateBody: aws.String(queuesV2),
	})

	var exists *cfntypes.AlreadyExistsException
	if !errors.As(err, &exists) {
		t.Fatalf("want AlreadyExistsException, got %v", err)
	}

	_, err = c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
		StackName: aws.String("absent"), ChangeSetName: aws.String("u"), TemplateBody: aws.String(queuesV2),
	})
	if code, msg := apiErrorCode(t, err); code != "ValidationError" || msg != "Stack [absent] does not exist" {
		t.Fatalf("UPDATE on a missing stack: %s %s", code, msg)
	}

	if _, err = c.DeleteChangeSet(ctx, &awscfn.DeleteChangeSetInput{ChangeSetName: same.Id}); err != nil {
		t.Fatalf("DeleteChangeSet: %v", err)
	}

	list, err := c.ListChangeSets(ctx, &awscfn.ListChangeSetsInput{StackName: aws.String("s")})
	if err != nil || len(list.Summaries) != 0 {
		t.Fatalf("ListChangeSets after delete: %v, %d", err, len(list.Summaries))
	}
}

func TestListChangeSetsRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	for _, name := range []string{"one", "two"} {
		if _, err := c.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
			StackName: aws.String("review"), ChangeSetName: aws.String(name), Description: aws.String("d-" + name),
			ChangeSetType: cfntypes.ChangeSetTypeCreate, TemplateBody: aws.String(queuesV1),
		}); err != nil {
			t.Fatalf("CreateChangeSet %s: %v", name, err)
		}
	}

	var got []cfntypes.ChangeSetSummary

	p := awscfn.NewListChangeSetsPaginator(c, &awscfn.ListChangeSetsInput{StackName: aws.String("review")})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListChangeSets: %v", err)
		}

		got = append(got, page.Summaries...)
	}

	if len(got) != 2 || aws.ToString(got[1].ChangeSetName) != "two" || aws.ToString(got[1].Description) != "d-two" ||
		got[0].Status != cfntypes.ChangeSetStatusCreateComplete || got[0].ExecutionStatus != cfntypes.ExecutionStatusAvailable ||
		aws.ToString(got[0].StackName) != "review" || got[0].CreationTime == nil {
		t.Fatalf("summaries: %+v", got)
	}
}

func TestChangeSetClientTokenRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	in := &awscfn.CreateChangeSetInput{
		StackName: aws.String("tok"), ChangeSetName: aws.String("c"), ChangeSetType: cfntypes.ChangeSetTypeCreate,
		TemplateBody: aws.String(queuesV1), ClientToken: aws.String("t1"),
	}

	first, err := c.CreateChangeSet(ctx, in)
	if err != nil {
		t.Fatalf("CreateChangeSet: %v", err)
	}

	again, err := c.CreateChangeSet(ctx, in)
	if err != nil || aws.ToString(again.Id) != aws.ToString(first.Id) {
		t.Fatalf("retry: %v, id %s want %s", err, aws.ToString(again.Id), aws.ToString(first.Id))
	}

	in.ClientToken = aws.String("t2")

	var exists *cfntypes.AlreadyExistsException
	if _, err = c.CreateChangeSet(ctx, in); !errors.As(err, &exists) {
		t.Fatalf("other token: want AlreadyExistsException, got %v", err)
	}

	exec := &awscfn.ExecuteChangeSetInput{ChangeSetName: first.Id, ClientRequestToken: aws.String("r1")}
	for range 2 {
		if _, err = c.ExecuteChangeSet(ctx, exec); err != nil {
			t.Fatalf("ExecuteChangeSet: %v", err)
		}
	}
}

// After an update that fails with DisableRollback, GetTemplate returns the
// submitted body byte for byte, comments included, as Terraform compares it.
func TestUpdateFailedKeepsSubmittedTemplateRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{StackName: aws.String("tf"), TemplateBody: aws.String(queuesV1)}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	v2 := "# second version\n" + queuesV2 + "  Broken: # never created\n    Type: AWS::Unknown::Thing\n"

	if _, err := c.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String("tf"), TemplateBody: aws.String(v2), DisableRollback: aws.Bool(true),
	}); err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}

	st := describeStack(t, c, "tf")
	if st.StackStatus != cfntypes.StackStatusUpdateFailed || !aws.ToBool(st.DisableRollback) {
		t.Fatalf("status %s, DisableRollback %v", st.StackStatus, aws.ToBool(st.DisableRollback))
	}

	out, err := c.GetTemplate(ctx, &awscfn.GetTemplateInput{StackName: aws.String("tf")})
	if err != nil || aws.ToString(out.TemplateBody) != v2 {
		t.Fatalf("GetTemplate: %v\n%q\nwant\n%q", err, aws.ToString(out.TemplateBody), v2)
	}
}

// aws cloudformation deploy reads the live stack's parameter keys with
// GetTemplateSummary before it plans an update.
func TestGetTemplateSummaryRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	body := "AWSTemplateFormatVersion: '2010-09-09'\nDescription: summary\n" +
		"Parameters:\n  Name: {Type: String, Default: q}\n" + queuesV1

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{StackName: aws.String("sum"), TemplateBody: aws.String(body)}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	for _, in := range []*awscfn.GetTemplateSummaryInput{
		{StackName: aws.String("sum")},
		{TemplateBody: aws.String(body)},
	} {
		out, err := c.GetTemplateSummary(ctx, in)
		if err != nil {
			t.Fatalf("GetTemplateSummary: %v", err)
		}

		if len(out.Parameters) != 1 || aws.ToString(out.Parameters[0].ParameterKey) != "Name" ||
			aws.ToString(out.Parameters[0].ParameterType) != "String" || aws.ToString(out.Parameters[0].DefaultValue) != "q" ||
			aws.ToString(out.Description) != "summary" || aws.ToString(out.Version) != "2010-09-09" ||
			len(out.ResourceTypes) != 1 || out.ResourceTypes[0] != "AWS::SQS::Queue" {
			t.Fatalf("summary: %+v", out)
		}
	}

	_, err := c.GetTemplateSummary(ctx, &awscfn.GetTemplateSummaryInput{StackName: aws.String("nope")})
	if code, _ := apiErrorCode(t, err); code != "ValidationError" {
		t.Fatalf("missing stack: %s", code)
	}
}
