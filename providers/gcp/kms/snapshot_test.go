package kms

import (
	"bytes"
	"context"
	"testing"
)

func TestSnapshotKeepsKeyMaterial(t *testing.T) {
	ctx := context.Background()
	src := New(nil)
	ring := Ref{Project: "p", Location: "l", KeyRing: "r"}

	if _, err := src.CreateKeyRing(&ring); err != nil {
		t.Fatalf("CreateKeyRing: %v", err)
	}

	tests := []struct {
		id, purpose, alg string
	}{
		{id: "rsa", purpose: purposeAsymmetricSign, alg: "RSA_SIGN_PKCS1_2048_SHA256"},
		{id: "ed", purpose: purposeAsymmetricSign, alg: algEd25519},
		{id: "oaep", purpose: purposeAsymmetricDecrypt, alg: "RSA_DECRYPT_OAEP_2048_SHA256"},
		{id: "mac", purpose: purposeMAC, alg: "HMAC_SHA256"},
		{id: "sym", purpose: PurposeEncryptDecrypt, alg: algSymmetric},
	}

	for _, tc := range tests {
		if _, err := src.CreateCryptoKey(&ring, &KeyConfig{ID: tc.id, Purpose: tc.purpose, Algorithm: tc.alg}, false); err != nil {
			t.Fatalf("CreateCryptoKey %s: %v", tc.id, err)
		}

		ref := ring
		ref.CryptoKey, ref.Version = tc.id, "1"

		src.mu.Lock()
		_, _, err := src.usable(&ref, tc.purpose)
		src.mu.Unlock()

		if err != nil {
			t.Fatalf("generate material %s: %v", tc.id, err)
		}
	}

	symRef := &Ref{Project: "p", Location: "l", KeyRing: "r", CryptoKey: "sym"}

	ct, err := src.Encrypt(symRef, []byte("hi"), nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := New(nil)
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			name := RingName(&ring) + "/cryptoKeys/" + tc.id + "/cryptoKeyVersions/1"
			want, _ := src.versions.Get(name)
			got, _ := dst.versions.Get(name)

			if len(want.Secret)+len(want.PrivateKey) == 0 ||
				!bytes.Equal(got.Secret, want.Secret) || !bytes.Equal(got.PrivateKey, want.PrivateKey) {
				t.Fatal("key material missing or changed across restore")
			}
		})
	}

	ref := ring
	ref.CryptoKey, ref.Version = "ed", "1"

	if _, err := dst.AsymmetricSign(&ref, nil, []byte("m")); err != nil {
		t.Fatalf("sign with restored Ed25519 key: %v", err)
	}

	pt, err := dst.Decrypt(symRef, ct.Out, nil)
	if err != nil || string(pt.Out) != "hi" {
		t.Fatalf("Decrypt after restore = %q, %v", pt.Out, err)
	}
}

// TestRestoreEmptySnapshot covers snapshots written before KMS was persisted:
// a missing or empty section restores to an empty mock without error.
func TestRestoreEmptySnapshot(t *testing.T) {
	m := New(nil)
	if err := m.Restore(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := m.ListKeyRings(&Ref{Project: "p", Location: "l"}); len(got) != 0 {
		t.Fatalf("rings = %v, want none", got)
	}
}
