package cloudformation

import (
	"context"
	"errors"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// paramProv is a fake Parameter Store: names are unique, so creating a name
// that exists fails, and Value changes in place. Only Name forces a new one.
type paramProv struct {
	values  map[string]string
	updates *int
}

func newParamProv() paramProv { return paramProv{values: map[string]string{}, updates: new(int)} }

func (p paramProv) Create(_ context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := cfn.PropString(req.Properties, "Name")
	if _, taken := p.values[name]; taken {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "parameter %s already exists", name)
	}

	p.values[name] = cfn.PropString(req.Properties, "Value")

	return &cfn.ProvisionedResource{PhysicalID: name, Attributes: map[string]string{"Value": p.values[name]}}, nil
}

func (p paramProv) Delete(_ context.Context, physicalID string, _ map[string]any) error {
	if _, ok := p.values[physicalID]; !ok {
		return cerrors.Newf(cerrors.NotFound, "parameter %s not found", physicalID)
	}

	delete(p.values, physicalID)

	return nil
}

func (paramProv) RequiresReplacement(property string) bool { return property == "Name" }

func (p paramProv) Update(
	_ context.Context, physicalID string, _ map[string]any, req cfn.ResourceRequest,
) (*cfn.ProvisionedResource, error) {
	if _, ok := p.values[physicalID]; !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "parameter %s not found", physicalID)
	}

	*p.updates++
	p.values[physicalID] = cfn.PropString(req.Properties, "Value")

	return &cfn.ProvisionedResource{PhysicalID: physicalID, Attributes: map[string]string{"Value": p.values[physicalID]}}, nil
}

func newParamMock(p paramProv) *Mock {
	m := newTestMock(newBacking())
	m.registry["Test::Param"] = p

	return m
}

const paramV1 = `{"Resources":{
	"Old":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v1"}}
}}`

// paramV2 renames Old, so it is replaced, gives its old name to New, then
// fails on Bad. Rolling Old back to /p then collides with New.
const paramV2 = `{"Resources":{
	"Old":{"Type":"Test::Param","Properties":{"Name":"/q","Value":"v1"}},
	"New":{"Type":"Test::Param","DependsOn":"Old","Properties":{"Name":"/p","Value":"new"}},
	"Bad":{"Type":"Test::Boom","DependsOn":"New","Properties":{}}
}}`

func stackStatus(t *testing.T, m *Mock, name string) cfn.Stack {
	t.Helper()

	st, err := m.DescribeStacks(context.Background(), name)
	requireNoError(t, err)

	return st[0]
}

// failIntoRollbackFailed leaves stack "s" in UPDATE_ROLLBACK_FAILED.
func failIntoRollbackFailed(t *testing.T, m *Mock) {
	t.Helper()

	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV2})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackFailed, "status")
}

func TestUpdateRollbackFailedKeepsRestoreError(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	failIntoRollbackFailed(t, m)

	st := stackStatus(t, m, "s")
	want := "The following resource(s) failed to create: [Old]. parameter /p already exists"
	assertEqual(t, st.StatusReason, want, "stack reason")
	assertEqual(t, strings.Join(m.mustData(t, "s").rollbackFailed, ","), "Old", "failed set")

	events, err := m.DescribeStackEvents(context.Background(), "s")
	requireNoError(t, err)

	sawCreateFailed := false

	for _, e := range events {
		if e.LogicalID == "Old" && e.Status == cfn.ResourceCreateFailed && e.StatusReason == "parameter /p already exists" {
			sawCreateFailed = true
		}
	}

	if !sawCreateFailed {
		t.Fatal("expected a CREATE_FAILED event for Old with the restore error")
	}

	// Cleanup still removed the update's new resource.
	if _, ok := p.values["/p"]; ok {
		t.Fatalf("New should be cleaned up, values = %v", p.values)
	}

	// The template reverted to the previous one.
	body, err := m.GetTemplate(context.Background(), "s")
	requireNoError(t, err)
	assertEqual(t, body, paramV1, "template")

	_, err = m.UpdateStack(context.Background(), &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV1})
	assertErrMsg(t, err, "Stack:"+st.ID+" is in UPDATE_ROLLBACK_FAILED state and can not be updated.")
}

func TestContinueUpdateRollbackRetries(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)
	failIntoRollbackFailed(t, m)

	requireNoError(t, m.ContinueUpdateRollback(ctx, &cfn.ContinueUpdateRollbackInput{StackName: "s"}))

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, p.values["/p"], "v1", "Old restored")

	if len(p.values) != 1 {
		t.Fatalf("only Old should exist, values = %v", p.values)
	}

	res, err := m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, len(res), 1, "resources")
	assertEqual(t, res[0].PhysicalID, "/p", "physical id")

	err = m.ContinueUpdateRollback(ctx, &cfn.ContinueUpdateRollbackInput{StackName: "s"})
	assertErrMsg(t, err, "ContinueUpdateRollback cannot be called from current stack status")

	// The restored stack takes updates again.
	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV1})
	assertErrMsg(t, err, "No updates are to be performed.")
}

func TestContinueUpdateRollbackSkipsResources(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)
	failIntoRollbackFailed(t, m)

	err := m.ContinueUpdateRollback(ctx, &cfn.ContinueUpdateRollbackInput{StackName: "s", ResourcesToSkip: []string{"New"}})
	assertErrMsg(t, err, "Resource [New] is not in a failed state and cannot be skipped")
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateRollbackFailed, "a bad skip changes nothing")

	requireNoError(t, m.ContinueUpdateRollback(ctx, &cfn.ContinueUpdateRollbackInput{
		StackName: "s", ResourcesToSkip: []string{"Old"},
	}))

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")

	if len(p.values) != 0 {
		t.Fatalf("a skipped resource is not restored, values = %v", p.values)
	}

	res, err := m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, len(res), 1, "resources")
	assertEqual(t, res[0].Status, cfn.ResourceUpdateComplete, "skipped status")
	assertEqual(t, res[0].StatusReason, "Resource skipped during rollback", "skipped reason")
	assertEqual(t, res[0].Type, "Test::Param", "skipped type")
	assertEqual(t, res[0].PhysicalID, "/q", "skipped keeps its last physical id")
}

func TestUpdateRejectsTypeChange(t *testing.T) {
	ctx := context.Background()
	m := newParamMock(newParamProv())

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: `{"Resources":{
		"Old":{"Type":"Test::Bucket","Properties":{"Name":"/p"}}}}`})
	assertErrMsg(t, err, "Update of resource type is not permitted. "+
		"The new template modifies resource type of the following resources: [Old]")
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusCreateComplete, "stack untouched")
}

func TestContinueUpdateRollbackSurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)
	failIntoRollbackFailed(t, m)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newParamMock(p)
	requireNoError(t, restored.Restore(ctx, data))
	requireNoError(t, restored.ContinueUpdateRollback(ctx, &cfn.ContinueUpdateRollbackInput{
		StackName: "s", ResourcesToSkip: []string{"Old"},
	}))
	assertEqual(t, stackStatus(t, restored, "s").Status, cfn.StatusUpdateRollbackComplete, "status")
}

func TestUpdateFailureStatusReason(t *testing.T) {
	ctx := context.Background()
	m := newParamMock(newParamProv())

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	failing := `{"Resources":{
		"Old":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v1"}},
		"Bad":{"Type":"Test::Boom","Properties":{}}
	}}`

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: failing})
	requireNoError(t, err)

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, st.StatusReason, "The following resource(s) failed to create: [Bad].", "stack reason")

	events, err := m.DescribeStackEvents(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, events[0].Status, cfn.StatusUpdateRollbackComplete, "newest event")
	assertEqual(t, events[0].StatusReason, "", "terminal event reason")

	_, err = m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "c", TemplateBody: `{"Resources":{
		"Bad":{"Type":"Test::Boom","Properties":{}}}}`})
	requireNoError(t, err)

	st = stackStatus(t, m, "c")
	assertEqual(t, st.Status, cfn.StatusRollbackComplete, "create status")
	assertEqual(t, st.StatusReason,
		"The following resource(s) failed to create: [Bad]. Rollback requested by user.", "create reason")

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "c", TemplateBody: paramV1})
	assertErrMsg(t, err, "Stack:"+st.ID+" is in ROLLBACK_COMPLETE state and can not be updated.")
}

func TestUpdateInPlaceVersusReplacement(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	inPlace := strings.Replace(paramV1, `"v1"`, `"v2"`, 1)

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: inPlace})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "status")
	assertEqual(t, *p.updates, 1, "updated in place")
	assertEqual(t, p.values["/p"], "v2", "new value")

	for _, e := range st.Events {
		if e.LogicalID == "Old" && e.Status == cfn.ResourceDeleteInProgress {
			t.Fatal("an in-place update must not delete the resource")
		}
	}

	renamed := strings.Replace(inPlace, `"/p"`, `"/r"`, 1)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: renamed})
	requireNoError(t, err)
	assertEqual(t, *p.updates, 1, "a rename is not in place")

	if _, ok := p.values["/p"]; ok || p.values["/r"] != "v2" {
		t.Fatalf("rename should replace /p with /r, values = %v", p.values)
	}
}

func TestRollbackRevertsInPlaceUpdate(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	failing := `{"Resources":{
		"Old":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v2"}},
		"Bad":{"Type":"Test::Boom","DependsOn":"Old","Properties":{}}
	}}`

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: failing})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "status")
	assertEqual(t, p.values["/p"], "v1", "value reverted")
	assertEqual(t, *p.updates, 2, "forward and back in place")
}

func TestUpdateNoChanges(t *testing.T) {
	ctx := context.Background()
	m := newParamMock(newParamProv())

	body := `{"Parameters":{"V":{"Type":"String","Default":"v1"}},
	"Resources":{"Old":{"Type":"Test::Param","Properties":{"Name":"/p","Value":{"Ref":"V"}}}},
	"Outputs":{"Out":{"Value":{"Ref":"Old"}}}}`

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: body})
	requireNoError(t, err)

	before := len(stackStatus(t, m, "s").Events)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: body})
	assertErrMsg(t, err, "No updates are to be performed.")

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", UsePreviousTemplate: true,
		Parameters: []cfn.Parameter{{Key: "V", UsePreviousValue: true}},
	})
	assertErrMsg(t, err, "No updates are to be performed.")

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusCreateComplete, "status unchanged")
	assertEqual(t, len(st.Events), before, "no events")

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", UsePreviousTemplate: true, TemplateBody: body})
	assertErrMsg(t, err, "You cannot specify both usePreviousTemplate and Template Body/Template Url")

	up, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", UsePreviousTemplate: true, Parameters: []cfn.Parameter{{Key: "V", Value: "v2"}},
	})
	requireNoError(t, err)
	assertEqual(t, up.Status, cfn.StatusUpdateComplete, "a new parameter value updates")
}

// A changed Parameter Store value reaches the resource that uses it, even
// though the template and the parameter name are unchanged.
func TestSSMParameterChangeUpdatesResource(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)

	ssmValue := "v1"
	m.SetParameterReader(func(context.Context, string) (value, typ string, err error) {
		return ssmValue, "String", nil
	})

	body := `{"Parameters":{"V":{"Type":"AWS::SSM::Parameter::Value<String>","Default":"/cfg/v"}},
	"Resources":{
		"Target":{"Type":"Test::Param","Properties":{"Name":"/target","Value":{"Ref":"V"}}},
		"Copy":{"Type":"Test::Bucket","Properties":{"Name":{"Fn::Sub":"copy-${V}"}}}
	}}`

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: body})
	requireNoError(t, err)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", UsePreviousTemplate: true})
	assertErrMsg(t, err, "No updates are to be performed.")

	ssmValue = "v2"

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", UsePreviousTemplate: true, Parameters: []cfn.Parameter{{Key: "V", UsePreviousValue: true}},
	})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "status")
	assertEqual(t, p.values["/target"], "v2", "updated in place")
	assertEqual(t, *p.updates, 1, "in place")

	res, err := m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)

	for _, r := range res {
		if r.LogicalID == "Copy" && r.PhysicalID != "copy-v2" {
			t.Fatalf("Copy should be replaced as copy-v2, got %s", r.PhysicalID)
		}
	}
}

func TestCapabilitiesEnforced(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())
	m.registry["AWS::IAM::Role"] = recProv{store: newBacking()}

	unnamed := `{"Resources":{"R":{"Type":"AWS::IAM::Role","Properties":{}}}}`
	named := `{"Resources":{"R":{"Type":"AWS::IAM::Role","Properties":{"Name":"x","RoleName":"x"}}}}`

	cases := []struct {
		body string
		caps []string
		want string
	}{
		{unnamed, nil, "Requires capabilities : [CAPABILITY_IAM]"},
		{named, []string{cfn.CapabilityIAM}, "Requires capabilities : [CAPABILITY_NAMED_IAM]"},
		{unnamed, []string{cfn.CapabilityNamedIAM}, ""},
	}

	for i, c := range cases {
		_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s" + string(rune('a'+i)), TemplateBody: c.body, Capabilities: c.caps})
		if c.want == "" {
			requireNoError(t, err)
			continue
		}

		var ex *cfn.ExceptionError
		if !errors.As(err, &ex) || ex.Exception() != cfn.ExceptionInsufficientCapabilities || cerrors.Message(err) != c.want {
			t.Fatalf("case %d: err = %v", i, err)
		}
	}

	_, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "sc", TemplateBody: named})

	var ex *cfn.ExceptionError
	if !errors.As(err, &ex) {
		t.Fatalf("UpdateStack must check capabilities too, got %v", err)
	}
}

func (m *Mock) mustData(t *testing.T, name string) *stackData {
	t.Helper()

	sd, err := m.activeStack(name)
	requireNoError(t, err)

	return sd
}
