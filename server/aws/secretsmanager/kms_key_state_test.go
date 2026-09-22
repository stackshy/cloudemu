package secretsmanager_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// TestSDKDisabledKMSKeyIsDecryptionAndEncryptionFailure covers that once a
// secret's KMS key is disabled, reads answer DecryptionFailure and writes
// answer EncryptionFailure — the dedicated Secrets Manager exceptions, not the
// generic InvalidRequestException.
func TestSDKDisabledKMSKeyIsDecryptionAndEncryptionFailure(t *testing.T) {
	client, cloud := newSecretsClientWithCloud(t)
	ctx := context.Background()

	key, err := cloud.KMS.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("kms-state"),
		SecretString: aws.String("v1"),
		KmsKeyId:     aws.String(key.ARN),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if err := cloud.KMS.DisableKey(ctx, key.KeyID); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	_, err = client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("kms-state")})

	var df *smtypes.DecryptionFailure
	if !errors.As(err, &df) {
		t.Fatalf("GetSecretValue with disabled key: err = %v, want DecryptionFailure", err)
	}

	_, err = client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId:     aws.String("kms-state"),
		SecretString: aws.String("v2"),
	})

	var ef *smtypes.EncryptionFailure
	if !errors.As(err, &ef) {
		t.Fatalf("PutSecretValue with disabled key: err = %v, want EncryptionFailure", err)
	}
}
