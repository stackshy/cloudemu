package ssm_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/providers/aws/kmscrypto"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

func newEncryptedMock(t *testing.T) (*ssm.Mock, *kms.Mock) {
	t.Helper()

	k := kms.New(config.NewOptions(config.WithRegion("us-east-1")))
	m := ssm.New(config.NewOptions())
	m.SetKMSCrypto(kmscrypto.New(k))

	return m, k
}

// TestSecureStringEncryptedAtRest verifies a SecureString is genuine ciphertext
// at rest: WithDecryption=true returns the value, WithDecryption=false returns
// an opaque blob that is neither the plaintext nor empty.
func TestSecureStringEncryptedAtRest(t *testing.T) {
	m, _ := newEncryptedMock(t)
	ctx := context.Background()

	const secret = "topsecret-value"

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/db/password", Value: secret, Type: driver.TypeSecureString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	dec, err := m.GetParameter(ctx, "/db/password", true)
	if err != nil {
		t.Fatalf("GetParameter(WithDecryption=true): %v", err)
	}

	if dec.Value != secret {
		t.Fatalf("decrypted value = %q, want %q", dec.Value, secret)
	}

	opaque, err := m.GetParameter(ctx, "/db/password", false)
	if err != nil {
		t.Fatalf("GetParameter(WithDecryption=false): %v", err)
	}

	if opaque.Value == secret {
		t.Fatal("WithDecryption=false returned the plaintext; want opaque ciphertext")
	}

	if opaque.Value == "" {
		t.Fatal("WithDecryption=false returned an empty value")
	}
}

// TestStringParameterNotEncrypted verifies String parameters are untouched by
// KMS: their value is returned in the clear regardless of WithDecryption.
func TestStringParameterNotEncrypted(t *testing.T) {
	m, _ := newEncryptedMock(t)
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/app/host", Value: "db.internal", Type: driver.TypeString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	for _, withDecryption := range []bool{false, true} {
		got, err := m.GetParameter(ctx, "/app/host", withDecryption)
		if err != nil {
			t.Fatalf("GetParameter(%v): %v", withDecryption, err)
		}

		if got.Value != "db.internal" {
			t.Fatalf("String value (WithDecryption=%v) = %q, want db.internal", withDecryption, got.Value)
		}
	}
}

// TestSecureStringFailsAfterKeyDisabled verifies a decrypting read fails once the
// KMS key backing the parameter is disabled, matching real Parameter Store.
func TestSecureStringFailsAfterKeyDisabled(t *testing.T) {
	m, k := newEncryptedMock(t)
	ctx := context.Background()

	key, err := k.CreateKey(ctx, kmsdriver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/db/password", Value: "s3cr3t", Type: driver.TypeSecureString, KeyID: key.KeyID,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if err := k.DisableKey(ctx, key.KeyID); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	if _, err := m.GetParameter(ctx, "/db/password", true); err == nil {
		t.Fatal("GetParameter(WithDecryption=true) after DisableKey: want error, got nil")
	}
}

// TestSecureStringNoKMSFallback verifies that without a KMS backend wired, a
// SecureString is stored and returned verbatim (the library plaintext fallback)
// and nothing panics.
func TestSecureStringNoKMSFallback(t *testing.T) {
	m := ssm.New(config.NewOptions())
	ctx := context.Background()

	if _, _, err := m.PutParameter(ctx, driver.PutConfig{
		Name: "/db/password", Value: "plain-secret", Type: driver.TypeSecureString,
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	got, err := m.GetParameter(ctx, "/db/password", true)
	if err != nil {
		t.Fatalf("GetParameter: %v", err)
	}

	if got.Value != "plain-secret" {
		t.Fatalf("fallback value = %q, want plain-secret", got.Value)
	}
}
