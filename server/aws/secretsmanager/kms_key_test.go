package secretsmanager_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// TestCreateSecretKmsKeyIdReflectedOnDescribe drives the real-user flow: create
// a customer KMS key, create a secret encrypted with it, then DescribeSecret and
// confirm the KmsKeyId round-trips (the key->secret reference is visible,
// matching the DescribeSecret KmsKeyId field a tool like Terraform reads back).
func TestCreateSecretKmsKeyIdReflectedOnDescribe(t *testing.T) {
	client, cloud := newSecretsClientWithCloud(t)
	ctx := context.Background()

	// The referenced key must exist: CreateSecret validates KmsKeyId against KMS,
	// as real Secrets Manager does.
	key, err := cloud.KMS.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("encrypted-secret"),
		SecretString: aws.String("hunter2"),
		KmsKeyId:     aws.String(key.ARN),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{
		SecretId: aws.String("encrypted-secret"),
	})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if got := aws.ToString(desc.KmsKeyId); got != key.ARN {
		t.Fatalf("DescribeSecret KmsKeyId = %q, want %q", got, key.ARN)
	}
}

// TestCreateSecretRejectsUnknownKmsKey confirms CreateSecret validates the
// referenced KMS key: a KmsKeyId that names no key is rejected, matching real
// Secrets Manager rather than storing a dangling reference.
func TestCreateSecretRejectsUnknownKmsKey(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	_, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("bad-key-secret"),
		SecretString: aws.String("hunter2"),
		KmsKeyId:     aws.String("arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab"),
	})
	if err == nil {
		t.Fatal("CreateSecret with unknown KmsKeyId returned nil error, want failure")
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
