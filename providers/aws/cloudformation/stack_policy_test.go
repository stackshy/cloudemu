package cloudformation

import (
	"context"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// denyReplaceOld protects Old from replacement and allows everything else.
const denyReplaceOld = `{"Statement":[
	{"Effect":"Deny","Action":"Update:Replace","Principal":"*","Resource":"LogicalResourceId/Old"},
	{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}
]}`

const allowAll = `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}`

// paramRenamed renames Old, which replaces it.
const paramRenamed = `{"Resources":{
	"Old":{"Type":"Test::Param","Properties":{"Name":"/p2","Value":"v1"}}
}}`

// paramModified changes Old's value in place.
const paramModified = `{"Resources":{
	"Old":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v2"}}
}}`

const deniedReplaceReason = "Action denied by stack policy: Statement [#1] does not allow [Update:Replace] " +
	"for resource [LogicalResourceId/Old]"

func createWithPolicy(t *testing.T, m *Mock, policy string) {
	t.Helper()

	st, err := m.CreateStack(context.Background(), &cfn.CreateStackInput{
		StackName: "s", TemplateBody: paramV1, StackPolicyBody: policy,
	})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusCreateComplete, "create status")
}

// lastEventReason returns the reason of the newest event of id with status.
func lastEventReason(t *testing.T, m *Mock, stack, id, status string) string {
	t.Helper()

	events, err := m.DescribeStackEvents(context.Background(), stack)
	requireNoError(t, err)

	for _, e := range events {
		if e.LogicalID == id && e.Status == status {
			return e.StatusReason
		}
	}

	t.Fatalf("no %s event for %s", status, id)

	return ""
}

func TestStackPolicyDeniesReplacementAndRollsBack(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	createWithPolicy(t, m, denyReplaceOld)

	st, err := m.UpdateStack(context.Background(), &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramRenamed})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, lastEventReason(t, m, "s", "Old", cfn.ResourceUpdateFailed), deniedReplaceReason, "reason")

	if _, ok := p.values["/p2"]; ok {
		t.Fatal("the denied replacement must not create the new resource")
	}

	assertEqual(t, p.values["/p"], "v1", "old resource intact")
	assertEqual(t, stackStatus(t, m, "s").StatusReason, "The following resource(s) failed to update: [Old].", "stack reason")
}

func TestStackPolicyAllowsWhatItDoesNotDeny(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	createWithPolicy(t, m, denyReplaceOld)

	st, err := m.UpdateStack(context.Background(), &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "status")
	assertEqual(t, p.values["/p"], "v2", "modified in place")
}

func TestStackPolicyDefaultDeny(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	createWithPolicy(t, m, `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*",`+
		`"Resource":"LogicalResourceId/Other"}]}`)

	st, err := m.UpdateStack(context.Background(), &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramModified})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, lastEventReason(t, m, "s", "Old", cfn.ResourceUpdateFailed),
		"Action denied by stack policy: No statement allows [Update:Modify] for resource [LogicalResourceId/Old]", "reason")
	assertEqual(t, p.values["/p"], "v1", "value kept")
}

func TestStackPolicyDeniesDelete(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	two := `{"Resources":{
		"Old":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v1"}},
		"Keep":{"Type":"Test::Param","Properties":{"Name":"/k","Value":"k"}}
	}}`

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: two,
		StackPolicyBody: `{"Statement":[{"Effect":"Allow","NotAction":"Update:Delete","Principal":"*","Resource":"*"}]}`,
	})
	requireNoError(t, err)

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, lastEventReason(t, m, "s", "Keep", cfn.ResourceUpdateFailed),
		"Action denied by stack policy: No statement allows [Update:Delete] for resource [LogicalResourceId/Keep]", "reason")
	assertEqual(t, p.values["/k"], "k", "the protected resource is kept")
}

func TestStackPolicyDuringUpdateOverridesOnce(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()
	createWithPolicy(t, m, denyReplaceOld)

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: paramRenamed, StackPolicyDuringUpdateBody: allowAll,
	})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "override update")
	assertEqual(t, p.values["/p2"], "v1", "replaced")

	body, err := m.GetStackPolicy(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, denyReplaceOld, "the stored policy is unchanged")

	// The next update is governed by the stored policy again.
	st, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "denied again")
}

func TestSetStackPolicyReplacesButNeverRemoves(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	body, err := m.GetStackPolicy(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, "", "no policy yet")

	assertValidation(t, m.SetStackPolicy(ctx, &cfn.SetStackPolicyInput{StackName: "s"}), msgPolicyRequired)
	assertValidation(t, m.SetStackPolicy(ctx, &cfn.SetStackPolicyInput{StackName: "s", StackPolicyBody: "{"}), "")
	assertValidation(t, m.SetStackPolicy(ctx, &cfn.SetStackPolicyInput{
		StackName: "s", StackPolicyBody: allowAll, StackPolicyURL: "https://b.s3.amazonaws.com/p.json",
	}), msgBothPolicies)
	assertValidation(t, m.SetStackPolicy(ctx, &cfn.SetStackPolicyInput{StackName: "missing", StackPolicyBody: allowAll}), "")

	requireNoError(t, m.SetStackPolicy(ctx, &cfn.SetStackPolicyInput{StackName: "s", StackPolicyBody: denyReplaceOld}))

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramRenamed})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "denied by the set policy")

	// UpdateStack's StackPolicyBody replaces the policy for later updates.
	st, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: paramModified, StackPolicyBody: allowAll,
	})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "modify allowed")

	body, err = m.GetStackPolicy(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, allowAll, "replaced by UpdateStack")
}

func TestStackPolicyFromURL(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	m.SetTemplateFetcher(func(_ context.Context, bucket, key, _ string) ([]byte, error) {
		if bucket == "b" && key == "policy.json" {
			return []byte(denyReplaceOld), nil
		}

		return nil, errBoom
	})

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: paramV1, StackPolicyURL: "https://b.s3.amazonaws.com/policy.json",
	})
	requireNoError(t, err)

	body, err := m.GetStackPolicy(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, denyReplaceOld, "fetched from S3")

	err = m.SetStackPolicy(ctx, &cfn.SetStackPolicyInput{StackName: "s", StackPolicyURL: "https://b.s3.amazonaws.com/nope.json"})
	assertValidation(t, err, "")
}

func TestExecuteChangeSetHonoursStackPolicy(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()
	createWithPolicy(t, m, denyReplaceOld)

	cs := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "c", TemplateBody: paramRenamed})
	requireNoError(t, m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID}))

	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, lastEventReason(t, m, "s", "Old", cfn.ResourceUpdateFailed), deniedReplaceReason, "reason")
	assertEqual(t, describeCS(t, m, "s", cs.ID).ExecutionStatus, cfn.ExecutionFailed, "execution status")
}

func TestStackPolicySurvivesSnapshot(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()
	createWithPolicy(t, m, denyReplaceOld)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newParamMock(p)
	requireNoError(t, restored.Restore(ctx, data))

	body, err := restored.GetStackPolicy(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, denyReplaceOld, "policy restored")

	st, err := restored.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramRenamed})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "still enforced")
}

func TestCreateStackRejectsBadPolicy(t *testing.T) {
	m := newParamMock(newParamProv())

	_, err := m.CreateStack(context.Background(), &cfn.CreateStackInput{
		StackName: "s", TemplateBody: paramV1, StackPolicyBody: `{"Statement":[{"Effect":"Allow"}]}`,
	})
	assertValidation(t, err, "")

	if _, _, ok := m.findStack("s"); ok {
		t.Fatal("a stack with a bad policy must not be created")
	}

	createWithPolicy(t, m, allowAll)

	_, err = m.UpdateStack(context.Background(), &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: paramModified, StackPolicyDuringUpdateBody: allowAll,
		StackPolicyDuringUpdateURL: "https://b.s3.amazonaws.com/p.json",
	})
	assertValidation(t, err, msgBothDuringPolicies)

	_, err = m.UpdateStack(context.Background(), &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: paramModified, StackPolicyDuringUpdateBody: `{"Statement":[]`,
	})
	assertValidation(t, err, "")
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusCreateComplete, "nothing started")
}
