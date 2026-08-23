package kms_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKDecryptMalformedCiphertext guards the fix mapping a malformed
// CiphertextBlob to InvalidCiphertextException rather than ValidationException.
func TestSDKDecryptMalformedCiphertext(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	_, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: []byte{0x01}})

	var invalid *kmstypes.InvalidCiphertextException
	if !errors.As(err, &invalid) {
		t.Fatalf("Decrypt(malformed): got %v, want InvalidCiphertextException", err)
	}
}

// TestSDKDescribeKeyEncryptionAlgorithms guards a symmetric key advertising
// EncryptionAlgorithms=[SYMMETRIC_DEFAULT].
func TestSDKDescribeKeyEncryptionAlgorithms(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	// CreateKey response itself carries the algorithms.
	if got := key.KeyMetadata.EncryptionAlgorithms; len(got) != 1 || got[0] != kmstypes.EncryptionAlgorithmSpecSymmetricDefault {
		t.Fatalf("CreateKey EncryptionAlgorithms = %v, want [SYMMETRIC_DEFAULT]", got)
	}

	desc, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: key.KeyMetadata.KeyId})
	if err != nil {
		t.Fatalf("DescribeKey: %v", err)
	}

	if got := desc.KeyMetadata.EncryptionAlgorithms; len(got) != 1 || got[0] != kmstypes.EncryptionAlgorithmSpecSymmetricDefault {
		t.Fatalf("DescribeKey EncryptionAlgorithms = %v, want [SYMMETRIC_DEFAULT]", got)
	}
}

// TestSDKListKeysPagination guards Limit/Marker being honored: Truncated + a
// NextMarker on the first page, and the rest delivered on the second.
func TestSDKListKeysPagination(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	const total = 3
	for i := 0; i < total; i++ {
		if _, err := c.CreateKey(ctx, &awskms.CreateKeyInput{}); err != nil {
			t.Fatalf("CreateKey: %v", err)
		}
	}

	first, err := c.ListKeys(ctx, &awskms.ListKeysInput{Limit: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListKeys(page1): %v", err)
	}

	if len(first.Keys) != 2 {
		t.Fatalf("page1 = %d keys, want 2", len(first.Keys))
	}

	if !first.Truncated || aws.ToString(first.NextMarker) == "" {
		t.Fatalf("page1 Truncated=%v NextMarker=%q, want truncated with a marker", first.Truncated, aws.ToString(first.NextMarker))
	}

	second, err := c.ListKeys(ctx, &awskms.ListKeysInput{Limit: aws.Int32(2), Marker: first.NextMarker})
	if err != nil {
		t.Fatalf("ListKeys(page2): %v", err)
	}

	if len(second.Keys) != 1 {
		t.Fatalf("page2 = %d keys, want 1", len(second.Keys))
	}

	if second.Truncated {
		t.Fatalf("page2 Truncated=%v, want false", second.Truncated)
	}
}
