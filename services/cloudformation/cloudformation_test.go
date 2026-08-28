package cloudformation_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

func TestParseTemplate(t *testing.T) {
	t.Parallel()

	body := `{
		"AWSTemplateFormatVersion":"2010-09-09",
		"Description":"demo",
		"Parameters":{"Env":{"Type":"String","Default":"dev"}},
		"Resources":{"B":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"b"}}},
		"Outputs":{"Name":{"Value":{"Ref":"B"}}}
	}`

	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)
	assert.Equal(t, "demo", tmpl.Description)
	assert.Len(t, tmpl.Resources, 1)
	assert.Equal(t, "AWS::S3::Bucket", tmpl.Resources["B"].Type)
	assert.Equal(t, "dev", cfn.Stringify(tmpl.Parameters["Env"].Default))
}

func TestParseTemplateErrors(t *testing.T) {
	t.Parallel()

	_, err := cfn.ParseTemplate("")
	require.Error(t, err)

	_, err = cfn.ParseTemplate("{not json")
	require.Error(t, err)

	_, err = cfn.ParseTemplate(`{"Resources":{}}`)
	require.Error(t, err, "a template with no resources is rejected")

	_, err = cfn.ParseTemplate(`{"Resources":{"X":{"Properties":{}}}}`)
	require.Error(t, err, "a resource with no Type is rejected")
}

func newResolver() *cfn.Resolver {
	return &cfn.Resolver{
		Params: map[string]string{"Env": "prod"},
		Resources: map[string]cfn.ResolvedResource{
			"Bucket": {RefValue: "my-bucket", Attributes: map[string]string{"Arn": "arn:aws:s3:::my-bucket"}},
		},
		Region:    "us-east-1",
		AccountID: "123456789012",
		StackName: "demo",
	}
}

func TestResolverRefAndPseudo(t *testing.T) {
	t.Parallel()

	r := newResolver()

	got, err := r.ResolveString(map[string]any{"Ref": "Bucket"})
	require.NoError(t, err)
	assert.Equal(t, "my-bucket", got)

	got, err = r.ResolveString(map[string]any{"Ref": "Env"})
	require.NoError(t, err)
	assert.Equal(t, "prod", got)

	got, err = r.ResolveString(map[string]any{"Ref": "AWS::Region"})
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", got)

	_, err = r.ResolveString(map[string]any{"Ref": "Missing"})
	require.Error(t, err)
}

func TestResolverGetAtt(t *testing.T) {
	t.Parallel()

	r := newResolver()

	got, err := r.ResolveString(map[string]any{"Fn::GetAtt": []any{"Bucket", "Arn"}})
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:s3:::my-bucket", got)

	_, err = r.ResolveString(map[string]any{"Fn::GetAtt": []any{"Bucket", "Missing"}})
	require.Error(t, err)
}

func TestResolverSub(t *testing.T) {
	t.Parallel()

	r := newResolver()

	got, err := r.ResolveString(map[string]any{"Fn::Sub": "${Env}-${Bucket}-${AWS::AccountId}"})
	require.NoError(t, err)
	assert.Equal(t, "prod-my-bucket-123456789012", got)

	got, err = r.ResolveString(map[string]any{"Fn::Sub": "${Bucket.Arn}"})
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:s3:::my-bucket", got)

	got, err = r.ResolveString(map[string]any{"Fn::Sub": []any{
		"${Prefix}-${Env}", map[string]any{"Prefix": "app"},
	}})
	require.NoError(t, err)
	assert.Equal(t, "app-prod", got)

	got, err = r.ResolveString(map[string]any{"Fn::Sub": "${!Literal}"})
	require.NoError(t, err)
	assert.Equal(t, "${Literal}", got)
}

func TestResolverJoin(t *testing.T) {
	t.Parallel()

	r := newResolver()

	got, err := r.ResolveString(map[string]any{"Fn::Join": []any{
		"/", []any{"a", map[string]any{"Ref": "Bucket"}, "c"},
	}})
	require.NoError(t, err)
	assert.Equal(t, "a/my-bucket/c", got)
}

func TestOrderResourcesDependencies(t *testing.T) {
	t.Parallel()

	body := `{"Resources":{
		"Queue":{"Type":"AWS::SQS::Queue","Properties":{}},
		"Topic":{"Type":"AWS::SNS::Topic","Properties":{"X":{"Ref":"Queue"}}},
		"Fn":{"Type":"AWS::Lambda::Function","Properties":{"Arn":{"Fn::GetAtt":["Topic","TopicArn"]}}}
	}}`

	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	order, err := cfn.OrderResources(tmpl)
	require.NoError(t, err)

	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}

	assert.Less(t, pos["Queue"], pos["Topic"], "Queue precedes Topic (Ref)")
	assert.Less(t, pos["Topic"], pos["Fn"], "Topic precedes Fn (GetAtt)")
}

func TestOrderResourcesCycle(t *testing.T) {
	t.Parallel()

	body := `{"Resources":{
		"A":{"Type":"AWS::SQS::Queue","Properties":{"X":{"Ref":"B"}}},
		"B":{"Type":"AWS::SQS::Queue","Properties":{"X":{"Ref":"A"}}}
	}}`

	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	_, err = cfn.OrderResources(tmpl)
	require.Error(t, err, "a dependency cycle is rejected")
}

func TestOrderResourcesDependsOn(t *testing.T) {
	t.Parallel()

	body := `{"Resources":{
		"A":{"Type":"AWS::SQS::Queue","Properties":{}},
		"B":{"Type":"AWS::SQS::Queue","Properties":{},"DependsOn":"A"}
	}}`

	tmpl, err := cfn.ParseTemplate(body)
	require.NoError(t, err)

	order, err := cfn.OrderResources(tmpl)
	require.NoError(t, err)
	require.Len(t, order, 2)
	assert.Equal(t, "A", order[0])
	assert.Equal(t, "B", order[1])
}

// TestStringifyInteger guards that a JSON number default renders without a
// spurious decimal, so a numeric parameter round-trips as an integer string.
func TestStringifyInteger(t *testing.T) {
	t.Parallel()

	var n any
	require.NoError(t, json.Unmarshal([]byte("42"), &n))
	assert.Equal(t, "42", cfn.Stringify(n))
}
