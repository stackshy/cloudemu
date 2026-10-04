package kms_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newGapicKMS returns a real cloud.google.com/go/kms REST client pointed at a
// fresh GCP wire server, with the test key ring already created.
func newGapicKMS(t *testing.T) *kms.KeyManagementClient {
	t.Helper()

	srv := gcpserver.New(gcpserver.Drivers{Clock: config.NewFakeClock(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := kms.NewKeyManagementRESTClient(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("NewKeyManagementRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	_, err = c.CreateKeyRing(context.Background(), &kmspb.CreateKeyRingRequest{
		Parent: testLocationParent, KeyRingId: testKeyRingID,
	})
	if err != nil {
		t.Fatalf("CreateKeyRing: %v", err)
	}

	return c
}

func mustGapicKey(t *testing.T, c *kms.KeyManagementClient, id string, purpose kmspb.CryptoKey_CryptoKeyPurpose,
	alg kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm,
) string {
	t.Helper()

	ck, err := c.CreateCryptoKey(context.Background(), &kmspb.CreateCryptoKeyRequest{
		Parent: testKeyRingName, CryptoKeyId: id,
		CryptoKey: &kmspb.CryptoKey{
			Purpose:         purpose,
			VersionTemplate: &kmspb.CryptoKeyVersionTemplate{Algorithm: alg},
		},
	})
	if err != nil {
		t.Fatalf("CreateCryptoKey %s: %v", id, err)
	}

	return ck.GetName()
}

// wantStatus asserts an HTTP 400 whose body carries the canonical status
// (INVALID_ARGUMENT, FAILED_PRECONDITION) real Cloud KMS reports over REST.
func wantStatus(t *testing.T, err error, wantStatus, msgPart string) {
	t.Helper()

	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		t.Fatalf("want %s googleapi.Error, got %v", wantStatus, err)
	}

	if ge.Code != 400 || !strings.Contains(ge.Body, `"status":"`+wantStatus+`"`) ||
		!strings.Contains(ge.Message, msgPart) {
		t.Fatalf("got %d %s, want 400 %s mentioning %q", ge.Code, ge.Body, wantStatus, msgPart)
	}
}

func TestKMSEncryptDecryptRotation(t *testing.T) {
	ctx := context.Background()
	c := newGapicKMS(t)
	key := mustGapicKey(t, c, "sym", kmspb.CryptoKey_ENCRYPT_DECRYPT,
		kmspb.CryptoKeyVersion_GOOGLE_SYMMETRIC_ENCRYPTION)
	aad := []byte("tenant=a")

	enc1, err := c.Encrypt(ctx, &kmspb.EncryptRequest{Name: key, Plaintext: []byte("hello"), AdditionalAuthenticatedData: aad})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if enc1.GetName() != key+"/cryptoKeyVersions/1" || bytes.Contains(enc1.GetCiphertext(), []byte("hello")) {
		t.Fatalf("encrypt name %q, ciphertext %x", enc1.GetName(), enc1.GetCiphertext())
	}

	// Rotate: version 2 becomes primary; new ciphertexts use it.
	if _, err = c.CreateCryptoKeyVersion(ctx, &kmspb.CreateCryptoKeyVersionRequest{Parent: key}); err != nil {
		t.Fatalf("CreateCryptoKeyVersion: %v", err)
	}

	if _, err = c.UpdateCryptoKeyPrimaryVersion(ctx, &kmspb.UpdateCryptoKeyPrimaryVersionRequest{
		Name: key, CryptoKeyVersionId: "2",
	}); err != nil {
		t.Fatalf("UpdateCryptoKeyPrimaryVersion: %v", err)
	}

	enc2, err := c.Encrypt(ctx, &kmspb.EncryptRequest{Name: key, Plaintext: []byte("world")})
	if err != nil || enc2.GetName() != key+"/cryptoKeyVersions/2" {
		t.Fatalf("Encrypt after rotation: name %q err %v", enc2.GetName(), err)
	}

	tests := []struct {
		name        string
		ct, aad     []byte
		want        string
		usedPrimary bool
	}{
		{name: "old version after rotation", ct: enc1.GetCiphertext(), aad: aad, want: "hello"},
		{name: "primary version", ct: enc2.GetCiphertext(), want: "world", usedPrimary: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := c.Decrypt(ctx, &kmspb.DecryptRequest{Name: key, Ciphertext: tc.ct, AdditionalAuthenticatedData: tc.aad})
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}

			if string(dec.GetPlaintext()) != tc.want || dec.GetUsedPrimary() != tc.usedPrimary {
				t.Fatalf("plaintext %q usedPrimary %v, want %q %v", dec.GetPlaintext(), dec.GetUsedPrimary(), tc.want, tc.usedPrimary)
			}
		})
	}

	_, err = c.Decrypt(ctx, &kmspb.DecryptRequest{Name: key, Ciphertext: enc1.GetCiphertext(), AdditionalAuthenticatedData: []byte("tenant=b")})
	wantStatus(t, err, "INVALID_ARGUMENT", "ciphertext is invalid")

	// A disabled version keeps its material but refuses to decrypt.
	_, err = c.UpdateCryptoKeyVersion(ctx, &kmspb.UpdateCryptoKeyVersionRequest{
		CryptoKeyVersion: &kmspb.CryptoKeyVersion{Name: key + "/cryptoKeyVersions/1", State: kmspb.CryptoKeyVersion_DISABLED},
		UpdateMask:       &fieldmaskpb.FieldMask{Paths: []string{"state"}},
	})
	if err != nil {
		t.Fatalf("disable version 1: %v", err)
	}

	_, err = c.Decrypt(ctx, &kmspb.DecryptRequest{Name: key, Ciphertext: enc1.GetCiphertext(), AdditionalAuthenticatedData: aad})
	wantStatus(t, err, "FAILED_PRECONDITION", "DISABLED")
}

func TestKMSAsymmetricSign(t *testing.T) {
	ctx := context.Background()
	c := newGapicKMS(t)
	msg := []byte("sign me")
	sum := sha256.Sum256(msg)

	tests := []struct {
		id     string
		alg    kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm
		verify func(pub any, sig []byte) bool
	}{
		{id: "ec", alg: kmspb.CryptoKeyVersion_EC_SIGN_P256_SHA256, verify: func(pub any, sig []byte) bool {
			return ecdsa.VerifyASN1(pub.(*ecdsa.PublicKey), sum[:], sig)
		}},
		{id: "pss", alg: kmspb.CryptoKeyVersion_RSA_SIGN_PSS_2048_SHA256, verify: func(pub any, sig []byte) bool {
			return rsa.VerifyPSS(pub.(*rsa.PublicKey), crypto.SHA256, sum[:], sig,
				&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		}},
		{id: "pkcs1", alg: kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_2048_SHA256, verify: func(pub any, sig []byte) bool {
			return rsa.VerifyPKCS1v15(pub.(*rsa.PublicKey), crypto.SHA256, sum[:], sig) == nil
		}},
		{id: "ed", alg: kmspb.CryptoKeyVersion_EC_SIGN_ED25519, verify: func(pub any, sig []byte) bool {
			return ed25519.Verify(pub.(ed25519.PublicKey), msg, sig)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			ver := mustGapicKey(t, c, tc.id, kmspb.CryptoKey_ASYMMETRIC_SIGN, tc.alg) + "/cryptoKeyVersions/1"

			req := &kmspb.AsymmetricSignRequest{Name: ver, Digest: &kmspb.Digest{Digest: &kmspb.Digest_Sha256{Sha256: sum[:]}}}
			if tc.alg == kmspb.CryptoKeyVersion_EC_SIGN_ED25519 {
				req = &kmspb.AsymmetricSignRequest{Name: ver, Data: msg}
			}

			sig, err := c.AsymmetricSign(ctx, req)
			if err != nil {
				t.Fatalf("AsymmetricSign: %v", err)
			}

			pk, err := c.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: ver})
			if err != nil {
				t.Fatalf("GetPublicKey: %v", err)
			}

			block, _ := pem.Decode([]byte(pk.GetPem()))
			if block == nil {
				t.Fatalf("public key is not PEM: %q", pk.GetPem())
			}

			pub, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				t.Fatalf("ParsePKIXPublicKey: %v", err)
			}

			if pk.GetAlgorithm() != tc.alg || !tc.verify(pub, sig.GetSignature()) {
				t.Fatalf("algorithm %v, signature does not verify with the version's public key", pk.GetAlgorithm())
			}
		})
	}
}

func TestKMSAsymmetricDecryptAndMAC(t *testing.T) {
	ctx := context.Background()
	c := newGapicKMS(t)

	ver := mustGapicKey(t, c, "oaep", kmspb.CryptoKey_ASYMMETRIC_DECRYPT,
		kmspb.CryptoKeyVersion_RSA_DECRYPT_OAEP_2048_SHA256) + "/cryptoKeyVersions/1"

	pk, err := c.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: ver})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	block, _ := pem.Decode([]byte(pk.GetPem()))

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}

	ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub.(*rsa.PublicKey), []byte("secret"), nil)
	if err != nil {
		t.Fatalf("EncryptOAEP: %v", err)
	}

	dec, err := c.AsymmetricDecrypt(ctx, &kmspb.AsymmetricDecryptRequest{Name: ver, Ciphertext: ct})
	if err != nil || string(dec.GetPlaintext()) != "secret" {
		t.Fatalf("AsymmetricDecrypt = %q, %v", dec.GetPlaintext(), err)
	}

	macVer := mustGapicKey(t, c, "mac", kmspb.CryptoKey_MAC, kmspb.CryptoKeyVersion_HMAC_SHA256) + "/cryptoKeyVersions/1"

	signed, err := c.MacSign(ctx, &kmspb.MacSignRequest{Name: macVer, Data: []byte("payload")})
	if err != nil {
		t.Fatalf("MacSign: %v", err)
	}

	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{name: "same data", data: []byte("payload"), want: true},
		{name: "tampered data", data: []byte("payload!"), want: false},
	} {
		got, err := c.MacVerify(ctx, &kmspb.MacVerifyRequest{Name: macVer, Data: tc.data, Mac: signed.GetMac()})
		if err != nil || got.GetSuccess() != tc.want {
			t.Fatalf("%s: MacVerify success=%v err=%v, want %v", tc.name, got.GetSuccess(), err, tc.want)
		}
	}

	if len(signed.GetMac()) != sha256.Size {
		t.Fatalf("MAC is %d bytes, want %d", len(signed.GetMac()), sha256.Size)
	}

	// A symmetric key cannot sign.
	_, err = c.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{Name: macVer, Data: []byte("x")})
	wantStatus(t, err, "FAILED_PRECONDITION", "ASYMMETRIC_SIGN")

	rnd, err := c.GenerateRandomBytes(ctx, &kmspb.GenerateRandomBytesRequest{
		Location: testLocationParent, LengthBytes: 32, ProtectionLevel: kmspb.ProtectionLevel_SOFTWARE,
	})
	if err != nil || len(rnd.GetData()) != 32 {
		t.Fatalf("GenerateRandomBytes = %d bytes, %v", len(rnd.GetData()), err)
	}
}
