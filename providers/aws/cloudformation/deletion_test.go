package cloudformation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// stuckProv is a backend whose delete of a name in stuck fails, the way S3
// refuses to delete a bucket that still holds objects.
type stuckProv struct {
	store *backing
	stuck map[string]bool
}

func (p stuckProv) Create(_ context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := cfn.PropString(req.Properties, "Name")
	p.store.items[name] = true

	return &cfn.ProvisionedResource{PhysicalID: name}, nil
}

func (p stuckProv) Delete(_ context.Context, physicalID string, _ map[string]any) error {
	if p.stuck[physicalID] {
		return cerrors.New(cerrors.FailedPrecondition, "The bucket you tried to delete is not empty")
	}

	delete(p.store.items, physicalID)

	return nil
}

func newStuckMock(store *backing, stuck ...string) *Mock {
	m := newTestMock(store)

	p := stuckProv{store: store, stuck: map[string]bool{}}
	for _, s := range stuck {
		p.stuck[s] = true
	}

	m.registry["Test::Stuck"] = p

	return m
}

// eventStatuses lists the statuses of a resource's events, oldest first.
func eventStatuses(t *testing.T, m *Mock, stack, logicalID string) []string {
	t.Helper()

	events, err := m.DescribeStackEvents(context.Background(), stack)
	requireNoError(t, err)

	var out []string

	for i := len(events) - 1; i >= 0; i-- {
		if events[i].LogicalID == logicalID {
			out = append(out, events[i].Status)
		}
	}

	return out
}

const retainTemplate = `{"Resources":{
	"Kept":{"Type":"Test::Bucket","DeletionPolicy":"Retain","Properties":{"Name":"kept"}},
	"Gone":{"Type":"Test::Bucket","Properties":{"Name":"gone"}}
}}`

func TestDeletionPolicyRetainOnStackDelete(t *testing.T) {
	store := newBacking()
	m := newTestMock(store)

	st := createOK(t, m, "s", retainTemplate)
	deleteOK(t, m, "s")

	assertEqual(t, lastStatus(t, m, st.ID).Status, cfn.StatusDeleteComplete, "status")
	assertEqual(t, store.items["kept"], true, "retained resource")
	assertEqual(t, store.items["gone"], false, "deleted resource")

	statuses := eventStatuses(t, m, st.ID, "Kept")
	assertEqual(t, statuses[len(statuses)-1], cfn.ResourceDeleteSkipped, "retained event")
}

func TestDeletionPolicyOnUpdateRemoval(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	createOK(t, m, "s", `{"Resources":{
		"Kept":{"Type":"Test::Bucket","DeletionPolicy":"RetainExceptOnCreate","Properties":{"Name":"kept"}},
		"Gone":{"Type":"Test::Bucket","Properties":{"Name":"gone"}},
		"Stay":{"Type":"Test::Bucket","Properties":{"Name":"stay"}}
	}}`)

	_, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: `{"Resources":{"Stay":{"Type":"Test::Bucket","Properties":{"Name":"stay"}}}}`,
	})
	requireNoError(t, err)

	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateComplete, "status")
	assertEqual(t, store.items["kept"], true, "retained on removal")
	assertEqual(t, store.items["gone"], false, "deleted on removal")

	res, err := m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, len(res), 1, "resources left in the stack")
}

func TestDeletionPolicyOnCreateRollback(t *testing.T) {
	const body = `{"Resources":{
		"Retain":{"Type":"Test::Bucket","DeletionPolicy":"Retain","Properties":{"Name":"retain"}},
		"Except":{"Type":"Test::Bucket","DeletionPolicy":"RetainExceptOnCreate","Properties":{"Name":"except"}},
		"Bad":{"Type":"Test::Boom","DependsOn":["Retain","Except"]}
	}}`

	cases := []struct {
		name         string
		flag         bool
		retainExists bool
	}{
		{name: "retain kept", retainExists: true},
		{name: "RetainExceptOnCreate flag deletes retain", flag: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newBacking()
			m := newTestMock(store)

			_, err := m.CreateStack(context.Background(), &cfn.CreateStackInput{
				StackName: "s", TemplateBody: body, RetainExceptOnCreate: tc.flag,
			})
			requireNoError(t, err)

			st := stackStatus(t, m, "s")
			assertEqual(t, st.Status, cfn.StatusRollbackComplete, "status")
			assertEqual(t, st.RetainExceptOnCreate, tc.flag, "flag on the stack")
			assertEqual(t, store.items["retain"], tc.retainExists, "Retain resource")
			assertEqual(t, store.items["except"], false, "RetainExceptOnCreate resource")
		})
	}
}

func TestUpdateRollbackWithRetainExceptOnCreate(t *testing.T) {
	const next = `{"Resources":{
		"Old":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v1"}},
		"New":{"Type":"Test::Param","DeletionPolicy":"Retain","DependsOn":"Old","Properties":{"Name":"/n","Value":"new"}},
		"Bad":{"Type":"Test::Boom","DependsOn":"New"}
	}}`

	for _, flag := range []bool{false, true} {
		p := newParamProv()
		m := newParamMock(p)

		createOK(t, m, "s", paramV1)

		_, err := m.UpdateStack(context.Background(), &cfn.UpdateStackInput{
			StackName: "s", TemplateBody: next, RetainExceptOnCreate: flag,
		})
		requireNoError(t, err)

		assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateRollbackComplete, "status")

		_, kept := p.values["/n"]
		assertEqual(t, kept, !flag, "new Retain resource after rollback")
	}
}

func TestSnapshotPolicyOnATypeWithoutSnapshotsDeletes(t *testing.T) {
	store := newBacking()
	m := newTestMock(store)

	createOK(t, m, "s", `{"Resources":{"B":{"Type":"Test::Bucket","DeletionPolicy":"Snapshot","Properties":{"Name":"b"}}}}`)
	deleteOK(t, m, "s")

	assertEqual(t, store.items["b"], false, "Snapshot falls back to Delete")
}

func TestUpdateReplacePolicyRetainKeepsTheOldResource(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)

	createOK(t, m, "s", `{"Resources":{
		"P":{"Type":"Test::Param","UpdateReplacePolicy":"Retain","Properties":{"Name":"/a","Value":"v"}}
	}}`)

	_, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: `{"Resources":{
		"P":{"Type":"Test::Param","UpdateReplacePolicy":"Retain","Properties":{"Name":"/b","Value":"v"}}
	}}`})
	requireNoError(t, err)

	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateComplete, "status")

	_, oldKept := p.values["/a"]
	_, newMade := p.values["/b"]
	assertEqual(t, oldKept, true, "old resource retained")
	assertEqual(t, newMade, true, "replacement created")

	statuses := eventStatuses(t, m, "s", "P")
	assertEqual(t, statuses[len(statuses)-1], cfn.ResourceDeleteSkipped, "old resource skipped")

	deleteOK(t, m, "s")

	_, oldKept = p.values["/a"]
	_, newMade = p.values["/b"]
	assertEqual(t, oldKept, true, "retained old resource outlives the stack")
	assertEqual(t, newMade, false, "stack resource deleted")
}

func TestDeletionPolicyOnlyChangeIsAnUpdate(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	createOK(t, m, "s", `{"Resources":{"B":{"Type":"Test::Bucket","Properties":{"Name":"b"}}}}`)

	_, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "s", TemplateBody: `{"Resources":{"B":{"Type":"Test::Bucket","DeletionPolicy":"Retain","Properties":{"Name":"b"}}}}`,
	})
	requireNoError(t, err)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateComplete, "status")

	deleteOK(t, m, "s")
	assertEqual(t, store.items["b"], true, "new DeletionPolicy applies")
}

func TestDeleteFailedThenRetainResources(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newStuckMock(store, "full")

	st := createOK(t, m, "s", `{"Resources":{
		"Full":{"Type":"Test::Stuck","Properties":{"Name":"full"}},
		"Empty":{"Type":"Test::Stuck","Properties":{"Name":"empty"}}
	}}`)

	err := m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s", RetainResources: []string{"Full"}})
	assertErrorContains(t, err, "Invalid operation on stack ["+st.ID+"]. When you delete a stack, "+
		"specify which resources to retain only when the stack is in the DELETE_FAILED state.")

	deleteOK(t, m, "s")

	st = stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusDeleteFailed, "status")
	assertEqual(t, st.StatusReason, "The following resource(s) failed to delete: [Full].", "reason")
	assertEqual(t, store.items["empty"], false, "deletable resource deleted")

	res, err := m.DescribeStackResources(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, len(res), 1, "failed resource kept")
	assertEqual(t, res[0].Status, cfn.ResourceDeleteFailed, "resource status")

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", UsePreviousTemplate: true})
	assertErrorContains(t, err, "is in DELETE_FAILED state and can not be updated")

	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s", RetainResources: []string{"Full"}}))
	assertEqual(t, lastStatus(t, m, st.ID).Status, cfn.StatusDeleteComplete, "retry status")
	assertEqual(t, store.items["full"], true, "retained resource")
}

func TestForceDeleteStackKeepsWhatCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newStuckMock(store, "full")

	st := createOK(t, m, "s", `{"Resources":{"Full":{"Type":"Test::Stuck","Properties":{"Name":"full"}}}}`)

	deleteOK(t, m, "s")
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusDeleteFailed, "first delete")

	err := m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s", DeletionMode: "WRONG"})
	assertErrorContains(t, err, "Member must satisfy enum value set: [STANDARD, FORCE_DELETE_STACK]")

	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s", DeletionMode: cfn.DeletionModeForceDelete}))

	final := lastStatus(t, m, st.ID)
	assertEqual(t, final.Status, cfn.StatusDeleteComplete, "forced status")
	assertEqual(t, final.DeletionMode, cfn.DeletionModeForceDelete, "deletion mode")
	assertEqual(t, store.items["full"], true, "undeletable resource kept")
}

func TestTerminationProtectionBlocksDelete(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: twoResourceTemplate, EnableTerminationProtection: true,
	})
	requireNoError(t, err)
	assertEqual(t, stackStatus(t, m, "s").EnableTerminationProtection, true, "protection on")

	err = m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"})
	assertErrorContains(t, err, "Stack [s] cannot be deleted while TerminationProtection is enabled")

	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want a ValidationError, got %v", err)
	}

	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusCreateComplete, "stack unchanged")

	id, err := m.UpdateTerminationProtection(ctx, &cfn.UpdateTerminationProtectionInput{StackName: "s"})
	requireNoError(t, err)
	assertEqual(t, id, stackStatus(t, m, "s").ID, "returned id")

	deleteOK(t, m, "s")
	assertEqual(t, lastStatus(t, m, id).Status, cfn.StatusDeleteComplete, "deleted")

	_, err = m.UpdateTerminationProtection(ctx, &cfn.UpdateTerminationProtectionInput{StackName: "s", Enable: true})
	assertErrorContains(t, err, "does not exist")
}

func TestCreateStackFailureOptions(t *testing.T) {
	const body = `{"Resources":{
		"A":{"Type":"Test::Bucket","Properties":{"Name":"a"}},
		"Bad":{"Type":"Test::Boom","DependsOn":"A"}
	}}`

	cases := []struct {
		name            string
		in              cfn.CreateStackInput
		status          string
		kept            bool
		disableRollback bool
	}{
		{name: "default rolls back", status: cfn.StatusRollbackComplete},
		{name: "DO_NOTHING", in: cfn.CreateStackInput{OnFailure: cfn.OnStackFailureDoNothing},
			status: cfn.StatusCreateFailed, kept: true, disableRollback: true},
		{name: "DisableRollback", in: cfn.CreateStackInput{DisableRollback: true},
			status: cfn.StatusCreateFailed, kept: true, disableRollback: true},
		{name: "DELETE", in: cfn.CreateStackInput{OnFailure: cfn.OnStackFailureDelete}, status: cfn.StatusDeleteComplete},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newBacking()
			m := newTestMock(store)

			in := tc.in
			in.StackName, in.TemplateBody = "s", body

			out, err := m.CreateStack(context.Background(), &in)
			requireNoError(t, err)

			st := lastStatus(t, m, out.ID)
			assertEqual(t, st.Status, tc.status, "status")
			assertEqual(t, st.DisableRollback, tc.disableRollback, "DisableRollback")
			assertEqual(t, store.items["a"], tc.kept, "created resource")
		})
	}
}

func TestCreateStackFailureOptionErrors(t *testing.T) {
	m := newTestMock(newBacking())
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "s", TemplateBody: twoResourceTemplate, OnFailure: cfn.OnStackFailureDelete, DisableRollback: true,
	})
	assertErrorContains(t, err, "Either DisableRollback or OnFailure can be specified, not both.")

	_, err = m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: twoResourceTemplate, OnFailure: "KEEP"})
	assertErrorContains(t, err, "Value 'KEEP' at 'onFailure' failed to satisfy constraint")
}

func TestStackLimitIsEnforced(t *testing.T) {
	m := newTestMock(newBacking())

	for i := range limitStacks {
		m.stacks.Set(fmt.Sprintf("filler-%d", i), &stackData{
			stack: cfn.Stack{Name: "filler", Status: cfn.StatusCreateComplete},
		})
	}

	_, err := m.CreateStack(context.Background(), &cfn.CreateStackInput{StackName: "one-more", TemplateBody: twoResourceTemplate})
	assertErrorContains(t, err, "Limit for stacks has been exceeded")

	var named *cfn.ExceptionError
	if !errors.As(err, &named) || named.Exception() != cfn.ExceptionLimitExceeded {
		t.Fatalf("want LimitExceededException, got %v", err)
	}
}

func TestDescribeAccountLimitsAndEstimateTemplateCost(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	limits, err := m.DescribeAccountLimits(ctx, "")
	requireNoError(t, err)
	assertEqual(t, len(limits), 3, "limits")
	assertEqual(t, limits[0].Name, "StackLimit", "first limit")
	assertEqual(t, limits[0].Value, 2000, "stack limit")
	assertEqual(t, limits[1].Value, 200, "outputs limit")
	assertEqual(t, limits[2].Name, "ConcurrentResourcesLimit", "third limit")

	in := &cfn.EstimateTemplateCostInput{TemplateBody: twoResourceTemplate}

	u1, err := m.EstimateTemplateCost(ctx, in)
	requireNoError(t, err)

	u2, err := m.EstimateTemplateCost(ctx, in)
	requireNoError(t, err)
	assertEqual(t, u1, u2, "same input, same link")

	if !strings.HasPrefix(u1, "https://calculator.aws/#/estimate?id=") {
		t.Fatalf("url = %s", u1)
	}

	_, err = m.EstimateTemplateCost(ctx, &cfn.EstimateTemplateCostInput{TemplateBody: "{"})
	if err == nil {
		t.Fatal("bad template accepted")
	}
}

func TestChangeSetReportsPolicyActions(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)

	createOK(t, m, "s", `{"Resources":{
		"Dropped":{"Type":"Test::Param","DeletionPolicy":"Retain","Properties":{"Name":"/d","Value":"v"}},
		"Replaced":{"Type":"Test::Param","UpdateReplacePolicy":"Retain","Properties":{"Name":"/r","Value":"v"}}
	}}`)

	_, err := m.CreateChangeSet(ctx, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "cs", TemplateBody: `{"Resources":{
		"Replaced":{"Type":"Test::Param","UpdateReplacePolicy":"Retain","Properties":{"Name":"/r2","Value":"v"}}
	}}`})
	requireNoError(t, err)

	cs, err := m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{StackName: "s", ChangeSetName: "cs"})
	requireNoError(t, err)

	got := map[string]string{}
	for _, c := range cs.Changes {
		got[c.LogicalID] = c.PolicyAction
	}

	assertEqual(t, got["Dropped"], cfn.PolicyRetain, "Remove policy action")
	assertEqual(t, got["Replaced"], cfn.PolicyReplaceAndRetain, "replace policy action")
}
