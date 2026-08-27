package kms_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Encrypt rejects a plaintext larger than the 4096-byte KMS limit with
// ValidationException (HTTP 400); a 4096-byte payload still succeeds.
func TestSDKEncryptPlaintextLimit(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)

	_, err = c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: aws.String(keyID), Plaintext: bytes.Repeat([]byte("x"), 4097),
	})
	if err == nil {
		t.Fatal("Encrypt of 4097 bytes should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("want ValidationException, got %v", err)
	}

	var respErr *smithyhttp.ResponseError
	if !errors.As(err, &respErr) || respErr.HTTPStatusCode() != 400 {
		t.Fatalf("want HTTP 400, got %v", err)
	}

	// The documented maximum (4096 bytes) must still encrypt successfully.
	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: aws.String(keyID), Plaintext: bytes.Repeat([]byte("x"), 4096),
	})
	if err != nil {
		t.Fatalf("Encrypt of 4096 bytes should succeed: %v", err)
	}

	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if len(dec.Plaintext) != 4096 {
		t.Fatalf("roundtrip length = %d, want 4096", len(dec.Plaintext))
	}
}

// CreateKey rejects an unrecognized KeySpec with ValidationException (HTTP 400)
// and creates no key, instead of producing a key with no usable material.
func TestSDKCreateKeyRejectsUnknownSpec(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	before, err := c.ListKeys(ctx, &awskms.ListKeysInput{})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	_, err = c.CreateKey(ctx, &awskms.CreateKeyInput{KeySpec: kmstypes.KeySpec("BOGUS_SPEC")})
	if err == nil {
		t.Fatal("CreateKey with an unknown KeySpec should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("want ValidationException, got %v", err)
	}

	var respErr *smithyhttp.ResponseError
	if !errors.As(err, &respErr) || respErr.HTTPStatusCode() != 400 {
		t.Fatalf("want HTTP 400, got %v", err)
	}

	after, err := c.ListKeys(ctx, &awskms.ListKeysInput{})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	if len(after.Keys) != len(before.Keys) {
		t.Fatalf("no key should be created: before=%d after=%d", len(before.Keys), len(after.Keys))
	}
}

// CreateKey accepts the standard HMAC_224 KeySpec (a real AWS value) and the
// resulting key produces a usable HMAC-SHA-224 MAC.
func TestSDKCreateKeyHMAC224(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecHmac224, KeyUsage: kmstypes.KeyUsageTypeGenerateVerifyMac,
	})
	if err != nil {
		t.Fatalf("CreateKey HMAC_224 should succeed: %v", err)
	}

	if key.KeyMetadata.KeySpec != kmstypes.KeySpecHmac224 {
		t.Fatalf("KeySpec = %q, want HMAC_224", key.KeyMetadata.KeySpec)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)
	msg := []byte("authenticate me")

	mac, err := c.GenerateMac(ctx, &awskms.GenerateMacInput{
		KeyId: aws.String(keyID), Message: msg, MacAlgorithm: kmstypes.MacAlgorithmSpecHmacSha224,
	})
	if err != nil {
		t.Fatalf("GenerateMac: %v", err)
	}

	ver, err := c.VerifyMac(ctx, &awskms.VerifyMacInput{
		KeyId: aws.String(keyID), Message: msg, Mac: mac.Mac,
		MacAlgorithm: kmstypes.MacAlgorithmSpecHmacSha224,
	})
	if err != nil {
		t.Fatalf("VerifyMac: %v", err)
	}

	if !ver.MacValid {
		t.Fatal("MAC should be valid")
	}
}
