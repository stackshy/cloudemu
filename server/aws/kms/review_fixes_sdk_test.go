package kms_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// High 1: Sign/GenerateMac must reject a disabled key with DisabledException.
func TestSDKSignOnDisabledKeyRejected(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)
	if _, err := c.DisableKey(ctx, &awskms.DisableKeyInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	_, err = c.Sign(ctx, &awskms.SignInput{
		KeyId: aws.String(keyID), Message: []byte("m"),
		MessageType: kmstypes.MessageTypeRaw, SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err == nil {
		t.Fatal("Sign on disabled key should fail")
	}

	var disabled *kmstypes.DisabledException
	if !errors.As(err, &disabled) {
		t.Fatalf("want DisabledException, got %v", err)
	}
}

// High 2: ciphertext created before an on-demand rotation must still decrypt.
func TestSDKDecryptSurvivesRotation(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	plaintext := []byte("pre-rotation secret")
	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: plaintext})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := c.RotateKeyOnDemand(ctx, &awskms.RotateKeyOnDemandInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("RotateKeyOnDemand: %v", err)
	}

	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	if err != nil {
		t.Fatalf("Decrypt after rotation: %v", err)
	}

	if !bytes.Equal(dec.Plaintext, plaintext) {
		t.Fatal("pre-rotation ciphertext did not decrypt after rotation")
	}

	// And a fresh encrypt (new version) still round-trips too.
	enc2, _ := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("post")})
	dec2, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc2.CiphertextBlob})
	if err != nil || string(dec2.Plaintext) != "post" {
		t.Fatalf("post-rotation roundtrip failed: %v / %q", err, dec2.Plaintext)
	}
}

// Med 5: tampered ciphertext maps to InvalidCiphertextException.
func TestSDKTamperedCiphertextTyped(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	enc, _ := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: key.KeyMetadata.KeyId, Plaintext: []byte("secret"),
	})

	blob := append([]byte(nil), enc.CiphertextBlob...)
	blob[len(blob)-1] ^= 0xFF // flip a ciphertext byte

	_, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: blob})
	if err == nil {
		t.Fatal("tampered ciphertext should fail")
	}

	var badCT *kmstypes.InvalidCiphertextException
	if !errors.As(err, &badCT) {
		t.Fatalf("want InvalidCiphertextException, got %v", err)
	}
}

// Med 4: signing an ECC key with an RSA algorithm is rejected.
func TestSDKSignAlgorithmSpecMismatch(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP256, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})

	_, err := c.Sign(ctx, &awskms.SignInput{
		KeyId: key.KeyMetadata.KeyId, Message: []byte("m"),
		MessageType: kmstypes.MessageTypeRaw, SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err == nil {
		t.Fatal("ECC key with RSASSA algorithm should be rejected")
	}

	var badUsage *kmstypes.InvalidKeyUsageException
	if !errors.As(err, &badUsage) {
		t.Fatalf("want InvalidKeyUsageException, got %v", err)
	}
}

// Med 6: scheduling deletion twice is rejected with KMSInvalidStateException.
func TestSDKDoubleScheduleDeletionRejected(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	if _, err := c.ScheduleKeyDeletion(ctx, &awskms.ScheduleKeyDeletionInput{
		KeyId: aws.String(keyID), PendingWindowInDays: aws.Int32(7),
	}); err != nil {
		t.Fatalf("first ScheduleKeyDeletion: %v", err)
	}

	_, err := c.ScheduleKeyDeletion(ctx, &awskms.ScheduleKeyDeletionInput{
		KeyId: aws.String(keyID), PendingWindowInDays: aws.Int32(7),
	})
	if err == nil {
		t.Fatal("second ScheduleKeyDeletion should fail")
	}

	var invalidState *kmstypes.KMSInvalidStateException
	if !errors.As(err, &invalidState) {
		t.Fatalf("want KMSInvalidStateException, got %v", err)
	}
}

// e2e audit: EnableKeyRotation on an asymmetric key must reject with
// UnsupportedOperationException, matching real KMS — not a generic
// ValidationException.
func TestSDKEnableRotationOnAsymmetricKeyRejected(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	_, err = c.EnableKeyRotation(ctx, &awskms.EnableKeyRotationInput{KeyId: key.KeyMetadata.KeyId})
	if err == nil {
		t.Fatal("EnableKeyRotation on an asymmetric key should fail")
	}

	var unsupported *kmstypes.UnsupportedOperationException
	if !errors.As(err, &unsupported) {
		t.Fatalf("want UnsupportedOperationException, got %v", err)
	}
}

// e2e audit: RotateKeyOnDemand on an asymmetric key must reject with
// UnsupportedOperationException, matching real KMS.
func TestSDKRotateOnDemandOnAsymmetricKeyRejected(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP256, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	_, err = c.RotateKeyOnDemand(ctx, &awskms.RotateKeyOnDemandInput{KeyId: key.KeyMetadata.KeyId})
	if err == nil {
		t.Fatal("RotateKeyOnDemand on an asymmetric key should fail")
	}

	var unsupported *kmstypes.UnsupportedOperationException
	if !errors.As(err, &unsupported) {
		t.Fatalf("want UnsupportedOperationException, got %v", err)
	}
}

// e2e audit: EnableKeyRotation on a key with imported key material (Origin
// EXTERNAL) must also reject with UnsupportedOperationException.
func TestSDKEnableRotationOnImportedKeyRejected(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{Origin: kmstypes.OriginTypeExternal})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	_, err = c.EnableKeyRotation(ctx, &awskms.EnableKeyRotationInput{KeyId: key.KeyMetadata.KeyId})
	if err == nil {
		t.Fatal("EnableKeyRotation on an imported-material key should fail")
	}

	var unsupported *kmstypes.UnsupportedOperationException
	if !errors.As(err, &unsupported) {
		t.Fatalf("want UnsupportedOperationException, got %v", err)
	}
}

// e2e audit: real KMS key IDs (and ARNs) for multi-Region keys begin with the
// "mrk-" prefix (the rest of the identifier is the UUID with dashes
// removed) — callers commonly branch on this prefix to detect multi-Region
// keys. See https://docs.aws.amazon.com/kms/latest/developerguide/concepts.html#key-id-key-id.
func TestSDKMultiRegionKeyIDHasMRKPrefix(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{MultiRegion: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)
	if !strings.HasPrefix(keyID, "mrk-") {
		t.Fatalf("multi-Region KeyId = %q, want mrk- prefix", keyID)
	}

	if arn := aws.ToString(key.KeyMetadata.Arn); !strings.Contains(arn, "key/mrk-") {
		t.Fatalf("multi-Region key Arn = %q, want key/mrk- segment", arn)
	}

	// A single-Region key must NOT get the prefix.
	single, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey (single-Region): %v", err)
	}

	if strings.HasPrefix(aws.ToString(single.KeyMetadata.KeyId), "mrk-") {
		t.Fatalf("single-Region key unexpectedly has mrk- prefix: %q", aws.ToString(single.KeyMetadata.KeyId))
	}

	// DescribeKey by the mrk- KeyId must still resolve.
	desc, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("DescribeKey by mrk- id: %v", err)
	}

	if aws.ToString(desc.KeyMetadata.KeyId) != keyID {
		t.Fatalf("DescribeKey KeyId = %q, want %q", aws.ToString(desc.KeyMetadata.KeyId), keyID)
	}
}
