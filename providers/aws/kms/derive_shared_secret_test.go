package kms_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

func wantException(t *testing.T, err error, exception string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != exception {
		t.Fatalf("err = %v, want %s", err, exception)
	}
}

// --- DeriveSharedSecret (ECDH) ---

func TestDeriveSharedSecretMatchesLocalECDH(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	for _, tc := range []struct {
		spec  string
		curve elliptic.Curve
	}{
		{driver.SpecECCNISTP256, elliptic.P256()},
		{driver.SpecECCNISTP384, elliptic.P384()},
		{driver.SpecECCNISTP521, elliptic.P521()},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			k, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: tc.spec, KeyUsage: driver.UsageKeyAgreement})
			if err != nil {
				t.Fatalf("CreateKey KEY_AGREEMENT: %v", err)
			}

			if len(k.KeyAgreementAlgorithms) != 1 || k.KeyAgreementAlgorithms[0] != driver.KeyAgreementECDH {
				t.Fatalf("KeyAgreementAlgorithms = %v, want [ECDH]", k.KeyAgreementAlgorithms)
			}

			if len(k.SigningAlgorithms) != 0 || len(k.EncryptionAlgorithms) != 0 {
				t.Fatalf("unexpected algorithms on KEY_AGREEMENT key: %+v", k)
			}

			// Bob's key pair lives outside KMS.
			bob, err := ecdsa.GenerateKey(tc.curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}

			bobPub, err := x509.MarshalPKIXPublicKey(&bob.PublicKey)
			if err != nil {
				t.Fatal(err)
			}

			out, err := m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
				KeyID: k.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: bobPub,
			})
			if err != nil {
				t.Fatalf("DeriveSharedSecret: %v", err)
			}

			if out.KeyAgreementAlgorithm != driver.KeyAgreementECDH || out.KeyOrigin != driver.OriginAWSKMS {
				t.Fatalf("output = %+v", out)
			}

			// Bob derives the same secret from Alice's (KMS) public key.
			pk, err := m.GetPublicKey(ctx, driver.GetPublicKeyInput{KeyID: k.KeyID})
			if err != nil {
				t.Fatalf("GetPublicKey: %v", err)
			}

			if len(pk.KeyAgreementAlgorithms) != 1 {
				t.Fatalf("GetPublicKey KeyAgreementAlgorithms = %v", pk.KeyAgreementAlgorithms)
			}

			parsed, err := x509.ParsePKIXPublicKey(pk.PublicKey)
			if err != nil {
				t.Fatal(err)
			}

			alicePub, err := parsed.(*ecdsa.PublicKey).ECDH()
			if err != nil {
				t.Fatal(err)
			}

			bobECDH, err := bob.ECDH()
			if err != nil {
				t.Fatal(err)
			}

			want, err := bobECDH.ECDH(alicePub)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(out.SharedSecret, want) {
				t.Fatalf("shared secret mismatch:\n kms   %x\n local %x", out.SharedSecret, want)
			}
		})
	}
}

func TestDeriveSharedSecretErrors(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	agree, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: driver.SpecECCNISTP256, KeyUsage: driver.UsageKeyAgreement})
	if err != nil {
		t.Fatal(err)
	}

	signKey, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: driver.SpecECCNISTP256, KeyUsage: driver.UsageSignVerify})
	if err != nil {
		t.Fatal(err)
	}

	peer256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	peerDER, _ := x509.MarshalPKIXPublicKey(&peer256.PublicKey)

	peer384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	peer384DER, _ := x509.MarshalPKIXPublicKey(&peer384.PublicKey)

	// SIGN_VERIFY key → InvalidKeyUsageException.
	_, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: signKey.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if !errors.Is(err, driver.ErrInvalidKeyUsage) {
		t.Fatalf("sign key: err = %v, want ErrInvalidKeyUsage", err)
	}

	// Peer on a different curve → validation error.
	_, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peer384DER,
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("curve mismatch: err = %v, want InvalidArgument", err)
	}

	// Garbage public key / bad algorithm → validation error.
	if _, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: []byte("nope"),
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad DER: err = %v", err)
	}

	if _, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: "XDH", PublicKey: peerDER,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("bad algorithm: err = %v", err)
	}

	// Disabled key → DisabledException.
	if err := m.DisableKey(ctx, agree.KeyID); err != nil {
		t.Fatal(err)
	}

	_, err = m.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: agree.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if !errors.Is(err, driver.ErrKeyDisabled) {
		t.Fatalf("disabled: err = %v", err)
	}

	// A KEY_AGREEMENT key can't sign.
	if err := m.EnableKey(ctx, agree.KeyID); err != nil {
		t.Fatal(err)
	}

	_, err = m.Sign(ctx, driver.SignInput{
		KeyID: agree.KeyID, Message: []byte("x"), MessageType: driver.MessageTypeRaw,
		SigningAlgorithm: driver.SignECDSASHA256,
	})
	if !errors.Is(err, driver.ErrInvalidKeyUsage) {
		t.Fatalf("sign with KEY_AGREEMENT key: err = %v", err)
	}
}

func TestCreateKeyKeyAgreementSpecRules(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	for _, spec := range []string{driver.SpecSymmetricDefault, driver.SpecRSA2048, driver.SpecHMAC256} {
		if _, err := m.CreateKey(ctx, driver.CreateKeyInput{KeySpec: spec, KeyUsage: driver.UsageKeyAgreement}); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("%s + KEY_AGREEMENT: err = %v, want InvalidArgument", spec, err)
		}
	}
}

func TestKeyAgreementKeySurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	k, err := src.CreateKey(ctx, driver.CreateKeyInput{KeySpec: driver.SpecECCNISTP256, KeyUsage: driver.UsageKeyAgreement})
	if err != nil {
		t.Fatal(err)
	}

	peer, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	peerDER, _ := x509.MarshalPKIXPublicKey(&peer.PublicKey)

	before, err := src.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: k.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if err != nil {
		t.Fatal(err)
	}

	snap, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	after, err := dst.DeriveSharedSecret(ctx, driver.DeriveSharedSecretInput{
		KeyID: k.KeyID, KeyAgreementAlgorithm: driver.KeyAgreementECDH, PublicKey: peerDER,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(before.SharedSecret, after.SharedSecret) {
		t.Fatal("shared secret changed across snapshot/restore")
	}
}
