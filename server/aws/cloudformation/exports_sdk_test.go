package cloudformation_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

const sdkExporter = `Resources:
  Queue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: shared-queue
Outputs:
  QueueName:
    Value: !GetAtt Queue.QueueName
    Export:
      Name: shared-queue-name
`

const sdkImporter = `Resources:
  Param:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /imported
      Type: String
      Value: !ImportValue shared-queue-name
`

// Exports and imports through the real SDK: ListExports, ListImports, the
// in-use guard on delete, and the exporter delete once the importer is gone.
func TestExportsAndImportsRealSDK(t *testing.T) {
	ctx := context.Background()
	c, cloud := bootWithProvider(t)

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("exporter"), TemplateBody: aws.String(sdkExporter),
	}); err != nil {
		t.Fatalf("CreateStack exporter: %v", err)
	}

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("importer"), TemplateBody: aws.String(sdkImporter),
	}); err != nil {
		t.Fatalf("CreateStack importer: %v", err)
	}

	if st := describeStack(t, c, "importer"); st.StackStatus != cfntypes.StackStatusCreateComplete {
		t.Fatalf("importer = %s (%s)", st.StackStatus, aws.ToString(st.StackStatusReason))
	}

	p, err := cloud.SSM.GetParameter(ctx, "/imported", false)
	if err != nil || p.Value != "shared-queue" {
		t.Fatalf("imported parameter = %+v, %v", p, err)
	}

	exports, err := c.ListExports(ctx, &awscfn.ListExportsInput{})
	if err != nil || len(exports.Exports) != 1 {
		t.Fatalf("ListExports = %+v, %v", exports, err)
	}

	e := exports.Exports[0]
	if aws.ToString(e.Name) != "shared-queue-name" || aws.ToString(e.Value) != "shared-queue" ||
		aws.ToString(e.ExportingStackId) != aws.ToString(describeStack(t, c, "exporter").StackId) {
		t.Fatalf("export = %+v", e)
	}

	imports, err := c.ListImports(ctx, &awscfn.ListImportsInput{ExportName: aws.String("shared-queue-name")})
	if err != nil || strings.Join(imports.Imports, ",") != "importer" {
		t.Fatalf("ListImports = %+v, %v", imports, err)
	}

	if _, err = c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("exporter")}); err != nil {
		t.Fatalf("DeleteStack exporter: %v", err)
	}

	st := describeStack(t, c, "exporter")
	if st.StackStatus != cfntypes.StackStatusDeleteFailed ||
		aws.ToString(st.StackStatusReason) != "Cannot delete export shared-queue-name as it is in use by importer" {
		t.Fatalf("exporter = %s (%s)", st.StackStatus, aws.ToString(st.StackStatusReason))
	}

	if _, err = c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("importer")}); err != nil {
		t.Fatalf("DeleteStack importer: %v", err)
	}

	_, err = c.ListImports(ctx, &awscfn.ListImportsInput{ExportName: aws.String("shared-queue-name")})
	if code, msg := apiErrorCode(t, err); code != "ValidationError" || msg != "Export 'shared-queue-name' is not imported by any stack." {
		t.Fatalf("ListImports after delete = %s: %s", code, msg)
	}

	if _, err = c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: st.StackId}); err != nil {
		t.Fatalf("DeleteStack retry: %v", err)
	}

	if final := describeStack(t, c, aws.ToString(st.StackId)); final.StackStatus != cfntypes.StackStatusDeleteComplete {
		t.Fatalf("exporter retry = %s", final.StackStatus)
	}
}

// A DynamoDB table with DeletionPolicy Retain keeps its item after the
// stack is deleted, and a bucket that holds an object fails the delete until
// a retry retains it.
func TestDeletionPolicyAndRetainResourcesRealSDK(t *testing.T) {
	ctx := context.Background()
	c, cloud := bootWithProvider(t)

	const body = `Resources:
  Table:
    Type: AWS::DynamoDB::Table
    DeletionPolicy: Retain
    Properties:
      TableName: kept-table
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions: [{AttributeName: id, AttributeType: S}]
      KeySchema: [{AttributeName: id, KeyType: HASH}]
  Bucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: full-bucket
`
	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{StackName: aws.String("data"), TemplateBody: aws.String(body)}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	stackID := aws.ToString(describeStack(t, c, "data").StackId)

	if err := cloud.DynamoDB.PutItem(ctx, "kept-table", map[string]any{"id": "1", "v": "x"}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	if err := cloud.S3.PutObject(ctx, "full-bucket", "k", []byte("v"), "text/plain", nil); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	_, err := c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("data"), RetainResources: []string{"Bucket"}})
	if code, msg := apiErrorCode(t, err); code != "ValidationError" || !strings.Contains(msg, "only when the stack is in the DELETE_FAILED state") {
		t.Fatalf("RetainResources before DELETE_FAILED = %s: %s", code, msg)
	}

	if _, err = c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("data")}); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}

	st := describeStack(t, c, "data")
	if st.StackStatus != cfntypes.StackStatusDeleteFailed ||
		aws.ToString(st.StackStatusReason) != "The following resource(s) failed to delete: [Bucket]." {
		t.Fatalf("after delete = %s (%s)", st.StackStatus, aws.ToString(st.StackStatusReason))
	}

	if _, err = c.DeleteStack(ctx, &awscfn.DeleteStackInput{
		StackName: aws.String("data"), RetainResources: []string{"Bucket"},
	}); err != nil {
		t.Fatalf("DeleteStack retain: %v", err)
	}

	if final := describeStack(t, c, stackID); final.StackStatus != cfntypes.StackStatusDeleteComplete {
		t.Fatalf("after retain = %s", final.StackStatus)
	}

	item, err := cloud.DynamoDB.GetItem(ctx, "kept-table", map[string]any{"id": "1"})
	if err != nil || item["v"] != "x" {
		t.Fatalf("retained item = %v, %v", item, err)
	}

	if _, err = cloud.S3.GetBucketVersioning(ctx, "full-bucket"); err != nil {
		t.Fatalf("retained bucket: %v", err)
	}
}

// Termination protection, CreateStack's failure options and the account
// operations over the real SDK.
func TestTerminationProtectionAndFailureOptionsRealSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := bootWithProvider(t)

	const queue = `{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"guarded"}}}}`

	if _, err := c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("guarded"), TemplateBody: aws.String(queue), EnableTerminationProtection: aws.Bool(true),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	if !aws.ToBool(describeStack(t, c, "guarded").EnableTerminationProtection) {
		t.Fatal("EnableTerminationProtection not reported")
	}

	_, err := c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("guarded")})
	if code, msg := apiErrorCode(t, err); code != "ValidationError" ||
		msg != "Stack [guarded] cannot be deleted while TerminationProtection is enabled" {
		t.Fatalf("protected delete = %s: %s", code, msg)
	}

	out, err := c.UpdateTerminationProtection(ctx, &awscfn.UpdateTerminationProtectionInput{
		StackName: aws.String("guarded"), EnableTerminationProtection: aws.Bool(false),
	})
	if err != nil || aws.ToString(out.StackId) != aws.ToString(describeStack(t, c, "guarded").StackId) {
		t.Fatalf("UpdateTerminationProtection = %+v, %v", out, err)
	}

	if _, err = c.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("guarded")}); err != nil {
		t.Fatalf("DeleteStack: %v", err)
	}

	const failing = `{"Resources":{
		"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"kept-on-failure"}},
		"Bad":{"Type":"AWS::Unknown::Thing","DependsOn":"Q"}
	}}`

	if _, err = c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("failing"), TemplateBody: aws.String(failing), OnFailure: cfntypes.OnFailureDoNothing,
	}); err != nil {
		t.Fatalf("CreateStack DO_NOTHING: %v", err)
	}

	if st := describeStack(t, c, "failing"); st.StackStatus != cfntypes.StackStatusCreateFailed || !aws.ToBool(st.DisableRollback) {
		t.Fatalf("DO_NOTHING = %s, DisableRollback %v", st.StackStatus, aws.ToBool(st.DisableRollback))
	}

	_, err = c.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String("both"), TemplateBody: aws.String(queue),
		OnFailure: cfntypes.OnFailureDelete, DisableRollback: aws.Bool(true),
	})
	if code, msg := apiErrorCode(t, err); code != "ValidationError" || msg != "Either DisableRollback or OnFailure can be specified, not both." {
		t.Fatalf("both options = %s: %s", code, msg)
	}

	limits, err := c.DescribeAccountLimits(ctx, &awscfn.DescribeAccountLimitsInput{})
	if err != nil || len(limits.AccountLimits) != 3 || aws.ToString(limits.AccountLimits[0].Name) != "StackLimit" ||
		aws.ToInt32(limits.AccountLimits[0].Value) != 2000 {
		t.Fatalf("DescribeAccountLimits = %+v, %v", limits, err)
	}

	cost, err := c.EstimateTemplateCost(ctx, &awscfn.EstimateTemplateCostInput{TemplateBody: aws.String(queue)})
	if err != nil || !strings.HasPrefix(aws.ToString(cost.Url), "https://calculator.aws/#/estimate?id=") {
		t.Fatalf("EstimateTemplateCost = %+v, %v", cost, err)
	}
}
