package kms_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKDisabledExceptionMessage checks that DisabledException reaches the
// client with the message real KMS sends, "<key ARN> is disabled.".
func TestSDKDisabledExceptionMessage(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if _, err := c.DisableKey(ctx, &awskms.DisableKeyInput{KeyId: key.KeyMetadata.KeyId}); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	_, err = c.Encrypt(ctx, &awskms.EncryptInput{KeyId: key.KeyMetadata.KeyId, Plaintext: []byte("x")})

	var disabled *kmstypes.DisabledException
	if !errors.As(err, &disabled) {
		t.Fatalf("want DisabledException, got %v", err)
	}

	want := aws.ToString(key.KeyMetadata.Arn) + " is disabled."
	if got := disabled.ErrorMessage(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}
