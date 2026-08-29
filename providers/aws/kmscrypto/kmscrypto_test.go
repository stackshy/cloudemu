package kmscrypto_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/providers/aws/kmscrypto"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func newEnvelope() (*kmscrypto.Envelope, *kms.Mock) {
	k := kms.New(config.NewOptions(config.WithRegion("us-east-1")))
	return kmscrypto.New(k), k
}

func TestEnvelopeRoundTripWithExplicitKey(t *testing.T) {
	e, k := newEnvelope()
	ctx := context.Background()

	key, err := k.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	plaintext := []byte("secret payload")

	blob, err := e.Encrypt(ctx, key.KeyID, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Equal(blob, plaintext) {
		t.Fatal("Encrypt returned the plaintext")
	}

	got, err := e.Decrypt(ctx, blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Decrypt = %q, want %q", got, plaintext)
	}
}

// TestEnvelopeCreatesManagedKeyOnDemand covers the reserved managed-key alias
// path: a reference KMS cannot resolve gets a key created for it, and the same
// reference reuses that key.
func TestEnvelopeCreatesManagedKeyOnDemand(t *testing.T) {
	e, _ := newEnvelope()
	ctx := context.Background()

	first, err := e.Encrypt(ctx, "alias/aws/secretsmanager", []byte("a"))
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}

	second, err := e.Encrypt(ctx, "alias/aws/secretsmanager", []byte("b"))
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}

	for _, blob := range [][]byte{first, second} {
		if _, err := e.Decrypt(ctx, blob); err != nil {
			t.Fatalf("Decrypt managed-key blob: %v", err)
		}
	}
}

func TestEnvelopeDecryptFailsAfterKeyDisabled(t *testing.T) {
	e, k := newEnvelope()
	ctx := context.Background()

	key, err := k.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	blob, err := e.Encrypt(ctx, key.KeyID, []byte("v"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if err := k.DisableKey(ctx, key.KeyID); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	if _, err := e.Decrypt(ctx, blob); err == nil {
		t.Fatal("Decrypt after DisableKey: want error, got nil")
	}
}

func TestEnvelopeDecryptRejectsGarbage(t *testing.T) {
	e, _ := newEnvelope()

	if _, err := e.Decrypt(context.Background(), []byte("not a blob")); err == nil {
		t.Fatal("Decrypt(garbage): want error, got nil")
	}
}
