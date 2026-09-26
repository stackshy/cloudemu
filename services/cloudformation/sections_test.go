package cloudformation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// TestScalarFieldsCoerce checks the lenient scalar reads CloudFormation does,
// in both formats.
func TestScalarFieldsCoerce(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"JSON": `{"AWSTemplateFormatVersion": 2010, "Description": 42,
			"Parameters": {"Secret": {"Type": "String", "NoEcho": "true", "Description": 7},
				"Plain": {"Type": "String", "NoEcho": "False"},
				"Account": {"Type": "String", "Default": "012345678901"}},
			"Resources": {"B": {"Type": "AWS::S3::Bucket"}},
			"Outputs": {"O": {"Value": "v", "Description": true}}}`,
		"YAML": `AWSTemplateFormatVersion: 2010
Description: 42
Parameters:
  Secret: {Type: String, NoEcho: "true", Description: 7}
  Plain: {Type: String, NoEcho: "False"}
  Account: {Type: String, Default: 012345678901}
Resources:
  B: {Type: AWS::S3::Bucket}
Outputs:
  O: {Value: v, Description: true}
`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := cfn.ParseTemplate(body)
			require.NoError(t, err)
			assert.Equal(t, "2010", tmpl.FormatVersion)
			assert.Equal(t, "42", tmpl.Description)
			assert.True(t, tmpl.Parameters["Secret"].NoEcho)
			assert.Equal(t, "7", tmpl.Parameters["Secret"].Description)
			assert.False(t, tmpl.Parameters["Plain"].NoEcho)
			assert.Equal(t, "012345678901", cfn.Stringify(tmpl.Parameters["Account"].Default))
			assert.Equal(t, "true", tmpl.Outputs["O"].Description)
		})
	}
}

func TestSectionShapeErrors(t *testing.T) {
	t.Parallel()

	const res = `"Resources": {"B": {"Type": "AWS::S3::Bucket"}}`

	cases := []struct {
		name, body, msg string
	}{
		{"NoEcho word", `{"Parameters": {"P": {"Type": "String", "NoEcho": "maybe"}}, ` + res + `}`,
			"Template format error: [/Parameters/P/NoEcho] must be a boolean"},
		{"NoEcho YAML list", "Parameters:\n  P: {Type: String, NoEcho: [true]}\nResources:\n  B: {Type: X}\n",
			"Template format error: [/Parameters/P/NoEcho] must be a boolean"},
		{"parameter without Type", `{"Parameters": {"P": {"Default": "x"}}, ` + res + `}`,
			"Template format error: [/Parameters/P] Every Parameters object must contain a Type member."},
		{"Description object", `{"Description": {"a": 1}, ` + res + `}`,
			"Template format error: [/Description] must be a string"},
		{"version list", "AWSTemplateFormatVersion: [2010]\nResources:\n  B: {Type: X}\n",
			"Template format error: [/AWSTemplateFormatVersion] must be a string"},
		{"Parameters not object", `{"Parameters": ["P"], ` + res + `}`,
			"Template format error: [/Parameters] must be an object"},
		{"resource not object", `{"Resources": {"B": "AWS::S3::Bucket"}}`,
			"Template format error: [/Resources/B] Every Resources member must be an object."},
		{"resource Type list", "Resources:\n  B: {Type: [a]}\n",
			"Template format error: [/Resources/B/Type] must be a string"},
		{"Properties list", "Resources:\n  B:\n    Type: X\n    Properties: [a]\n",
			"Template format error: [/Resources/B/Properties] must be an object"},
		{"AllowedValues scalar", `{"Parameters": {"P": {"Type": "String", "AllowedValues": "a"}}, ` + res + `}`,
			"Template format error: [/Parameters/P/AllowedValues] must be a list"},
		{"output without Value", `{"Outputs": {"O": {"Description": "d"}}, ` + res + `}`,
			"Template format error: [/Outputs/O] Every Outputs member must contain a Value object"},
		{"Export scalar", `{"Outputs": {"O": {"Value": "v", "Export": "n"}}, ` + res + `}`,
			"Template format error: [/Outputs/O/Export] must be an object"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := cfn.ParseTemplate(tc.body)
			require.Error(t, err)
			assert.True(t, cerrors.IsInvalidArgument(err))
			assert.Equal(t, tc.msg, cerrors.Message(err))
			assert.NotContains(t, cerrors.Message(err), "json:", "no Go decoder text")
		})
	}
}
