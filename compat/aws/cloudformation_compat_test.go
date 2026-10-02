package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSCloudFormationCompat drives a full stack lifecycle through the real
// aws-sdk-go-v2 CloudFormation client, exercising every operation the
// "cloudformation" coverage row lists. The stack creates an S3 bucket and a
// DynamoDB table, so it also proves the orchestrator provisions resources into
// the real service backends.
func TestAWSCloudFormationCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{
		CloudFormation: provider.CloudFormation,
		S3:             provider.S3,
		DynamoDB:       provider.DynamoDB,
		Region:         provider.Region,
		AccountID:      provider.AccountID,
	})

	client := awscfn.NewFromConfig(sess.Config(), func(o *awscfn.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		svc   = "cloudformation"
		stack = "compat-stack"
	)

	sess.Op(svc, "CreateStack", func() error {
		_, err := client.CreateStack(ctx, &awscfn.CreateStackInput{
			StackName:    aws.String(stack),
			TemplateBody: aws.String(compatTemplate),
		})

		return err
	})

	sess.Op(svc, "DescribeStacks", func() error {
		out, err := client.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String(stack)})
		if err != nil {
			return err
		}

		if len(out.Stacks) != 1 {
			return errCompat("expected 1 stack")
		}

		return nil
	})

	sess.Op(svc, "DescribeStackResources", func() error {
		_, err := client.DescribeStackResources(ctx, &awscfn.DescribeStackResourcesInput{StackName: aws.String(stack)})
		return err
	})

	sess.Op(svc, "ListStackResources", func() error {
		_, err := client.ListStackResources(ctx, &awscfn.ListStackResourcesInput{StackName: aws.String(stack)})
		return err
	})

	sess.Op(svc, "DescribeStackEvents", func() error {
		_, err := client.DescribeStackEvents(ctx, &awscfn.DescribeStackEventsInput{StackName: aws.String(stack)})
		return err
	})

	sess.Op(svc, "ListStacks", func() error {
		_, err := client.ListStacks(ctx, &awscfn.ListStacksInput{})
		return err
	})

	sess.Op(svc, "GetTemplate", func() error {
		_, err := client.GetTemplate(ctx, &awscfn.GetTemplateInput{StackName: aws.String(stack)})
		return err
	})

	sess.Op(svc, "ValidateTemplate", func() error {
		_, err := client.ValidateTemplate(ctx, &awscfn.ValidateTemplateInput{TemplateBody: aws.String(compatTemplate)})
		return err
	})

	sess.Op(svc, "GetTemplateSummary", func() error {
		out, err := client.GetTemplateSummary(ctx, &awscfn.GetTemplateSummaryInput{StackName: aws.String(stack)})
		if err != nil {
			return err
		}

		if len(out.ResourceTypes) != 2 {
			return errCompat("expected 2 resource types")
		}

		return nil
	})

	sess.Op(svc, "UpdateStack", func() error {
		_, err := client.UpdateStack(ctx, &awscfn.UpdateStackInput{
			StackName:    aws.String(stack),
			TemplateBody: aws.String(compatTemplateUpdated),
		})

		return err
	})

	sess.Op(svc, "ContinueUpdateRollback", func() error {
		return continueUpdateRollback(ctx, client)
	})

	changeSetOps(ctx, sess, client)
	exportOps(ctx, sess, client)

	sess.Op(svc, "DeleteStack", func() error {
		_, err := client.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String(stack)})
		return err
	})
}

// changeSetOps creates a stack from a CREATE change set, then creates and
// deletes an UPDATE change set on it.
func changeSetOps(ctx context.Context, sess *compat.AWSSession, client *awscfn.Client) {
	const (
		svc   = "cloudformation"
		stack = "compat-cs"
	)

	var id string

	sess.Op(svc, "CreateChangeSet", func() error {
		out, err := client.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
			StackName: aws.String(stack), ChangeSetName: aws.String("create"),
			ChangeSetType: cfntypes.ChangeSetTypeCreate, TemplateBody: aws.String(changeSetTemplate),
		})
		if err != nil {
			return err
		}

		id = aws.ToString(out.Id)

		return nil
	})

	sess.Op(svc, "DescribeChangeSet", func() error {
		out, err := client.DescribeChangeSet(ctx, &awscfn.DescribeChangeSetInput{ChangeSetName: aws.String(id)})
		if err != nil {
			return err
		}

		if out.Status != cfntypes.ChangeSetStatusCreateComplete || len(out.Changes) != 1 {
			return errCompat("change set " + string(out.Status))
		}

		return nil
	})

	sess.Op(svc, "ListChangeSets", func() error {
		out, err := client.ListChangeSets(ctx, &awscfn.ListChangeSetsInput{StackName: aws.String(stack)})
		if err != nil {
			return err
		}

		if len(out.Summaries) != 1 {
			return errCompat("expected 1 change set")
		}

		return nil
	})

	sess.Op(svc, "ExecuteChangeSet", func() error {
		if _, err := client.ExecuteChangeSet(ctx, &awscfn.ExecuteChangeSetInput{ChangeSetName: aws.String(id)}); err != nil {
			return err
		}

		out, err := client.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String(stack)})
		if err != nil {
			return err
		}

		if out.Stacks[0].StackStatus != cfntypes.StackStatusCreateComplete {
			return errCompat("status " + string(out.Stacks[0].StackStatus))
		}

		return nil
	})

	sess.Op(svc, "DeleteChangeSet", func() error {
		if _, err := client.CreateChangeSet(ctx, &awscfn.CreateChangeSetInput{
			StackName: aws.String(stack), ChangeSetName: aws.String("update"), UsePreviousTemplate: aws.Bool(true),
		}); err != nil {
			return err
		}

		_, err := client.DeleteChangeSet(ctx, &awscfn.DeleteChangeSetInput{
			StackName: aws.String(stack), ChangeSetName: aws.String("update"),
		})

		return err
	})
}

// exportOps exports a bucket name from one stack, imports it into another,
// then protects the importer and checks the account operations.
func exportOps(ctx context.Context, sess *compat.AWSSession, client *awscfn.Client) {
	const svc = "cloudformation"

	sess.Op(svc, "ListExports", func() error {
		if _, err := client.CreateStack(ctx, &awscfn.CreateStackInput{
			StackName: aws.String("compat-exporter"), TemplateBody: aws.String(exporterTemplate),
		}); err != nil {
			return err
		}

		out, err := client.ListExports(ctx, &awscfn.ListExportsInput{})
		if err != nil {
			return err
		}

		if len(out.Exports) != 1 || aws.ToString(out.Exports[0].Value) != "compat-exported" {
			return errCompat("expected 1 export")
		}

		return nil
	})

	sess.Op(svc, "ListImports", func() error {
		if _, err := client.CreateStack(ctx, &awscfn.CreateStackInput{
			StackName: aws.String("compat-importer"), TemplateBody: aws.String(importerTemplate),
		}); err != nil {
			return err
		}

		out, err := client.ListImports(ctx, &awscfn.ListImportsInput{ExportName: aws.String("compat-export")})
		if err != nil {
			return err
		}

		if len(out.Imports) != 1 || out.Imports[0] != "compat-importer" {
			return errCompat("expected 1 import")
		}

		return nil
	})

	sess.Op(svc, "UpdateTerminationProtection", func() error {
		_, err := client.UpdateTerminationProtection(ctx, &awscfn.UpdateTerminationProtectionInput{
			StackName: aws.String("compat-importer"), EnableTerminationProtection: aws.Bool(true),
		})
		if err != nil {
			return err
		}

		if _, err = client.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String("compat-importer")}); err == nil {
			return errCompat("protected stack deleted")
		}

		return nil
	})

	sess.Op(svc, "DescribeAccountLimits", func() error {
		out, err := client.DescribeAccountLimits(ctx, &awscfn.DescribeAccountLimitsInput{})
		if err != nil {
			return err
		}

		if len(out.AccountLimits) != 3 {
			return errCompat("expected 3 limits")
		}

		return nil
	})

	sess.Op(svc, "EstimateTemplateCost", func() error {
		out, err := client.EstimateTemplateCost(ctx, &awscfn.EstimateTemplateCostInput{TemplateBody: aws.String(exporterTemplate)})
		if err != nil {
			return err
		}

		if aws.ToString(out.Url) == "" {
			return errCompat("empty url")
		}

		return nil
	})
}

const exporterTemplate = `{
  "Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"compat-exported"}}},
  "Outputs":{"Name":{"Value":{"Ref":"Bucket"},"Export":{"Name":"compat-export"}}}
}`

const importerTemplate = `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{
  "BucketName":{"Fn::Join":["-",[{"Fn::ImportValue":"compat-export"},"copy"]]}
}}}}`

const changeSetTemplate = `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"compat-cs-bucket"}}}}`

// continueUpdateRollback drives a stack into UPDATE_ROLLBACK_FAILED, where
// Parameter Store refuses to move a parameter back from the Advanced tier,
// then continues the rollback skipping it.
func continueUpdateRollback(ctx context.Context, client *awscfn.Client) error {
	const name = "compat-rollback"

	if _, err := client.CreateStack(ctx, &awscfn.CreateStackInput{
		StackName: aws.String(name), TemplateBody: aws.String(rollbackTemplate),
	}); err != nil {
		return err
	}

	if _, err := client.UpdateStack(ctx, &awscfn.UpdateStackInput{
		StackName: aws.String(name), TemplateBody: aws.String(rollbackTemplateFailing),
	}); err != nil {
		return err
	}

	if _, err := client.ContinueUpdateRollback(ctx, &awscfn.ContinueUpdateRollbackInput{
		StackName: aws.String(name), ResourcesToSkip: []string{"Old"},
	}); err != nil {
		return err
	}

	out, err := client.DescribeStacks(ctx, &awscfn.DescribeStacksInput{StackName: aws.String(name)})
	if err != nil {
		return err
	}

	if out.Stacks[0].StackStatus != "UPDATE_ROLLBACK_COMPLETE" {
		return errCompat("status " + string(out.Stacks[0].StackStatus))
	}

	return nil
}

const rollbackTemplate = `{"Resources":{
  "Old":{"Type":"AWS::SSM::Parameter","Properties":{"Name":"/compat/p","Type":"String","Value":"v","Tier":"Standard"}}
}}`

const rollbackTemplateFailing = `{"Resources":{
  "Old":{"Type":"AWS::SSM::Parameter","Properties":{"Name":"/compat/p","Type":"String","Value":"v","Tier":"Advanced"}},
  "Bad":{"Type":"AWS::Unknown::Thing","DependsOn":"Old"}
}}`

type errCompat string

func (e errCompat) Error() string { return string(e) }

const compatTemplate = `{
  "Resources":{
    "Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"compat-bucket"}},
    "Table":{
      "Type":"AWS::DynamoDB::Table",
      "Properties":{
        "TableName":"compat-table",
        "BillingMode":"PAY_PER_REQUEST",
        "AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],
        "KeySchema":[{"AttributeName":"id","KeyType":"HASH"}]
      }
    }
  },
  "Outputs":{"BucketArn":{"Value":{"Fn::GetAtt":["Bucket","Arn"]}}}
}`

const compatTemplateUpdated = `{
  "Resources":{
    "Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"compat-bucket"}},
    "Table":{
      "Type":"AWS::DynamoDB::Table",
      "Properties":{
        "TableName":"compat-table",
        "BillingMode":"PAY_PER_REQUEST",
        "AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],
        "KeySchema":[{"AttributeName":"id","KeyType":"HASH"}]
      }
    },
    "Queue":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"compat-queue"}}
  }
}`
