package cloudformation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// bothFormats returns a YAML template and the same template as JSON, so each
// case runs through both decoders. The JSON is the YAML's decoded tree.
func bothFormats(t *testing.T, yamlBody string) map[string]string {
	t.Helper()

	tree, err := decodeTemplate(yamlBody)
	require.NoError(t, err)

	js, err := json.Marshal(tree)
	require.NoError(t, err)

	return map[string]string{"YAML": yamlBody, "JSON": string(js)}
}

// prepareBody parses a template and applies its conditions for the given
// parameter values and region.
func prepareBody(body string, params map[string]string, region string) (*Template, *Resolver, error) {
	tmpl, err := ParseTemplate(body)
	if err != nil {
		return nil, nil, err
	}

	r := &Resolver{
		Params: params, Resources: map[string]ResolvedResource{},
		Region: region, AccountID: "123456789012", StackName: "demo",
		StackID:          "arn:aws:cloudformation:" + region + ":123456789012:stack/demo/id-1",
		NotificationARNs: []string{"arn:aws:sns:us-east-1:123456789012:a", "arn:aws:sns:us-east-1:123456789012:b"},
	}

	out, err := r.Prepare(tmpl)

	return out, r, err
}

func errMessage(err error) string {
	if err == nil {
		return ""
	}

	return cerrors.Message(err)
}

const envConditions = `Parameters:
  Env: {Type: String, AllowedValues: [prod, dev], Default: dev}
  Size: {Type: String, Default: small}
Conditions:
  IsProd: !Equals [!Ref Env, prod]
  IsBig: !Equals [!Ref Size, big]
  ProdAndBig: !And [!Condition IsProd, !Condition IsBig]
  ProdOrBig: !Or [!Condition IsProd, !Condition IsBig]
  NotProd: !Not [!Condition IsProd]
`

func TestConditionsDecideResourcesAndOutputs(t *testing.T) {
	t.Parallel()

	body := envConditions + `Resources:
  Always: {Type: AWS::S3::Bucket}
  Prod: {Type: AWS::S3::Bucket, Condition: IsProd}
  Both: {Type: AWS::S3::Bucket, Condition: ProdAndBig}
  Either: {Type: AWS::S3::Bucket, Condition: ProdOrBig}
  Dev: {Type: AWS::S3::Bucket, Condition: NotProd}
Outputs:
  AlwaysName: {Value: !Ref Always}
  ProdName: {Value: !Ref Prod, Condition: IsProd}
`

	cases := []struct {
		name      string
		params    map[string]string
		resources []string
		outputs   []string
	}{
		{"prod small", map[string]string{"Env": "prod", "Size": "small"}, []string{"Always", "Either", "Prod"}, []string{"AlwaysName", "ProdName"}},
		{"prod big", map[string]string{"Env": "prod", "Size": "big"}, []string{"Always", "Both", "Either", "Prod"}, []string{"AlwaysName", "ProdName"}},
		{"dev big", map[string]string{"Env": "dev", "Size": "big"}, []string{"Always", "Dev", "Either"}, []string{"AlwaysName"}},
		{"dev small", map[string]string{"Env": "dev", "Size": "small"}, []string{"Always", "Dev"}, []string{"AlwaysName"}},
	}

	for format, b := range bothFormats(t, body) {
		for _, tc := range cases {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				out, r, err := prepareBody(b, tc.params, "us-east-1")
				require.NoError(t, err)
				assert.Equal(t, tc.resources, sortedKeys(out.Resources))
				assert.Equal(t, tc.outputs, sortedKeys(out.Outputs))
				assert.Equal(t, tc.params["Env"] == "prod", r.conditions["IsProd"])
			})
		}
	}
}

func TestPrepareErrors(t *testing.T) {
	t.Parallel()

	const res = "Resources:\n  B: {Type: AWS::S3::Bucket}\n"

	cases := []struct {
		name string
		body string
		msg  string
	}{
		{
			"ref to skipped resource",
			envConditions + "Resources:\n  Prod: {Type: AWS::S3::Bucket, Condition: IsProd}\n  Q: {Type: AWS::SQS::Queue, Properties: {Name: !Ref Prod}}\n",
			"Template format error: Unresolved resource dependencies [Prod] in the Resources block of the template",
		},
		{
			"GetAtt of skipped resource",
			envConditions + "Resources:\n  Prod: {Type: AWS::S3::Bucket, Condition: IsProd}\n  Q: {Type: AWS::SQS::Queue, Properties: {Name: !GetAtt Prod.Arn}}\n",
			"Template format error: Unresolved resource dependencies [Prod] in the Resources block of the template",
		},
		{
			"DependsOn skipped resource",
			envConditions + "Resources:\n  Prod: {Type: AWS::S3::Bucket, Condition: IsProd}\n  Q: {Type: AWS::SQS::Queue, DependsOn: Prod}\n",
			"Template format error: Unresolved resource dependencies [Prod] in the Resources block of the template",
		},
		{
			"output of skipped resource",
			envConditions + "Resources:\n  Prod: {Type: AWS::S3::Bucket, Condition: IsProd}\n  B: {Type: AWS::S3::Bucket}\nOutputs:\n  O: {Value: !Ref Prod}\n",
			"Template format error: Unresolved resource dependencies [Prod] in the Outputs block of the template",
		},
		{
			"unknown mapping key",
			"Mappings:\n  M: {us-east-1: {Ami: ami-1}}\nResources:\n  B: {Type: AWS::S3::Bucket, Properties: {A: !FindInMap [M, eu-west-1, Ami]}}\n",
			"Template error: Unable to get mapping for M::eu-west-1::Ami",
		},
		{
			"unknown mapping key in condition",
			"Mappings:\n  M: {us-east-1: {Key: v}}\nConditions:\n  C: !Equals [!FindInMap [M, !Ref 'AWS::Region', Missing], x]\n" + res,
			"Template error: Unable to get mapping for M::us-east-1::Missing",
		},
	}

	for _, tc := range cases {
		for format, b := range bothFormats(t, tc.body) {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				_, _, err := prepareBody(b, map[string]string{"Env": "dev", "Size": "small"}, "us-east-1")
				require.Error(t, err)
				assert.True(t, cerrors.IsInvalidArgument(err))
				assert.Equal(t, tc.msg, errMessage(err))
			})
		}
	}
}

// TestIfUntakenBranchMayReferenceSkippedResource checks the usual pattern: a
// resource that exists only in prod is referenced through Fn::If.
func TestIfUntakenBranchMayReferenceSkippedResource(t *testing.T) {
	t.Parallel()

	body := envConditions + `Resources:
  Prod: {Type: AWS::S3::Bucket, Condition: IsProd}
  Q:
    Type: AWS::SQS::Queue
    Properties:
      Name: !If [IsProd, !Ref Prod, !Ref "AWS::NoValue"]
`
	for format, b := range bothFormats(t, body) {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			out, _, err := prepareBody(b, map[string]string{"Env": "dev", "Size": "small"}, "us-east-1")
			require.NoError(t, err)
			assert.Equal(t, []string{"Q"}, sortedKeys(out.Resources))
			assert.Empty(t, Dependencies(out)["Q"], "the untaken branch adds no dependency")
		})
	}
}

func TestStaticValidationErrors(t *testing.T) {
	t.Parallel()

	const res = "Resources:\n  B: {Type: AWS::S3::Bucket}\n"

	cases := []struct {
		name string
		body string
		msg  string
	}{
		{"unknown Ref", "Resources:\n  B: {Type: AWS::S3::Bucket, Properties: {N: !Ref Nope}}\n",
			"Template format error: Unresolved resource dependencies [Nope] in the Resources block of the template"},
		{"unknown Sub variable", "Resources:\n  B: {Type: AWS::S3::Bucket, Properties: {N: !Sub '${Nope}-x'}}\n",
			"Template format error: Unresolved resource dependencies [Nope] in the Resources block of the template"},
		{"Sub local variable", "Resources:\n  B: {Type: AWS::S3::Bucket, Properties: {N: !Sub ['${L}-x', {L: y}]}}\n", ""},
		{"unknown DependsOn", "Resources:\n  B: {Type: AWS::S3::Bucket, DependsOn: [Nope]}\n",
			"Template format error: Unresolved resource dependencies [Nope] in the Resources block of the template"},
		{"unknown output Ref", res + "Outputs:\n  O: {Value: !GetAtt Nope.Arn}\n",
			"Template format error: Unresolved resource dependencies [Nope] in the Outputs block of the template"},
		{"condition refs resource", "Conditions:\n  C: !Equals [!Ref B, x]\n" + res,
			"Template format error: Unresolved dependencies [B]. Cannot reference resources in the Conditions block of the template"},
		{"unknown resource condition", "Resources:\n  B: {Type: AWS::S3::Bucket, Condition: Nope}\n",
			"Template format error: Unresolved condition dependency Nope in the Resources block of the template"},
		{"unknown output condition", res + "Outputs:\n  O: {Value: x, Condition: Nope}\n",
			"Template format error: Unresolved condition dependency Nope in the Outputs block of the template"},
		{"unknown If condition", "Resources:\n  B: {Type: AWS::S3::Bucket, Properties: {N: !If [Nope, a, b]}}\n",
			"Template error: unresolved condition dependency Nope in Fn::If"},
		{"unknown nested condition", "Conditions:\n  A: !Not [!Condition Nope]\n" + res,
			"Template error: unresolved condition dependency Nope in Condition"},
		{"condition cycle", "Conditions:\n  A: !Not [!Condition B]\n  B: !Not [!Condition A]\n" + res,
			"Template format error: Circular dependency between conditions: [A]"},
		{"unknown mapping", "Resources:\n  B: {Type: AWS::S3::Bucket, Properties: {A: !FindInMap [Nope, a, b]}}\n",
			"Template error: Mapping named 'Nope' is not present in the 'Mappings' section of template."},
		{"bad mapping shape", "Mappings:\n  M: {a: b}\n" + res,
			"Template format error: [/Mappings/M/a] Every Mappings attribute must be a map"},
		{"unknown parameter type", "Parameters:\n  P: {Type: Strin}\n" + res,
			"Template format error: Unrecognized parameter type: Strin"},
		{"AWS-specific parameter type", "Parameters:\n  P: {Type: 'AWS::EC2::KeyPair::KeyName'}\n  L: {Type: 'List<AWS::EC2::Subnet::Id>'}\n" + res, ""},
	}

	for _, tc := range cases {
		for format, b := range bothFormats(t, tc.body) {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := ParseTemplate(b)
				assert.Equal(t, tc.msg, errMessage(err))
			})
		}
	}
}

func TestConditionOperandErrors(t *testing.T) {
	t.Parallel()

	const res = "Resources:\n  B: {Type: AWS::S3::Bucket}\n"

	cases := []struct {
		name string
		cond string
		msg  string
	}{
		{"And with one operand", "!And [!Equals [a, a]]",
			"Template error: every Fn::And object requires a list of at least 2 and at most 10 boolean parameters."},
		{"Or with eleven operands", "!Or [" + repeatCond(11) + "]",
			"Template error: every Fn::Or object requires a list of at least 2 and at most 10 boolean parameters."},
		{"Or with ten operands", "!Or [" + repeatCond(10) + "]", ""},
		{"Not with two operands", "!Not [!Equals [a, a], !Equals [a, a]]",
			"Template error: every Fn::Not object requires a list with exactly 1 boolean parameter."},
		{"literal condition", "yes",
			"Template format error: Conditions can only be boolean operations on parameters and other conditions"},
	}

	for _, tc := range cases {
		for format, b := range bothFormats(t, "Conditions:\n  C: "+tc.cond+"\n"+res) {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				_, _, err := prepareBody(b, nil, "us-east-1")
				assert.Equal(t, tc.msg, errMessage(err))
			})
		}
	}
}

func repeatCond(n int) string {
	out := ""

	for i := range n {
		if i > 0 {
			out += ", "
		}

		out += "!Equals [a, b]"
	}

	return out
}

// resolvedProps prepares a template and resolves resource R's properties.
func resolvedProps(t *testing.T, body string, params map[string]string, region string) map[string]any {
	t.Helper()

	out, r, err := prepareBody(body, params, region)
	require.NoError(t, err)

	got, err := r.Resolve(out.Resources["R"].Properties)
	require.NoError(t, err)

	m, ok := got.(map[string]any)
	require.True(t, ok)

	return m
}

func TestIntrinsicFunctions(t *testing.T) {
	t.Parallel()

	head := `Parameters:
  Env: {Type: String, Default: dev}
  Subnets: {Type: CommaDelimitedList, Default: "subnet-1, subnet-2,subnet-3"}
  Ports: {Type: List<Number>, Default: "80,443"}
Mappings:
  RegionMap:
    us-east-1: {Ami: ami-east, Size: 2}
    eu-west-1: {Ami: ami-west, Size: 3}
Conditions:
  IsProd: !Equals [!Ref Env, prod]
  EastMap: !Equals [!FindInMap [RegionMap, !Ref "AWS::Region", Ami], ami-east]
Resources:
  R:
    Type: AWS::S3::Bucket
    Properties:
      P: `

	cases := []struct {
		name  string
		value string
		want  any
	}{
		{"If false branch", `!If [IsProd, big, small]`, "small"},
		{"If on a mapping condition", `!If [EastMap, east, other]`, "east"},
		{"If NoValue in a list", `[a, !If [IsProd, b, !Ref "AWS::NoValue"], c]`, []any{"a", "c"}},
		{"If NoValue nested key", `{Keep: 1, Drop: !If [IsProd, x, !Ref "AWS::NoValue"]}`, map[string]any{"Keep": json.Number("1")}},
		{"FindInMap", `!FindInMap [RegionMap, !Ref "AWS::Region", Ami]`, "ami-east"},
		{"FindInMap number", `!FindInMap [RegionMap, eu-west-1, Size]`, json.Number("3")},
		{"Select literal", `!Select [1, [a, b, c]]`, "b"},
		{"Select list parameter", `!Select [2, !Ref Subnets]`, "subnet-3"},
		{"Ref list parameter", `!Ref Subnets`, []any{"subnet-1", "subnet-2", "subnet-3"}},
		{"Ref number list", `!Ref Ports`, []any{"80", "443"}},
		{"Join list parameter", `!Join ["|", !Ref Subnets]`, "subnet-1|subnet-2|subnet-3"},
		{"Split", `!Split [",", "a,b,,c"]`, []any{"a", "b", "", "c"}},
		{"Join of Split", `!Join ["-", !Split [",", "a,b"]]`, "a-b"},
		{"Base64", `!Base64 "hello world"`, "aGVsbG8gd29ybGQ="},
		{"Base64 of Sub", `!Base64 {"Fn::Sub": "${Env}-x"}`, "ZGV2LXg="},
		{"GetAZs empty", `!GetAZs ""`, []any{"us-east-1a", "us-east-1b", "us-east-1c"}},
		{"GetAZs region", `!GetAZs eu-west-1`, []any{"eu-west-1a", "eu-west-1b", "eu-west-1c"}},
		{"Select GetAZs", `!Select [0, !GetAZs {Ref: "AWS::Region"}]`, "us-east-1a"},
		{"Cidr IPv4", `!Cidr ["192.168.0.0/24", 6, 5]`, []any{
			"192.168.0.0/27", "192.168.0.32/27", "192.168.0.64/27", "192.168.0.96/27", "192.168.0.128/27", "192.168.0.160/27",
		}},
		{"Cidr unmasked block", `!Cidr ["10.0.7.9/16", "2", "8"]`, []any{"10.0.0.0/24", "10.0.1.0/24"}},
		{"Cidr IPv6", `!Cidr ["2600:1f18:abc:de00::/56", 2, 64]`, []any{"2600:1f18:abc:de00::/64", "2600:1f18:abc:de01::/64"}},
	}

	for _, tc := range cases {
		for format, b := range bothFormats(t, head+tc.value+"\n") {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				got := resolvedProps(t, b, map[string]string{"Env": "dev", "Subnets": "subnet-1, subnet-2,subnet-3", "Ports": "80,443"}, "us-east-1")
				assert.Equal(t, tc.want, got["P"])
			})
		}
	}
}

func TestIfNoValueDropsProperty(t *testing.T) {
	t.Parallel()

	body := envConditions + `Resources:
  R:
    Type: AWS::S3::Bucket
    Properties:
      Name: fixed
      Versioning: !If [IsProd, {Status: Enabled}, !Ref "AWS::NoValue"]
`
	for format, b := range bothFormats(t, body) {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			dev := resolvedProps(t, b, map[string]string{"Env": "dev", "Size": "small"}, "us-east-1")
			assert.Equal(t, map[string]any{"Name": "fixed"}, dev)

			prod := resolvedProps(t, b, map[string]string{"Env": "prod", "Size": "small"}, "us-east-1")
			assert.Equal(t, map[string]any{"Name": "fixed", "Versioning": map[string]any{"Status": "Enabled"}}, prod)
		})
	}
}

func TestIntrinsicErrors(t *testing.T) {
	t.Parallel()

	head := "Resources:\n  R:\n    Type: AWS::S3::Bucket\n    Properties:\n      P: "

	cases := []struct {
		name  string
		value string
		msg   string
	}{
		{"Select out of range", `!Select [3, [a, b]]`, "Template error: Fn::Select cannot select nonexistent value at index 3"},
		{"Select bad index", `!Select [x, [a, b]]`,
			"Template error: Fn::Select requires a list argument with two elements: an integer index and a list"},
		{"Cidr too many blocks", `!Cidr ["10.0.0.0/24", 9, 5]`, "Template error: Fn::Cidr cannot fit 9 blocks with 5 CIDR bits in 10.0.0.0/24"},
		{"Cidr count range", `!Cidr ["10.0.0.0/16", 0, 8]`,
			"Template error: Fn::Cidr count must be between 1 and 256 and cidrBits must be an integer"},
		{"Cidr bad block", `!Cidr ["10.0.0/16", 1, 8]`, "Template error: Fn::Cidr ipBlock 10.0.0/16 is not a valid CIDR block"},
		{"Split malformed", `!Split [","]`,
			"Template error: Fn::Split requires a list argument with two elements: a delimiter and a source string"},
	}

	for _, tc := range cases {
		for format, b := range bothFormats(t, head+tc.value+"\n") {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				out, r, err := prepareBody(b, nil, "us-east-1")
				require.NoError(t, err)

				_, err = r.Resolve(out.Resources["R"].Properties)
				assert.Equal(t, tc.msg, errMessage(err))
			})
		}
	}
}

func TestPseudoParameters(t *testing.T) {
	t.Parallel()

	body := `Resources:
  R:
    Type: AWS::S3::Bucket
    Properties:
      Region: !Ref "AWS::Region"
      Account: !Ref "AWS::AccountId"
      Name: !Ref "AWS::StackName"
      Id: !Ref "AWS::StackId"
      Partition: !Ref "AWS::Partition"
      Suffix: !Sub "s3.${AWS::URLSuffix}"
      Topics: !Ref "AWS::NotificationARNs"
      FirstTopic: !Select [0, !Ref "AWS::NotificationARNs"]
      Gone: !Ref "AWS::NoValue"
`
	topics := []any{"arn:aws:sns:us-east-1:123456789012:a", "arn:aws:sns:us-east-1:123456789012:b"}

	cases := []struct {
		region, partition, suffix string
	}{
		{"us-east-1", "aws", "amazonaws.com"},
		{"cn-north-1", "aws-cn", "amazonaws.com.cn"},
		{"us-gov-west-1", "aws-us-gov", "amazonaws.com"},
	}

	for _, tc := range cases {
		for format, b := range bothFormats(t, body) {
			t.Run(format+"/"+tc.region, func(t *testing.T) {
				t.Parallel()

				got := resolvedProps(t, b, nil, tc.region)
				assert.Equal(t, map[string]any{
					"Region": tc.region, "Account": "123456789012", "Name": "demo",
					"Id":        "arn:aws:cloudformation:" + tc.region + ":123456789012:stack/demo/id-1",
					"Partition": tc.partition, "Suffix": "s3." + tc.suffix,
					"Topics": topics, "FirstTopic": topics[0],
				}, got, "AWS::NoValue removes Gone")
			})
		}
	}
}

func TestParameterConstraints(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		param string
		value string
		msg   string
	}{
		{"allowed value", `{Type: String, AllowedValues: [prod, dev]}`, "dev", ""},
		{"not an allowed value", `{Type: String, AllowedValues: [prod, dev]}`, "test", "Parameter 'P' must be one of AllowedValues"},
		{"pattern match", `{Type: String, AllowedPattern: "[a-z]+"}`, "abc", ""},
		{"pattern must match all", `{Type: String, AllowedPattern: "[a-z]+"}`, "abc1", "Parameter 'P' must match pattern [a-z]+"},
		{"constraint description", `{Type: String, AllowedPattern: "[a-z]+", ConstraintDescription: lower case only}`, "ABC",
			"Parameter 'P' failed to satisfy constraint: lower case only"},
		{"too short", `{Type: String, MinLength: 3}`, "ab", "Parameter 'P' must contain at least 3 characters"},
		{"too long", `{Type: String, MaxLength: "4"}`, "abcde", "Parameter 'P' must contain at most 4 characters"},
		{"not a number", `{Type: Number}`, "ten", "Parameter 'P' must be a number"},
		{"number too small", `{Type: Number, MinValue: 1}`, "0", "Parameter 'P' must be a number not less than 1"},
		{"number too big", `{Type: Number, MaxValue: 2.5}`, "3", "Parameter 'P' must be a number not greater than 2.5"},
		{"number in range", `{Type: Number, MinValue: 1, MaxValue: 10}`, "10", ""},
		{"number allowed values", `{Type: Number, AllowedValues: [1, 2]}`, "3", "Parameter 'P' must be one of AllowedValues"},
		{"list items allowed", `{Type: CommaDelimitedList, AllowedValues: [a, b]}`, "a, b", ""},
		{"list item not allowed", `{Type: CommaDelimitedList, AllowedValues: [a, b]}`, "a,c", "Parameter 'P' must be one of AllowedValues"},
		{"number list item", `{Type: List<Number>}`, "1,x", "Parameter 'P' must be a number"},
		{"ssm name checked as string", `{Type: "AWS::SSM::Parameter::Value<List<String>>", AllowedPattern: "/app/.*"}`, "/other",
			"Parameter 'P' must match pattern /app/.*"},
	}

	for _, tc := range cases {
		for format, b := range bothFormats(t, "Parameters:\n  P: "+tc.param+"\nResources:\n  B: {Type: AWS::S3::Bucket}\n") {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				tmpl, err := ParseTemplate(b)
				require.NoError(t, err)

				def := tmpl.Parameters["P"]
				assert.Equal(t, tc.msg, errMessage(CheckParameterValue("P", &def, tc.value)))
			})
		}
	}
}

func TestParameterTypeHelpers(t *testing.T) {
	t.Parallel()

	lists := []string{"CommaDelimitedList", "List<Number>", "List<AWS::EC2::Subnet::Id>", "AWS::SSM::Parameter::Value<List<String>>"}
	for _, typ := range lists {
		assert.True(t, IsListType(typ), typ)
	}

	for _, typ := range []string{"String", "Number", "AWS::SSM::Parameter::Value<String>"} {
		assert.False(t, IsListType(typ), typ)
	}

	inner, ok := SSMValueType("AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>")
	assert.True(t, ok)
	assert.Equal(t, "AWS::EC2::Image::Id", inner)
}
