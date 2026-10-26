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

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKDeriveSharedSecret: a real client creates a KEY_AGREEMENT key, derives
// a shared secret with a locally generated peer key, and checks it equals the
// secret the peer computes from the key's downloaded public key.
func TestSDKDeriveSharedSecret(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP256, KeyUsage: kmstypes.KeyUsageTypeKeyAgreement,
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	md := key.KeyMetadata
	if md.KeyUsage != kmstypes.KeyUsageTypeKeyAgreement ||
		len(md.KeyAgreementAlgorithms) != 1 || md.KeyAgreementAlgorithms[0] != kmstypes.KeyAgreementAlgorithmSpecEcdh {
		t.Fatalf("metadata = usage %s, KeyAgreementAlgorithms %v", md.KeyUsage, md.KeyAgreementAlgorithms)
	}

	peer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	peerDER, err := x509.MarshalPKIXPublicKey(&peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	out, err := c.DeriveSharedSecret(ctx, &awskms.DeriveSharedSecretInput{
		KeyId: md.KeyId, KeyAgreementAlgorithm: kmstypes.KeyAgreementAlgorithmSpecEcdh, PublicKey: peerDER,
	})
	if err != nil {
		t.Fatalf("DeriveSharedSecret: %v", err)
	}

	if out.KeyAgreementAlgorithm != kmstypes.KeyAgreementAlgorithmSpecEcdh || out.KeyOrigin != kmstypes.OriginTypeAwsKms {
		t.Fatalf("response = %+v", out)
	}

	pub, err := c.GetPublicKey(ctx, &awskms.GetPublicKeyInput{KeyId: md.KeyId})
	if err != nil {
		t.Fatalf("GetPublicKey: %v", err)
	}

	if len(pub.KeyAgreementAlgorithms) != 1 {
		t.Fatalf("GetPublicKey KeyAgreementAlgorithms = %v", pub.KeyAgreementAlgorithms)
	}

	parsed, err := x509.ParsePKIXPublicKey(pub.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	kmsPub, err := parsed.(*ecdsa.PublicKey).ECDH()
	if err != nil {
		t.Fatal(err)
	}

	peerECDH, err := peer.ECDH()
	if err != nil {
		t.Fatal(err)
	}

	want, err := peerECDH.ECDH(kmsPub)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(out.SharedSecret, want) {
		t.Fatalf("shared secret mismatch:\n kms   %x\n local %x", out.SharedSecret, want)
	}

	// Wrong key usage → InvalidKeyUsageException.
	signKey, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		KeySpec: kmstypes.KeySpecEccNistP256, KeyUsage: kmstypes.KeyUsageTypeSignVerify,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.DeriveSharedSecret(ctx, &awskms.DeriveSharedSecretInput{
		KeyId: signKey.KeyMetadata.KeyId, KeyAgreementAlgorithm: kmstypes.KeyAgreementAlgorithmSpecEcdh, PublicKey: peerDER,
	})

	var usageErr *kmstypes.InvalidKeyUsageException
	if !errors.As(err, &usageErr) {
		t.Fatalf("sign key: err = %v, want InvalidKeyUsageException", err)
	}
}
