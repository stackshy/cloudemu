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

	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func TestGetPublicKeyRSASignVerify(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{
		KeySpec: driver.SpecRSA2048, KeyUsage: driver.UsageSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	out, err := m.GetPublicKey(ctx, driver.GetPublicKeyInput{KeyID: k.KeyID})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	pub, err := x509.ParsePKIXPublicKey(out.PublicKey)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T, want *rsa.PublicKey", pub)
	}

	if out.KeySpec != driver.SpecRSA2048 || out.KeyUsage != driver.UsageSignVerify {
		t.Fatalf("spec/usage = %s/%s", out.KeySpec, out.KeyUsage)
	}

	if len(out.SigningAlgorithms) != 6 || len(out.EncryptionAlgorithms) != 0 {
		t.Fatalf("SigningAlgorithms=%v EncryptionAlgorithms=%v", out.SigningAlgorithms, out.EncryptionAlgorithms)
	}

	// A signature produced by KMS Sign verifies offline against the returned key.
	msg := []byte("verify me offline")
	digest := sha256.Sum256(msg)

	sig, err := m.Sign(ctx, driver.SignInput{
		KeyID: k.KeyID, Message: msg, MessageType: driver.MessageTypeRaw,
		SigningAlgorithm: driver.SignRSASSAPSSSHA256,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := rsa.VerifyPSS(rsaPub, crypto.SHA256, digest[:], sig.Signature, nil); err != nil {
		t.Fatalf("offline VerifyPSS: %v", err)
	}
}

func TestGetPublicKeyRSAEncryptDecrypt(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{
		KeySpec: driver.SpecRSA2048, KeyUsage: driver.UsageEncryptDecrypt,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	out, err := m.GetPublicKey(ctx, driver.GetPublicKeyInput{KeyID: k.KeyID})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	if len(out.EncryptionAlgorithms) != 2 || len(out.SigningAlgorithms) != 0 {
		t.Fatalf("EncryptionAlgorithms=%v SigningAlgorithms=%v", out.EncryptionAlgorithms, out.SigningAlgorithms)
	}

	pub, err := x509.ParsePKIXPublicKey(out.PublicKey)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T, want *rsa.PublicKey", pub)
	}

	// Offline RSA-OAEP-SHA-256 encrypt with the downloaded public key, then KMS
	// Decrypt (with the KeyId) must recover the plaintext.
	plaintext := []byte("hybrid crypto payload")

	ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, rsaPub, plaintext, nil)
	if err != nil {
		t.Fatalf("offline EncryptOAEP: %v", err)
	}

	dec, err := m.Decrypt(ctx, driver.DecryptInput{KeyID: k.KeyID, CiphertextBlob: ct})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if string(dec.Plaintext) != string(plaintext) {
		t.Fatalf("Decrypt = %q, want %q", dec.Plaintext, plaintext)
	}
}

func TestGetPublicKeyECC(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{
		KeySpec: driver.SpecECCNISTP256, KeyUsage: driver.UsageSignVerify,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	out, err := m.GetPublicKey(ctx, driver.GetPublicKeyInput{KeyID: k.KeyID})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	pub, err := x509.ParsePKIXPublicKey(out.PublicKey)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	if _, ok := pub.(*ecdsa.PublicKey); !ok {
		t.Fatalf("public key type = %T, want *ecdsa.PublicKey", pub)
	}

	if out.KeySpec != driver.SpecECCNISTP256 {
		t.Fatalf("KeySpec = %s, want ECC_NIST_P256", out.KeySpec)
	}

	if len(out.SigningAlgorithms) != 1 || out.SigningAlgorithms[0] != driver.SignECDSASHA256 {
		t.Fatalf("SigningAlgorithms = %v, want [ECDSA_SHA_256]", out.SigningAlgorithms)
	}
}

func TestGetPublicKeySymmetricRejected(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, _ := m.CreateKey(ctx, driver.CreateKeyInput{})

	_, err := m.GetPublicKey(ctx, driver.GetPublicKeyInput{KeyID: k.KeyID})
	if err == nil {
		t.Fatal("GetPublicKey on a symmetric key should fail")
	}

	if !errors.Is(err, driver.ErrUnsupportedOperation) {
		t.Fatalf("GetPublicKey(symmetric) = %v, want ErrUnsupportedOperation", err)
	}
}
