package cloudformation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

const csV1 = `{"Resources":{
	"P":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v1"}},
	"Q":{"Type":"Test::Param","Properties":{"Name":"/q","Value":"q"}}
}}`

// csV2 changes P in place and renames Q, which replaces it.
const csV2 = `{"Resources":{
	"P":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v2"}},
	"Q":{"Type":"Test::Param","Properties":{"Name":"/q2","Value":"q"}}
}}`

const csFailing = `{"Resources":{
	"P":{"Type":"Test::Param","Properties":{"Name":"/p","Value":"v1"}},
	"Bad":{"Type":"Test::Boom","DependsOn":"P"}
}}`

func createCS(t *testing.T, m *Mock, in *cfn.CreateChangeSetInput) *cfn.ChangeSet {
	t.Helper()

	cs, err := m.CreateChangeSet(context.Background(), in)
	requireNoError(t, err)

	return cs
}

func describeCS(t *testing.T, m *Mock, stack, name string) *cfn.ChangeSet {
	t.Helper()

	cs, err := m.DescribeChangeSet(context.Background(), &cfn.DescribeChangeSetInput{StackName: stack, ChangeSetName: name})
	requireNoError(t, err)

	return cs
}

func assertException(t *testing.T, err error, exception, msg string) {
	t.Helper()

	var named *cfn.ExceptionError
	if !errors.As(err, &named) {
		t.Fatalf("want %s, got %v", exception, err)
	}

	assertEqual(t, named.Exception(), exception, "exception")

	if msg != "" {
		assertEqual(t, cerrors.Message(err), msg, "message")
	}
}

func assertValidation(t *testing.T, err error, msg string) {
	t.Helper()

	if !cerrors.IsInvalidArgument(err) && !cerrors.IsNotFound(err) {
		t.Fatalf("want a ValidationError, got %v", err)
	}

	var named *cfn.ExceptionError
	if errors.As(err, &named) {
		t.Fatalf("want a ValidationError, got %s", named.Exception())
	}

	if msg != "" {
		assertEqual(t, cerrors.Message(err), msg, "message")
	}
}

func TestCreateChangeSetCreateMakesReviewStack(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	cs := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "cs1", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
		Description: "first", Tags: map[string]string{"team": "a"},
	})

	if !strings.Contains(cs.ID, ":changeSet/cs1/") || !strings.HasPrefix(cs.ID, "arn:aws:cloudformation:us-east-1:123456789012:") {
		t.Fatalf("change set id %q", cs.ID)
	}

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusReviewInProgress, "stack status")
	assertEqual(t, st.StatusReason, "User Initiated", "stack reason")
	assertEqual(t, cs.StackID, st.ID, "stack id")
	assertEqual(t, len(st.Resources), 0, "no resources yet")
	assertEqual(t, len(p.values), 0, "nothing provisioned")

	if !st.LastUpdated.IsZero() {
		t.Fatalf("a review stack has no LastUpdatedTime")
	}

	events, err := m.DescribeStackEvents(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, len(events), 1, "events")
	assertEqual(t, events[0].Status, cfn.StatusReviewInProgress, "event status")

	got := describeCS(t, m, "s", "cs1")
	assertEqual(t, got.Status, cfn.ChangeSetStatusCreateComplete, "status")
	assertEqual(t, got.ExecutionStatus, cfn.ExecutionAvailable, "execution status")
	assertEqual(t, got.Description, "first", "description")
	assertEqual(t, got.Tags["team"], "a", "tags")
	assertEqual(t, len(got.Changes), 2, "changes")
	assertEqual(t, got.Changes[0].Action, cfn.ChangeActionAdd, "action")
	assertEqual(t, got.Changes[0].LogicalID, "P", "logical id")
	assertEqual(t, got.Changes[0].ResourceType, "Test::Param", "type")

	// The review stack holds the name: CreateStack and UpdateStack refuse it.
	_, err = m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: csV1})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("CreateStack on a review stack: %v", err)
	}

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: csV1})
	assertValidation(t, err, "Stack:"+st.ID+" is in REVIEW_IN_PROGRESS state and can not be updated.")
}

func TestExecuteCreateChangeSet(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	cs1 := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "cs1", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
	})
	cs2 := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "cs2", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV2,
	})
	assertEqual(t, cs2.StackID, cs1.StackID, "second CREATE change set joins the review stack")

	requireNoError(t, m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs1.ID}))

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusCreateComplete, "stack status")
	assertEqual(t, st.ChangeSetID, cs1.ID, "stack change set id")
	assertEqual(t, p.values["/p"], "v1", "P created")
	assertEqual(t, p.values["/q"], "q", "Q created")

	got := describeCS(t, m, "s", "cs1")
	assertEqual(t, got.ExecutionStatus, cfn.ExecutionComplete, "execution status")

	_, err := m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{StackName: "s", ChangeSetName: "cs2"})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "ChangeSet [cs2] does not exist")

	list, err := m.ListChangeSets(ctx, &cfn.ListChangeSetsInput{StackName: "s"})
	requireNoError(t, err)
	assertEqual(t, len(list.Summaries), 1, "only the executed change set is left")

	events, err := m.DescribeStackEvents(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, events[0].Status, cfn.StatusCreateComplete, "last event")
	assertEqual(t, events[len(events)-1].Status, cfn.StatusReviewInProgress, "first event")
}

func TestCreateChangeSetErrors(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "live", TemplateBody: csV1})
	requireNoError(t, err)
	createCS(t, m, &cfn.CreateChangeSetInput{StackName: "review", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1})

	reviewID := stackStatus(t, m, "review").ID

	tests := []struct {
		name string
		in   cfn.CreateChangeSetInput
		exc  string
		msg  string
	}{
		{"update missing stack", cfn.CreateChangeSetInput{StackName: "nope", ChangeSetName: "c", TemplateBody: csV1},
			"", "Stack [nope] does not exist"},
		{"create over live stack", cfn.CreateChangeSetInput{
			StackName: "live", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
		}, "", "Stack [live] already exists and cannot be created again with the changeSet [c]."},
		{"update review stack", cfn.CreateChangeSetInput{StackName: "review", ChangeSetName: "u", TemplateBody: csV1},
			"", "Stack:" + reviewID + " is in REVIEW_IN_PROGRESS state and can not be updated."},
		{"duplicate name", cfn.CreateChangeSetInput{
			StackName: "review", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
		}, cfn.ExceptionAlreadyExists, "ChangeSet [c] already exists"},
		{"bad name", cfn.CreateChangeSetInput{StackName: "live", ChangeSetName: "1bad", TemplateBody: csV2},
			"", "1 validation error detected: Value '1bad' at 'changeSetName' failed to satisfy constraint: " +
				"Member must satisfy regular expression pattern: [a-zA-Z][-a-zA-Z0-9]*"},
		{"bad type", cfn.CreateChangeSetInput{StackName: "live", ChangeSetName: "c", ChangeSetType: "MERGE", TemplateBody: csV2},
			"", "1 validation error detected: Value 'MERGE' at 'changeSetType' failed to satisfy constraint: " +
				"Member must satisfy enum value set: [CREATE, UPDATE, IMPORT]"},
		{"delete on update", cfn.CreateChangeSetInput{
			StackName: "live", ChangeSetName: "c", TemplateBody: csV2, OnStackFailure: cfn.OnStackFailureDelete,
		}, "", msgOnFailureDeleteUpdate},
		{"bad template", cfn.CreateChangeSetInput{
			StackName: "fresh", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: "{",
		}, "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, cerr := m.CreateChangeSet(ctx, &tc.in)
			if tc.exc != "" {
				assertException(t, cerr, tc.exc, tc.msg)
				return
			}

			assertValidation(t, cerr, tc.msg)
		})
	}

	// A template error on a CREATE change set leaves no review stack behind.
	if _, err = m.DescribeStacks(ctx, "fresh"); err == nil {
		t.Fatalf("a failed CREATE change set must not create a stack")
	}
}

func TestUpdateChangeSetDescribesAndExecutes(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: csV1})
	requireNoError(t, err)

	cs := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "upd", TemplateBody: csV2})
	other := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "other", TemplateBody: csV2})

	got := describeCS(t, m, "s", cs.ID)
	assertEqual(t, got.Status, cfn.ChangeSetStatusCreateComplete, "status")
	assertEqual(t, len(got.Changes), 2, "changes")

	pc, qc := got.Changes[0], got.Changes[1]
	assertEqual(t, pc.LogicalID+pc.Action+pc.Replacement+pc.PhysicalID, "PModifyFalse/p", "P change")
	assertEqual(t, pc.Details[0].Target.Name, "Value", "P detail")
	assertEqual(t, pc.Details[0].Target.RequiresRecreation, cfn.RecreationNever, "P recreation")
	assertEqual(t, qc.LogicalID+qc.Action+qc.Replacement+qc.PolicyAction, "QModifyTrueReplaceAndDelete", "Q change")
	assertEqual(t, qc.Details[0].Target.RequiresRecreation, cfn.RecreationAlways, "Q recreation")

	if got.Changes[0].Details[0].Target.BeforeValue != "" {
		t.Fatalf("property values are only returned with IncludePropertyValues")
	}

	withValues, err := m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: cs.ID, IncludePropertyValues: true})
	requireNoError(t, err)
	assertEqual(t, withValues.Changes[0].Details[0].Target.BeforeValue, "v1", "before value")
	assertEqual(t, withValues.Changes[0].Details[0].Target.AfterValue, "v2", "after value")

	// Nothing changes until the change set runs.
	assertEqual(t, p.values["/p"], "v1", "P untouched")

	requireNoError(t, m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{StackName: "s", ChangeSetName: "upd"}))

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "stack status")
	assertEqual(t, p.values["/p"], "v2", "P updated in place")
	assertEqual(t, p.values["/q2"], "q", "Q replaced")

	if _, ok := p.values["/q"]; ok {
		t.Fatalf("old Q must be deleted in cleanup")
	}

	assertEqual(t, describeCS(t, m, "s", "upd").ExecutionStatus, cfn.ExecutionComplete, "executed")

	_, err = m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: other.ID})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "")
}

func TestNoChangeChangeSetFails(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: csV1})
	requireNoError(t, err)

	cs := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "same", TemplateBody: csV1})

	got := describeCS(t, m, "s", "same")
	assertEqual(t, got.Status, cfn.ChangeSetStatusFailed, "status")
	assertEqual(t, got.ExecutionStatus, cfn.ExecutionUnavailable, "execution status")
	assertEqual(t, got.StatusReason, msgNoChanges, "reason")
	assertEqual(t, len(got.Changes), 0, "changes")

	err = m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID})
	assertException(t, err, cfn.ExceptionInvalidChangeSetStatus,
		"ChangeSet ["+cs.ID+"] cannot be executed in its current status of [FAILED]")

	// UsePreviousTemplate with no other change is also empty.
	createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "prev", UsePreviousTemplate: true})
	assertEqual(t, describeCS(t, m, "s", "prev").Status, cfn.ChangeSetStatusFailed, "previous template")
}

func TestExecuteChangeSetStatusErrors(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: csV1})
	requireNoError(t, err)

	done := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "done", TemplateBody: csV2})
	requireNoError(t, m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: done.ID}))

	err = m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: done.ID})
	assertException(t, err, cfn.ExceptionInvalidChangeSetStatus,
		"ChangeSet ["+done.ID+"] cannot be executed in its current execution status of [EXECUTE_COMPLETE]")

	// A direct UpdateStack makes the stack's open change sets obsolete.
	stale := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "stale", TemplateBody: csV1})
	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: csV1})
	requireNoError(t, err)

	assertEqual(t, describeCS(t, m, "s", "stale").ExecutionStatus, cfn.ExecutionObsolete, "obsolete")

	err = m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: stale.ID})
	assertException(t, err, cfn.ExceptionInvalidChangeSetStatus,
		"ChangeSet ["+stale.ID+"] cannot be executed in its current execution status of [OBSOLETE]")
}

func TestChangeSetLookup(t *testing.T) {
	m := newParamMock(newParamProv())
	ctx := context.Background()

	cs := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
	})

	byARN, err := m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: cs.ID})
	requireNoError(t, err)
	assertEqual(t, byARN.Name, "c", "by ARN")
	assertEqual(t, byARN.StackName, "s", "stack name")

	byStackID := describeCS(t, m, cs.StackID, "c")
	assertEqual(t, byStackID.ID, cs.ID, "by stack id")

	_, err = m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: "c"})
	assertValidation(t, err, "StackName must be specified if ChangeSetName is not specified as an ARN.")

	_, err = m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{StackName: "s", ChangeSetName: "nope"})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "ChangeSet [nope] does not exist")

	_, err = m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{StackName: "missing", ChangeSetName: "c"})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "ChangeSet [c] does not exist")

	err = m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{StackName: "s", ChangeSetName: "nope"})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "")

	_, err = m.ListChangeSets(ctx, &cfn.ListChangeSetsInput{StackName: "missing"})
	assertValidation(t, err, "")
}

func TestExecuteChangeSetOnStackFailure(t *testing.T) {
	tests := []struct {
		name       string
		onFailure  string
		disable    *bool
		wantStatus string
		wantKept   bool
	}{
		{name: "default rolls back", wantStatus: cfn.StatusRollbackComplete},
		{name: "rollback", onFailure: cfn.OnStackFailureRollback, wantStatus: cfn.StatusRollbackComplete},
		{name: "do nothing", onFailure: cfn.OnStackFailureDoNothing, wantStatus: cfn.StatusCreateFailed, wantKept: true},
		{name: "disable rollback", disable: boolPtr(true), wantStatus: cfn.StatusCreateFailed, wantKept: true},
		{name: "delete", onFailure: cfn.OnStackFailureDelete, wantStatus: cfn.StatusDeleteComplete},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newParamProv()
			m := newParamMock(p)
			ctx := context.Background()

			cs := createCS(t, m, &cfn.CreateChangeSetInput{
				StackName: "s", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate,
				TemplateBody: csFailing, OnStackFailure: tc.onFailure,
			})

			requireNoError(t, m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID, DisableRollback: tc.disable}))

			st, err := m.DescribeStacks(ctx, cs.StackID)
			requireNoError(t, err)
			assertEqual(t, st[0].Status, tc.wantStatus, "stack status")

			_, kept := p.values["/p"]
			assertEqual(t, kept, tc.wantKept, "created resource kept")

			if tc.wantKept {
				assertEqual(t, st[0].DisableRollback, true, "DisableRollback")
				assertEqual(t, st[0].StatusReason, "The following resource(s) failed to create: [Bad].", "reason")
				assertEqual(t, describeCS(t, m, "s", "c").ExecutionStatus, cfn.ExecutionFailed, "execution failed")
			}
		})
	}
}

func TestExecuteChangeSetRejectsBothFailureOptions(t *testing.T) {
	m := newParamMock(newParamProv())

	cs := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate,
		TemplateBody: csV1, OnStackFailure: cfn.OnStackFailureRollback,
	})

	err := m.ExecuteChangeSet(context.Background(), &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID, DisableRollback: boolPtr(false)})
	assertValidation(t, err, msgBothFailureOptions)
	assertEqual(t, describeCS(t, m, "s", "c").ExecutionStatus, cfn.ExecutionAvailable, "still available")
}

const keepV1 = `{"Resources":{
	"A":{"Type":"Test::Param","Properties":{"Name":"/a","Value":"data"}},
	"B":{"Type":"Test::Param","DependsOn":"A","Properties":{"Name":"/b","Value":"b"}}
}}`

// keepFailing replaces A, then fails to replace B onto a name in use.
const keepFailing = `{"Resources":{
	"A":{"Type":"Test::Param","Properties":{"Name":"/a2","Value":"data"}},
	"B":{"Type":"Test::Param","DependsOn":"A","Properties":{"Name":"/taken","Value":"b"}}
}}`

// failDoNothing leaves stack "s" UPDATE_FAILED after A was replaced and B
// failed, with rollback disabled.
func failDoNothing(t *testing.T, m *Mock, p paramProv) {
	t.Helper()

	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: keepV1})
	requireNoError(t, err)

	p.values["/taken"] = "outside"

	cs := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "c", TemplateBody: keepFailing, OnStackFailure: cfn.OnStackFailureDoNothing,
	})
	requireNoError(t, m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID}))
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateFailed, "stack status")
}

// A failed update that is not rolled back keeps the old physical resource
// of a replacement. Only a later successful update cleans it up.
func TestDoNothingKeepsReplacedResources(t *testing.T) {
	ctx := context.Background()

	t.Run("kept until a successful update", func(t *testing.T) {
		p := newParamProv()
		m := newParamMock(p)
		failDoNothing(t, m, p)

		assertEqual(t, p.values["/a"], "data", "old A kept with its data")
		assertEqual(t, p.values["/a2"], "data", "new A created")
		assertEqual(t, p.values["/taken"], "outside", "outside resource untouched")

		_, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: strings.ReplaceAll(keepFailing, "/taken", "/b3")})
		requireNoError(t, err)
		assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateComplete, "retried")

		if _, ok := p.values["/a"]; ok {
			t.Fatalf("the old A must be cleaned up after the successful update")
		}

		assertEqual(t, p.values["/a2"], "data", "new A kept")
	})

	t.Run("survives a snapshot and is deleted with the stack", func(t *testing.T) {
		p := newParamProv()
		m := newParamMock(p)
		failDoNothing(t, m, p)

		data, err := m.Snapshot(ctx, false)
		requireNoError(t, err)

		restored := newParamMock(p)
		requireNoError(t, restored.Restore(ctx, json.RawMessage(data)))
		requireNoError(t, restored.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))

		for _, name := range []string{"/a", "/a2", "/b"} {
			if _, ok := p.values[name]; ok {
				t.Fatalf("%s left behind after DeleteStack", name)
			}
		}

		assertEqual(t, p.values["/taken"], "outside", "outside resource untouched")
	})
}

// After a DO_NOTHING failure the stack keeps each resource's last applied
// state: A at its new name, B at its old one, because B never changed. An
// update back to the first template takes the retained /a back instead of
// creating it again, and never touches the name B failed on.
func TestDoNothingFailureKeepsLastAppliedState(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)
	failDoNothing(t, m, p)

	body, err := m.GetTemplate(ctx, "s")
	requireNoError(t, err)

	assertEqual(t, body, keepFailing, "the stack keeps the submitted template verbatim")

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: keepV1})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "back to the first template")
	assertEqual(t, p.values["/a"], "data", "retained A taken back with its data")
	assertEqual(t, p.values["/b"], "b", "B untouched")
	assertEqual(t, p.values["/taken"], "outside", "outside resource untouched")

	if _, ok := p.values["/a2"]; ok {
		t.Fatalf("the A created by the failed update must be cleaned up")
	}

	assertEqual(t, len(m.mustData(t, "s").retained), 0, "nothing left retained")

	// A second failure now rolls back to the last applied state, not onto
	// the name B failed on.
	p2 := newParamProv()
	m2 := newParamMock(p2)
	failDoNothing(t, m2, p2)

	failing := strings.Replace(keepV1, `}
}}`, `},
	"Bad":{"Type":"Test::Boom","DependsOn":"B"}
}}`, 1)

	st, err = m2.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: failing})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "rollback to the last applied state")
	assertEqual(t, p2.values["/taken"], "outside", "outside resource untouched")
	assertEqual(t, p2.values["/b"], "b", "B untouched")
	assertEqual(t, p2.values["/a"], "data", "the retained A taken back and rolled back keeps its data")
	assertEqual(t, p2.values["/a2"], "data", "A points at /a2 again")

	// The retained A is still retained, so a later update takes it back.
	st, err = m2.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: keepV1})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "back to the first template")
	assertEqual(t, p2.values["/a"], "data", "A back at /a with its data")
}

// The documented retry after UPDATE_FAILED: fix the cause, then update with
// the previous template. The resources that failed or never ran are applied.
func TestRetryAfterUpdateFailedWithPreviousTemplate(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)
	failDoNothing(t, m, p)

	delete(p.values, "/taken")

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", UsePreviousTemplate: true, DisableRollback: true})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "retry status")
	assertEqual(t, p.values["/taken"], "b", "B now at its new name")
	assertEqual(t, p.values["/a2"], "data", "A kept at its new name")

	for _, gone := range []string{"/a", "/b"} {
		if _, ok := p.values[gone]; ok {
			t.Fatalf("%s must be cleaned up after the successful retry", gone)
		}
	}

	body, err := m.GetTemplate(ctx, "s")
	requireNoError(t, err)
	assertEqual(t, body, keepFailing, "template kept verbatim")
}

// UpdateStack with DisableRollback leaves a failed update UPDATE_FAILED
// instead of rolling it back.
func TestUpdateStackDisableRollback(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV2, DisableRollback: true})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateFailed, "status")
	assertEqual(t, st.DisableRollback, true, "DisableRollback")
	assertEqual(t, p.values["/p"], "v2", "Old not rolled back")
	assertEqual(t, p.values["/n"], "new", "New kept")

	// Without it the next failure rolls back to the last applied state.
	st, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: strings.Replace(paramV2, `"v2"`, `"v3"`, 1)})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateRollbackComplete, "rolled back")
	assertEqual(t, st.DisableRollback, false, "DisableRollback cleared")
	assertEqual(t, p.values["/p"], "v2", "Old back at its last applied value")
	assertEqual(t, p.values["/n"], "new", "New kept")
}

// A retained resource deleted out of band is created again by a later
// update, and the cleanup must not delete that new resource.
func TestRetainedCleanupSparesRecreatedResource(t *testing.T) {
	ctx := context.Background()
	p := newParamProv()
	m := newParamMock(p)
	failDoNothing(t, m, p)

	delete(p.values, "/a")

	st, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: keepV1})
	requireNoError(t, err)
	assertEqual(t, st.Status, cfn.StatusUpdateComplete, "status")
	assertEqual(t, p.values["/a"], "data", "the recreated A survives cleanup")

	if _, ok := p.values["/a2"]; ok {
		t.Fatalf("the A created by the failed update must be cleaned up")
	}

	assertEqual(t, len(m.mustData(t, "s").retained), 0, "nothing left retained")
}

func TestUpdateChangeSetDoNothingLeavesUpdateFailed(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)

	cs := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "c", TemplateBody: paramV2, OnStackFailure: cfn.OnStackFailureDoNothing,
	})
	requireNoError(t, m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID}))

	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusUpdateFailed, "stack status")
	assertEqual(t, st.StatusReason, "The following resource(s) failed to create: [Bad].", "reason")
	assertEqual(t, p.values["/p"], "v2", "no rollback of Old")
	assertEqual(t, p.values["/n"], "new", "New kept")

	// A stack left UPDATE_FAILED can be updated again.
	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: paramV1})
	requireNoError(t, err)
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusUpdateComplete, "retried")
}

func TestDeleteChangeSet(t *testing.T) {
	m := newParamMock(newParamProv())
	ctx := context.Background()

	cs := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
	})

	requireNoError(t, m.DeleteChangeSet(ctx, &cfn.DeleteChangeSetInput{StackName: "s", ChangeSetName: "c"}))

	_, err := m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: cs.ID})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "")

	err = m.DeleteChangeSet(ctx, &cfn.DeleteChangeSetInput{ChangeSetName: cs.ID})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "")

	// By name, deleting a change set that is gone succeeds while the stack
	// exists, which is what lets the CDK clear a fixed change set name.
	requireNoError(t, m.DeleteChangeSet(ctx, &cfn.DeleteChangeSetInput{StackName: "s", ChangeSetName: "c"}))

	err = m.DeleteChangeSet(ctx, &cfn.DeleteChangeSetInput{StackName: "missing", ChangeSetName: "c"})
	assertValidation(t, err, "Stack [missing] does not exist")

	// Deleting the only change set leaves the review stack in place, as AWS
	// does. A later CREATE change set reuses it.
	st := stackStatus(t, m, "s")
	assertEqual(t, st.Status, cfn.StatusReviewInProgress, "stack stays in review")

	list, err := m.ListChangeSets(ctx, &cfn.ListChangeSetsInput{StackName: "s"})
	requireNoError(t, err)
	assertEqual(t, len(list.Summaries), 0, "no change sets")

	again := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
	})
	assertEqual(t, again.StackID, st.ID, "same review stack")

	// DeleteStack removes the review stack and its change sets.
	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))

	_, err = m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: again.ID})
	assertException(t, err, cfn.ExceptionChangeSetNotFound, "")

	gone, err := m.DescribeStacks(ctx, st.ID)
	requireNoError(t, err)
	assertEqual(t, gone[0].Status, cfn.StatusDeleteComplete, "deleted")
}

func TestListChangeSetsPaginates(t *testing.T) {
	m := newParamMock(newParamProv())
	ctx := context.Background()

	const total = changeSetsPageSize + 5

	for i := range total {
		createCS(t, m, &cfn.CreateChangeSetInput{
			StackName: "s", ChangeSetName: fmt.Sprintf("c%03d", i), ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
		})
	}

	first, err := m.ListChangeSets(ctx, &cfn.ListChangeSetsInput{StackName: "s"})
	requireNoError(t, err)
	assertEqual(t, len(first.Summaries), changeSetsPageSize, "first page")

	if first.NextToken == "" {
		t.Fatalf("want a NextToken")
	}

	second, err := m.ListChangeSets(ctx, &cfn.ListChangeSetsInput{StackName: "s", NextToken: first.NextToken})
	requireNoError(t, err)
	assertEqual(t, len(second.Summaries), 5, "second page")
	assertEqual(t, second.NextToken, "", "last page")
	assertEqual(t, second.Summaries[4].Name, "c104", "creation order")
	assertEqual(t, len(second.Summaries[0].Changes), 0, "summaries carry no changes")
}

// DescribeChangeSet pages the changes once a response passes 1 MB.
func TestDescribeChangeSetPagesLargeChanges(t *testing.T) {
	m := newParamMock(newParamProv())
	ctx := context.Background()

	big := strings.Repeat("x", 400*1024)
	body := fmt.Sprintf(`{"Resources":{
		"A":{"Type":"Test::Param","Properties":{"Name":"/a","Value":%q}},
		"B":{"Type":"Test::Param","Properties":{"Name":"/b","Value":%q}},
		"C":{"Type":"Test::Param","Properties":{"Name":"/c","Value":%q}}
	}}`, big, big, big)

	cs := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: body})

	plain := describeCS(t, m, "s", "c")
	assertEqual(t, len(plain.Changes), 3, "small changes fit one page")
	assertEqual(t, plain.NextToken, "", "no token")

	var names []string

	token := ""

	for range 3 {
		page, err := m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{
			ChangeSetName: cs.ID, IncludePropertyValues: true, NextToken: token,
		})
		requireNoError(t, err)

		for _, c := range page.Changes {
			names = append(names, c.LogicalID)
		}

		if token = page.NextToken; token == "" {
			break
		}
	}

	assertEqual(t, strings.Join(names, ","), "A,B,C", "all changes across pages")

	first, err := m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: cs.ID, IncludePropertyValues: true})
	requireNoError(t, err)
	assertEqual(t, len(first.Changes), 2, "first page holds what fits in 1 MB")

	_, err = m.DescribeChangeSet(ctx, &cfn.DescribeChangeSetInput{ChangeSetName: cs.ID, NextToken: "bogus"})
	assertValidation(t, err, "")
}

func TestChangeSetSnapshotRoundTrip(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: csV1})
	requireNoError(t, err)

	cs := createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "upd", TemplateBody: csV2, Tags: map[string]string{"k": "v"},
	})
	createCS(t, m, &cfn.CreateChangeSetInput{StackName: "r", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1})

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newParamMock(p)
	requireNoError(t, restored.Restore(ctx, json.RawMessage(data)))

	got := describeCS(t, restored, "s", "upd")
	assertEqual(t, got.ID, cs.ID, "id kept")
	assertEqual(t, got.ExecutionStatus, cfn.ExecutionAvailable, "status kept")
	assertEqual(t, len(got.Changes), 2, "changes kept")
	assertEqual(t, stackStatus(t, restored, "r").Status, cfn.StatusReviewInProgress, "review stack kept")

	requireNoError(t, restored.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID}))
	assertEqual(t, p.values["/p"], "v2", "restored change set executes")
	assertEqual(t, stackStatus(t, restored, "s").Tags["k"], "v", "change set tags applied")
}

// Two concurrent executions of one change set: exactly one runs.
func TestConcurrentExecuteChangeSet(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: csV1})
	requireNoError(t, err)

	cs := createCS(t, m, &cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "c", TemplateBody: csV2})

	const workers = 8

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: cs.ID}) == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assertEqual(t, wins, 1, "executions that ran")
	assertEqual(t, *p.updates, 1, "P updated once")
}

// A CreateChangeSet retry with the same ClientToken returns the change set it
// made. Another token, or none, is a duplicate name. An ExecuteChangeSet
// retry with the same ClientRequestToken succeeds without running again.
func TestChangeSetClientTokens(t *testing.T) {
	p := newParamProv()
	m := newParamMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: csV1})
	requireNoError(t, err)

	in := cfn.CreateChangeSetInput{StackName: "s", ChangeSetName: "c", TemplateBody: csV2, ClientToken: "tok-1"}
	first := createCS(t, m, &in)

	retry := in
	again := createCS(t, m, &retry)
	assertEqual(t, again.ID, first.ID, "retry returns the same change set")

	for _, token := range []string{"tok-2", ""} {
		other := in
		other.ClientToken = token
		_, err = m.CreateChangeSet(ctx, &other)
		assertException(t, err, cfn.ExceptionAlreadyExists, "ChangeSet [c] already exists")
	}

	exec := &cfn.ExecuteChangeSetInput{ChangeSetName: first.ID, ClientRequestToken: "run-1"}
	requireNoError(t, m.ExecuteChangeSet(ctx, exec))
	requireNoError(t, m.ExecuteChangeSet(ctx, exec))
	assertEqual(t, *p.updates, 1, "executed once")

	err = m.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: first.ID, ClientRequestToken: "run-2"})
	assertException(t, err, cfn.ExceptionInvalidChangeSetStatus, "")
}

// GetTemplateSummary on a stack still in review summarizes the template of
// its change set.
func TestGetTemplateSummaryReviewStack(t *testing.T) {
	m := newParamMock(newParamProv())
	ctx := context.Background()

	createCS(t, m, &cfn.CreateChangeSetInput{
		StackName: "s", ChangeSetName: "c", ChangeSetType: cfn.ChangeSetTypeCreate, TemplateBody: csV1,
	})

	sum, err := m.GetTemplateSummary(ctx, &cfn.GetTemplateSummaryInput{StackName: "s"})
	requireNoError(t, err)
	assertEqual(t, strings.Join(sum.ResourceTypes, ","), "Test::Param", "resource types")
}

func boolPtr(b bool) *bool { return &b }
