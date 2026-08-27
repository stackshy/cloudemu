package kms_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kms"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func snapshotOpts() *config.Options {
	return config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Unix(0, 0))),
		config.WithRegion("us-east-1"),
		config.WithAccountID("000000000000"),
	)
}

// TestSnapshotRoundTripKMS proves a snapshot/restore round-trip preserves a key
// (with its symmetric material), its alias, so a ciphertext created before the
// snapshot still decrypts after a restore into a fresh mock.
func TestSnapshotRoundTripKMS(t *testing.T) {
	ctx := context.Background()
	src := kms.New(snapshotOpts())

	k, err := src.CreateKey(ctx, driver.CreateKeyInput{Description: "app key"})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	if err := src.CreateAlias(ctx, "alias/app", k.KeyID); err != nil {
		t.Fatalf("create alias: %v", err)
	}

	enc, err := src.Encrypt(ctx, driver.EncryptInput{KeyID: k.KeyID, Plaintext: []byte("secret")})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := kms.New(snapshotOpts())
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if _, err := dst.DescribeKey(ctx, k.KeyID); err != nil {
		t.Fatalf("describe restored key: %v", err)
	}

	// Alias resolves against the restored key.
	if _, err := dst.DescribeKey(ctx, "alias/app"); err != nil {
		t.Fatalf("describe restored key by alias: %v", err)
	}

	dec, err := dst.Decrypt(ctx, driver.DecryptInput{KeyID: k.KeyID, CiphertextBlob: enc.CiphertextBlob})
	if err != nil {
		t.Fatalf("decrypt after restore: %v", err)
	}

	if !bytes.Equal(dec.Plaintext, []byte("secret")) {
		t.Fatalf("decrypted %q, want secret", dec.Plaintext)
	}
}
