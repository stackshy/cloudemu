package cloudformation_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// TestResourcePolicies checks how DeletionPolicy and UpdateReplacePolicy are
// read, and which values they take.
func TestResourcePolicies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, attrs, wantErr         string
		deletion, replace            string
		effectiveDelete, effectiveRe string
	}{
		{name: "defaults", effectiveDelete: "Delete", effectiveRe: "Delete"},
		{
			name: "retain", attrs: `"DeletionPolicy":"Retain","UpdateReplacePolicy":"Retain",`,
			deletion: "Retain", replace: "Retain", effectiveDelete: "Retain", effectiveRe: "Retain",
		},
		{
			name: "snapshot without snapshots", attrs: `"DeletionPolicy":"Snapshot","UpdateReplacePolicy":"Snapshot",`,
			deletion: "Snapshot", replace: "Snapshot", effectiveDelete: "Delete", effectiveRe: "Delete",
		},
		{
			name: "retain except on create", attrs: `"DeletionPolicy":"RetainExceptOnCreate",`,
			deletion: "RetainExceptOnCreate", effectiveDelete: "RetainExceptOnCreate", effectiveRe: "Delete",
		},
		{
			name: "unknown deletion policy", attrs: `"DeletionPolicy":"Keep",`,
			wantErr: "Template format error: Unrecognized DeletionPolicy Keep for resource B",
		},
		{
			name: "RetainExceptOnCreate is not a replace policy", attrs: `"UpdateReplacePolicy":"RetainExceptOnCreate",`,
			wantErr: "Unrecognized UpdateReplacePolicy RetainExceptOnCreate for resource B",
		},
		{
			name: "intrinsic deletion policy", attrs: `"DeletionPolicy":{"Ref":"P"},`,
			wantErr: "Template format error: Every DeletionPolicy member must be a string.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := cfn.ParseTemplate(`{"Resources":{"B":{` + tc.attrs + `"Type":"AWS::S3::Bucket"}}}`)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)

			b := tmpl.Resources["B"]
			assert.Equal(t, tc.deletion, b.DeletionPolicy)
			assert.Equal(t, tc.replace, b.UpdateReplacePolicy)
			assert.Equal(t, tc.effectiveDelete, b.EffectiveDeletionPolicy())
			assert.Equal(t, tc.effectiveRe, b.EffectiveReplacePolicy())
		})
	}
}

// TestSnapshotPolicyOnASnapshotType keeps Snapshot for a type that has
// snapshots.
func TestSnapshotPolicyOnASnapshotType(t *testing.T) {
	t.Parallel()

	r := cfn.ResourceDef{Type: "AWS::RDS::DBInstance", DeletionPolicy: "Snapshot"}
	assert.Equal(t, "Snapshot", r.EffectiveDeletionPolicy())
}

// TestKeepsOnDelete is the DeletionPolicy decision matrix.
func TestKeepsOnDelete(t *testing.T) {
	t.Parallel()

	cases := []struct {
		policy                 string
		rollbackOfCreate, flag bool
		keep                   bool
	}{
		{policy: "Delete"},
		{policy: "Retain", keep: true},
		{policy: "Retain", rollbackOfCreate: true, keep: true},
		{policy: "Retain", rollbackOfCreate: true, flag: true},
		{policy: "Retain", flag: true, keep: true},
		{policy: "RetainExceptOnCreate", keep: true},
		{policy: "RetainExceptOnCreate", rollbackOfCreate: true},
	}

	for _, tc := range cases {
		got := cfn.KeepsOnDelete(tc.policy, tc.rollbackOfCreate, tc.flag)
		assert.Equal(t, tc.keep, got, "%+v", tc)
	}
}

// TestOutputsLimit rejects a template with more than 200 outputs.
func TestOutputsLimit(t *testing.T) {
	t.Parallel()

	outputs := make([]string, 0, cfn.MaxOutputs+1)
	for i := range cfn.MaxOutputs + 1 {
		outputs = append(outputs, fmt.Sprintf(`"O%d":{"Value":"v"}`, i))
	}

	body := `{"Resources":{"B":{"Type":"AWS::S3::Bucket"}},"Outputs":{%s}}`

	_, err := cfn.ParseTemplate(fmt.Sprintf(body, strings.Join(outputs, ",")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Template format error: Outputs count 201 is greater than max allowed 200")

	_, err = cfn.ParseTemplate(fmt.Sprintf(body, strings.Join(outputs[:cfn.MaxOutputs], ",")))
	require.NoError(t, err)
}

// TestImportValue resolves Fn::ImportValue, in long and short form, from the
// exports the resolver sees.
func TestImportValue(t *testing.T) {
	t.Parallel()

	body := `Parameters:
  Env: {Type: String}
Resources:
  B:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !ImportValue {"Fn::Sub": "${Env}-bucket"}
      Other: {"Fn::ImportValue": shared}
`
	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	r := &cfn.Resolver{
		Params:  map[string]string{"Env": "prod"},
		Exports: map[string]string{"prod-bucket": "b-1", "shared": "s-1"},
	}

	prepared, err := r.Prepare(tmpl)
	require.NoError(t, err)

	names, err := r.ImportNames(prepared)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod-bucket", "shared"}, names)

	props, err := r.Resolve(prepared.Resources["B"].Properties)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"BucketName": "b-1", "Other": "s-1"}, props)

	r.Exports = nil
	_, err = r.Resolve(prepared.Resources["B"].Properties)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No export named")
}

// TestImportValueNameRules rejects an import name that depends on a
// resource, an attribute or another import.
func TestImportValueNameRules(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]string{
		"ref":    `{"Ref":"A"}`,
		"getatt": `{"Fn::GetAtt":["A","Arn"]}`,
		"nested": `{"Fn::ImportValue":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := cfn.ParseTemplate(`{"Resources":{"A":{"Type":"AWS::S3::Bucket"},
				"B":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":{"Fn::ImportValue":` + value + `}}}}}`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "must not depend on any resources, imported values, or Fn::GetAZs")
		})
	}
}
