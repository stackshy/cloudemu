package kms_test

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// TestDisabledKeyMessageNamesKey checks that every operation refused for a
// Disabled key returns DisabledException with the key ARN in the message, as
// real KMS does ("<key ARN> is disabled."), while errors.Is still matches
// driver.ErrKeyDisabled for callers such as Secrets Manager.
func TestDisabledKeyMessageNamesKey(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	k, err := m.CreateKey(ctx, driver.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if err := m.DisableKey(ctx, k.KeyID); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	want := k.ARN + " is disabled."

	ops := map[string]func() error{
		"Encrypt": func() error {
			_, err := m.Encrypt(ctx, driver.EncryptInput{KeyID: k.KeyID, Plaintext: []byte("x")})
			return err
		},
		"GenerateDataKey": func() error {
			_, err := m.GenerateDataKey(ctx, driver.GenerateDataKeyInput{KeyID: k.KeyID, KeySpec: "AES_256"})
			return err
		},
		"EnableKeyRotation": func() error {
			return m.EnableKeyRotation(ctx, k.KeyID, 0)
		},
	}

	for name, call := range ops {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !errors.Is(err, driver.ErrKeyDisabled) {
				t.Fatalf("err = %v, want ErrKeyDisabled", err)
			}

			if got := cerrors.Message(err); got != want {
				t.Fatalf("message = %q, want %q", got, want)
			}
		})
	}
}
