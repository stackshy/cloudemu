package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// These tests reproduce the read-back / routing / error-shape gaps that made
// `terraform apply/plan/destroy` against the hashicorp/aws provider fail. They
// drive the real aws-sdk-go-v2 clients (the SDK Terraform uses) against the
// in-process wire server. They intentionally do NOT record compat-matrix cells
// (no sess.Op) — they assert the exact shapes the provider depends on.

// TestTerraformEC2InstanceAttributes covers the aws_instance apply blocker: the
// provider reads DescribeInstanceAttribute for instanceInitiatedShutdownBehavior,
// disableApiStop and disableApiTermination, which previously errored with
// InvalidParameterValue and aborted the apply.
func TestTerraformEC2InstanceAttributes(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{EC2: provider.EC2})

	client := awsec2.NewFromConfig(sess.Config(), func(o *awsec2.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	run, err := client.RunInstances(ctx, &awsec2.RunInstancesInput{
		ImageId:      aws.String("ami-12345678"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	id := aws.ToString(run.Instances[0].InstanceId)

	// instanceInitiatedShutdownBehavior defaults to "stop".
	shutdown, err := client.DescribeInstanceAttribute(ctx, &awsec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameInstanceInitiatedShutdownBehavior,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute(instanceInitiatedShutdownBehavior): %v", err)
	}

	if shutdown.InstanceInitiatedShutdownBehavior == nil ||
		aws.ToString(shutdown.InstanceInitiatedShutdownBehavior.Value) != "stop" {
		t.Fatalf("instanceInitiatedShutdownBehavior = %+v, want stop", shutdown.InstanceInitiatedShutdownBehavior)
	}

	// disableApiStop defaults to false.
	stop, err := client.DescribeInstanceAttribute(ctx, &awsec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameDisableApiStop,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute(disableApiStop): %v", err)
	}

	if stop.DisableApiStop == nil || aws.ToBool(stop.DisableApiStop.Value) {
		t.Fatalf("disableApiStop = %+v, want false", stop.DisableApiStop)
	}

	// disableApiTermination defaults to false.
	term, err := client.DescribeInstanceAttribute(ctx, &awsec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameDisableApiTermination,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute(disableApiTermination): %v", err)
	}

	if term.DisableApiTermination == nil || aws.ToBool(term.DisableApiTermination.Value) {
		t.Fatalf("disableApiTermination = %+v, want false", term.DisableApiTermination)
	}

	// A ModifyInstanceAttribute round-trips through DescribeInstanceAttribute.
	if _, err := client.ModifyInstanceAttribute(ctx, &awsec2.ModifyInstanceAttributeInput{
		InstanceId:                        aws.String(id),
		InstanceInitiatedShutdownBehavior: &ec2types.AttributeValue{Value: aws.String("terminate")},
	}); err != nil {
		t.Fatalf("ModifyInstanceAttribute(instanceInitiatedShutdownBehavior): %v", err)
	}

	got, err := client.DescribeInstanceAttribute(ctx, &awsec2.DescribeInstanceAttributeInput{
		InstanceId: aws.String(id),
		Attribute:  ec2types.InstanceAttributeNameInstanceInitiatedShutdownBehavior,
	})
	if err != nil {
		t.Fatalf("DescribeInstanceAttribute after modify: %v", err)
	}

	if aws.ToString(got.InstanceInitiatedShutdownBehavior.Value) != "terminate" {
		t.Fatalf("instanceInitiatedShutdownBehavior after modify = %q, want terminate",
			aws.ToString(got.InstanceInitiatedShutdownBehavior.Value))
	}
}

// TestTerraformLambdaCodeSigningConfig covers the aws_lambda_function apply
// blocker: the provider reads GetFunctionCodeSigningConfig on every function
// refresh. The request previously fell through to the S3 catch-all and returned
// XML the REST-JSON client could not parse.
func TestTerraformLambdaCodeSigningConfig(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{Lambda: provider.Lambda})

	client := awslambda.NewFromConfig(sess.Config(), func(o *awslambda.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const fnName = "tf-fn"

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String(fnName),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("fake-zip")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	out, err := client.GetFunctionCodeSigningConfig(ctx, &awslambda.GetFunctionCodeSigningConfigInput{
		FunctionName: aws.String(fnName),
	})
	if err != nil {
		t.Fatalf("GetFunctionCodeSigningConfig: %v", err)
	}

	if aws.ToString(out.FunctionName) != fnName {
		t.Fatalf("FunctionName = %q, want %q", aws.ToString(out.FunctionName), fnName)
	}

	if aws.ToString(out.CodeSigningConfigArn) != "" {
		t.Fatalf("CodeSigningConfigArn = %q, want empty (no config)", aws.ToString(out.CodeSigningConfigArn))
	}

	// A missing function still 404s (typed ResourceNotFoundException).
	_, err = client.GetFunctionCodeSigningConfig(ctx, &awslambda.GetFunctionCodeSigningConfigInput{
		FunctionName: aws.String("nope"),
	})

	var rnf *lambdatypes.ResourceNotFoundException
	if !errors.As(err, &rnf) {
		t.Fatalf("GetFunctionCodeSigningConfig(missing) error = %v, want ResourceNotFoundException", err)
	}
}

// TestTerraformDynamoDBPPRGSIThroughput covers the PAY_PER_REQUEST + GSI
// perpetual-drift blocker: each GSI must read back with its IndexName and a
// ProvisionedThroughput block of zeros, or the provider blanks the index and
// re-plans it forever.
func TestTerraformDynamoDBPPRGSIThroughput(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{DynamoDB: provider.DynamoDB})

	client := awsdynamodb.NewFromConfig(sess.Config(), func(o *awsdynamodb.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	const (
		tableName = "tf-ppr"
		indexName = "gsi1"
	)

	if _, err := client.CreateTable(ctx, &awsdynamodb.CreateTableInput{
		TableName:   aws.String(tableName),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sort"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{{
			IndexName: aws.String(indexName),
			KeySchema: []ddbtypes.KeySchemaElement{
				{AttributeName: aws.String("sort"), KeyType: ddbtypes.KeyTypeHash},
			},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
		}},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	desc, err := client.DescribeTable(ctx, &awsdynamodb.DescribeTableInput{TableName: aws.String(tableName)})
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}

	gsis := desc.Table.GlobalSecondaryIndexes
	if len(gsis) != 1 {
		t.Fatalf("GlobalSecondaryIndexes = %d, want 1", len(gsis))
	}

	if aws.ToString(gsis[0].IndexName) != indexName {
		t.Fatalf("GSI IndexName = %q, want %q", aws.ToString(gsis[0].IndexName), indexName)
	}

	pt := gsis[0].ProvisionedThroughput
	if pt == nil {
		t.Fatal("GSI ProvisionedThroughput is nil; TF blanks the index and re-plans forever")
	}

	if aws.ToInt64(pt.ReadCapacityUnits) != 0 || aws.ToInt64(pt.WriteCapacityUnits) != 0 {
		t.Fatalf("GSI ProvisionedThroughput = r%d/w%d, want 0/0 for PAY_PER_REQUEST",
			aws.ToInt64(pt.ReadCapacityUnits), aws.ToInt64(pt.WriteCapacityUnits))
	}
}

// TestTerraformSQSNonExistentQueue covers the destroy blocker: the provider's
// delete waiter polls GetQueueAttributes on the deleted queue and must see the
// typed QueueDoesNotExist error to treat the queue as gone. That typed error only
// deserializes when the JSON __type is the modeled shape name "QueueDoesNotExist"
// (not the legacy query code), so this locks that shape in.
func TestTerraformSQSNonExistentQueue(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{SQS: provider.SQS})

	client := awssqs.NewFromConfig(sess.Config(), func(o *awssqs.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})
	ctx := context.Background()

	created, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("tf-doomed")})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := client.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: created.QueueUrl}); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	_, err = client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       created.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err == nil {
		t.Fatal("GetQueueAttributes on deleted queue returned nil error, want QueueDoesNotExist")
	}

	var qdne *sqstypes.QueueDoesNotExist
	if !errors.As(err, &qdne) {
		t.Fatalf("GetQueueAttributes error = %v, want typed *QueueDoesNotExist", err)
	}
}
