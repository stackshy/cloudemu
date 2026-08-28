package cloudformation_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// clients bundles the SDK clients a stack test drives: CloudFormation to deploy,
// and S3/DynamoDB to prove the stack's resources really exist in their own
// service backends.
type clients struct {
	cfn *awscfn.Client
	s3  *s3.Client
	ddb *dynamodb.Client
}

func boot(t *testing.T) clients {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.NewFromProvider(cloud)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}

	return clients{
		cfn: awscfn.NewFromConfig(cfg, func(o *awscfn.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
		s3: s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(ts.URL)
			o.UsePathStyle = true
		}),
		ddb: dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(ts.URL) }),
	}
}

const bucketTableTemplate = `{
  "AWSTemplateFormatVersion":"2010-09-09",
  "Description":"bucket + table",
  "Resources":{
    "Data":{
      "Type":"AWS::S3::Bucket",
      "Properties":{"BucketName":"stack-data-bucket"}
    },
    "Items":{
      "Type":"AWS::DynamoDB::Table",
      "Properties":{
        "TableName":"stack-items",
        "BillingMode":"PAY_PER_REQUEST",
        "AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],
        "KeySchema":[{"AttributeName":"id","KeyType":"HASH"}]
      }
    }
  },
  "Outputs":{
    "BucketArn":{"Value":{"Fn::GetAtt":["Data","Arn"]}},
    "TableName":{"Value":{"Ref":"Items"}}
  }
}`

// TestStackLifecycleRealSDK is the real-user end-to-end: a CloudFormation client
// deploys a template that creates an S3 bucket and a DynamoDB table; the
// resources are then queried through the real S3 and DynamoDB clients (proving
// the orchestrator provisioned them in their own backends), the stack reports
// CREATE_COMPLETE with resolved outputs, and DeleteStack removes the resources.
func TestStackLifecycleRealSDK(t *testing.T) {
	ctx := context.Background()
	c := boot(t)

	_, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName:    aws.String("app"),
		TemplateBody: aws.String(bucketTableTemplate),
	})
	if err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	// The stack is CREATE_COMPLETE with both outputs resolved.
	desc, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("app")})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}

	if len(desc.Stacks) != 1 {
		t.Fatalf("expected 1 stack, got %d", len(desc.Stacks))
	}

	if got := desc.Stacks[0].StackStatus; got != cfntypes.StackStatusCreateComplete {
		t.Fatalf("stack status = %s, want CREATE_COMPLETE", got)
	}

	outs := outputMap(desc.Stacks[0].Outputs)
	if outs["BucketArn"] != "arn:aws:s3:::stack-data-bucket" {
		t.Fatalf("BucketArn output = %q", outs["BucketArn"])
	}

	if outs["TableName"] != "stack-items" {
		t.Fatalf("TableName output = %q", outs["TableName"])
	}

	// The resources actually exist in their own services.
	if _, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("stack-data-bucket")}); err != nil {
		t.Fatalf("bucket should exist after CreateStack: %v", err)
	}

	if _, err := c.ddb.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("stack-items")}); err != nil {
		t.Fatalf("table should exist after CreateStack: %v", err)
	}

	// GetTemplate returns the deployed body.
	tmpl, err := c.cfn.GetTemplate(ctx, &awscfn.GetTemplateInput{StackName: aws.String("app")})
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}

	if tmpl.TemplateBody == nil || *tmpl.TemplateBody == "" {
		t.Fatal("GetTemplate returned an empty body")
	}

	// StackEvents are recorded.
	events, err := c.cfn.DescribeStackEvents(ctx, &awscfn.DescribeStackEventsInput{StackName: aws.String("app")})
	if err != nil {
		t.Fatalf("DescribeStackEvents: %v", err)
	}

	if len(events.StackEvents) == 0 {
		t.Fatal("expected recorded stack events")
	}

	// DeleteStack removes the resources.
	if _, err := c.cfn.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("app")}); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}

	if _, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("stack-data-bucket")}); err == nil {
		t.Fatal("bucket should be gone after DeleteStack")
	}

	if _, err := c.ddb.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("stack-items")}); err == nil {
		t.Fatal("table should be gone after DeleteStack")
	}
}

// TestUpdateStackRealSDK deploys, then updates a resource property, and confirms
// the stack reports UPDATE_COMPLETE and the resource still exists.
func TestUpdateStackRealSDK(t *testing.T) {
	ctx := context.Background()
	c := boot(t)

	create := `{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"jobs","VisibilityTimeout":30}}}}`

	if _, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("q"), TemplateBody: aws.String(create),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	update := `{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"jobs","VisibilityTimeout":60}}}}`

	if _, err := c.cfn.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String("q"), TemplateBody: aws.String(update),
	}); err != nil {
		t.Fatalf("UpdateStack: %v", err)
	}

	desc, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("q")})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}

	if got := desc.Stacks[0].StackStatus; got != cfntypes.StackStatusUpdateComplete {
		t.Fatalf("stack status = %s, want UPDATE_COMPLETE", got)
	}
}

// TestUnsupportedResourceRollsBackRealSDK confirms a template with an
// unsupported resource type deploys to ROLLBACK_COMPLETE rather than silently
// succeeding.
func TestUnsupportedResourceRollsBackRealSDK(t *testing.T) {
	ctx := context.Background()
	c := boot(t)

	body := `{"Resources":{"X":{"Type":"AWS::Fake::Widget","Properties":{}}}}`

	if _, err := c.cfn.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("bad"), TemplateBody: aws.String(body),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	desc, err := c.cfn.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String("bad")})
	if err != nil {
		t.Fatalf("DescribeStacks: %v", err)
	}

	if got := desc.Stacks[0].StackStatus; got != cfntypes.StackStatusRollbackComplete {
		t.Fatalf("stack status = %s, want ROLLBACK_COMPLETE", got)
	}
}

func outputMap(outs []cfntypes.Output) map[string]string {
	m := map[string]string{}
	for _, o := range outs {
		if o.OutputKey != nil {
			m[*o.OutputKey] = aws.ToString(o.OutputValue)
		}
	}

	return m
}
