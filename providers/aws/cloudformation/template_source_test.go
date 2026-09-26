package cloudformation

import (
	"context"
	"errors"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

const yamlStackTemplate = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Env:
    Type: String
    Default: dev
Resources:
  MyBucket:
    Type: Test::Bucket
    Properties:
      Name: !Sub "${Env}-data"
  MyTopic:
    Type: Test::Topic
    Properties:
      Name: !Join ["-", [!Ref MyBucket, events]]
Outputs:
  BucketArn:
    Value: !GetAtt MyBucket.Arn
  TopicArn:
    Value: !GetAtt [MyTopic, Arn]
`

func assertErrMsg(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("want error %q, got nil", want)
	}

	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}

	if got := cerrors.Message(err); got != want {
		t.Fatalf("message: got %q, want %q", got, want)
	}
}

func outputMap(s *cfn.Stack) map[string]string {
	out := map[string]string{}
	for _, o := range s.Outputs {
		out[o.Key] = o.Value
	}

	return out
}

func TestCreateStackYAMLShortForms(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	stack, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: yamlStackTemplate})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusCreateComplete, "stack status")

	if !store.items["dev-data"] || !store.items["dev-data-events"] {
		t.Fatalf("resources not provisioned: %v", store.items)
	}

	out := outputMap(stack)
	assertEqual(t, out["BucketArn"], "arn:aws:s3:::dev-data", "dotted GetAtt output")
	assertEqual(t, out["TopicArn"], "arn:aws:sns:us-east-1:123456789012:dev-data-events", "list GetAtt output")

	body, err := m.GetTemplate(ctx, "demo")
	requireNoError(t, err)
	assertEqual(t, body, yamlStackTemplate, "GetTemplate returns the YAML body unchanged")

	stack, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "demo", TemplateBody: strings.Replace(yamlStackTemplate, "-data", "-v2", 1),
	})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusUpdateComplete, "update status")
	assertEqual(t, outputMap(stack)["BucketArn"], "arn:aws:s3:::dev-v2", "output after update")
}

func TestCreateStackMalformedYAML(t *testing.T) {
	m := newTestMock(newBacking())

	_, err := m.CreateStack(context.Background(), &cfn.CreateStackInput{
		StackName: "bad", TemplateBody: "Resources:\n  B:\n    Type: Test::Bucket\n    Properties:\n      Name: !Nope x\n",
	})
	assertErrMsg(t, err, "Template format error: YAML not well-formed. (line 5, column 13)")

	if _, derr := m.DescribeStacks(context.Background(), "bad"); !cerrors.IsNotFound(derr) {
		t.Fatalf("a rejected template must not create a stack, got %v", derr)
	}
}

// fakeS3 is a TemplateFetcher over an in-memory bucket/key map.
type fakeS3 map[string]string

func (f fakeS3) fetch(_ context.Context, bucket, key string) ([]byte, error) {
	body, ok := f[bucket+"/"+key]
	if !ok {
		return nil, errors.New("NoSuchKey")
	}

	return []byte(body), nil
}

func TestCreateStackFromTemplateURL(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)
	m.SetTemplateFetcher(fakeS3{
		"tpl-bucket/stacks/app.yaml": yamlStackTemplate,
		"tpl-bucket/stacks/v2.yaml":  strings.Replace(yamlStackTemplate, "-data", "-v2", 1),
	}.fetch)

	stack, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "from-url", TemplateURL: "https://tpl-bucket.s3.us-east-1.amazonaws.com/stacks/app.yaml",
	})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusCreateComplete, "stack status")
	assertEqual(t, stack.TemplateBody, yamlStackTemplate, "stored body is the fetched object")

	stack, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "from-url", TemplateURL: "http://localhost:4566/tpl-bucket/stacks/v2.yaml",
	})
	requireNoError(t, err)
	assertEqual(t, outputMap(stack)["BucketArn"], "arn:aws:s3:::dev-v2", "update from a path-style URL")
}

func TestTemplateSourceErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())
	m.SetTemplateFetcher(fakeS3{}.fetch)

	cases := []struct {
		name, body, url, msg string
	}{
		{"neither", "", "", cfn.MsgNoTemplate},
		{"both", yamlStackTemplate, "https://s3.amazonaws.com/b/k", msgBothTemplates},
		{"not http", "", "s3://b/k", msgNotS3URL},
		{"no key", "", "https://s3.amazonaws.com/b", msgNotS3URL},
		{"missing object", "", "https://s3.amazonaws.com/b/k.yaml", msgTemplateAccess},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: tc.body, TemplateURL: tc.url})
			assertErrMsg(t, err, tc.msg)
		})
	}

	unwired := newTestMock(newBacking())
	_, err := unwired.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateURL: "https://s3.amazonaws.com/b/k"})
	assertErrMsg(t, err, msgTemplateAccess)
}

func TestParseS3URL(t *testing.T) {
	cases := []struct {
		url, bucket, key string
		ok               bool
	}{
		{"https://s3.amazonaws.com/b/k.yaml", "b", "k.yaml", true},
		{"https://s3.eu-west-1.amazonaws.com/b/dir/k.json", "b", "dir/k.json", true},
		{"https://s3-eu-west-1.amazonaws.com/b/k", "b", "k", true},
		{"https://b.s3.amazonaws.com/dir/k", "b", "dir/k", true},
		{"https://my.bucket.s3.us-west-2.amazonaws.com/k", "my.bucket", "k", true},
		{"https://b.s3-us-west-2.amazonaws.com/k", "b", "k", true},
		{"http://localhost:4566/b/k%20x.yaml", "b", "k x.yaml", true},
		{"http://b.s3.localhost.localstack.cloud:4566/k", "b", "k", true},
		{"https://b.s3.amazonaws.com/", "", "", false},
		{"https://s3.amazonaws.com/b/", "", "", false},
		{"ftp://s3.amazonaws.com/b/k", "", "", false},
		{"not a url", "", "", false},
	}

	for _, tc := range cases {
		b, k, ok := parseS3URL(tc.url)
		if ok != tc.ok || b != tc.bucket || k != tc.key {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", tc.url, b, k, ok, tc.bucket, tc.key, tc.ok)
		}
	}
}

func TestValidateTemplate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())
	m.SetTemplateFetcher(fakeS3{"b/iam.yaml": "Resources:\n  R: {Type: AWS::IAM::Role}\n"}.fetch)

	sum, err := m.ValidateTemplate(ctx, &cfn.ValidateTemplateInput{TemplateBody: yamlStackTemplate})
	requireNoError(t, err)

	if len(sum.Parameters) != 1 || sum.Parameters[0].Key != "Env" || sum.Parameters[0].DefaultValue != "dev" {
		t.Fatalf("parameters: %+v", sum.Parameters)
	}

	sum, err = m.ValidateTemplate(ctx, &cfn.ValidateTemplateInput{TemplateURL: "https://b.s3.amazonaws.com/iam.yaml"})
	requireNoError(t, err)

	if len(sum.Capabilities) != 1 || sum.Capabilities[0] != cfn.CapabilityIAM {
		t.Fatalf("capabilities: %v", sum.Capabilities)
	}

	// TemplateBody wins when both are given.
	_, err = m.ValidateTemplate(ctx, &cfn.ValidateTemplateInput{
		TemplateBody: yamlStackTemplate, TemplateURL: "https://b.s3.amazonaws.com/missing",
	})
	requireNoError(t, err)

	_, err = m.ValidateTemplate(ctx, &cfn.ValidateTemplateInput{TemplateBody: "Resources: [\n"})
	if err == nil || !cerrors.IsInvalidArgument(err) {
		t.Fatalf("malformed YAML: want InvalidArgument, got %v", err)
	}

	_, err = m.ValidateTemplate(ctx, &cfn.ValidateTemplateInput{})
	assertErrMsg(t, err, cfn.MsgNoTemplate)
}
