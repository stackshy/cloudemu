package secretsmanager_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// TestCreateSecretKmsKeyIdReflectedOnDescribe drives the real-user flow: create
// a secret encrypted with a customer KMS key, then DescribeSecret and confirm
// the KmsKeyId round-trips (the key->secret reference is visible, matching the
// DescribeSecret KmsKeyId field a tool like Terraform reads back).
func TestCreateSecretKmsKeyIdReflectedOnDescribe(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	const keyARN = "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab"

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("encrypted-secret"),
		SecretString: aws.String("hunter2"),
		KmsKeyId:     aws.String(keyARN),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{
		SecretId: aws.String("encrypted-secret"),
	})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if got := aws.ToString(desc.KmsKeyId); got != keyARN {
		t.Fatalf("DescribeSecret KmsKeyId = %q, want %q", got, keyARN)
	}
}

// TestCreateSecretDefaultKeyOmitsKmsKeyId confirms a secret created without a
// customer key reports no KmsKeyId (real Secrets Manager omits it when the
// default aws/secretsmanager key is used).
func TestCreateSecretDefaultKeyOmitsKmsKeyId(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("default-key-secret"),
		SecretString: aws.String("hunter2"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{
		SecretId: aws.String("default-key-secret"),
	})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if got := aws.ToString(desc.KmsKeyId); got != "" {
		t.Fatalf("DescribeSecret KmsKeyId = %q, want empty", got)
	}
}
