package cloudformation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

func mustTemplate(t *testing.T, body string) *cfn.Template {
	t.Helper()

	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	return tmpl
}

func planRegistry() cfn.Registry {
	return cfn.Registry{"Test::Named": namedProv{}, "Test::Plain": plainProv{}}
}

func changeFor(t *testing.T, changes []cfn.ResourceChange, id string) cfn.ResourceChange {
	t.Helper()

	for _, c := range changes {
		if c.LogicalID == id {
			return c
		}
	}

	t.Fatalf("no change for %s in %+v", id, changes)

	return cfn.ResourceChange{}
}

func TestPlanChangesNewStackAddsEveryResource(t *testing.T) {
	t.Parallel()

	changes := cfn.PlanChanges(&cfn.ChangePlanInput{
		New: mustTemplate(t, `{"Resources":{
			"B":{"Type":"Test::Named","Properties":{"Name":"b"}},
			"A":{"Type":"Test::Plain"}
		}}`),
		Registry: planRegistry(),
	})

	require.Len(t, changes, 2)
	assert.Equal(t, "A", changes[0].LogicalID)
	assert.Equal(t, cfn.ChangeActionAdd, changes[0].Action)
	assert.Equal(t, "Test::Plain", changes[0].ResourceType)
	assert.Empty(t, changes[0].Replacement)
	assert.Empty(t, changes[0].Details)
	assert.Empty(t, changes[0].PhysicalID)
	assert.Equal(t, "B", changes[1].LogicalID)
}

const planOld = `{
	"Parameters":{"Size":{"Type":"String"}},
	"Resources":{
		"Src":{"Type":"Test::Named","Properties":{"Name":"src","Size":"1"}},
		"ByRef":{"Type":"Test::Named","Properties":{"Name":{"Ref":"Src"}}},
		"ByAtt":{"Type":"Test::Named","Properties":{"Label":{"Fn::GetAtt":["Src","Arn"]}}},
		"ByParam":{"Type":"Test::Named","Properties":{"Size":{"Ref":"Size"}}},
		"Gone":{"Type":"Test::Plain"}
	}
}`

func planLive() map[string]cfn.LiveResource {
	return map[string]cfn.LiveResource{
		"Src":     {Type: "Test::Named", PhysicalID: "src", Props: map[string]any{"Name": "src", "Size": "1"}},
		"ByRef":   {Type: "Test::Named", PhysicalID: "byref", Props: map[string]any{"Name": "src"}},
		"ByAtt":   {Type: "Test::Named", PhysicalID: "byatt", Props: map[string]any{"Label": "arn:src"}},
		"ByParam": {Type: "Test::Named", PhysicalID: "byparam", Props: map[string]any{"Size": "5"}},
		"Gone":    {Type: "Test::Plain", PhysicalID: "gone"},
	}
}

// A new Name on Src replaces it. The replacement reaches ByRef through Ref, so
// ByRef's Name, which also requires replacement, may be replaced. ByAtt reads
// an attribute of the modified Src. ByParam uses a changed parameter.
func TestPlanChangesUpdateMatrix(t *testing.T) {
	t.Parallel()

	newBody := `{
		"Parameters":{"Size":{"Type":"String"}},
		"Resources":{
			"Src":{"Type":"Test::Named","Properties":{"Name":"src2","Size":"1"}},
			"ByRef":{"Type":"Test::Named","Properties":{"Name":{"Ref":"Src"}}},
			"ByAtt":{"Type":"Test::Named","Properties":{"Label":{"Fn::GetAtt":["Src","Arn"]}}},
			"ByParam":{"Type":"Test::Named","Properties":{"Size":{"Ref":"Size"}}},
			"Fresh":{"Type":"Test::Plain","Properties":{"Label":{"Ref":"Src"},"Size":"3"}}
		}
	}`

	changes := cfn.PlanChanges(&cfn.ChangePlanInput{
		Old:           mustTemplate(t, planOld),
		New:           mustTemplate(t, newBody),
		Live:          planLive(),
		NewProps: map[string]map[string]any{
			"Src": {"Name": "src2", "Size": "1"}, "ByRef": {"Name": "src"}, "Fresh": {"Label": "src", "Size": "3"},
		},
		ChangedParams: map[string]bool{"Size": true},
		Registry:      planRegistry(),
	})

	require.Len(t, changes, 6)

	src := changeFor(t, changes, "Src")
	assert.Equal(t, cfn.ChangeActionModify, src.Action)
	assert.Equal(t, "src", src.PhysicalID)
	assert.Equal(t, cfn.ReplacementTrue, src.Replacement)
	assert.Equal(t, cfn.PolicyReplaceAndDelete, src.PolicyAction)
	assert.Equal(t, []string{cfn.AttributeProperties}, src.Scope)
	require.Len(t, src.Details, 1)
	assert.Equal(t, cfn.ChangeDetail{
		Target: cfn.ChangeTarget{
			Attribute: cfn.AttributeProperties, Name: "Name", RequiresRecreation: cfn.RecreationAlways,
			BeforeValue: "src", AfterValue: "src2", AttributeChangeType: cfn.ChangeActionModify,
		},
		Evaluation: cfn.EvaluationStatic, ChangeSource: cfn.SourceDirectModification,
	}, src.Details[0])

	byRef := changeFor(t, changes, "ByRef")
	assert.Equal(t, cfn.ReplacementConditional, byRef.Replacement)
	assert.Empty(t, byRef.PolicyAction)
	require.Len(t, byRef.Details, 1)
	assert.Equal(t, cfn.EvaluationDynamic, byRef.Details[0].Evaluation)
	assert.Equal(t, cfn.SourceResourceReference, byRef.Details[0].ChangeSource)
	assert.Equal(t, "Src", byRef.Details[0].CausingEntity)
	assert.Equal(t, cfn.RecreationAlways, byRef.Details[0].Target.RequiresRecreation)

	byAtt := changeFor(t, changes, "ByAtt")
	assert.Equal(t, cfn.ReplacementFalse, byAtt.Replacement)
	require.Len(t, byAtt.Details, 1)
	assert.Equal(t, cfn.SourceResourceAttribute, byAtt.Details[0].ChangeSource)
	assert.Equal(t, "Src.Arn", byAtt.Details[0].CausingEntity)
	assert.Equal(t, cfn.RecreationNever, byAtt.Details[0].Target.RequiresRecreation)

	byParam := changeFor(t, changes, "ByParam")
	assert.Equal(t, cfn.ReplacementFalse, byParam.Replacement)
	require.Len(t, byParam.Details, 1)
	assert.Equal(t, cfn.EvaluationStatic, byParam.Details[0].Evaluation)
	assert.Equal(t, cfn.SourceParameterReference, byParam.Details[0].ChangeSource)
	assert.Equal(t, "Size", byParam.Details[0].CausingEntity)

	gone := changeFor(t, changes, "Gone")
	assert.Equal(t, cfn.ChangeActionRemove, gone.Action)
	assert.Equal(t, "gone", gone.PhysicalID)
	assert.Equal(t, cfn.PolicyDelete, gone.PolicyAction)
	assert.Empty(t, gone.Replacement)

	fresh := changeFor(t, changes, "Fresh")
	assert.Equal(t, cfn.ChangeActionAdd, fresh.Action)

	// A value only known once Src is replaced shows as the placeholder in
	// the after context, never as the stale resolved value.
	assert.JSONEq(t, `{"Properties":{"Name":"{{changeSet:KNOWN_AFTER_APPLY}}"}}`, byRef.AfterContext)
	assert.JSONEq(t, `{"Properties":{"Label":"{{changeSet:KNOWN_AFTER_APPLY}}","Size":"3"}}`, fresh.AfterContext)
}

// An unchanged resource is not listed, and a resource whose referenced
// resource is only modified in place keeps its Ref value, so it is not listed
// either.
func TestPlanChangesInPlaceDoesNotPropagateRef(t *testing.T) {
	t.Parallel()

	oldBody := `{"Resources":{
		"Src":{"Type":"Test::Named","Properties":{"Name":"src","Size":"1"}},
		"ByRef":{"Type":"Test::Named","Properties":{"Label":{"Ref":"Src"}}},
		"Same":{"Type":"Test::Plain","Properties":{"X":"1"}}
	}}`
	newBody := `{"Resources":{
		"Src":{"Type":"Test::Named","Properties":{"Name":"src","Size":"2"}},
		"ByRef":{"Type":"Test::Named","Properties":{"Label":{"Ref":"Src"}}},
		"Same":{"Type":"Test::Plain","Properties":{"X":"1"}}
	}}`

	changes := cfn.PlanChanges(&cfn.ChangePlanInput{
		Old: mustTemplate(t, oldBody),
		New: mustTemplate(t, newBody),
		Live: map[string]cfn.LiveResource{
			"Src":   {Type: "Test::Named", PhysicalID: "src", Props: map[string]any{"Name": "src", "Size": "1"}},
			"ByRef": {Type: "Test::Named", PhysicalID: "r", Props: map[string]any{"Label": "src"}},
			"Same":  {Type: "Test::Plain", PhysicalID: "s", Props: map[string]any{"X": "1"}},
		},
		Registry: planRegistry(),
	})

	require.Len(t, changes, 1)
	assert.Equal(t, "Src", changes[0].LogicalID)
	assert.Equal(t, cfn.ReplacementFalse, changes[0].Replacement)
	assert.Equal(t, cfn.RecreationNever, changes[0].Details[0].Target.RequiresRecreation)
}

// A Tags change is reported under the Tags attribute with no property name.
// A type with no ReplacementSchema replaces on every change.
func TestPlanChangesTagsAndSchemaless(t *testing.T) {
	t.Parallel()

	oldBody := `{"Resources":{
		"Tagged":{"Type":"Test::Named","Properties":{"Tags":[{"Key":"a","Value":"1"}]}},
		"Plain":{"Type":"Test::Plain","Properties":{"X":"1"}}
	}}`
	newBody := `{"Resources":{
		"Tagged":{"Type":"Test::Named","Properties":{"Tags":[{"Key":"a","Value":"2"}]}},
		"Plain":{"Type":"Test::Plain","Properties":{"X":"2"}}
	}}`

	changes := cfn.PlanChanges(&cfn.ChangePlanInput{
		Old: mustTemplate(t, oldBody),
		New: mustTemplate(t, newBody),
		Live: map[string]cfn.LiveResource{
			"Tagged": {Type: "Test::Named", PhysicalID: "t", Props: map[string]any{"Tags": []any{map[string]any{"Key": "a", "Value": "1"}}}},
			"Plain":  {Type: "Test::Plain", PhysicalID: "p"},
		},
		NewProps: map[string]map[string]any{"Tagged": {"Tags": []any{map[string]any{"Key": "a", "Value": "2"}}}},
		Registry: planRegistry(),
	})

	require.Len(t, changes, 2)

	plain := changeFor(t, changes, "Plain")
	assert.Equal(t, cfn.ReplacementTrue, plain.Replacement)
	assert.Equal(t, cfn.RecreationAlways, plain.Details[0].Target.RequiresRecreation)

	tagged := changeFor(t, changes, "Tagged")
	assert.Equal(t, []string{cfn.AttributeTags}, tagged.Scope)
	assert.Equal(t, cfn.ReplacementFalse, tagged.Replacement)
	assert.Equal(t, cfn.ChangeTarget{
		Attribute: cfn.AttributeTags, RequiresRecreation: cfn.RecreationNever,
		BeforeValue: `[{"Key":"a","Value":"1"}]`, AfterValue: `[{"Key":"a","Value":"2"}]`,
		AttributeChangeType: cfn.ChangeActionModify,
	}, tagged.Details[0].Target)
}

// A resolved value that changed with no template edit, such as an SSM
// parameter that now resolves differently, is still reported.
func TestPlanChangesResolvedOnlyChange(t *testing.T) {
	t.Parallel()

	body := `{"Resources":{"Q":{"Type":"Test::Named","Properties":{"Size":{"Fn::Sub":"${AWS::NotificationARNs}"}}}}}`

	changes := cfn.PlanChanges(&cfn.ChangePlanInput{
		Old:      mustTemplate(t, body),
		New:      mustTemplate(t, body),
		Live:     map[string]cfn.LiveResource{"Q": {Type: "Test::Named", PhysicalID: "q", Props: map[string]any{"Size": "a"}}},
		NewProps: map[string]map[string]any{"Q": {"Size": "b"}},
		Registry: planRegistry(),
	})

	require.Len(t, changes, 1)
	assert.Equal(t, cfn.SourceDirectModification, changes[0].Details[0].ChangeSource)
	assert.Equal(t, cfn.ReplacementFalse, changes[0].Replacement)
	assert.JSONEq(t, `{"Properties":{"Size":"a"}}`, changes[0].BeforeContext)
	assert.JSONEq(t, `{"Properties":{"Size":"b"}}`, changes[0].AfterContext)
}
