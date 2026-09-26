package cloudformation_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const yamlBucketTableTemplate = `AWSTemplateFormatVersion: "2010-09-09"
Description: yaml bucket + table
Parameters:
  Prefix:
    Type: String
    Default: yaml
Resources:
  Data:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub "${Prefix}-stack-bucket"
  Items:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Join ["-", [!Ref Prefix, items]]
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions:
        - AttributeName: id
          AttributeType: S
      KeySchema:
        - AttributeName: id
          KeyType: HASH
Outputs:
  BucketArn:
    Value: !GetAtt Data.Arn
  TableName:
    Value: !Ref Items
`

func TestYAMLStackRealSDK(t *testing.T) {
	c := boot(t)
	ctx := context.Background()

	_, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("yaml-stack"), TemplateBody: aws.String(yamlBucketTableTemplate),
	})
	if err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	desc, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("yaml-stack")})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}

	st := desc.Stacks[0]
	if st.StackStatus != "CREATE_COMPLETE" {
		t.Fatalf("status = %s (%s)", st.StackStatus, aws.ToString(st.StackStatusReason))
	}

	outs := outputMap(st.Outputs)
	if outs["BucketArn"] != "arn:aws:s3:::yaml-stack-bucket" || outs["TableName"] != "yaml-items" {
		t.Fatalf("outputs = %v", outs)
	}

	if _, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("yaml-stack-bucket")}); err != nil {
		t.Fatalf("bucket not created: %v", err)
	}

	tpl, err := c.cfn.GetTemplate(ctx, &awscfn.GetTemplateInput{StackName: aws.String("yaml-stack")})
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}

	if aws.ToString(tpl.TemplateBody) != yamlBucketTableTemplate {
		t.Fatal("GetTemplate must return the YAML body unchanged")
	}
}

func TestTemplateURLRealSDK(t *testing.T) {
	c := boot(t)
	ctx := context.Background()

	if _, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("templates")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("templates"), Key: aws.String("app/stack.yaml"),
		Body: strings.NewReader(yamlBucketTableTemplate),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	url := "https://templates.s3.us-east-1.amazonaws.com/app/stack.yaml"

	val, err := c.cfn.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{TemplateURL: aws.String(url)})
	if err != nil {
		t.Fatalf("ValidateTemplate: %v", err)
	}

	if aws.ToString(val.Description) != "yaml bucket + table" || len(val.Parameters) != 1 ||
		aws.ToString(val.Parameters[0].ParameterKey) != "Prefix" || aws.ToString(val.Parameters[0].DefaultValue) != "yaml" {
		t.Fatalf("ValidateTemplate = %+v", val)
	}

	if _, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("url-stack"), TemplateURL: aws.String(url),
	}); err != nil {
		t.Fatalf("CreateStack from TemplateURL: %v", err)
	}

	desc, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("url-stack")})
	if err != nil || desc.Stacks[0].StackStatus != "CREATE_COMPLETE" {
		t.Fatalf("DescribeStacks: %v %+v", err, desc)
	}

	_, err = c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("missing"), TemplateURL: aws.String("https://templates.s3.amazonaws.com/nope.yaml"),
	})
	requireValidationError(t, err, "TemplateURL must reference a valid S3 object to which you have access.")

	_, err = c.cfn.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{
		TemplateURL: aws.String("https://templates.example.com/app/stack.yaml"),
	})
	requireValidationError(t, err, "TemplateURL must be an Amazon S3 URL.")
}

func TestTemplateURLVersionRealSDK(t *testing.T) {
	c := boot(t)
	ctx := context.Background()

	if _, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("vtemplates")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if _, err := c.s3.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket:                  aws.String("vtemplates"),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	put := func(desc string) string {
		out, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String("vtemplates"), Key: aws.String("t.yaml"),
			Body: strings.NewReader("Description: " + desc + "\nResources:\n  B: {Type: AWS::S3::Bucket}\n"),
		})
		if err != nil {
			t.Fatalf("PutObject: %v", err)
		}

		return aws.ToString(out.VersionId)
	}

	first := put("first")
	put("second")

	base := "https://vtemplates.s3.amazonaws.com/t.yaml"

	for url, want := range map[string]string{base: "second", base + "?versionId=" + first: "first"} {
		out, err := c.cfn.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{TemplateURL: aws.String(url)})
		if err != nil {
			t.Fatalf("ValidateTemplate %s: %v", url, err)
		}

		if got := aws.ToString(out.Description); got != want {
			t.Fatalf("%s: Description = %q, want %q", url, got, want)
		}
	}
}

func TestValidateTemplateRealSDK(t *testing.T) {
	c := boot(t)
	ctx := context.Background()

	body := "Resources:\n  Role:\n    Type: AWS::IAM::Role\n    Properties:\n      RoleName: fixed\n" +
		"Transform: AWS::Serverless-2016-10-31\n"

	out, err := c.cfn.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{TemplateBody: aws.String(body)})
	if err != nil {
		t.Fatalf("ValidateTemplate: %v", err)
	}

	if len(out.Capabilities) != 1 || out.Capabilities[0] != "CAPABILITY_NAMED_IAM" {
		t.Fatalf("Capabilities = %v", out.Capabilities)
	}

	if aws.ToString(out.CapabilitiesReason) != "The following resource(s) require capabilities: [AWS::IAM::Role]" {
		t.Fatalf("CapabilitiesReason = %q", aws.ToString(out.CapabilitiesReason))
	}

	if len(out.DeclaredTransforms) != 1 || out.DeclaredTransforms[0] != "AWS::Serverless-2016-10-31" {
		t.Fatalf("DeclaredTransforms = %v", out.DeclaredTransforms)
	}

	_, err = c.cfn.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{
		TemplateBody: aws.String("Resources:\n  B:\n    Type: AWS::S3::Bucket\n    Properties:\n      Name: !GetAttr B.Arn\n"),
	})
	requireValidationError(t, err, "Template format error: YAML not well-formed. (line 5, column 13)")

	_, err = c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("bad"), TemplateBody: aws.String("Resources:\n  A: &a {Type: AWS::S3::Bucket}\n  B: *a\n"),
	})
	requireValidationError(t, err, "Template error: YAML aliases are not allowed in CloudFormation templates")

	_, err = c.cfn.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{})
	requireValidationError(t, err, "Either Template URL or Template Body must be specified.")
}

func requireValidationError(t *testing.T, err error, msg string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want an API error, got %v", err)
	}

	if apiErr.ErrorCode() != "ValidationError" || apiErr.ErrorMessage() != msg {
		t.Fatalf("got %s: %q, want ValidationError: %q", apiErr.ErrorCode(), apiErr.ErrorMessage(), msg)
	}
}
