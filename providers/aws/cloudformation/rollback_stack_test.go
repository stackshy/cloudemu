package cloudformation

import (
	"context"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

func TestRollbackStackFromUpdateFailed(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV2, DisableRollback: true})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateFailed, "update failed")
	assertEqual(t, p.values["/p"], "v2", "Old was updated")

	id, err := m.RollbackStack(ctx, &cfn.RollbackStackInput{StackName: "s", ClientRequestToken: "rb-1"})
	requireNoError(t, err)
	assertEqual(t, id, st.ID, "stack id")

	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateRollbackComplete, "rolled back")
	assertEqual(t, p.values["/p"], "v1", "Old restored")

	if _, ok := p.values["/n"]; ok {
		t.Fatal("the resource the update created must be deleted")
	}

	body, err := m.GetTemplate(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, paramV1, "template reverted")

	// The stack updates normally afterwards.
	st, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "later update")
}

func TestRollbackStackRestoresReplacedResources(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	failDoNothing(t, m, p)

	_, err := m.RollbackStack(context.Background(), &cfn.RollbackStackInput{StackName: "s"})
	requireNoError(t, err)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateRollbackComplete, "rolled back")

	if _, ok := p.values["/a2"]; ok {
		t.Fatalf("the replacement of A must be deleted, values = %v", p.values)
	}

	assertEqual(t, p.values["/a"], "data", "the old A is back")
	assertEqual(t, len(m.mustData(t, "s").retained), 0, "nothing retained")
}

func TestRollbackStackFromCreateFailed(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	st, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV2, DisableRollback: true})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusCreateFailed, "create failed")

	_, err = m.RollbackStack(ctx, &cfn.RollbackStackInput{StackName: "s"})
	requireNoError(t, err)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusRollbackComplete, "rolled back")
	assertEqual(t, len(p.values), 0, "created resources deleted")
}

func TestRollbackStackRejectsOtherStates(t *testing.T) {
	m := newParamMock(newParamProv())
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	_, err = m.RollbackStack(ctx, &cfn.RollbackStackInput{StackName: "s"})
	assertValidation(t, err, "")

	_, err = m.RollbackStack(ctx, &cfn.RollbackStackInput{StackName: "nope"})
	assertValidation(t, err, "")
}

func TestClientRequestTokens(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	first, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1, ClientRequestToken: "tok-1"})
	requireNoError(t, err)

	// A retry of the same create returns the stack instead of AlreadyExists.
	again, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1, ClientRequestToken: "tok-1"})
	requireNoError(t, err)
	assertEqual(t, again.ID, first.ID, "same stack")

	events, err := m.DescribeStackEvents(ctx, "s")
	requireNoError(t, err)

	for _, e := range events {
		assertEqual(t, e.ClientRequestToken, "tok-1", "create events carry the token")
	}

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified, ClientRequestToken: "tok-1"})
	assertException(t, err, cfn.ExceptionTokenAlreadyExists, "")

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified, ClientRequestToken: "tok-2"})
	requireNoError(t, err)

	// Retrying the update does not run it again or fail with "No updates".
	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified, ClientRequestToken: "tok-2"})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "retried update")
	assertEqual(t, *p.updates, 1, "one real update")

	err = m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s", ClientRequestToken: "tok-2"})
	assertException(t, err, cfn.ExceptionTokenAlreadyExists, "")

	err = m.ContinueUpdateRollback(ctx, &cfn.ContinueUpdateRollbackInput{StackName: "s", ClientRequestToken: "tok-1"})
	assertException(t, err, cfn.ExceptionTokenAlreadyExists, "")

	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s", ClientRequestToken: "tok-3"}))
}
