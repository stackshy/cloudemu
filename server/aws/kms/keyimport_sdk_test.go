package kms_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func TestSDKImportKeyMaterial(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	// EXTERNAL-origin key starts PendingImport.
	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{Origin: kmstypes.OriginTypeExternal})
	if err != nil {
		t.Fatalf("CreateKey EXTERNAL: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)
	if key.KeyMetadata.KeyState != kmstypes.KeyStatePendingImport {
		t.Fatalf("EXTERNAL key state = %s, want PendingImport", key.KeyMetadata.KeyState)
	}

	params, err := c.GetParametersForImport(ctx, &awskms.GetParametersForImportInput{
		KeyId:             aws.String(keyID),
		WrappingAlgorithm: kmstypes.AlgorithmSpecRsaesOaepSha256,
		WrappingKeySpec:   kmstypes.WrappingKeySpecRsa2048,
	})
	if err != nil {
		t.Fatalf("GetParametersForImport: %v", err)
	}

	// Parse the returned wrapping public key and wrap 32 bytes of material.
	pubAny, err := x509.ParsePKIXPublicKey(params.PublicKey)
	if err != nil {
		t.Fatalf("parse wrapping public key: %v", err)
	}

	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("wrapping key is not RSA: %T", pubAny)
	}

	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		t.Fatalf("rand: %v", err)
	}

	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, material, nil)
	if err != nil {
		t.Fatalf("wrap material: %v", err)
	}

	if _, err := c.ImportKeyMaterial(ctx, &awskms.ImportKeyMaterialInput{
		KeyId:                aws.String(keyID),
		ImportToken:          params.ImportToken,
		EncryptedKeyMaterial: wrapped,
		ExpirationModel:      kmstypes.ExpirationModelTypeKeyMaterialDoesNotExpire,
	}); err != nil {
		t.Fatalf("ImportKeyMaterial: %v", err)
	}

	// Key is now Enabled and usable for encryption; the imported material is
	// the AES key, so encrypt/decrypt must round-trip.
	desc, _ := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if desc.KeyMetadata.KeyState != kmstypes.KeyStateEnabled {
		t.Fatalf("after import, key state = %s, want Enabled", desc.KeyMetadata.KeyState)
	}

	enc, err := c.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("imported")})
	if err != nil {
		t.Fatalf("Encrypt with imported key: %v", err)
	}

	dec, err := c.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: enc.CiphertextBlob})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(dec.Plaintext, []byte("imported")) {
		t.Fatal("imported-key encrypt/decrypt did not round-trip")
	}

	// DeleteImportedKeyMaterial returns it to PendingImport.
	if _, err := c.DeleteImportedKeyMaterial(ctx, &awskms.DeleteImportedKeyMaterialInput{
		KeyId: aws.String(keyID),
	}); err != nil {
		t.Fatalf("DeleteImportedKeyMaterial: %v", err)
	}

	desc, _ = c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if desc.KeyMetadata.KeyState != kmstypes.KeyStatePendingImport {
		t.Fatalf("after delete, key state = %s, want PendingImport", desc.KeyMetadata.KeyState)
	}
}

func TestSDKMultiRegion(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{MultiRegion: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateKey MRK: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)
	if !aws.ToBool(key.KeyMetadata.MultiRegion) {
		t.Fatal("key should be multi-region")
	}

	rep, err := c.ReplicateKey(ctx, &awskms.ReplicateKeyInput{
		KeyId: aws.String(keyID), ReplicaRegion: aws.String("us-west-2"),
	})
	if err != nil {
		t.Fatalf("ReplicateKey: %v", err)
	}

	if rep.ReplicaKeyMetadata == nil ||
		rep.ReplicaKeyMetadata.MultiRegionConfiguration == nil ||
		rep.ReplicaKeyMetadata.MultiRegionConfiguration.MultiRegionKeyType != kmstypes.MultiRegionKeyTypeReplica {
		t.Fatalf("replica metadata not shaped correctly: %+v", rep.ReplicaKeyMetadata)
	}

	if _, err := c.UpdatePrimaryRegion(ctx, &awskms.UpdatePrimaryRegionInput{
		KeyId: aws.String(keyID), PrimaryRegion: aws.String("us-west-2"),
	}); err != nil {
		t.Fatalf("UpdatePrimaryRegion: %v", err)
	}
}
