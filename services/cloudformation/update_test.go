package cloudformation_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

type plainProv struct{}

func (plainProv) Create(context.Context, cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	return &cfn.ProvisionedResource{}, nil
}

func (plainProv) Delete(context.Context, string, map[string]any) error { return nil }

type namedProv struct{ plainProv }

// schemaProv knows its replacement properties but has no Updater.
type schemaProv struct{ plainProv }

func (schemaProv) RequiresReplacement(property string) bool { return property == "Name" }

func (namedProv) RequiresReplacement(property string) bool { return property == "Name" }

func (namedProv) Update(context.Context, string, map[string]any, cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	return &cfn.ProvisionedResource{}, nil
}

func TestPlanResourceUpdate(t *testing.T) {
	t.Parallel()

	old := map[string]any{"Name": "a", "Size": json.Number("5"), "Tags": []any{"x"}}

	cases := []struct {
		name string
		prov cfn.Provisioner
		next map[string]any
		want cfn.UpdateAction
	}{
		{"same after a snapshot round trip", namedProv{}, map[string]any{"Name": "a", "Size": 5.0, "Tags": []any{"x"}}, cfn.UpdateNone},
		{"in place", namedProv{}, map[string]any{"Name": "a", "Size": json.Number("6"), "Tags": []any{"x"}}, cfn.UpdateInPlace},
		{"dropped property in place", namedProv{}, map[string]any{"Name": "a", "Size": json.Number("5")}, cfn.UpdateInPlace},
		{"replacement property", namedProv{}, map[string]any{"Name": "b", "Size": json.Number("5"), "Tags": []any{"x"}}, cfn.UpdateReplace},
		{"no updater replaces", plainProv{}, map[string]any{"Name": "a", "Size": json.Number("6"), "Tags": []any{"x"}}, cfn.UpdateReplace},
		{"no updater no change", plainProv{}, old, cfn.UpdateNone},
		{"schema only in place", schemaProv{}, map[string]any{"Name": "a", "Size": json.Number("6"), "Tags": []any{"x"}}, cfn.UpdateInPlace},
		{"schema only replacement", schemaProv{}, map[string]any{"Name": "b", "Size": json.Number("5"), "Tags": []any{"x"}}, cfn.UpdateReplace},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, cfn.PlanResourceUpdate(c.prov, old, c.next))
		})
	}

	assert.Equal(t, []string{"Name", "Tags"}, cfn.ChangedProperties(old, map[string]any{"Name": "b", "Size": json.Number("5")}))
}

func TestCheckCapabilities(t *testing.T) {
	t.Parallel()

	parse := func(body string) *cfn.Template {
		tpl, err := cfn.ParseTemplate(body)
		require.NoError(t, err)

		return tpl
	}

	unnamed := parse(`{"Resources":{"R":{"Type":"AWS::IAM::Role","Properties":{}}}}`)
	named := parse(`{"Resources":{"R":{"Type":"AWS::IAM::Role","Properties":{"RoleName":"r"}}}}`)
	macro := parse(`{"Transform":"AWS::Serverless-2016-10-31","Resources":{"Q":{"Type":"AWS::SQS::Queue"}}}`)
	plain := parse(`{"Resources":{"Q":{"Type":"AWS::SQS::Queue"}}}`)

	cases := []struct {
		name string
		t    *cfn.Template
		caps []string
		want string
	}{
		{"none needed", plain, nil, ""},
		{"iam missing", unnamed, nil, "Requires capabilities : [CAPABILITY_IAM]"},
		{"named covers iam", unnamed, []string{cfn.CapabilityNamedIAM}, ""},
		{"named missing", named, []string{cfn.CapabilityIAM}, "Requires capabilities : [CAPABILITY_NAMED_IAM]"},
		{"auto expand missing", macro, nil, "Requires capabilities : [CAPABILITY_AUTO_EXPAND]"},
		{"auto expand given", macro, []string{cfn.CapabilityAutoExpand}, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := cfn.CheckCapabilities(c.t, c.caps)
			if c.want == "" {
				require.NoError(t, err)
				return
			}

			var ex *cfn.ExceptionError
			require.True(t, errors.As(err, &ex))
			assert.Equal(t, cfn.ExceptionInsufficientCapabilities, ex.Exception())
			assert.Equal(t, c.want, cerrors.Message(err))
			assert.True(t, cerrors.IsInvalidArgument(err))
		})
	}
}
