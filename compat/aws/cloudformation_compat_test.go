package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"

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

	sess.Op(svc, "DeleteStack", func() error {
		_, err := client.DeleteStack(ctx, &awscfn.DeleteStackInput{StackName: aws.String(stack)})
		return err
	})
}

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
