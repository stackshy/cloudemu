package sfn_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

// TestSDKEncryptionConfigRoundTrip verifies a customer-managed-key
// encryptionConfiguration set at create time survives DescribeStateMachine.
// Real Step Functions echoes encryptionConfiguration back; dropping it makes
// Terraform's aws_sfn_state_machine drift perpetually.
func TestSDKEncryptionConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)

	out, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name:       aws.String("enc"),
		Definition: aws.String(definition),
		RoleArn:    aws.String("arn:aws:iam::123456789012:role/svc"),
		EncryptionConfiguration: &sfntypes.EncryptionConfiguration{
			Type:                         sfntypes.EncryptionTypeCustomerManagedKmsKey,
			KmsKeyId:                     aws.String("arn:aws:kms:us-east-1:123456789012:key/abc"),
			KmsDataKeyReusePeriodSeconds: aws.Int32(300),
		},
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}

	desc, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
		StateMachineArn: out.StateMachineArn,
	})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}

	ec := desc.EncryptionConfiguration
	if ec == nil {
		t.Fatal("encryptionConfiguration missing from DescribeStateMachine")
	}

	if ec.Type != sfntypes.EncryptionTypeCustomerManagedKmsKey {
		t.Fatalf("type = %s, want CUSTOMER_MANAGED_KMS_KEY", ec.Type)
	}

	if aws.ToString(ec.KmsKeyId) != "arn:aws:kms:us-east-1:123456789012:key/abc" {
		t.Fatalf("kmsKeyId = %q", aws.ToString(ec.KmsKeyId))
	}

	if aws.ToInt32(ec.KmsDataKeyReusePeriodSeconds) != 300 {
		t.Fatalf("kmsDataKeyReusePeriodSeconds = %d, want 300", aws.ToInt32(ec.KmsDataKeyReusePeriodSeconds))
	}
}

// TestSDKEncryptionConfigDefault verifies a state machine created without an
// encryptionConfiguration reports the AWS_OWNED_KEY default on describe, as real
// Step Functions does.
func TestSDKEncryptionConfigDefault(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "plain-enc")

	desc, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
		StateMachineArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}

	if desc.EncryptionConfiguration == nil {
		t.Fatal("encryptionConfiguration missing; want AWS_OWNED_KEY default")
	}

	if desc.EncryptionConfiguration.Type != sfntypes.EncryptionTypeAwsOwnedKey {
		t.Fatalf("type = %s, want AWS_OWNED_KEY", desc.EncryptionConfiguration.Type)
	}
}

// TestSDKUpdateEncryptionConfig verifies UpdateStateMachine can change the
// encryptionConfiguration and the change is observable on describe.
func TestSDKUpdateEncryptionConfig(t *testing.T) {
	ctx := context.Background()
	c := newSFNClient(t)
	arn := createSM(t, c, "upd-enc")

	if _, err := c.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: aws.String(arn),
		EncryptionConfiguration: &sfntypes.EncryptionConfiguration{
			Type:                         sfntypes.EncryptionTypeCustomerManagedKmsKey,
			KmsKeyId:                     aws.String("arn:aws:kms:us-east-1:123456789012:key/xyz"),
			KmsDataKeyReusePeriodSeconds: aws.Int32(600),
		},
	}); err != nil {
		t.Fatalf("UpdateStateMachine: %v", err)
	}

	desc, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{
		StateMachineArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("DescribeStateMachine: %v", err)
	}

	if desc.EncryptionConfiguration == nil ||
		desc.EncryptionConfiguration.Type != sfntypes.EncryptionTypeCustomerManagedKmsKey ||
		aws.ToString(desc.EncryptionConfiguration.KmsKeyId) != "arn:aws:kms:us-east-1:123456789012:key/xyz" {
		t.Fatalf("encryptionConfiguration not updated: %+v", desc.EncryptionConfiguration)
	}
}
