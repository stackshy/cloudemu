package cloudformation_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// propYAML wraps a property value in a one-resource YAML template.
func propYAML(value string) string {
	return "Resources:\n  R:\n    Type: AWS::S3::Bucket\n    Properties:\n      P: " + value + "\n"
}

func propJSON(value string) string {
	return `{"Resources":{"R":{"Type":"AWS::S3::Bucket","Properties":{"P":` + value + `}}}}`
}

func parsedProp(t *testing.T, body string) any {
	t.Helper()

	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	return tmpl.Resources["R"].Properties["P"]
}

// TestYAMLShortForms checks every short-form tag against its YAML long form
// and its JSON form. All three must give the same tree.
func TestYAMLShortForms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		short string
		long  string
		json  string
	}{
		{"Ref", `!Ref Env`, `{Ref: Env}`, `{"Ref":"Env"}`},
		{"Ref pseudo", `!Ref "AWS::Region"`, `{Ref: "AWS::Region"}`, `{"Ref":"AWS::Region"}`},
		{"Condition", `!Condition IsProd`, `{Condition: IsProd}`, `{"Condition":"IsProd"}`},
		{"GetAtt dotted", `!GetAtt Bucket.Arn`, `{"Fn::GetAtt": Bucket.Arn}`, `{"Fn::GetAtt":"Bucket.Arn"}`},
		{"GetAtt list", `!GetAtt [Bucket, Arn]`, `{"Fn::GetAtt": [Bucket, Arn]}`, `{"Fn::GetAtt":["Bucket","Arn"]}`},
		{"Sub string", `!Sub "${AWS::StackName}-x"`, `{"Fn::Sub": "${AWS::StackName}-x"}`, `{"Fn::Sub":"${AWS::StackName}-x"}`},
		{
			"Sub with vars", `!Sub ["${A}-y", {A: !Ref B}]`, `{"Fn::Sub": ["${A}-y", {A: {Ref: B}}]}`,
			`{"Fn::Sub":["${A}-y",{"A":{"Ref":"B"}}]}`,
		},
		{
			"Join", `!Join ["-", [a, !Ref B]]`, `{"Fn::Join": ["-", [a, {Ref: B}]]}`,
			`{"Fn::Join":["-",["a",{"Ref":"B"}]]}`,
		},
		{
			"If", `!If [IsProd, !Ref A, !Ref "AWS::NoValue"]`,
			`{"Fn::If": [IsProd, {Ref: A}, {Ref: "AWS::NoValue"}]}`,
			`{"Fn::If":["IsProd",{"Ref":"A"},{"Ref":"AWS::NoValue"}]}`,
		},
		{
			"Select GetAZs", `!Select [0, !GetAZs ""]`, `{"Fn::Select": [0, {"Fn::GetAZs": ""}]}`,
			`{"Fn::Select":[0,{"Fn::GetAZs":""}]}`,
		},
		{"Split", `!Split [",", "a,b"]`, `{"Fn::Split": [",", "a,b"]}`, `{"Fn::Split":[",","a,b"]}`},
		{
			"FindInMap", `!FindInMap [Map, !Ref "AWS::Region", Ami]`,
			`{"Fn::FindInMap": [Map, {Ref: "AWS::Region"}, Ami]}`,
			`{"Fn::FindInMap":["Map",{"Ref":"AWS::Region"},"Ami"]}`,
		},
		{"ImportValue", `!ImportValue shared-vpc`, `{"Fn::ImportValue": shared-vpc}`, `{"Fn::ImportValue":"shared-vpc"}`},
		{
			"ImportValue with long Sub", `!ImportValue {"Fn::Sub": "${Net}-id"}`,
			`{"Fn::ImportValue": {"Fn::Sub": "${Net}-id"}}`, `{"Fn::ImportValue":{"Fn::Sub":"${Net}-id"}}`,
		},
		{"Base64", `!Base64 hello`, `{"Fn::Base64": hello}`, `{"Fn::Base64":"hello"}`},
		{
			"Base64 of Sub", `!Base64 {"Fn::Sub": "${A}"}`, `{"Fn::Base64": {"Fn::Sub": "${A}"}}`,
			`{"Fn::Base64":{"Fn::Sub":"${A}"}}`,
		},
		{
			"Cidr", `!Cidr [!GetAtt Vpc.CidrBlock, 6, 5]`, `{"Fn::Cidr": [{"Fn::GetAtt": Vpc.CidrBlock}, 6, 5]}`,
			`{"Fn::Cidr":[{"Fn::GetAtt":"Vpc.CidrBlock"},6,5]}`,
		},
		{"GetAZs region", `!GetAZs us-east-1`, `{"Fn::GetAZs": us-east-1}`, `{"Fn::GetAZs":"us-east-1"}`},
		{"Equals", `!Equals [!Ref Env, prod]`, `{"Fn::Equals": [{Ref: Env}, prod]}`, `{"Fn::Equals":[{"Ref":"Env"},"prod"]}`},
		{
			"And Not Or", `!And [!Not [!Condition A], !Or [!Condition B, !Equals [x, y]]]`,
			`{"Fn::And": [{"Fn::Not": [{Condition: A}]}, {"Fn::Or": [{Condition: B}, {"Fn::Equals": [x, y]}]}]}`,
			`{"Fn::And":[{"Fn::Not":[{"Condition":"A"}]},{"Fn::Or":[{"Condition":"B"},{"Fn::Equals":["x","y"]}]}]}`,
		},
		{
			"Transform", `!Transform {Name: "AWS::Include", Parameters: {Location: s3://b/k}}`,
			`{"Fn::Transform": {Name: "AWS::Include", Parameters: {Location: s3://b/k}}}`,
			`{"Fn::Transform":{"Name":"AWS::Include","Parameters":{"Location":"s3://b/k"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want := parsedProp(t, propJSON(tc.json))
			assert.Equal(t, want, parsedProp(t, propYAML(tc.short)), "short form")
			assert.Equal(t, want, parsedProp(t, propYAML(tc.long)), "long form")
		})
	}
}

// TestYAMLBlockShortForms covers short forms written as block sequences, the
// usual style in real templates.
func TestYAMLBlockShortForms(t *testing.T) {
	t.Parallel()

	body := `
Resources:
  R:
    Type: AWS::S3::Bucket
    Properties:
      P: !Join
        - ""
        - - !Ref Prefix
          - !GetAtt Other.Arn
          - !Sub
            - "${X}"
            - X: !Select
                - 1
                - !Split [",", !ImportValue list]
`
	want := parsedProp(t, propJSON(`{"Fn::Join":["",[{"Ref":"Prefix"},{"Fn::GetAtt":"Other.Arn"},
		{"Fn::Sub":["${X}",{"X":{"Fn::Select":[1,{"Fn::Split":[",",{"Fn::ImportValue":"list"}]}]}}]}]]}`))
	assert.Equal(t, want, parsedProp(t, body))
}

const jsonTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "demo",
  "Parameters": {"Env": {"Type": "String", "Default": "dev"}, "Size": {"Type": "Number", "Default": 3}},
  "Resources": {
    "Q": {"Type": "AWS::SQS::Queue", "Properties": {"VisibilityTimeout": 60, "Delay": 1.5}},
    "B": {"Type": "AWS::S3::Bucket", "DependsOn": "Q", "Properties": {"BucketName": {"Fn::Sub": "${Env}-b"}}}
  },
  "Outputs": {"Arn": {"Value": {"Fn::GetAtt": ["Q", "Arn"]}, "Export": {"Name": "q-arn"}}}
}`

const yamlTemplate = `AWSTemplateFormatVersion: 2010-09-09
Description: demo
Parameters:
  Env: {Type: String, Default: dev}
  Size:
    Type: Number
    Default: 3
Resources:
  Q:
    Type: AWS::SQS::Queue
    Properties:
      VisibilityTimeout: 60
      Delay: 1.5
  B:
    Type: AWS::S3::Bucket
    DependsOn: Q
    Properties:
      BucketName: !Sub "${Env}-b"
Outputs:
  Arn:
    Value: !GetAtt [Q, Arn]
    Export:
      Name: q-arn
`

func TestJSONAndYAMLTemplatesEqual(t *testing.T) {
	t.Parallel()

	fromJSON, err := cfn.ParseTemplate(jsonTemplate)
	require.NoError(t, err)

	fromYAML, err := cfn.ParseTemplate(yamlTemplate)
	require.NoError(t, err)

	assert.Equal(t, fromJSON, fromYAML)
	assert.Equal(t, "2010-09-09", fromYAML.FormatVersion)
	assert.Equal(t, json.Number("60"), fromJSON.Resources["Q"].Properties["VisibilityTimeout"],
		"JSON numbers decode as json.Number")
	assert.Equal(t, "3", cfn.Stringify(fromYAML.Parameters["Size"].Default))
}

// TestYAMLTemplateResolves runs short forms through the resolver.
func TestYAMLTemplateResolves(t *testing.T) {
	t.Parallel()

	body := `Resources:
  R:
    Type: AWS::S3::Bucket
    Properties:
      Name: !Sub "${Env}-${AWS::Region}"
      Arn: !GetAtt Bucket.Arn
      Deep: !GetAtt Bucket.Nested.Attr
      Joined: !Join [":", [!Ref Bucket, !Ref Env]]
      Count: 5
`
	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	r := newResolver()
	r.Resources["Bucket"].Attributes["Nested.Attr"] = "deep-value"

	got, err := r.Resolve(tmpl.Resources["R"].Properties)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"Name": "prod-us-east-1", "Arn": "arn:aws:s3:::my-bucket", "Deep": "deep-value",
		"Joined": "my-bucket:prod", "Count": json.Number("5"),
	}, got)
}

func TestParseTemplateFormatErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		msg  string
	}{
		{"empty", "  \n", "Either Template URL or Template Body must be specified."},
		{"bad JSON", "{\n  \"Resources\": {,\n}", "Template format error: JSON not well-formed. (line 2, column 17)"},
		{"trailing JSON", `{"Resources":{"B":{"Type":"X"}}} {}`, "Template format error: JSON not well-formed. (line 1, column 34)"},
		{
			"bad YAML indentation", "Resources:\n  B:\n    Type: X\n   Properties: {}\n",
			"Template format error: YAML not well-formed. (line 4)",
		},
		{
			"unknown short form", "Resources:\n  B:\n    Type: X\n    Properties:\n      Arn: !GetAttr B.Arn\n",
			"Template format error: YAML not well-formed. (line 5, column 12)",
		},
		{
			"two tags on one node", "Resources:\n  B:\n    Type: X\n    Properties:\n      V: !ImportValue !Sub x\n",
			"Template format error: YAML not well-formed. (line 5)",
		},
		{
			"alias", "Resources:\n  A: &a {Type: X}\n  B: *a\n",
			"Template error: YAML aliases are not allowed in CloudFormation templates",
		},
		{
			"duplicate key", "Resources:\n  A: {Type: X}\n  A: {Type: Y}\n",
			"Template format error: YAML not well-formed. (line 3, column 3)",
		},
		{"not an object", "- a\n- b\n", "Template format error: template must be an object"},
		{"no resources", `{"Resources":{}}`, "Template format error: At least one Resources member must be defined."},
		{"no resources YAML", "Description: x\n", "Template format error: At least one Resources member must be defined."},
		{
			"missing type", "Resources:\n  B:\n    Properties: {}\n",
			"Template format error: [/Resources/B] Every Resources object must contain a Type member.",
		},
		{
			"unknown section", "Resources:\n  B: {Type: X}\nBogus: 1\nOther: 2\n",
			"Template format error: Invalid template property or properties [Bogus, Other]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := cfn.ParseTemplate(tc.body)
			require.Error(t, err)
			assert.True(t, cerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
			assert.Equal(t, tc.msg, cerrors.Message(err))
		})
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		body       string
		caps       []string
		reason     string
		transforms []string
	}{
		{"no IAM", "Resources:\n  B: {Type: AWS::S3::Bucket}\n", nil, "", nil},
		{
			"IAM role", "Resources:\n  R: {Type: AWS::IAM::Role}\n  P: {Type: AWS::IAM::Policy}\n",
			[]string{cfn.CapabilityIAM},
			"The following resource(s) require capabilities: [AWS::IAM::Policy, AWS::IAM::Role]", nil,
		},
		{
			"named IAM role", "Resources:\n  R:\n    Type: AWS::IAM::Role\n    Properties: {RoleName: fixed}\n",
			[]string{cfn.CapabilityNamedIAM}, "The following resource(s) require capabilities: [AWS::IAM::Role]", nil,
		},
		{
			"transform list", "Transform: [AWS::Serverless-2016-10-31, MyMacro]\nResources:\n  B: {Type: AWS::S3::Bucket}\n",
			nil, "", []string{"AWS::Serverless-2016-10-31", "MyMacro"},
		},
		{
			"transform string", "Transform: AWS::Serverless-2016-10-31\nResources:\n  B: {Type: AWS::S3::Bucket}\n",
			nil, "", []string{"AWS::Serverless-2016-10-31"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := cfn.ParseTemplate(tc.body)
			require.NoError(t, err)

			sum := cfn.Summarize(tmpl)
			assert.Equal(t, tc.caps, sum.Capabilities)
			assert.Equal(t, tc.reason, sum.CapabilitiesReason)
			assert.Equal(t, tc.transforms, sum.DeclaredTransforms)
		})
	}
}

func TestSummarizeParameters(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"Description: my stack",
		"Parameters:",
		"  Size: {Type: Number, Default: 3, Description: how many}",
		"  Secret: {Type: String, NoEcho: true}",
		"Resources:",
		"  B: {Type: AWS::S3::Bucket}",
	}, "\n")

	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	sum := cfn.Summarize(tmpl)
	assert.Equal(t, "my stack", sum.Description)
	assert.Equal(t, []cfn.TemplateParameter{
		{Key: "Secret", NoEcho: true},
		{Key: "Size", DefaultValue: "3", HasDefault: true, Description: "how many"},
	}, sum.Parameters)
}
