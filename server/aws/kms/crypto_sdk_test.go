package kms_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func TestSDKSymmetricEncryptDecrypt(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	plaintext := []byte("top secret payload")
	encCtx := map[string]string{"purpose": "test", "app": "cloudemu"}

	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{
		KeyId: aws.String(keyID), Plaintext: plaintext, EncryptionContext: encCtx,
	})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Decrypt without KeyId (symmetric blob is self-describing) but with the
	// same encryption context.
	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{
		CiphertextBlob: enc.CiphertextBlob, EncryptionContext: encCtx,
	})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(dec.Plaintext, plaintext) {
		t.Fatalf("roundtrip mismatch: got %q want %q", dec.Plaintext, plaintext)
	}

	if aws.ToString(dec.KeyId) != keyID {
		t.Fatalf("Decrypt KeyId = %s, want %s", aws.ToString(dec.KeyId), keyID)
	}

	// Wrong encryption context must fail (AEAD binding).
	if _, err := c.Decrypt(ctx, &awskms.DecryptInput{
		CiphertextBlob: enc.CiphertextBlob, EncryptionContext: map[string]string{"purpose": "wrong"},
	}); err == nil {
		t.Fatal("Decrypt with wrong encryption context should fail")
	}
}

func TestSDKGenerateDataKeyAndRandom(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	dk, err := c.GenerateDataKey(ctx, &awskms.GenerateDataKeyInput{
		KeyId: aws.String(keyID), KeySpec: kmstypes.DataKeySpecAes256,
	})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}

	if len(dk.Plaintext) != 32 {
		t.Fatalf("AES_256 data key plaintext = %d bytes, want 32", len(dk.Plaintext))
	}

	// The ciphertext blob decrypts back to the same plaintext data key.
	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: dk.CiphertextBlob})
	if err != nil {
		t.Fatalf("Decrypt data key: %v", err)
	}

	if !bytes.Equal(dec.Plaintext, dk.Plaintext) {
		t.Fatal("data key ciphertext did not decrypt to the plaintext data key")
	}

	rnd, err := c.GenerateRandom(ctx, &awskms.GenerateRandomInput{NumberOfBytes: aws.Int32(24)})
	if err != nil {
		t.Fatalf("GenerateRandom: %v", err)
	}

	if len(rnd.Plaintext) != 24 {
		t.Fatalf("GenerateRandom = %d bytes, want 24", len(rnd.Plaintext))
	}
}

func TestSDKSignVerifyRSA(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey RSA: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)
	msg := []byte("message to sign")

	sig, err := c.Sign(ctx, &awskms.SignInput{
		KeyId: aws.String(keyID), Message: msg,
		MessageType: kmstypes.MessageTypeRaw, SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	ver, err := c.Verify(ctx, &awskms.VerifyInput{
		KeyId: aws.String(keyID), Message: msg, Signature: sig.Signature,
		MessageType: kmstypes.MessageTypeRaw, SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !ver.SignatureValid {
		t.Fatal("signature should be valid")
	}

	// Tampered message must be rejected with KMSInvalidSignatureException.
	_, err = c.Verify(ctx, &awskms.VerifyInput{
		KeyId: aws.String(keyID), Message: []byte("tampered"), Signature: sig.Signature,
		MessageType: kmstypes.MessageTypeRaw, SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err == nil {
		t.Fatal("Verify of tampered message should fail")
	}

	var invalid *kmstypes.KMSInvalidSignatureException
	if !errors.As(err, &invalid) {
		t.Fatalf("want KMSInvalidSignatureException, got %v", err)
	}
}

func TestSDKGenerateAndVerifyMac(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecHmac256, KeyUsage: kmstypes.KeyUsageTypeGenerateVerifyMac,
	})
	if err != nil {
		t.Fatalf("CreateKey HMAC: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)
	msg := []byte("authenticate me")

	mac, err := c.GenerateMac(ctx, &awskms.GenerateMacInput{
		KeyId: aws.String(keyID), Message: msg, MacAlgorithm: kmstypes.MacAlgorithmSpecHmacSha256,
	})
	if err != nil {
		t.Fatalf("GenerateMac: %v", err)
	}

	ver, err := c.VerifyMac(ctx, &awskms.VerifyMacInput{
		KeyId: aws.String(keyID), Message: msg, Mac: mac.Mac,
		MacAlgorithm: kmstypes.MacAlgorithmSpecHmacSha256,
	})
	if err != nil {
		t.Fatalf("VerifyMac: %v", err)
	}

	if !ver.MacValid {
		t.Fatal("MAC should be valid")
	}
}

func TestSDKGenerateDataKeyPair(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)

	pair, err := c.GenerateDataKeyPair(ctx, &awskms.GenerateDataKeyPairInput{
		KeyId: aws.String(keyID), KeyPairSpec: kmstypes.DataKeyPairSpecRsa2048,
	})
	if err != nil {
		t.Fatalf("GenerateDataKeyPair: %v", err)
	}

	if len(pair.PublicKey) == 0 || len(pair.PrivateKeyPlaintext) == 0 || len(pair.PrivateKeyCiphertextBlob) == 0 {
		t.Fatalf("empty key pair fields: %+v", pair)
	}

	// The encrypted private key decrypts back to the plaintext private key.
	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: pair.PrivateKeyCiphertextBlob})
	if err != nil {
		t.Fatalf("Decrypt private key: %v", err)
	}

	if !bytes.Equal(dec.Plaintext, pair.PrivateKeyPlaintext) {
		t.Fatal("encrypted private key did not decrypt to the plaintext private key")
	}
}
