package kms_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKGetPublicKeyRSASignVerify: a real client downloads the public key of a
// SIGN_VERIFY RSA key, parses the DER, and offline-verifies a KMS signature.
func TestSDKGetPublicKeyRSASignVerify(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)

	pubOut, err := c.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	if pubOut.KeySpec != kmstypes.KeySpecRsa2048 || pubOut.KeyUsage != kmstypes.KeyUsageTypeSignVerify {
		t.Fatalf("spec/usage = %s/%s", pubOut.KeySpec, pubOut.KeyUsage)
	}

	if len(pubOut.SigningAlgorithms) != 6 {
		t.Fatalf("SigningAlgorithms = %v, want 6", pubOut.SigningAlgorithms)
	}

	if len(pubOut.EncryptionAlgorithms) != 0 {
		t.Fatalf("EncryptionAlgorithms = %v, want none for SIGN_VERIFY", pubOut.EncryptionAlgorithms)
	}

	parsed, err := x509.ParsePKIXPublicKey(pubOut.PublicKey)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	rsaPub, ok := parsed.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T, want *rsa.PublicKey", parsed)
	}

	// KMS Sign, then verify the signature offline against the downloaded key.
	msg := []byte("sign this with kms, verify offline")

	sig, err := c.Sign(ctx, &awskms.SignInput{
		KeyId: aws.String(keyID), Message: msg,
		MessageType: kmstypes.MessageTypeRaw, SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPssSha256,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	digest := sha256.Sum256(msg)
	if err := rsa.VerifyPSS(rsaPub, crypto.SHA256, digest[:], sig.Signature, nil); err != nil {
		t.Fatalf("offline VerifyPSS against downloaded public key: %v", err)
	}
}

// TestSDKGetPublicKeyRSAEncryptDecrypt: download the public key of an
// ENCRYPT_DECRYPT RSA key, encrypt offline, and recover via KMS Decrypt.
func TestSDKGetPublicKeyRSAEncryptDecrypt(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecRsa2048, KeyUsage: kmstypes.KeyUsageTypeEncryptDecrypt,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)

	pubOut, err := c.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	if len(pubOut.EncryptionAlgorithms) != 2 || len(pubOut.SigningAlgorithms) != 0 {
		t.Fatalf("EncryptionAlgorithms=%v SigningAlgorithms=%v", pubOut.EncryptionAlgorithms, pubOut.SigningAlgorithms)
	}

	parsed, err := x509.ParsePKIXPublicKey(pubOut.PublicKey)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	rsaPub := parsed.(*rsa.PublicKey)
	plaintext := []byte("encrypt me offline")

	ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, rsaPub, plaintext, nil)
	if err != nil {
		t.Fatalf("offline EncryptOAEP: %v", err)
	}

	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{
		KeyId: aws.String(keyID), CiphertextBlob: ct,
		EncryptionAlgorithm: kmstypes.EncryptionAlgorithmSpecRsaesOaepSha256,
	})
	if err != nil {
		t.Fatalf("Decrypt offline ciphertext: %v", err)
	}

	if string(dec.Plaintext) != string(plaintext) {
		t.Fatalf("Decrypt = %q, want %q", dec.Plaintext, plaintext)
	}
}

// TestSDKGetPublicKeyECC: an ECC key returns an *ecdsa.PublicKey and the
// spec-matched single signing algorithm.
func TestSDKGetPublicKeyECC(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP384, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	pubOut, err := c.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: key.KeyMetadata.KeyId})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	if pubOut.KeySpec != kmstypes.KeySpecEccNistP384 {
		t.Fatalf("KeySpec = %s, want ECC_NIST_P384", pubOut.KeySpec)
	}

	if len(pubOut.SigningAlgorithms) != 1 || pubOut.SigningAlgorithms[0] != kmstypes.SigningAlgorithmSpecEcdsaSha384 {
		t.Fatalf("SigningAlgorithms = %v, want [ECDSA_SHA_384]", pubOut.SigningAlgorithms)
	}

	parsed, err := x509.ParsePKIXPublicKey(pubOut.PublicKey)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	if _, ok := parsed.(*ecdsa.PublicKey); !ok {
		t.Fatalf("public key type = %T, want *ecdsa.PublicKey", parsed)
	}
}

// TestSDKGetPublicKeySymmetricRejected: GetPublicKey on a symmetric key returns
// UnsupportedOperationException.
func TestSDKGetPublicKeySymmetricRejected(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	_, err = c.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: key.KeyMetadata.KeyId})
	if err == nil {
		t.Fatal("GetPublicKey on a symmetric key should fail")
	}

	var unsupported *kmstypes.UnsupportedOperationException
	if !errors.As(err, &unsupported) {
		t.Fatalf("GetPublicKey(symmetric) = %v, want UnsupportedOperationException", err)
	}
}
